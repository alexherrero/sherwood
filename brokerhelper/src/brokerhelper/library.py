"""The thin wrapper around the unofficial broker library.

This module is the only place the helper reaches the library (robin_stocks,
pinned in pyproject.toml). Everything else in the helper talks to the
``Library`` protocol below, and the tests replace it with a fake, so no test
ever imports the real library or touches the network.

The wrapper uses the library for what is hard to do by hand: the login, with
its second factor and device approval. For every other call it borrows only
the library's HTTP session (its headers) and two of its URL builders, and makes
each request itself, because the library's own helpers swallow HTTP errors,
can return a partial list without saying so, and send prices as floats. Here
every request has a timeout, no redirect is followed, the session is sent only
to the broker's own API host, and nothing is ever retried.

Two requests change something at the broker: sending an order (a POST to the
order list) and calling one off (a POST to that order's cancel link). They go
through ``_post``, which refuses any other address, and the order routes are
written out below rather than borrowed, so a test can pin exactly where a POST
may go. Nothing here sends a DELETE, a PUT or a PATCH, and nothing moves money.

Errors raised from here carry only text the wrapper wrote itself, never text
from the library, an exception or the broker, so no secret can ride along.
"""

from __future__ import annotations

import os
import re
import tempfile
import uuid
from dataclasses import dataclass
from types import SimpleNamespace
from typing import Any, Callable, Protocol

# The only host the session token is ever sent to.
API_ROOT = "https://api.robinhood.com/"

# Seconds to wait for each HTTP request.
REQUEST_TIMEOUT = 10.0

# A paged list longer than this is refused rather than followed for ever.
MAX_PAGES = 50

# The order routes, as the pinned library's urls.py writes them. These and
# nothing else may receive a POST.
ORDERS_URL = API_ROOT + "orders/"
INSTRUMENTS_URL = API_ROOT + "instruments/"

_ORDER_ID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")
_CANCEL_URL = re.compile(re.escape(ORDERS_URL) + _ORDER_ID.pattern + "/cancel/")

# HTTP answers a POST may succeed with. The broker answers 200 or 201 when it
# takes an order; anything else is a failed call.
_POST_OK = (200, 201)

# The namespace every client reference is made in: the version-5 UUID of the
# name below in the standard URL namespace, which is
# b2c9bd9c-91ea-56fa-8a48-f93a5fccfc5f written out (a test pins it). It is
# fixed for ever: changing it would make every order already sent unfindable.
CLIENT_REF_NAME = "https://github.com/alexherrero/sherwood/broker/client-ref"
CLIENT_REF_NAMESPACE = uuid.uuid5(uuid.NAMESPACE_URL, CLIENT_REF_NAME)


def client_ref(key: str) -> str:
    """The broker's client reference for an order with idempotency key ``key``.

    The broker wants a UUID, and sherwood's key is a longer hex digest, so the
    reference is the version-5 (SHA-1, name-based) UUID of the key's UTF-8
    bytes in CLIENT_REF_NAMESPACE. The same key always gives the same
    reference, so a second send of one order carries the reference of the
    first, and the broker can match the two. The derivation is one way: to
    match a reference the broker lists, derive it from each key you hold.
    """
    return str(uuid.uuid5(CLIENT_REF_NAMESPACE, key))


def order_url(order_id: str) -> str:
    """The broker's address for one order, by the broker's own id."""
    if not isinstance(order_id, str) or not _ORDER_ID.fullmatch(order_id):
        raise BrokerCallError("the broker's order id is not a UUID")
    return f"{ORDERS_URL}{order_id}/"


def cancel_url(order_id: str) -> str:
    """The address that calls off one order, by the broker's own id."""
    return order_url(order_id) + "cancel/"


def account_url(account_number: str) -> str:
    """The broker's address for one account, as an order names it."""
    return f"{API_ROOT}accounts/{account_number}/"


