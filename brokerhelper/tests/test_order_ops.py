"""The order and account operations, against the fake library.

Every broker answer here is a made-up record in the broker's shape. Each test
also checks how many times the fake was asked, because a call that changes
something at the broker must be made exactly once and never repeated.
"""

import json
import uuid
from datetime import timedelta

import pytest

import fakes
from brokerhelper import brokerfile
from brokerhelper.library import (
    CLIENT_REF_NAME,
    CLIENT_REF_NAMESPACE,
    BrokerAuthError,
    BrokerCallError,
    client_ref,
)
from brokerhelper.ops import STATES, Broker, broker_ops
from brokerhelper.server import handle

ORDER_OPS = ("place", "cancel", "lookup", "open_orders", "account")


@pytest.fixture
def data_dir(tmp_path):
    brokerfile.write(tmp_path, fakes.saved_session())
    return tmp_path


@pytest.fixture
def lib():
    return fakes.FakeLibrary()


def call(lib, data_dir, op, args=None):
    broker = Broker(library=lib, data_dir=data_dir, now=lambda: fakes.NOW)
    request = {"id": "1", "op": op}
    if args is not None:
        request["args"] = args
    raw = handle(json.dumps(request).encode(), broker_ops(broker))
    reply = json.loads(raw)
    assert reply["id"] == "1"
    # No float ever crosses the socket.
    assert_no_float(reply)
    return reply


def assert_no_float(value):
    if isinstance(value, dict):
        for v in value.values():
            assert_no_float(v)
    elif isinstance(value, list):
        for v in value:
            assert_no_float(v)
    else:
        assert not isinstance(value, float), value


def result_of(reply):
    assert reply["ok"] is True, reply
    return reply["result"]


def error_of(reply):
    assert reply["ok"] is False, reply
    return reply["error"]["code"], reply["error"]["message"]


def names(lib):
    return [c[0] for c in lib.calls]


BUY = {"ref": fakes.KEY_1, "symbol": "ZZA", "side": "buy", "type": "day_limit_buy", "quantity": "10", "price": "12.34"}
STOP = {"ref": fakes.KEY_2, "symbol": "ZZB", "side": "sell", "type": "stop", "quantity": "2.5", "price": "9.5"}
AT_OPEN = {"ref": fakes.KEY_3, "symbol": "ZZB", "side": "sell", "type": "sell_at_open", "quantity": "3"}


# --- The client reference -----------------------------------------------------


def test_the_client_reference_is_a_fixed_uuid5_of_the_key():
    # Pinned, so the Go side can derive the same reference and a change here
    # can never strand an order already sent.
    assert CLIENT_REF_NAMESPACE == uuid.uuid5(uuid.NAMESPACE_URL, CLIENT_REF_NAME)
    assert str(CLIENT_REF_NAMESPACE) == "b2c9bd9c-91ea-56fa-8a48-f93a5fccfc5f"
    assert client_ref("a" * 64) == "bfc5ea18-c366-55c9-8118-5fae949d99bc"
    assert client_ref(fakes.KEY_1) == client_ref(fakes.KEY_1)
    assert client_ref(fakes.KEY_1) != client_ref(fakes.KEY_2)
    assert uuid.UUID(client_ref(fakes.KEY_1)).version == 5


# --- place --------------------------------------------------------------------


def test_a_day_limit_buy_is_sent_once_as_a_limit_buy_good_for_the_day(lib, data_dir):
    lib.orders_data = []
    result = result_of(call(lib, data_dir, "place", BUY))
    sent = [c[1] for c in lib.calls if c[0] == "place_order"]
    assert sent == [
        {
            "account": f"{fakes.API}accounts/{fakes.ACCOUNT}/",
            "instrument": fakes.instrument_url(2),
            "symbol": "ZZA",
            "side": "buy",
            "quantity": "10",
            "type": "limit",
            "trigger": "immediate",
            "time_in_force": "gfd",
            "market_hours": "regular_hours",
            "extended_hours": False,
            "ref_id": client_ref(fakes.KEY_1),
            "order_form_version": 4,
            "price": "12.34",
        }
    ]
    assert names(lib) == ["instruments_for", "place_order"]
    assert result == {
        "ref": fakes.KEY_1,
        "client_ref": client_ref(fakes.KEY_1),
        "broker_id": fakes.order_id(100),
        "status": "accepted",
        "filled": "0",
        "fill_price": None,
        "detail": "",
    }


