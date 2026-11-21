"""The operations the helper offers.

Each operation takes the request's ``args`` object and returns the reply's
``result`` object, or raises HelperError with a typed code. An operation runs
exactly once per request: the helper never retries one, whatever goes wrong.

The helper offers ``ping``; the reads ``holdings``, ``cash``, ``account``,
``lookup`` and ``open_orders``; and the two calls that change something at the
broker, ``place`` and ``cancel``. A test holds the list to exactly these
eight. Every order the Go side sends here has already passed its risk check;
the helper checks only that the request is well formed, and sends each order
or cancellation to the broker exactly once. A failure is answered with its
code and never retried: the Go side treats every error as a broker error and
halts trading.

Every money amount and share count goes back as a decimal string, written
without an exponent and without trailing zeros (``"10"``, not
``"10.00000000"``), so the value is exact and the Go side's parsers accept it.
Error messages never quote a value from the broker or from broker.yaml.
"""

from __future__ import annotations

import os
import re
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import Any, Callable, Dict, Iterator
from zoneinfo import ZoneInfo

from . import __version__, brokerfile
from .library import (
    INSTRUMENTS_URL,
    BrokerAuthError,
    BrokerCallError,
    Library,
    RobinStocks,
    Session,
    account_url,
    client_ref,
)
from .protocol import ErrorCode, HelperError

Handler = Callable[[Dict[str, Any]], Dict[str, Any]]

# The broker's calendar: a position's opening date is its date in New York.
# Loaded on first use, so a missing time-zone database cannot stop ping.
EASTERN = "America/New_York"

DEFAULT_DATA_DIR = "/data"

LOGIN_AGAIN = "run `python3 -m brokerhelper login` on the NAS"


def ping(args: dict[str, Any]) -> dict[str, Any]:
    """Answer that the helper is alive, with its version and process id."""
    if args:
        raise HelperError(ErrorCode.BAD_REQUEST, "ping takes no arguments")
    return {"version": __version__, "pid": os.getpid()}


def _utcnow() -> datetime:
    return datetime.now(timezone.utc)


def _timestamp(t: datetime) -> str:
    return t.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


class BadReply(Exception):
    """The broker's answer was not in the expected shape. Names a field only."""


def _field(record: dict[str, Any], key: str) -> Any:
    if not isinstance(record, dict) or key not in record:
        raise BadReply(f"{key} is missing")
    return record[key]


def _string(record: dict[str, Any], key: str) -> str:
    value = _field(record, key)
    if not isinstance(value, str) or not value:
        raise BadReply(f"{key} is not a non-empty string")
    return value


def _decimal(record: dict[str, Any], key: str) -> Decimal:
    value = _field(record, key)
    # A JSON number would already have been through a float; only strings
    # carry an exact decimal.
    if not isinstance(value, str):
        raise BadReply(f"{key} is not a decimal string")
    try:
        d = Decimal(value.strip())
    except InvalidOperation:
        raise BadReply(f"{key} is not a decimal") from None
    if not d.is_finite():
        raise BadReply(f"{key} is not a finite decimal")
    return d


def decimal_text(d: Decimal) -> str:
    """Write d exactly, with no exponent and no trailing zeros."""
    if d == 0:
        return "0"
    return format(d.normalize(), "f")


def _opened_on(record: dict[str, Any]) -> str:
    value = _string(record, "created_at")
    try:
        t = datetime.fromisoformat(value)
    except ValueError:
        raise BadReply("created_at is not a timestamp") from None
    if t.tzinfo is None:
        raise BadReply("created_at has no time zone")
    return t.astimezone(ZoneInfo(EASTERN)).date().isoformat()


_SYMBOL = re.compile(r"^[A-Z][A-Z0-9.\-]{0,15}$")


def _symbol(value: Any) -> str:
    if not isinstance(value, str):
        raise BadReply("symbol is not a string")
    symbol = value.strip().upper()
    if not _SYMBOL.match(symbol):
        raise BadReply("symbol is not a ticker symbol")
    return symbol


