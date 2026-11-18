"""The operations the helper offers.

Each operation takes the request's ``args`` object and returns the reply's
``result`` object, or raises HelperError with a typed code. An operation runs
exactly once per request: the helper never retries one, whatever goes wrong.

The helper offers ``ping`` and two read-only broker operations, ``holdings``
and ``cash``. It offers no operation that places, changes or cancels an order;
a test holds the list to exactly these three.

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
from .library import BrokerAuthError, BrokerCallError, Library, RobinStocks, Session
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
        matches = [
            r for r in records if isinstance(r, dict) and r.get("account_number") == account_number
        ]
        if len(matches) != 1:
            raise BadReply("the saved account is not exactly once in the broker's account list")
        account = matches[0]
        amount = _decimal(account, "cash")
        buying_power = _decimal(account, "buying_power")
    return {
        "as_of": _timestamp(as_of),
        "cash": decimal_text(amount),
        "buying_power": decimal_text(buying_power),
    }


# The operations that need no broker. The serving process adds the broker
# operations with broker_ops.
OPS: dict[str, Handler] = {"ping": ping}


def broker_ops(broker: Broker) -> dict[str, Handler]:
    """Every operation the helper offers, with the broker ones bound to broker."""
    return {
        "ping": ping,
        "holdings": lambda args: holdings(broker, args),
        "cash": lambda args: cash(broker, args),
    }


def default_broker(data_dir: str | os.PathLike[str] = DEFAULT_DATA_DIR) -> Broker:
    """The broker the real helper uses: the real library, loaded on first use."""
    return Broker(library=RobinStocks(), data_dir=Path(data_dir))