def test_a_stop_is_a_standing_stop_sell(lib, data_dir):
    lib.orders_data = []
    result_of(call(lib, data_dir, "place", STOP))
    [sent] = [c[1] for c in lib.calls if c[0] == "place_order"]
    assert sent["side"] == "sell"
    assert (sent["type"], sent["trigger"], sent["time_in_force"]) == ("market", "stop", "gtc")
    assert sent["stop_price"] == "9.5"
    assert sent["quantity"] == "2.5"
    assert "price" not in sent
    assert sent["ref_id"] == client_ref(fakes.KEY_2)


def test_a_sell_at_open_is_a_market_sell_good_for_the_day_with_no_price(lib, data_dir):
    lib.orders_data = []
    result_of(call(lib, data_dir, "place", AT_OPEN))
    [sent] = [c[1] for c in lib.calls if c[0] == "place_order"]
    assert (sent["side"], sent["type"], sent["trigger"], sent["time_in_force"]) == ("sell", "market", "immediate", "gfd")
    assert "price" not in sent and "stop_price" not in sent


def test_a_price_of_null_is_the_same_as_none_for_sell_at_open(lib, data_dir):
    lib.orders_data = []
    result_of(call(lib, data_dir, "place", dict(AT_OPEN, price=None)))


def test_the_same_key_always_sends_the_same_reference(lib, data_dir):
    lib.orders_data = []
    call(lib, data_dir, "place", BUY)
    lib2 = fakes.FakeLibrary(orders=[])
    call(lib2, data_dir, "place", BUY)
    first = [c[1]["ref_id"] for c in lib.calls if c[0] == "place_order"]
    second = [c[1]["ref_id"] for c in lib2.calls if c[0] == "place_order"]
    assert first == second == [client_ref(fakes.KEY_1)]


def test_quantities_and_prices_go_out_exactly(lib, data_dir):
    lib.orders_data = []
    call(lib, data_dir, "place", dict(BUY, quantity="0.123456", price="0.0123"))
    [sent] = [c[1] for c in lib.calls if c[0] == "place_order"]
    assert (sent["quantity"], sent["price"]) == ("0.123456", "0.0123")
    for value in sent.values():
        assert not isinstance(value, float)


@pytest.mark.parametrize(
    "args,why",
    [
        ({k: v for k, v in BUY.items() if k != "ref"}, "needs ref"),
        (dict(BUY, ref=""), "ref"),
        (dict(BUY, ref="has space"), "ref"),
        (dict(BUY, ref="x" * 129), "ref"),
        (dict(BUY, ref=7), "ref"),
        (dict(BUY, symbol="zza"), "symbol"),
        (dict(BUY, symbol="ZZA;"), "symbol"),
        (dict(BUY, symbol=None), "symbol"),
        (dict(BUY, side="sell"), "must be a buy"),
        (dict(STOP, side="buy"), "must be a sell"),
        (dict(AT_OPEN, side="buy"), "must be a sell"),
        (dict(BUY, type="market"), "type"),
        (dict(BUY, type="trailing_stop"), "type"),
        (dict(BUY, quantity="0"), "quantity"),
        (dict(BUY, quantity="-1"), "quantity"),
        (dict(BUY, quantity="NaN"), "quantity"),
        (dict(BUY, quantity="Infinity"), "quantity"),
        (dict(BUY, quantity="ten"), "quantity"),
        (dict(BUY, quantity=10), "quantity"),
        (dict(BUY, quantity="1.1234567"), "quantity"),
        (dict(BUY, price="12.345"), "decimal places"),
        (dict(BUY, price="0.12345"), "decimal places"),
        (dict(BUY, price="0"), "price"),
        (dict(BUY, price=None), "needs a price"),
        ({k: v for k, v in BUY.items() if k != "price"}, "needs a price"),
        ({k: v for k, v in STOP.items() if k != "price"}, "needs a price"),
        (dict(AT_OPEN, price="10"), "takes no price"),
        (dict(BUY, session="2026-11-17"), "does not take"),
        (dict(BUY, account_number="FAKE0002"), "does not take"),
    ],
)
def test_a_malformed_order_is_refused_before_anything_is_asked(lib, data_dir, args, why):
    code, message = error_of(call(lib, data_dir, "place", args))
    assert code == "bad_request"
    assert why in message
    assert lib.calls == []