@dataclass
class Broker:
    """What the broker operations need: the library, the data folder, a clock."""

    library: Library
    data_dir: Path
    now: Callable[[], datetime] = _utcnow

    def session(self) -> tuple[Session, str]:
        """Load the saved session and account, or raise a typed HelperError."""
        try:
            saved = brokerfile.read(self.data_dir)
        except brokerfile.BrokerFileMissing:
            raise HelperError(
                ErrorCode.LOGIN_NEEDED, f"nobody has logged in to the broker yet; {LOGIN_AGAIN}"
            ) from None
        except brokerfile.BrokerFileError as exc:
            raise HelperError(ErrorCode.BROKER_SETTINGS, str(exc)) from None
        if saved.expires_at <= self.now():
            raise HelperError(
                ErrorCode.LOGIN_NEEDED, f"the broker session has expired; {LOGIN_AGAIN}"
            )
        return Session(saved.token_type, saved.access_token), saved.account_number


@contextmanager
def _broker_call(op: str) -> Iterator[None]:
    """Turn the wrapper's errors into typed replies, with the helper's own words."""
    try:
        yield
    except BrokerAuthError:
        raise HelperError(
            ErrorCode.LOGIN_NEEDED, f"{op}: the broker refused the saved session; {LOGIN_AGAIN}"
        ) from None
    except BrokerCallError as exc:
        raise HelperError(ErrorCode.BROKER_ERROR, f"{op}: {exc.safe_message()}") from None
    except BadReply as exc:
        raise HelperError(
            ErrorCode.BAD_BROKER_REPLY, f"{op}: the broker's answer was not as expected: {exc}"
        ) from None


def holdings(broker: Broker, args: dict[str, Any]) -> dict[str, Any]:
    """Read the account's stock positions.

    Result::

        {"as_of": "2026-11-17T21:05:00Z",
         "holdings": [{"symbol": "ABC", "quantity": "10", "average_cost": "12.34",
                       "opened_on": "2025-03-04"}, ...]}

    Holdings are sorted by symbol. ``opened_on`` is the date, in New York, that
    the broker created the position. ``as_of`` is when the helper read them.
    """
    if args:
        raise HelperError(ErrorCode.BAD_REQUEST, "holdings takes no arguments")
    session, account_number = broker.session()
    lib = broker.library
    with _broker_call("holdings"):
        positions = lib.positions(session, account_number)
        as_of = broker.now()
        rows: dict[str, dict[str, str]] = {}
        for position in positions:
            if not isinstance(position, dict):
                raise BadReply("a position is not an object")
            if position.get("account_number", account_number) != account_number:
                raise BadReply("a position belongs to another account")
            quantity = _decimal(position, "quantity")
            if quantity < 0:
                raise BadReply("quantity is negative")
            if quantity == 0:
                continue
            average_cost = _decimal(position, "average_buy_price")
            if average_cost < 0:
                raise BadReply("average_buy_price is negative")
            opened_on = _opened_on(position)
            if isinstance(position.get("symbol"), str):
                symbol = _symbol(position["symbol"])
            else:
                symbol = _symbol(_field(lib.instrument(session, _string(position, "instrument")), "symbol"))
            if symbol in rows:
                raise BadReply("two positions share one symbol")
            rows[symbol] = {
                "symbol": symbol,
                "quantity": decimal_text(quantity),
                "average_cost": decimal_text(average_cost),
                "opened_on": opened_on,
            }
    return {"as_of": _timestamp(as_of), "holdings": [rows[s] for s in sorted(rows)]}


