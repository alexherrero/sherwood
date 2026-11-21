"""A fake broker library and hand-written fixtures in the broker's shapes.

Every value here is made up. The shapes follow the unofficial library's
documented records (positions, instruments, accounts, portfolios, orders, the
login reply); none was captured from a real account. The symbols (ZZA, ZZB,
...) are not real tickers, and the ids are counting numbers dressed as UUIDs.
"""

from __future__ import annotations

import copy
from datetime import datetime, timedelta, timezone

from brokerhelper import brokerfile
from brokerhelper.library import BrokerAuthError, Session, client_ref

ACCOUNT = "FAKE0001"
OTHER_ACCOUNT = "FAKE0002"
ACCESS_TOKEN = "fake-access-token-for-tests"
REFRESH_TOKEN = "fake-refresh-token-for-tests"
NOW = datetime(2026, 11, 17, 21, 5, 0, tzinfo=timezone.utc)

API = "https://api.robinhood.com/"


def instrument_url(n: int) -> str:
    return f"{API}instruments/00000000-0000-0000-0000-00000000000{n}/"


def position(n: int, quantity: str, average: str, created_at: str, symbol: str | None = None) -> dict:
    record = {
        "url": f"{API}positions/{ACCOUNT}/00000000-0000-0000-0000-00000000000{n}/",
        "instrument": instrument_url(n),
        "instrument_id": f"00000000-0000-0000-0000-00000000000{n}",
        "account": f"{API}accounts/{ACCOUNT}/",
        "account_number": ACCOUNT,
        "average_buy_price": average,
        "pending_average_buy_price": average,
        "quantity": quantity,
        "intraday_average_buy_price": "0.0000",
        "intraday_quantity": "0.00000000",
        "shares_available_for_exercise": quantity,
        "shares_held_for_buys": "0.00000000",
        "shares_held_for_sells": "0.00000000",
        "shares_held_for_stock_grants": "0.00000000",
        "shares_held_for_options_collateral": "0.00000000",
        "shares_held_for_options_events": "0.00000000",
        "shares_pending_from_options_events": "0.00000000",
        "updated_at": "2026-11-16T15:00:00.000000Z",
        "created_at": created_at,
    }
    if symbol is not None:
        record["symbol"] = symbol
    return record


def instrument(n: int, symbol: str) -> dict:
    return {
        "id": f"00000000-0000-0000-0000-00000000000{n}",
        "url": instrument_url(n),
        "symbol": symbol,
        "name": f"Example Company {n}",
        "simple_name": f"Example {n}",
        "type": "stock",
        "tradeable": True,
        "country": "US",
        "state": "active",
    }


def account(number: str, cash: str = "1234.5600", buying_power: str = "1200.0000") -> dict:
    return {
        "url": f"{API}accounts/{number}/",
        "account_number": number,
        "type": "cash",
        "brokerage_account_type": "individual",
        "created_at": "2020-01-02T15:00:00.000000Z",
        "updated_at": "2026-11-16T15:00:00.000000Z",
        "deactivated": False,
        "portfolio_cash": cash,
        "cash": cash,
        "buying_power": buying_power,
        "cash_available_for_withdrawal": cash,
        "cash_held_for_orders": "0.0000",
        "uncleared_deposits": "0.0000",
        "unsettled_funds": "0.0000",
        "unsettled_debit": "0.0000",
        "sweep_enabled": False,
        "option_level": None,
        "state": "active",
        "locked": False,
        "permanently_deactivated": False,
    }


POSITIONS = [
    # Symbol given on the position itself.
    position(1, "10.00000000", "12.3400", "2025-03-04T15:30:00.000000Z", symbol="ZZB"),
    # Symbol only through the instrument link; created late in the evening in
    # New York, which is already the next day in UTC.
    position(2, "2.50000000", "100.0000", "2025-07-01T01:30:00.000000Z"),
    # A share count of zero is not a holding.
    position(3, "0.00000000", "5.0000", "2025-01-02T15:00:00.000000Z", symbol="ZZC"),
]

INSTRUMENTS = {instrument_url(2): instrument(2, "zza"), instrument_url(1): instrument(1, "ZZB")}

ACCOUNTS = [account(ACCOUNT)]


