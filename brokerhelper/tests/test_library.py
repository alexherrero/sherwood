"""The wrapper's own logic, with a stand-in for the library's parts.

The real library is never imported here: RobinStocks takes a loader, and these
tests hand it a namespace holding a fake HTTP session, fake URL builders and a
fake login, all in the shapes the wrapper uses.
"""

import os
from types import SimpleNamespace

import pytest

import fakes
from brokerhelper.library import (
    API_ROOT,
    MAX_PAGES,
    REQUEST_TIMEOUT,
    BrokerAuthError,
    BrokerCallError,
    RobinStocks,
    Session,
)

SESSION = Session("Bearer", fakes.ACCESS_TOKEN)


class Response:
    def __init__(self, status, body=None, bad_json=False):
        self.status_code = status
        self._body = body
        self._bad_json = bad_json

    def json(self):
        if self._bad_json:
            raise ValueError("Expecting value: line 1 column 1")
        return self._body


class HTTP:
    """A fake requests session: answers from a table keyed by (url, params)."""

    def __init__(self, table):
        self.table = table
        self.requests = []

    def get(self, url, params=None, headers=None, timeout=None, allow_redirects=True):
        self.requests.append(
            {"url": url, "params": params, "headers": headers, "timeout": timeout, "allow_redirects": allow_redirects}
        )
        answer = self.table[(url, tuple(sorted((params or {}).items())))]
        if isinstance(answer, BaseException):
            raise answer
        return answer


def make(table=None, login=None):
    http = HTTP(table or {})
    parts = SimpleNamespace(
        login=login,
        session=http,
        accounts_url=lambda number=None: f"{API_ROOT}accounts/?default_to_all_accounts=true",
        positions_url=lambda number=None: f"{API_ROOT}positions/?account_number={number}",
    )
    loads = []

    def loader():
        loads.append(1)
        return parts

    return RobinStocks(loader=loader), http, loads


POSITIONS_URL = f"{API_ROOT}positions/?account_number={fakes.ACCOUNT}"
NONZERO = (("nonzero", "true"),)


def test_the_library_is_loaded_only_on_first_use():
    lib, http, loads = make({(POSITIONS_URL, NONZERO): Response(200, {"results": [], "next": None})})
    assert loads == []
    lib.positions(SESSION, fakes.ACCOUNT)
    lib.positions(SESSION, fakes.ACCOUNT)
    assert loads == [1]


def test_positions_follow_every_page_with_the_session_and_a_timeout():
    page2 = f"{API_ROOT}positions/?account_number={fakes.ACCOUNT}&cursor=abc&nonzero=true"
    lib, http, _ = make(
        {
            (POSITIONS_URL, NONZERO): Response(200, {"results": fakes.POSITIONS[:2], "next": page2}),
            (page2, ()): Response(200, {"results": fakes.POSITIONS[2:], "next": None}),
        }
    )
    assert lib.positions(SESSION, fakes.ACCOUNT) == fakes.POSITIONS
    assert [r["url"] for r in http.requests] == [POSITIONS_URL, page2]
    for r in http.requests:
        assert r["headers"] == {"Authorization": f"Bearer {fakes.ACCESS_TOKEN}"}
        assert r["timeout"] == REQUEST_TIMEOUT
        assert r["allow_redirects"] is False


def test_accounts_and_instruments():
    accounts_url = f"{API_ROOT}accounts/?default_to_all_accounts=true"
    url = fakes.instrument_url(2)
    lib, _, _ = make(
        {
            (accounts_url, ()): Response(200, {"results": fakes.ACCOUNTS, "next": None}),
            (url, ()): Response(200, fakes.INSTRUMENTS[url]),
        }
    )
    assert lib.accounts(SESSION) == fakes.ACCOUNTS
    assert lib.instrument(SESSION, url)["symbol"] == "zza"


@pytest.mark.parametrize("status", [401, 403])
def test_a_refused_session_is_an_auth_error(status):
    lib, http, _ = make({(POSITIONS_URL, NONZERO): Response(status, {"detail": "x"})})
    with pytest.raises(BrokerAuthError):
        lib.positions(SESSION, fakes.ACCOUNT)
    assert len(http.requests) == 1


@pytest.mark.parametrize("status", [301, 404, 429, 500, 503])
def test_any_other_status_is_a_call_error_and_is_not_retried(status):
    lib, http, _ = make({(POSITIONS_URL, NONZERO): Response(status, {})})
    with pytest.raises(BrokerCallError, match=f"HTTP {status}"):
        lib.positions(SESSION, fakes.ACCOUNT)
    assert len(http.requests) == 1


def test_a_partial_list_is_an_error_not_a_shorter_list():
    page2 = f"{API_ROOT}positions/?cursor=abc"
    lib, http, _ = make(
        {
            (POSITIONS_URL, NONZERO): Response(200, {"results": fakes.POSITIONS[:1], "next": page2}),
            (page2, ()): Response(500, {}),
        }
    )
    with pytest.raises(BrokerCallError):
        lib.positions(SESSION, fakes.ACCOUNT)