def cash(broker: Broker, args: dict[str, Any]) -> dict[str, Any]:
    """Read the account's cash.

    Result::

        {"as_of": "2026-11-17T21:05:00Z", "cash": "1234.56", "buying_power": "1234.56"}

    ``cash`` is the broker's cash figure and may be negative; ``buying_power``
    is what the broker says can be spent now.
    """
    if args:
        raise HelperError(ErrorCode.BAD_REQUEST, "cash takes no arguments")
    session, account_number = broker.session()
    with _broker_call("cash"):
        records = broker.library.accounts(session)
        as_of = broker.now()
        record = _the_account(records, account_number)
        amount = _decimal(record, "cash")
        buying_power = _decimal(record, "buying_power")
    return {
        "as_of": _timestamp(as_of),
        "cash": decimal_text(amount),
        "buying_power": decimal_text(buying_power),
    }


def account(broker: Broker, args: dict[str, Any]) -> dict[str, Any]:
    """Read the account's cash, buying power and total value.

    Result::

        {"as_of": "2026-11-17T21:05:00Z", "cash": "1234.56",
         "buying_power": "1200", "value": "5678.9"}

    ``cash`` and ``buying_power`` are the same figures ``cash`` reads.
    ``value`` is the portfolio record's ``equity``: the cash plus the holdings
    at the broker's latest prices, which the daily and intraday loss limits
    are measured against.
    """
    if args:
        raise HelperError(ErrorCode.BAD_REQUEST, "account takes no arguments")
    session, account_number = broker.session()
    lib = broker.library
    with _broker_call("account"):
        record = _the_account(lib.accounts(session), account_number)
        amount = _decimal(record, "cash")
        buying_power = _decimal(record, "buying_power")
        value = _decimal(lib.portfolio(session, account_number), "equity")
        as_of = broker.now()
    return {
        "as_of": _timestamp(as_of),
        "cash": decimal_text(amount),
        "buying_power": decimal_text(buying_power),
        "value": decimal_text(value),
    }


def _the_account(records: Any, account_number: str) -> dict[str, Any]:
    if not isinstance(records, list):
        raise BadReply("the account list is not a list")
    matches = [r for r in records if isinstance(r, dict) and r.get("account_number") == account_number]
    if len(matches) != 1:
        raise BadReply("the saved account is not exactly once in the broker's account list")
    return matches[0]


# --- Orders -------------------------------------------------------------------

# The order types sherwood sends, with the side each must have, and how each
# is put to the broker: (broker type, trigger, time in force, price field).
#
# - day_limit_buy: a limit buy, good for the day.
# - stop: a standing stop sell, good until cancelled, that becomes a market
#   sell when the price reaches the stop.
# - sell_at_open: a market sell, good for the day; sent before the open, it
#   is filled at the open.
ORDER_TYPES: dict[str, tuple[str, str, str, str, str | None]] = {
    "day_limit_buy": ("buy", "limit", "immediate", "gfd", "price"),
    "stop": ("sell", "market", "stop", "gtc", "stop_price"),
    "sell_at_open": ("sell", "market", "immediate", "gfd", None),
}

# The broker's order states, and the status sherwood's adapter knows each by.
# A state not listed here is a malformed reply: the helper will not guess.
STATES = {
    "queued": "accepted",
    "unconfirmed": "accepted",
    "confirmed": "accepted",
    "pending": "accepted",
    "partially_filled": "partially_filled",
    "filled": "filled",
    "cancelled": "cancelled",
    "voided": "cancelled",
    "rejected": "rejected",
    "failed": "rejected",
    "expired": "expired",
}

# The statuses of an order still open at the broker.
OPEN_STATUSES = frozenset({"accepted", "partially_filled"})

_REF = re.compile(r"^[!-~]{1,128}$")
_ORDER_ID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
_DETAIL = re.compile(r"^[ -~]{1,200}$")

# Places a share count may have; the Go side's quantities have at most six.
QUANTITY_PLACES = 6


def _bad(message: str) -> HelperError:
    return HelperError(ErrorCode.BAD_REQUEST, message)