def test_a_float_price_is_refused_by_the_protocol(lib, data_dir):
    broker = Broker(library=lib, data_dir=data_dir, now=lambda: fakes.NOW)
    raw = b'{"id":"1","op":"place","args":' + json.dumps(BUY).replace('"12.34"', "12.34").encode() + b"}"
    reply = json.loads(handle(raw, broker_ops(broker)))
    assert reply["error"]["code"] == "bad_request"
    assert lib.calls == []


def test_a_symbol_the_broker_does_not_list_is_rejected_without_sending(lib, data_dir):
    result = result_of(call(lib, data_dir, "place", dict(BUY, symbol="ZZQ")))
    assert result["status"] == "rejected"
    assert result["broker_id"] is None
    assert result["ref"] == fakes.KEY_1
    assert "no stock" in result["detail"]
    assert names(lib) == ["instruments_for"]


def test_a_stock_that_cannot_be_traded_is_rejected_without_sending(lib, data_dir):
    lib.symbol_instruments["ZZA"][0]["tradeable"] = False
    result = result_of(call(lib, data_dir, "place", BUY))
    assert result["status"] == "rejected"
    assert "cannot be traded" in result["detail"]
    assert names(lib) == ["instruments_for"]


@pytest.mark.parametrize(
    "listed",
    [
        [fakes.instrument(2, "ZZA"), fakes.instrument(3, "ZZA")],
        [dict(fakes.instrument(2, "ZZA"), url="https://elsewhere.example.com/instruments/2/")],
        [dict(fakes.instrument(2, "ZZA"), url=None)],
    ],
)
def test_an_odd_instrument_list_is_a_bad_reply_and_nothing_is_sent(lib, data_dir, listed):
    lib.symbol_instruments["ZZA"] = listed
    code, _ = error_of(call(lib, data_dir, "place", BUY))
    assert code == "bad_broker_reply"
    assert "place_order" not in names(lib)


def test_a_lower_case_symbol_in_the_list_does_not_match(lib, data_dir):
    lib.symbol_instruments["ZZA"] = [fakes.instrument(2, "zza")]
    result = result_of(call(lib, data_dir, "place", BUY))
    assert result["status"] == "rejected"
    assert "place_order" not in names(lib)


@pytest.mark.parametrize(
    "state,status",
    [("unconfirmed", "accepted"), ("queued", "accepted"), ("confirmed", "accepted"),
     ("rejected", "rejected"), ("failed", "rejected"), ("filled", "filled")],
)
def test_the_answer_to_a_place_maps_the_brokers_state(lib, data_dir, state, status):
    reply = fakes.order(7, state, client_ref(fakes.KEY_1), reject_reason="made-up reason" if status == "rejected" else None)
    if state == "filled":
        reply.update(cumulative_quantity="10.00000000", average_price="12.3000")
    lib.place_reply = reply
    result = result_of(call(lib, data_dir, "place", BUY))
    assert result["status"] == status
    if status == "rejected":
        assert result["detail"] == "made-up reason"
    if state == "filled":
        assert (result["filled"], result["fill_price"]) == ("10", "12.3")
    assert names(lib).count("place_order") == 1


@pytest.mark.parametrize(
    "change",
    [
        {"ref_id": client_ref(fakes.KEY_2)},
        {"ref_id": None},
        {"state": "made_up_state"},
        {"state": None},
        {"id": "not-a-uuid"},
        {"cumulative_quantity": 0.0},
        {"cumulative_quantity": "-1"},
        {"account": f"{fakes.API}accounts/{fakes.OTHER_ACCOUNT}/"},
    ],
)
def test_a_malformed_answer_to_a_place_is_a_bad_reply_after_one_send(lib, data_dir, change):
    lib.place_reply = dict(fakes.order(7, "unconfirmed", client_ref(fakes.KEY_1)), **change)
    code, _ = error_of(call(lib, data_dir, "place", BUY))
    assert code == "bad_broker_reply"
    assert names(lib).count("place_order") == 1


