"""The thin wrapper around the unofficial broker library.

This module is the only place the helper reaches the library (robin_stocks,
pinned in pyproject.toml). Everything else in the helper talks to the
``Library`` protocol below, and the tests replace it with a fake, so no test
ever imports the real library or touches the network.

The wrapper uses the library for what is hard to do by hand: the login, with
its second factor and device approval. For the read-only calls it borrows only
the library's HTTP session (its headers) and its URL builders, and makes each
request itself, because the library's own read helpers swallow HTTP errors and
can return a partial list of positions without saying so. Here every request
has a timeout, every answer other than HTTP 200 is an error, the session is
sent only to the broker's own API host, and nothing is ever retried.

There is deliberately no way to place, change or cancel an order here. The
library can do those things; this wrapper does not offer them.

Errors raised from here carry only text the wrapper wrote itself, never text
from the library, an exception or the broker, so no secret can ride along.
"""

from __future__ import annotations

import os
import re
import tempfile
from dataclasses import dataclass
from types import SimpleNamespace
from typing import Any, Callable, Protocol

# The only host the session token is ever sent to.
API_ROOT = "https://api.robinhood.com/"

# Seconds to wait for each HTTP request.
REQUEST_TIMEOUT = 10.0

# A paged list longer than this is refused rather than followed for ever.
MAX_PAGES = 50


class BrokerAuthError(Exception):
    """The broker refused the login or the saved session (HTTP 401 or 403)."""


class BrokerCallError(Exception):
    """A broker call failed.

    The wrapper raises it with its own words only. Callers pass on
    ``safe_message()``, which is the message only when it has one of the
    wrapper's own shapes, so text from anywhere else can never ride along.
    """

    def safe_message(self) -> str:
        text = str(self)
        if text in _FIXED_MESSAGES or any(p.fullmatch(text) for p in _MESSAGE_SHAPES):
            return text
        return "the broker call failed"


_FIXED_MESSAGES = frozenset(
    {
        "the broker linked to a host other than its API; not followed",
        "the broker's answer was not JSON",
        "the broker's answer was not a JSON object",
        "a list page from the broker had no results",
    }
)
_MESSAGE_SHAPES = (
    re.compile(r"the broker answered HTTP \d{1,3}|the broker answered HTTP None"),
    re.compile(r"the (request|login) failed with [A-Za-z_][A-Za-z0-9_]{0,63}"),
    re.compile(r"the broker's list ran past \d+ pages"),
)


@dataclass(frozen=True)
class Session:
    """The saved session the read calls authenticate with."""

    token_type: str
    access_token: str

    def __repr__(self) -> str:
        return "Session(...)"

    __str__ = __repr__

    def header(self) -> str:
        return f"{self.token_type} {self.access_token}"


class Library(Protocol):
    """What the helper needs from the broker. The tests' fake implements this."""

    def login(self, username: str, password: str, mfa_code: str | None) -> dict[str, Any]:
        """Log in, following the broker's second-factor steps on the terminal.

        Returns the broker's token reply (``token_type``, ``access_token``,
        ``refresh_token``, ``expires_in``). Raises BrokerAuthError when the
        login is refused or does not finish.
        """
        ...

    def accounts(self, session: Session) -> list[dict[str, Any]]:
        """The broker's account records, as the broker sends them."""
        ...

    def positions(self, session: Session, account_number: str) -> list[dict[str, Any]]:
        """The account's open stock positions, every page, as the broker sends them."""
        ...

    def instrument(self, session: Session, url: str) -> dict[str, Any]:
        """The instrument record a position links to."""
        ...


def _load_robin_stocks() -> SimpleNamespace:
    """Import the real library. Only RobinStocks calls this, and only on use."""
    from robin_stocks.robinhood import authentication, urls
    from robin_stocks.robinhood import globals as rh_globals

    return SimpleNamespace(
        login=authentication.login,
        session=rh_globals.SESSION,
        accounts_url=urls.account_profile_url,
        positions_url=urls.positions_url,
    )


class RobinStocks:
    """The real Library, backed by robin_stocks.

    ``loader`` returns the parts of the library the wrapper uses; the tests
    pass a stand-in so this class can be checked without the real library.
    """

    def __init__(self, loader: Callable[[], Any] = _load_robin_stocks) -> None:
        self._loader = loader
        self._rh: Any = None

    def _lib(self) -> Any:
        if self._rh is None:
            self._rh = self._loader()
        return self._rh

    def login(self, username: str, password: str, mfa_code: str | None) -> dict[str, Any]:
        rh = self._lib()
        # The library insists on saving the session in a pickle file. Point it
        # at a private temporary folder that is deleted straight afterwards;
        # the login command saves what it needs in broker.yaml instead.
        with tempfile.TemporaryDirectory(prefix="brokerhelper-") as folder:
            os.chmod(folder, 0o700)
            try:
                reply = rh.login(
                    username=username,
                    password=password,
                    mfa_code=mfa_code or None,
                    store_session=True,
                    pickle_path=folder,
                )
            except Exception as exc:  # noqa: BLE001 - only the type is kept
                raise BrokerCallError(f"the login failed with {type(exc).__name__}") from None
        if not isinstance(reply, dict) or not reply.get("access_token"):
            raise BrokerAuthError("the broker did not accept the login, or it did not finish")
        return reply

    def _get(self, session: Session, url: str, params: dict[str, str] | None = None) -> dict[str, Any]:
        if not isinstance(url, str) or not url.startswith(API_ROOT):
            raise BrokerCallError("the broker linked to a host other than its API; not followed")
        rh = self._lib()
        try:
            res = rh.session.get(
                url,
                params=params,
                headers={"Authorization": session.header()},
                timeout=REQUEST_TIMEOUT,
                allow_redirects=False,
            )
        except Exception as exc:  # noqa: BLE001 - only the type is kept
            raise BrokerCallError(f"the request failed with {type(exc).__name__}") from None
        status = getattr(res, "status_code", None)
        if status in (401, 403):
            raise BrokerAuthError("the broker refused the saved session")
        if status != 200:
            raise BrokerCallError(f"the broker answered HTTP {status}")
        try:
            data = res.json()
        except Exception:  # noqa: BLE001
            raise BrokerCallError("the broker's answer was not JSON") from None
        if not isinstance(data, dict):
            raise BrokerCallError("the broker's answer was not a JSON object")
        return data

    def _pages(self, session: Session, url: str, params: dict[str, str] | None) -> list[dict[str, Any]]:
        items: list[dict[str, Any]] = []
        for _ in range(MAX_PAGES):
            page = self._get(session, url, params)
            results = page.get("results")
            if not isinstance(results, list):
                raise BrokerCallError("a list page from the broker had no results")
            items.extend(results)
            following = page.get("next")
            if not following:
                return items
            # The next link carries its own query, cursor included.
            url, params = following, None
        raise BrokerCallError(f"the broker's list ran past {MAX_PAGES} pages")

    def accounts(self, session: Session) -> list[dict[str, Any]]:
        return self._pages(session, self._lib().accounts_url(), None)

    def positions(self, session: Session, account_number: str) -> list[dict[str, Any]]:
        url = self._lib().positions_url(account_number)
        return self._pages(session, url, {"nonzero": "true"})

    def instrument(self, session: Session, url: str) -> dict[str, Any]:
        return self._get(session, url)