def _only(op: str, args: dict[str, Any], required: tuple[str, ...], optional: tuple[str, ...] = ()) -> None:
    extra = sorted(set(args) - set(required) - set(optional))
    if extra:
        raise _bad(f"{op} does not take {', '.join(extra)}")
    missing = [k for k in required if k not in args]
    if missing:
        raise _bad(f"{op} needs {', '.join(missing)}")


def _ref_arg(op: str, args: dict[str, Any]) -> str:
    ref = args["ref"]
    if not isinstance(ref, str) or not _REF.match(ref):
        raise _bad(f"{op}: ref must be 1 to 128 printable characters with no spaces")
    return ref


def _places(d: Decimal) -> int:
    exponent = d.normalize().as_tuple().exponent
    return max(0, -exponent) if isinstance(exponent, int) else 0


def _amount_arg(op: str, args: dict[str, Any], key: str) -> Decimal:
    value = args[key]
    if not isinstance(value, str):
        raise _bad(f"{op}: {key} must be a decimal string")
    try:
        d = Decimal(value)
    except InvalidOperation:
        raise _bad(f"{op}: {key} is not a decimal") from None
    if not d.is_finite() or d <= 0:
        raise _bad(f"{op}: {key} must be more than zero")
    return d


def _price_arg(op: str, args: dict[str, Any], key: str) -> Decimal:
    price = _amount_arg(op, args, key)
    # The broker takes whole cents at a dollar and above, and up to four
    # places below. A price it would round is refused, never rounded here.
    allowed = 2 if price >= 1 else 4
    if _places(price) > allowed:
        raise _bad(f"{op}: {key} has more than {allowed} decimal places")
    return price


def _detail(record: dict[str, Any], status: str) -> str:
    if status == "rejected":
        reason = record.get("reject_reason")
        if isinstance(reason, str) and _DETAIL.match(reason):
            return reason
        return "the broker gave no reason it could pass on"
    if record.get("state") == "voided":
        return "the broker voided the order"
    return ""


def _report(record: Any, ref: str | None) -> dict[str, Any]:
    """The adapter's view of one broker order record.

    ``ref`` is sherwood's key when the caller knows it; an order found only in
    the broker's list carries just its client reference.
    """
    if not isinstance(record, dict):
        raise BadReply("an order is not an object")
    broker_id = _string(record, "id")
    if not _ORDER_ID.match(broker_id):
        raise BadReply("id is not a UUID")
    reference = record.get("ref_id")
    if reference is not None and not isinstance(reference, str):
        raise BadReply("ref_id is not a string")
    state = _string(record, "state")
    if state not in STATES:
        raise BadReply("state is not one the helper knows")
    status = STATES[state]
    filled = _decimal(record, "cumulative_quantity")
    if filled < 0:
        raise BadReply("cumulative_quantity is negative")
    fill_price = None
    if filled > 0 and _field(record, "average_price") is not None:
        price = _decimal(record, "average_price")
        if price <= 0:
            raise BadReply("average_price is not more than zero")
        fill_price = decimal_text(price)
    return {
        "ref": ref,
        "client_ref": reference,
        "broker_id": broker_id,
        "status": status,
        "filled": decimal_text(filled),
        "fill_price": fill_price,
        "detail": _detail(record, status),
    }


def _check_account(record: Any, account_number: str) -> None:
    owner = record.get("account") if isinstance(record, dict) else None
    if owner is not None and (
        not isinstance(owner, str) or owner.rstrip("/") != account_url(account_number).rstrip("/")
    ):
        raise BadReply("an order belongs to another account")


def _refused_here(ref: str, reference: str, why: str) -> dict[str, Any]:
    """The answer for an order the broker said, before it was sent, it would not take."""
    return {
        "ref": ref,
        "client_ref": reference,
        "broker_id": None,
        "status": "rejected",
        "filled": "0",
        "fill_price": None,
        "detail": why,
    }