def test_an_answer_that_is_not_an_object_is_a_bad_reply(lib, data_dir):
    lib.place_reply = ["not", "an", "object"]
    code, _ = error_of(call(lib, data_dir, "place", BUY))
    assert code == "bad_broker_reply"


@pytest.mark.parametrize("step", ["instruments_for", "place_order"])
@pytest.mark.parametrize(
    "failure,code",
    [
        (BrokerAuthError("refused"), "login_needed"),
        (BrokerCallError("the request failed with Timeout"), "broker_error"),
        (BrokerCallError("the request failed with ConnectionError"), "broker_error"),
        (BrokerCallError("the broker answered HTTP 400"), "broker_error"),
        (BrokerCallError("the broker answered HTTP 500"), "broker_error"),
        (BrokerCallError("the broker's answer was not JSON"), "broker_error"),
    ],
)
def test_every_failure_of_a_place_has_its_code_and_is_not_retried(lib, data_dir, step, failure, code):
    lib.fail[step] = failure
    got, message = error_of(call(lib, data_dir, "place", BUY))
    assert got == code
    assert message.startswith("place: ")
    assert names(lib).count(step) == 1
    assert names(lib).count("place_order") == (1 if step == "place_order" else 0)


def test_an_unexpected_exception_is_internal_and_not_retried(lib, data_dir):
    lib.fail["place_order"] = RuntimeError("boom")
    code, message = error_of(call(lib, data_dir, "place", BUY))
    assert code == "internal"
    assert "boom" not in message
    assert names(lib).count("place_order") == 1


# --- Every broker op needs a live session ----------------------------------------

OP_ARGS = {
    "place": BUY,
    "cancel": {"ref": fakes.KEY_1},
    "lookup": {"ref": fakes.KEY_1},
    "open_orders": None,
    "account": None,
}


@pytest.mark.parametrize("op", ORDER_OPS)
def test_no_login_is_login_needed_without_asking_the_broker(tmp_path, lib, op):
    code, _ = error_of(call(lib, tmp_path, op, OP_ARGS[op]))
    assert code == "login_needed"
    assert lib.calls == []


@pytest.mark.parametrize("op", ORDER_OPS)
def test_an_expired_session_is_login_needed_without_asking_the_broker(tmp_path, lib, op):
    brokerfile.write(tmp_path, fakes.saved_session(expires_at=fakes.NOW - timedelta(seconds=1)))
    code, _ = error_of(call(lib, tmp_path, op, OP_ARGS[op]))
    assert code == "login_needed"
    assert lib.calls == []


@pytest.mark.parametrize("op", ORDER_OPS)
def test_a_refused_session_is_login_needed(tmp_path, lib, op):
    brokerfile.write(tmp_path, fakes.saved_session(access_token="stale-token"))
    lib.orders_data = []
    code, _ = error_of(call(lib, tmp_path, op, OP_ARGS[op]))
    assert code == "login_needed"


# --- lookup -------------------------------------------------------------------


def test_lookup_finds_an_order_by_its_reference(lib, data_dir):
    result = result_of(call(lib, data_dir, "lookup", {"ref": fakes.KEY_2}))
    assert result == {
        "ref": fakes.KEY_2,
        "client_ref": client_ref(fakes.KEY_2),
        "broker_id": fakes.order_id(2),
        "status": "partially_filled",
        "filled": "4",
        "fill_price": "9.48",
        "detail": "",
    }
    assert lib.calls == [("order_by_ref", fakes.ACCOUNT, client_ref(fakes.KEY_2))]


@pytest.mark.parametrize("state,status", sorted(STATES.items()))
def test_every_broker_state_maps_to_an_adapter_status(lib, data_dir, state, status):
    lib.orders_data = [fakes.order(1, state, client_ref(fakes.KEY_1))]
    assert result_of(call(lib, data_dir, "lookup", {"ref": fakes.KEY_1}))["status"] == status