def portfolio_url(account_number: str) -> str:
    """The broker's address for one account's portfolio record (its value)."""
    return f"{API_ROOT}portfolios/{account_number}/"


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
        "the broker's order id is not a UUID",
        "an order may be sent only to the broker's order routes; not sent",
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

    def instruments_for(self, session: Session, symbol: str) -> list[dict[str, Any]]:
        """The instrument records the broker lists for a ticker symbol."""
        ...

    def portfolio(self, session: Session, account_number: str) -> dict[str, Any]:
        """The account's portfolio record, which holds the account's value."""
        ...

    def orders(self, session: Session, account_number: str) -> list[dict[str, Any]]:
        """Every stock order record of the account, every page."""
        ...

    def order_by_ref(self, session: Session, account_number: str, ref: str) -> dict[str, Any] | None:
        """The order record whose client reference is ``ref``, or None.

        Reads the account's order list page by page and stops at the first
        match, so a recent order costs one page.
        """
        ...

    def place_order(self, session: Session, payload: dict[str, Any]) -> dict[str, Any]:
        """Send one order, exactly once, and return the broker's order record."""
        ...

    def cancel_order(self, session: Session, order_id: str) -> None:
        """Ask the broker, exactly once, to call off one order by its id."""
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
        _check_status(res, (200,))
        return _json_object(res)

    def _post(self, session: Session, url: str, payload: dict[str, Any] | None) -> Any:
        """The one way anything is sent to the broker: once, to an order route.

        The same rules as ``_get``: the session goes only to the API host, with
        a timeout, no redirect is followed, and the request is never repeated.
        On top of that, the address must be the order list or one order's
        cancel link, and the body is JSON with every decimal as a string.
        """
        if not isinstance(url, str) or not (url == ORDERS_URL or _CANCEL_URL.fullmatch(url)):
            raise BrokerCallError("an order may be sent only to the broker's order routes; not sent")
        rh = self._lib()
        headers = {"Authorization": session.header()}
        if payload is not None:
            # The library's session says form-encoded by default; this body is JSON.
            headers["Content-Type"] = "application/json"
        try:
            res = rh.session.post(
                url,
                json=payload,
                headers=headers,
                timeout=REQUEST_TIMEOUT,
                allow_redirects=False,
            )
        except Exception as exc:  # noqa: BLE001 - only the type is kept
            raise BrokerCallError(f"the request failed with {type(exc).__name__}") from None
        _check_status(res, _POST_OK)
        return res

    def _pages(self, session: Session, url: str, params: dict[str, str] | None) -> list[dict[str, Any]]:
        items: list[dict[str, Any]] = []
        for page in self._page_iter(session, url, params):
            items.extend(page)
        return items

    def _page_iter(self, session: Session, url: str, params: dict[str, str] | None):
        for _ in range(MAX_PAGES):
            page = self._get(session, url, params)
            results = page.get("results")
            if not isinstance(results, list):
                raise BrokerCallError("a list page from the broker had no results")
            yield results
            following = page.get("next")
            if not following:
                return
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

    def instruments_for(self, session: Session, symbol: str) -> list[dict[str, Any]]:
        return self._pages(session, INSTRUMENTS_URL, {"symbol": symbol})

    def portfolio(self, session: Session, account_number: str) -> dict[str, Any]:
        return self._get(session, portfolio_url(account_number))

    def orders(self, session: Session, account_number: str) -> list[dict[str, Any]]:
        return self._pages(session, ORDERS_URL, {"account_numbers": account_number})

    def order_by_ref(self, session: Session, account_number: str, ref: str) -> dict[str, Any] | None:
        for page in self._page_iter(session, ORDERS_URL, {"account_numbers": account_number}):
            for record in page:
                if isinstance(record, dict) and record.get("ref_id") == ref:
                    return record
        return None

    def place_order(self, session: Session, payload: dict[str, Any]) -> dict[str, Any]:
        return _json_object(self._post(session, ORDERS_URL, payload))

    def cancel_order(self, session: Session, order_id: str) -> None:
        # The broker's answer to a cancel carries nothing the helper needs;
        # only its HTTP status counts.
        self._post(session, cancel_url(order_id), None)


def _check_status(res: Any, ok: tuple[int, ...]) -> None:
    status = getattr(res, "status_code", None)
    if status in (401, 403):
        raise BrokerAuthError("the broker refused the saved session")
    if status not in ok:
        raise BrokerCallError(f"the broker answered HTTP {status}")


def _json_object(res: Any) -> dict[str, Any]:
    try:
        data = res.json()
    except Exception:  # noqa: BLE001
        raise BrokerCallError("the broker's answer was not JSON") from None
    if not isinstance(data, dict):
        raise BrokerCallError("the broker's answer was not a JSON object")
    return data