def place(broker: Broker, args: dict[str, Any]) -> dict[str, Any]:
    """Send one order to the broker, exactly once.

    Args::

        {"ref": "<idempotency key>", "symbol": "ABC", "side": "buy",
         "type": "day_limit_buy", "quantity": "10", "price": "12.34"}

    ``type`` is ``day_limit_buy`` (a buy; ``price`` is its limit), ``stop`` (a
    sell; ``price`` is its stop) or ``sell_at_open`` (a sell; no ``price``).
    Quantity and price are decimal strings.

    The broker's client reference is ``client_ref(ref)``, so a second send of
    the same key carries the same reference as the first.

    Result: an order report::

        {"ref": "<key>", "client_ref": "<uuid>", "broker_id": "<uuid>",
         "status": "accepted", "filled": "0", "fill_price": null, "detail": ""}

    When the broker lists no tradeable stock for the symbol, nothing is sent
    and the answer is ``rejected`` with ``broker_id`` null. Every failure is
    an error with its code; a failure after the order went out leaves its
    outcome unknown, and reconciliation's ``lookup`` settles it.
    """
    _only("place", args, ("ref", "symbol", "side", "type", "quantity"), ("price",))
    ref = _ref_arg("place", args)
    symbol = args["symbol"]
    if not isinstance(symbol, str) or not _SYMBOL.match(symbol):
        raise _bad("place: symbol must be an upper-case ticker symbol")
    kind = args["type"]
    if kind not in ORDER_TYPES:
        raise _bad("place: type must be day_limit_buy, stop or sell_at_open")
    side, broker_type, trigger, time_in_force, price_field = ORDER_TYPES[kind]
    if args["side"] != side:
        raise _bad(f"place: a {kind} must be a {side}")
    quantity = _amount_arg("place", args, "quantity")
    if _places(quantity) > QUANTITY_PLACES:
        raise _bad(f"place: quantity has more than {QUANTITY_PLACES} decimal places")
    price = None
    if price_field is not None:
        if args.get("price") is None:
            raise _bad(f"place: a {kind} needs a price")
        price = _price_arg("place", args, "price")
    elif args.get("price") is not None:
        raise _bad(f"place: a {kind} takes no price")

    reference = client_ref(ref)
    session, account_number = broker.session()
    lib = broker.library
    with _broker_call("place"):
        listed = lib.instruments_for(session, symbol)
        matches = [i for i in listed if isinstance(i, dict) and i.get("symbol") == symbol]
        if not matches:
            return _refused_here(ref, reference, "the broker lists no stock with this symbol")
        if len(matches) > 1:
            raise BadReply("the broker lists the symbol more than once")
        instrument = matches[0]
        instrument_url = _string(instrument, "url")
        if not instrument_url.startswith(INSTRUMENTS_URL):
            raise BadReply("the instrument's url is not the broker's")
        if instrument.get("tradeable") is not True:
            return _refused_here(ref, reference, "the broker says this stock cannot be traded")
        payload: dict[str, Any] = {
            "account": account_url(account_number),
            "instrument": instrument_url,
            "symbol": symbol,
            "side": side,
            "quantity": decimal_text(quantity),
            "type": broker_type,
            "trigger": trigger,
            "time_in_force": time_in_force,
            "market_hours": "regular_hours",
            "extended_hours": False,
            "ref_id": reference,
            "order_form_version": 4,
        }
        if price_field is not None:
            payload[price_field] = decimal_text(price)
        record = lib.place_order(session, payload)
        report = _report(record, ref)
        if report["client_ref"] != reference:
            raise BadReply("the broker answered about an order with another reference")
        _check_account(record, account_number)
    return report


