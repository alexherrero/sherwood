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
    INSTRUMENTS_URL,
    MAX_PAGES,
    ORDERS_URL,
    REQUEST_TIMEOUT,
    BrokerAuthError,
    BrokerCallError,
    RobinStocks,
    Session,
    cancel_url,
    client_ref,
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

    def post(self, url, json=None, headers=None, timeout=None, allow_redirects=True):
        self.requests.append(
            {"method": "POST", "url": url, "json": json, "headers": headers, "timeout": timeout,
             "allow_redirects": allow_redirects}
        )
        answer = self.table[("POST", url)]
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


# --- The order and account calls ----------------------------------------------

ORDERS_PARAMS = (("account_numbers", fakes.ACCOUNT),)
PAYLOAD = {"symbol": "ZZA", "quantity": "1", "price": "12.34", "ref_id": client_ref(fakes.KEY_1)}
ORDER_ID = fakes.order_id(1)
CANCEL = f"{ORDERS_URL}{ORDER_ID}/cancel/"


def test_the_order_routes_are_the_brokers():
    assert ORDERS_URL == "https://api.robinhood.com/orders/"
    assert INSTRUMENTS_URL == "https://api.robinhood.com/instruments/"
    assert cancel_url(ORDER_ID) == CANCEL


def test_place_order_posts_the_payload_once_as_json_with_the_session():
    record = fakes.order(1, "unconfirmed", PAYLOAD["ref_id"])
    lib, http, _ = make({("POST", ORDERS_URL): Response(201, record)})
    assert lib.place_order(SESSION, PAYLOAD) == record
    assert http.requests == [
        {
            "method": "POST",
            "url": ORDERS_URL,
            "json": PAYLOAD,
            "headers": {"Authorization": f"Bearer {fakes.ACCESS_TOKEN}", "Content-Type": "application/json"},
            "timeout": REQUEST_TIMEOUT,
            "allow_redirects": False,
        }
    ]


def test_a_200_is_also_a_taken_order():
    record = fakes.order(1, "unconfirmed", PAYLOAD["ref_id"])
    lib, _, _ = make({("POST", ORDERS_URL): Response(200, record)})
    assert lib.place_order(SESSION, PAYLOAD) == record


@pytest.mark.parametrize("status", [401, 403])
def test_a_refused_session_on_a_post_is_an_auth_error_sent_once(status):
    lib, http, _ = make({("POST", ORDERS_URL): Response(status, {"detail": "x"})})
    with pytest.raises(BrokerAuthError):
        lib.place_order(SESSION, PAYLOAD)
    assert len(http.requests) == 1


@pytest.mark.parametrize("status", [202, 204, 301, 302, 400, 404, 429, 500, 503, None])
def test_any_other_answer_to_a_post_is_a_call_error_sent_once(status):
    lib, http, _ = make({("POST", ORDERS_URL): Response(status, {})})
    with pytest.raises(BrokerCallError, match=f"HTTP {status}"):
        lib.place_order(SESSION, PAYLOAD)
    assert len(http.requests) == 1


@pytest.mark.parametrize("failure", [TimeoutError("read timed out"), ConnectionError(f"lost {fakes.ACCESS_TOKEN}")])
def test_a_post_that_fails_in_flight_keeps_only_the_type_and_is_not_retried(failure):
    lib, http, _ = make({("POST", ORDERS_URL): failure})
    with pytest.raises(BrokerCallError) as caught:
        lib.place_order(SESSION, PAYLOAD)
    assert str(caught.value) == f"the request failed with {type(failure).__name__}"
    assert fakes.ACCESS_TOKEN not in str(caught.value)
    assert caught.value.__cause__ is None
    assert len(http.requests) == 1


@pytest.mark.parametrize("response", [Response(201, bad_json=True), Response(201, ["x"]), Response(201, None)])
def test_a_taken_order_must_answer_with_a_json_object(response):
    lib, http, _ = make({("POST", ORDERS_URL): response})
    with pytest.raises(BrokerCallError):
        lib.place_order(SESSION, PAYLOAD)
    assert len(http.requests) == 1


def test_cancel_order_posts_once_to_the_orders_cancel_link_with_no_body():
    lib, http, _ = make({("POST", CANCEL): Response(200, bad_json=True)})
    assert lib.cancel_order(SESSION, ORDER_ID) is None
    assert http.requests == [
        {
            "method": "POST",
            "url": CANCEL,
            "json": None,
            "headers": {"Authorization": f"Bearer {fakes.ACCESS_TOKEN}"},
            "timeout": REQUEST_TIMEOUT,
            "allow_redirects": False,
        }
    ]


@pytest.mark.parametrize("status,error", [(401, BrokerAuthError), (400, BrokerCallError), (500, BrokerCallError)])
def test_a_failed_cancel_is_an_error_sent_once(status, error):
    lib, http, _ = make({("POST", CANCEL): Response(status, {})})
    with pytest.raises(error):
        lib.cancel_order(SESSION, ORDER_ID)
    assert len(http.requests) == 1


