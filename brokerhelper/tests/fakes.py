"""A fake broker library and hand-written fixtures in the broker's shapes.

Every value here is made up. The shapes follow the unofficial library's
documented records (positions, instruments, accounts, the login reply); none
was captured from a real account.
"""

from __future__ import annotations

import copy
from datetime import datetime, timedelta, timezone

from brokerhelper import brokerfile
from brokerhelper.library import BrokerAuthError, Session

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

    def __init__(self, positions=None, accounts=None, instruments=None, login_reply=None):
        self.positions_data = copy.deepcopy(POSITIONS if positions is None else positions)
        self.accounts_data = copy.deepcopy(ACCOUNTS if accounts is None else accounts)
        self.instruments_data = copy.deepcopy(INSTRUMENTS if instruments is None else instruments)
        self.login_reply = copy.deepcopy(LOGIN_REPLY if login_reply is None else login_reply)
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


def saved_session(expires_at: datetime | None = None, access_token: str = ACCESS_TOKEN) -> brokerfile.BrokerSession:
    return brokerfile.BrokerSession(
        account_number=ACCOUNT,
        token_type="Bearer",
        access_token=access_token,
        refresh_token=REFRESH_TOKEN,
        expires_at=expires_at or NOW + timedelta(hours=12),
        logged_in_at=NOW - timedelta(hours=12),
    )