def portfolio(number: str = ACCOUNT, equity: str = "5678.9000") -> dict:
    return {
        "url": f"{API}portfolios/{number}/",
        "account": f"{API}accounts/{number}/",
        "start_date": "2020-01-02",
        "market_value": "4444.3400",
        "equity": equity,
        "extended_hours_market_value": None,
        "extended_hours_equity": None,
        "extended_hours_portfolio_equity": None,
        "last_core_market_value": "4444.3400",
        "last_core_equity": equity,
        "last_core_portfolio_equity": equity,
        "excess_margin": "0.0000",
        "excess_maintenance": "0.0000",
        "equity_previous_close": "5600.0000",
        "portfolio_equity_previous_close": "5600.0000",
        "adjusted_equity_previous_close": "5600.0000",
        "adjusted_portfolio_equity_previous_close": "5600.0000",
        "withdrawable_amount": "1234.5600",
        "unwithdrawable_deposits": "0.0000",
        "unwithdrawable_grants": "0.0000",
    }


# Made-up idempotency keys, in the shape of sherwood's (64 hex characters).
KEY_1 = "1" * 64
KEY_2 = "2" * 64
KEY_3 = "3" * 64


def order_id(n: int) -> str:
    return f"00000000-0000-0000-0000-{n:012d}"


def order(
    n: int,
    state: str = "confirmed",
    ref: str | None = None,
    side: str = "buy",
    kind: str = "limit",
    trigger: str = "immediate",
    time_in_force: str = "gfd",
    quantity: str = "10.00000000",
    price: str | None = "12.34000000",
    stop_price: str | None = None,
    filled: str = "0.00000000",
    average_price: str | None = None,
    reject_reason: str | None = None,
    instrument_n: int = 1,
    number: str = ACCOUNT,
) -> dict:
    """An order record in the broker's shape. ``ref`` is a client reference."""
    oid = order_id(n)
    still_open = state in ("queued", "unconfirmed", "confirmed", "partially_filled")
    return {
        "id": oid,
        "ref_id": ref,
        "url": f"{API}orders/{oid}/",
        "account": f"{API}accounts/{number}/",
        "position": f"{API}positions/{number}/{instrument_n}/",
        "cancel": f"{API}orders/{oid}/cancel/" if still_open else None,
        "instrument": instrument_url(instrument_n),
        "instrument_id": f"00000000-0000-0000-0000-00000000000{instrument_n}",
        "cumulative_quantity": filled,
        "average_price": average_price,
        "fees": "0.00",
        "state": state,
        "derived_state": state,
        "pending_cancel_open_agent": False,
        "type": kind,
        "side": side,
        "time_in_force": time_in_force,
        "trigger": trigger,
        "price": price,
        "stop_price": stop_price,
        "quantity": quantity,
        "reject_reason": reject_reason,
        "created_at": "2026-11-17T14:31:00.000000Z",
        "updated_at": "2026-11-17T14:31:01.000000Z",
        "last_transaction_at": "2026-11-17T14:31:01.000000Z",
        "executions": [],
        "extended_hours": False,
        "market_hours": "regular_hours",
        "override_dtbp_checks": False,
        "override_day_trade_checks": False,
        "response_category": None,
        "stop_triggered_at": None,
        "last_trail_price": None,
        "last_trail_price_updated_at": None,
        "dollar_based_amount": None,
        "total_notional": None,
        "executed_notional": None,
        "investment_schedule_id": None,
        "is_ipo_access_order": False,
    }


ORDERS = [
    # An open limit buy sherwood sent.
    order(1, "confirmed", client_ref(KEY_1)),
    # A resting stop sherwood sent, partly filled.
    order(2, "partially_filled", client_ref(KEY_2), side="sell", kind="market", trigger="stop",
          time_in_force="gtc", price=None, stop_price="9.50000000", filled="4.00000000",
          average_price="9.4800", instrument_n=2),
    # A finished order sherwood sent.
    order(3, "filled", client_ref(KEY_3), filled="10.00000000", average_price="12.3000"),
    # An open order placed in the broker's own app, with its own reference.
    order(4, "queued", "99999999-9999-4999-8999-999999999999", instrument_n=2),
    # An old cancelled order with no reference at all.
    order(5, "cancelled", None),
]

# The instrument list the broker gives for one symbol.
SYMBOL_INSTRUMENTS = {"ZZA": [instrument(2, "ZZA")], "ZZB": [instrument(1, "ZZB")]}