@pytest.mark.parametrize(
    "order_id",
    ["", "../accounts", "00000000-0000-0000-0000-00000000000G", f"{ORDER_ID}/../x", "0000000A-0000-0000-0000-000000000001", None],
)
def test_a_cancel_with_a_strange_id_is_never_sent(order_id):
    lib, http, _ = make({})
    with pytest.raises(BrokerCallError, match="not a UUID"):
        lib.cancel_order(SESSION, order_id)
    assert http.requests == []


@pytest.mark.parametrize(
    "url",
    [
        f"{API_ROOT}accounts/{fakes.ACCOUNT}/",
        f"{API_ROOT}ach/transfers/",
        f"{API_ROOT}orders/{ORDER_ID}/",
        f"{API_ROOT}orders/{ORDER_ID}/cancel/extra",
        f"{API_ROOT}options/orders/",
        f"{API_ROOT}orders",
        "https://elsewhere.example.com/orders/",
        "http://api.robinhood.com/orders/",
        None,
    ],
)
def test_a_post_anywhere_but_the_order_routes_is_never_sent(url):
    lib, http, _ = make({})
    with pytest.raises(BrokerCallError, match="order routes"):
        lib._post(SESSION, url, {})
    assert http.requests == []


def test_orders_reads_every_page_for_the_account():
    page2 = f"{ORDERS_URL}?account_numbers={fakes.ACCOUNT}&cursor=abc"
    lib, http, _ = make(
        {
            (ORDERS_URL, ORDERS_PARAMS): Response(200, {"results": fakes.ORDERS[:2], "next": page2}),
            (page2, ()): Response(200, {"results": fakes.ORDERS[2:], "next": None}),
        }
    )
    assert lib.orders(SESSION, fakes.ACCOUNT) == fakes.ORDERS
    assert [r["url"] for r in http.requests] == [ORDERS_URL, page2]
    assert all(r["allow_redirects"] is False and r["timeout"] == REQUEST_TIMEOUT for r in http.requests)


def test_order_by_ref_stops_at_the_page_that_holds_the_order():
    page2 = f"{ORDERS_URL}?cursor=2"
    lib, http, _ = make(
        {
            (ORDERS_URL, ORDERS_PARAMS): Response(200, {"results": fakes.ORDERS[2:], "next": page2}),
            (page2, ()): Response(200, {"results": fakes.ORDERS[:2], "next": f"{ORDERS_URL}?cursor=3"}),
        }
    )
    found = lib.order_by_ref(SESSION, fakes.ACCOUNT, client_ref(fakes.KEY_1))
    assert found["id"] == fakes.order_id(1)
    # The third page was never asked for.
    assert [r["url"] for r in http.requests] == [ORDERS_URL, page2]


def test_order_by_ref_answers_none_after_the_last_page():
    lib, http, _ = make({(ORDERS_URL, ORDERS_PARAMS): Response(200, {"results": fakes.ORDERS, "next": None})})
    assert lib.order_by_ref(SESSION, fakes.ACCOUNT, client_ref("f" * 64)) is None
    assert len(http.requests) == 1


def test_order_by_ref_does_not_follow_an_endless_list():
    table = {}
    url, params = ORDERS_URL, ORDERS_PARAMS
    for n in range(MAX_PAGES + 1):
        following = f"{ORDERS_URL}?cursor={n}"
        table[(url, params)] = Response(200, {"results": [], "next": following})
        url, params = following, ()
    lib, http, _ = make(table)
    with pytest.raises(BrokerCallError, match="pages"):
        lib.order_by_ref(SESSION, fakes.ACCOUNT, client_ref(fakes.KEY_1))
    assert len(http.requests) == MAX_PAGES


def test_order_by_ref_fails_on_a_failed_page_rather_than_answering_none():
    page2 = f"{ORDERS_URL}?cursor=2"
    lib, _, _ = make(
        {
            (ORDERS_URL, ORDERS_PARAMS): Response(200, {"results": [], "next": page2}),
            (page2, ()): Response(500, {}),
        }
    )
    with pytest.raises(BrokerCallError):
        lib.order_by_ref(SESSION, fakes.ACCOUNT, client_ref(fakes.KEY_1))


def test_instruments_for_and_portfolio():
    portfolio = f"{API_ROOT}portfolios/{fakes.ACCOUNT}/"
    lib, http, _ = make(
        {
            (INSTRUMENTS_URL, (("symbol", "ZZA"),)): Response(200, {"results": fakes.SYMBOL_INSTRUMENTS["ZZA"], "next": None}),
            (portfolio, ()): Response(200, fakes.portfolio()),
        }
    )
    assert lib.instruments_for(SESSION, "ZZA") == fakes.SYMBOL_INSTRUMENTS["ZZA"]
    assert lib.portfolio(SESSION, fakes.ACCOUNT)["equity"] == "5678.9000"
    assert all("method" not in r for r in http.requests)


@pytest.mark.parametrize(
    "message",
    ["the broker's order id is not a UUID", "an order may be sent only to the broker's order routes; not sent"],
)
def test_the_new_messages_are_passed_on(message):
    assert BrokerCallError(message).safe_message() == message