def _find(broker: Broker, op: str, session: Session, account_number: str, ref: str) -> tuple[dict[str, Any], str]:
    reference = client_ref(ref)
    record = broker.library.order_by_ref(session, account_number, reference)
    if record is None:
        raise HelperError(ErrorCode.ORDER_NOT_FOUND, f"{op}: the broker lists no order with this reference")
    if not isinstance(record, dict) or record.get("ref_id") != reference:
        raise BadReply("the broker answered about an order with another reference")
    _check_account(record, account_number)
    return record, reference


def lookup(broker: Broker, args: dict[str, Any]) -> dict[str, Any]:
    """Read one order's status by sherwood's key.

    Args: ``{"ref": "<idempotency key>"}``. Result: an order report, as
    ``place`` gives. An order the broker does not list is the error
    ``order_not_found``.
    """
    _only("lookup", args, ("ref",))
    ref = _ref_arg("lookup", args)
    session, account_number = broker.session()
    with _broker_call("lookup"):
        record, _ = _find(broker, "lookup", session, account_number, ref)
        return _report(record, ref)


def cancel(broker: Broker, args: dict[str, Any]) -> dict[str, Any]:
    """Call off one open order by sherwood's key, asking the broker exactly once.

    Args: ``{"ref": "<idempotency key>"}``.

    The order is found by its client reference first. If it is already
    finished (filled, cancelled, rejected or expired), nothing is sent and the
    answer is its status, so calling off an order twice is harmless. Otherwise
    the broker is asked once to cancel it, and its acceptance of that request
    is answered as ``cancelled``, with the shares filled before it. A failure
    is an error with its code, and the order's real state is left for
    reconciliation's lookup.
    """
    _only("cancel", args, ("ref",))
    ref = _ref_arg("cancel", args)
    session, account_number = broker.session()
    with _broker_call("cancel"):
        record, _ = _find(broker, "cancel", session, account_number, ref)
        report = _report(record, ref)
        if report["status"] not in OPEN_STATUSES:
            return report
        broker.library.cancel_order(session, report["broker_id"])
    report["status"] = "cancelled"
    report["detail"] = ""
    return report


def open_orders(broker: Broker, args: dict[str, Any]) -> dict[str, Any]:
    """List the account's orders still open at the broker.

    Result::

        {"as_of": "2026-11-17T21:05:00Z", "orders": [<order report>, ...]}

    Each report's ``ref`` is null: the helper cannot turn a client reference
    back into sherwood's key, so the caller matches ``client_ref`` against
    ``client_ref(key)`` for the keys it holds. An order placed in the
    broker's own app carries a client reference no key gives, or none.
    Sorted by the broker's order id.
    """
    if args:
        raise HelperError(ErrorCode.BAD_REQUEST, "open_orders takes no arguments")
    session, account_number = broker.session()
    with _broker_call("open_orders"):
        records = broker.library.orders(session, account_number)
        as_of = broker.now()
        reports = []
        for record in records:
            report = _report(record, None)
            _check_account(record, account_number)
            if report["status"] in OPEN_STATUSES:
                reports.append(report)
    reports.sort(key=lambda r: r["broker_id"])
    return {"as_of": _timestamp(as_of), "orders": reports}


# The operations that need no broker. The serving process adds the broker
# operations with broker_ops.
OPS: dict[str, Handler] = {"ping": ping}


def broker_ops(broker: Broker) -> dict[str, Handler]:
    """Every operation the helper offers, with the broker ones bound to broker."""
    return {
        "ping": ping,
        "holdings": lambda args: holdings(broker, args),
        "cash": lambda args: cash(broker, args),
        "account": lambda args: account(broker, args),
        "place": lambda args: place(broker, args),
        "cancel": lambda args: cancel(broker, args),
        "lookup": lambda args: lookup(broker, args),
        "open_orders": lambda args: open_orders(broker, args),
    }


def default_broker(data_dir: str | os.PathLike[str] = DEFAULT_DATA_DIR) -> Broker:
    """The broker the real helper uses: the real library, loaded on first use."""
    return Broker(library=RobinStocks(), data_dir=Path(data_dir))