LOGIN_REPLY = {
    "access_token": ACCESS_TOKEN,
    "expires_in": 86400,
    "token_type": "Bearer",
    "scope": "internal",
    "refresh_token": REFRESH_TOKEN,
    "mfa_code": None,
    "backup_code": None,
}


class FakeLibrary:
    """Stands in for the wrapper. Records each call; ``fail`` makes one raise."""

    def __init__(self, positions=None, accounts=None, instruments=None, login_reply=None, orders=None):
        self.positions_data = copy.deepcopy(POSITIONS if positions is None else positions)
        self.accounts_data = copy.deepcopy(ACCOUNTS if accounts is None else accounts)
        self.instruments_data = copy.deepcopy(INSTRUMENTS if instruments is None else instruments)
        self.login_reply = copy.deepcopy(LOGIN_REPLY if login_reply is None else login_reply)
        self.orders_data = copy.deepcopy(ORDERS if orders is None else orders)
        self.symbol_instruments = copy.deepcopy(SYMBOL_INSTRUMENTS)
        self.portfolio_data = portfolio()
        # What place_order answers; None builds a fresh record from the payload.
        self.place_reply = None
        self.fail: dict[str, BaseException] = {}
        self.calls: list[tuple] = []

    def _maybe_fail(self, name: str) -> None:
        if name in self.fail:
            raise self.fail[name]

    def _check(self, session: Session) -> None:
        if session.access_token != ACCESS_TOKEN:
            raise BrokerAuthError("refused")

    def login(self, username, password, mfa_code):
        self.calls.append(("login", username, password, mfa_code))
        self._maybe_fail("login")
        return copy.deepcopy(self.login_reply)

    def accounts(self, session):
        self.calls.append(("accounts",))
        self._maybe_fail("accounts")
        self._check(session)
        return copy.deepcopy(self.accounts_data)

    def positions(self, session, account_number):
        self.calls.append(("positions", account_number))
        self._maybe_fail("positions")
        self._check(session)
        return copy.deepcopy(self.positions_data)

    def instrument(self, session, url):
        self.calls.append(("instrument", url))
        self._maybe_fail("instrument")
        self._check(session)
        return copy.deepcopy(self.instruments_data[url])

    def instruments_for(self, session, symbol):
        self.calls.append(("instruments_for", symbol))
        self._maybe_fail("instruments_for")
        self._check(session)
        return copy.deepcopy(self.symbol_instruments.get(symbol, []))

    def portfolio(self, session, account_number):
        self.calls.append(("portfolio", account_number))
        self._maybe_fail("portfolio")
        self._check(session)
        return copy.deepcopy(self.portfolio_data)

    def orders(self, session, account_number):
        self.calls.append(("orders", account_number))
        self._maybe_fail("orders")
        self._check(session)
        return copy.deepcopy(self.orders_data)

    def order_by_ref(self, session, account_number, ref):
        self.calls.append(("order_by_ref", account_number, ref))
        self._maybe_fail("order_by_ref")
        self._check(session)
        for record in self.orders_data:
            if record.get("ref_id") == ref:
                return copy.deepcopy(record)
        return None

    def place_order(self, session, payload):
        self.calls.append(("place_order", copy.deepcopy(payload)))
        self._maybe_fail("place_order")
        self._check(session)
        if self.place_reply is not None:
            return copy.deepcopy(self.place_reply)
        record = order(
            100 + len(self.orders_data),
            "unconfirmed",
            payload["ref_id"],
            side=payload["side"],
            kind=payload["type"],
            trigger=payload["trigger"],
            time_in_force=payload["time_in_force"],
            quantity=payload["quantity"],
            price=payload.get("price"),
            stop_price=payload.get("stop_price"),
        )
        self.orders_data.append(record)
        return copy.deepcopy(record)

    def cancel_order(self, session, order_id):
        self.calls.append(("cancel_order", order_id))
        self._maybe_fail("cancel_order")
        self._check(session)


def saved_session(expires_at: datetime | None = None, access_token: str = ACCESS_TOKEN) -> brokerfile.BrokerSession:
    return brokerfile.BrokerSession(
        account_number=ACCOUNT,
        token_type="Bearer",
        access_token=access_token,
        refresh_token=REFRESH_TOKEN,
        expires_at=expires_at or NOW + timedelta(hours=12),
        logged_in_at=NOW - timedelta(hours=12),
    )