def test_the_statuses_are_exactly_the_adapters():
    assert set(STATES.values()) == {"accepted", "rejected", "partially_filled", "filled", "cancelled", "expired"}


def test_a_filled_order_without_an_average_price_has_no_fill_price(lib, data_dir):
    lib.orders_data = [fakes.order(1, "filled", client_ref(fakes.KEY_1), filled="10", average_price=None)]
    result = result_of(call(lib, data_dir, "lookup", {"ref": fakes.KEY_1}))
    assert (result["filled"], result["fill_price"]) == ("10", None)


def test_an_unfilled_order_has_no_fill_price_whatever_the_broker_says(lib, data_dir):
    lib.orders_data = [fakes.order(1, "confirmed", client_ref(fakes.KEY_1), average_price="12.0000")]
    assert result_of(call(lib, data_dir, "lookup", {"ref": fakes.KEY_1}))["fill_price"] is None


@pytest.mark.parametrize("average", ["0.0000", "-1", "abc", 12.3])
def test_a_bad_average_price_on_a_fill_is_a_bad_reply(lib, data_dir, average):
    lib.orders_data = [fakes.order(1, "filled", client_ref(fakes.KEY_1), filled="1", average_price=average)]
    code, _ = error_of(call(lib, data_dir, "lookup", {"ref": fakes.KEY_1}))
    assert code == "bad_broker_reply"


@pytest.mark.parametrize(
    "reason,detail",
    [
        ("made-up reason", "made-up reason"),
        (None, "the broker gave no reason it could pass on"),
        ("line\nbreak", "the broker gave no reason it could pass on"),
        ("x" * 201, "the broker gave no reason it could pass on"),
        (42, "the broker gave no reason it could pass on"),
    ],
)
def test_a_rejection_carries_a_safe_reason(lib, data_dir, reason, detail):
    lib.orders_data = [fakes.order(1, "rejected", client_ref(fakes.KEY_1), reject_reason=reason)]
    assert result_of(call(lib, data_dir, "lookup", {"ref": fakes.KEY_1}))["detail"] == detail


def test_a_voided_order_is_cancelled_and_says_so(lib, data_dir):
    lib.orders_data = [fakes.order(1, "voided", client_ref(fakes.KEY_1))]
    result = result_of(call(lib, data_dir, "lookup", {"ref": fakes.KEY_1}))
    assert (result["status"], result["detail"]) == ("cancelled", "the broker voided the order")


def test_an_order_the_broker_does_not_list_is_order_not_found(lib, data_dir):
    code, message = error_of(call(lib, data_dir, "lookup", {"ref": "f" * 64}))
    assert code == "order_not_found"
    assert message == "lookup: the broker lists no order with this reference"
    assert names(lib) == ["order_by_ref"]


@pytest.mark.parametrize(
    "change",
    [
        {"account": f"{fakes.API}accounts/{fakes.OTHER_ACCOUNT}/"},
        {"account": 5},
        {"state": "made_up_state"},
        {"cumulative_quantity": None},
    ],
)
def test_a_malformed_order_record_is_a_bad_reply(lib, data_dir, change):
    lib.orders_data = [dict(fakes.order(1, "confirmed", client_ref(fakes.KEY_1)), **change)]
    code, _ = error_of(call(lib, data_dir, "lookup", {"ref": fakes.KEY_1}))
    assert code == "bad_broker_reply"


def test_a_record_with_another_reference_is_a_bad_reply(lib, data_dir):
    class WrongRecord(fakes.FakeLibrary):
        def order_by_ref(self, session, account_number, ref):
            super().order_by_ref(session, account_number, ref)
            return fakes.order(1, "confirmed", client_ref(fakes.KEY_2))

    wrong = WrongRecord()
    code, _ = error_of(call(wrong, data_dir, "lookup", {"ref": fakes.KEY_1}))
    assert code == "bad_broker_reply"