def test_a_network_failure_keeps_only_the_exception_type():
    lib, _, _ = make({(POSITIONS_URL, NONZERO): ConnectionError(f"failed with {fakes.ACCESS_TOKEN}")})
    with pytest.raises(BrokerCallError) as caught:
        lib.positions(SESSION, fakes.ACCOUNT)
    assert str(caught.value) == "the request failed with ConnectionError"
    assert caught.value.__cause__ is None
    assert caught.value.__suppress_context__ is True


@pytest.mark.parametrize("body", [None, [], "text"])
def test_an_answer_that_is_not_an_object_or_list_page_is_refused(body):
    lib, _, _ = make({(POSITIONS_URL, NONZERO): Response(200, body)})
    with pytest.raises(BrokerCallError):
        lib.positions(SESSION, fakes.ACCOUNT)


def test_an_answer_that_is_not_json_is_refused():
    lib, _, _ = make({(POSITIONS_URL, NONZERO): Response(200, bad_json=True)})
    with pytest.raises(BrokerCallError, match="not JSON"):
        lib.positions(SESSION, fakes.ACCOUNT)


@pytest.mark.parametrize(
    "link",
    [
        "https://elsewhere.example.com/positions/?cursor=abc",
        "http://api.robinhood.com/positions/?cursor=abc",
        "https://api.robinhood.com.example.com/positions/",
    ],
)
def test_the_session_is_never_sent_to_another_host(link):
    lib, http, _ = make({(POSITIONS_URL, NONZERO): Response(200, {"results": [], "next": link})})
    with pytest.raises(BrokerCallError, match="another host|other than"):
        lib.positions(SESSION, fakes.ACCOUNT)
    assert [r["url"] for r in http.requests] == [POSITIONS_URL]
    with pytest.raises(BrokerCallError):
        lib.instrument(SESSION, link)


def test_an_endless_list_is_refused():
    table = {}
    url, params = POSITIONS_URL, NONZERO
    for n in range(MAX_PAGES + 1):
        following = f"{API_ROOT}positions/?cursor={n}"
        table[(url, params)] = Response(200, {"results": [], "next": following})
        url, params = following, ()
    lib, http, _ = make(table)
    with pytest.raises(BrokerCallError, match="pages"):
        lib.positions(SESSION, fakes.ACCOUNT)
    assert len(http.requests) == MAX_PAGES


def test_login_keeps_the_library_pickle_in_a_private_folder_that_is_removed():
    seen = {}

    def fake_login(**kwargs):
        seen.update(kwargs)
        folder = kwargs["pickle_path"]
        seen["mode"] = os.stat(folder).st_mode & 0o777
        with open(os.path.join(folder, "robinhood.pickle"), "wb") as f:
            f.write(b"session")
        return dict(fakes.LOGIN_REPLY)

    lib, _, _ = make(login=fake_login)
    reply = lib.login("user@example.com", "made-up-password", None)
    assert reply["access_token"] == fakes.ACCESS_TOKEN
    assert seen["username"] == "user@example.com"
    assert seen["password"] == "made-up-password"
    assert seen["mfa_code"] is None
    assert seen["store_session"] is True
    assert seen["mode"] == 0o700
    assert not os.path.exists(seen["pickle_path"])


@pytest.mark.parametrize("reply", [None, {}, {"access_token": ""}, "Login failed"])
def test_a_login_without_a_token_is_refused(reply):
    lib, _, _ = make(login=lambda **kwargs: reply)
    with pytest.raises(BrokerAuthError):
        lib.login("user@example.com", "made-up-password", "123456")


def test_a_login_that_raises_keeps_only_the_exception_type():
    def fake_login(**kwargs):
        raise KeyError(kwargs["password"])

    lib, _, _ = make(login=fake_login)
    with pytest.raises(BrokerCallError) as caught:
        lib.login("user@example.com", "made-up-password", None)
    assert "made-up-password" not in str(caught.value)
    assert caught.value.__cause__ is None


def test_session_repr_shows_no_token():
    assert fakes.ACCESS_TOKEN not in repr(SESSION)
    assert fakes.ACCESS_TOKEN not in str(SESSION)


@pytest.mark.parametrize(
    "message",
    [
        "the broker answered HTTP 503",
        "the request failed with ConnectionError",
        "the login failed with KeyError",
        "the broker's list ran past 50 pages",
        "the broker's answer was not JSON",
    ],
)
def test_the_wrappers_own_messages_are_passed_on(message):
    assert BrokerCallError(message).safe_message() == message


@pytest.mark.parametrize(
    "message",
    [
        f"the broker answered HTTP 503 {fakes.ACCESS_TOKEN}",
        f"the request failed with {fakes.ACCESS_TOKEN}",
        f"failed: {fakes.ACCESS_TOKEN}",
        "",
    ],
)
def test_any_other_message_is_replaced(message):
    assert BrokerCallError(message).safe_message() == "the broker call failed"