@pytest.mark.parametrize("op", ["lookup", "cancel"])
@pytest.mark.parametrize(
    "failure,code",
    [
        (BrokerAuthError("refused"), "login_needed"),
        (BrokerCallError("the request failed with Timeout"), "broker_error"),
        (BrokerCallError("the broker answered HTTP 502"), "broker_error"),
        (BrokerCallError("the broker's answer was not a JSON object"), "broker_error"),
    ],
)
def test_every_failure_to_find_an_order_has_its_code_and_is_not_retried(lib, data_dir, op, failure, code):
    lib.fail["order_by_ref"] = failure
    got, _ = error_of(call(lib, data_dir, op, {"ref": fakes.KEY_1}))
    assert got == code
    assert names(lib) == ["order_by_ref"]


@pytest.mark.parametrize("op", ["lookup", "cancel"])
@pytest.mark.parametrize("args", [{}, {"ref": ""}, {"ref": fakes.KEY_1, "extra": 1}, {"ref": None}])
def test_lookup_and_cancel_need_exactly_a_ref(lib, data_dir, op, args):
    code, _ = error_of(call(lib, data_dir, op, args))
    assert code == "bad_request"
    assert lib.calls == []


# --- cancel -------------------------------------------------------------------


def test_cancel_asks_the_broker_once_and_answers_cancelled(lib, data_dir):
    result = result_of(call(lib, data_dir, "cancel", {"ref": fakes.KEY_1}))
    assert result == {
        "ref": fakes.KEY_1,
        "client_ref": client_ref(fakes.KEY_1),
        "broker_id": fakes.order_id(1),
        "status": "cancelled",
        "filled": "0",
        "fill_price": None,
        "detail": "",
    }
    assert lib.calls == [
        ("order_by_ref", fakes.ACCOUNT, client_ref(fakes.KEY_1)),
        ("cancel_order", fakes.order_id(1)),
    ]


def test_cancelling_a_partly_filled_stop_keeps_the_shares_filled(lib, data_dir):
    result = result_of(call(lib, data_dir, "cancel", {"ref": fakes.KEY_2}))
    assert (result["status"], result["filled"], result["fill_price"]) == ("cancelled", "4", "9.48")
    assert names(lib).count("cancel_order") == 1


@pytest.mark.parametrize("state", ["filled", "cancelled", "rejected", "failed", "expired", "voided"])
def test_a_finished_order_is_not_sent_a_cancel(lib, data_dir, state):
    lib.orders_data = [fakes.order(1, state, client_ref(fakes.KEY_1))]
    result = result_of(call(lib, data_dir, "cancel", {"ref": fakes.KEY_1}))
    assert result["status"] == STATES[state]
    assert "cancel_order" not in names(lib)


def test_a_cancel_of_an_order_the_broker_does_not_list_is_order_not_found(lib, data_dir):
    code, message = error_of(call(lib, data_dir, "cancel", {"ref": "f" * 64}))
    assert code == "order_not_found"
    assert message.startswith("cancel: ")
    assert "cancel_order" not in names(lib)


@pytest.mark.parametrize(
    "failure,code",
    [
        (BrokerAuthError("refused"), "login_needed"),
        (BrokerCallError("the request failed with Timeout"), "broker_error"),
        (BrokerCallError("the broker answered HTTP 400"), "broker_error"),
        (BrokerCallError("the broker answered HTTP 503"), "broker_error"),
    ],
)
def test_every_failure_of_a_cancel_has_its_code_and_is_not_retried(lib, data_dir, failure, code):
    lib.fail["cancel_order"] = failure
    got, _ = error_of(call(lib, data_dir, "cancel", {"ref": fakes.KEY_1}))
    assert got == code
    assert names(lib).count("cancel_order") == 1


# --- open_orders ----------------------------------------------------------------


def test_open_orders_lists_only_open_orders_sorted_by_broker_id(lib, data_dir):
    result = result_of(call(lib, data_dir, "open_orders"))
    assert result["as_of"] == "2026-11-17T21:05:00Z"
    assert [(o["broker_id"], o["status"]) for o in result["orders"]] == [
        (fakes.order_id(1), "accepted"),
        (fakes.order_id(2), "partially_filled"),
        (fakes.order_id(4), "accepted"),
    ]
    # The helper cannot turn a reference back into a key; the caller matches
    # client_ref against the keys it holds.
    assert {o["ref"] for o in result["orders"]} == {None}
    assert [o["client_ref"] for o in result["orders"]] == [
        client_ref(fakes.KEY_1),
        client_ref(fakes.KEY_2),
        "99999999-9999-4999-8999-999999999999",
    ]
    assert lib.calls == [("orders", fakes.ACCOUNT)]


def test_no_orders_is_an_empty_list(lib, data_dir):
    lib.orders_data = []
    assert result_of(call(lib, data_dir, "open_orders"))["orders"] == []


@pytest.mark.parametrize(
    "bad",
    [
        "not an object",
        dict(fakes.order(9, "confirmed"), state="made_up_state"),
        dict(fakes.order(9, "confirmed"), account=f"{fakes.API}accounts/{fakes.OTHER_ACCOUNT}/"),
        dict(fakes.order(9, "filled"), cumulative_quantity=1),
    ],
)
def test_one_malformed_order_spoils_the_list(lib, data_dir, bad):
    lib.orders_data.append(bad)
    code, _ = error_of(call(lib, data_dir, "open_orders"))
    assert code == "bad_broker_reply"


@pytest.mark.parametrize(
    "failure,code",
    [
        (BrokerAuthError("refused"), "login_needed"),
        (BrokerCallError("the request failed with Timeout"), "broker_error"),
        (BrokerCallError("the broker answered HTTP 500"), "broker_error"),
        (BrokerCallError("the broker's list ran past 50 pages"), "broker_error"),
    ],
)
def test_every_failure_to_list_orders_has_its_code_and_is_not_retried(lib, data_dir, failure, code):
    lib.fail["orders"] = failure
    got, _ = error_of(call(lib, data_dir, "open_orders"))
    assert got == code
    assert names(lib) == ["orders"]


# --- account ------------------------------------------------------------------


def test_account_reads_cash_buying_power_and_value(lib, data_dir):
    result = result_of(call(lib, data_dir, "account"))
    assert result == {"as_of": "2026-11-17T21:05:00Z", "cash": "1234.56", "buying_power": "1200", "value": "5678.9"}
    assert lib.calls == [("accounts",), ("portfolio", fakes.ACCOUNT)]


@pytest.mark.parametrize("equity", [None, 5678.9, "abc", "NaN"])
def test_a_bad_value_is_a_bad_reply(lib, data_dir, equity):
    lib.portfolio_data["equity"] = equity
    code, message = error_of(call(lib, data_dir, "account"))
    assert code == "bad_broker_reply"
    assert "equity" in message


def test_a_missing_value_is_a_bad_reply(lib, data_dir):
    del lib.portfolio_data["equity"]
    code, _ = error_of(call(lib, data_dir, "account"))
    assert code == "bad_broker_reply"


def test_an_account_missing_from_the_list_is_a_bad_reply(lib, data_dir):
    lib.accounts_data = [fakes.account(fakes.OTHER_ACCOUNT)]
    code, _ = error_of(call(lib, data_dir, "account"))
    assert code == "bad_broker_reply"
    assert "portfolio" not in names(lib)


@pytest.mark.parametrize("step", ["accounts", "portfolio"])
@pytest.mark.parametrize(
    "failure,code",
    [
        (BrokerAuthError("refused"), "login_needed"),
        (BrokerCallError("the request failed with Timeout"), "broker_error"),
        (BrokerCallError("the broker answered HTTP 404"), "broker_error"),
    ],
)
def test_every_failure_of_account_has_its_code_and_is_not_retried(lib, data_dir, step, failure, code):
    lib.fail[step] = failure
    got, _ = error_of(call(lib, data_dir, "account"))
    assert got == code
    assert names(lib).count(step) == 1


@pytest.mark.parametrize("op", ["account", "open_orders"])
def test_reads_take_no_arguments(lib, data_dir, op):
    code, _ = error_of(call(lib, data_dir, op, {"x": 1}))
    assert code == "bad_request"
    assert lib.calls == []
