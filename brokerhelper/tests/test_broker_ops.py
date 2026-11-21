"""The holdings and cash reads, against the fake library.

The order and account operations are in test_order_ops.py.
"""

import json
import os
from datetime import timedelta

import pytest

import fakes
from brokerhelper import brokerfile
from brokerhelper.library import BrokerAuthError, BrokerCallError
from brokerhelper.ops import Broker, broker_ops
from brokerhelper.server import handle


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
    reply = json.loads(handle(json.dumps(request).encode(), broker_ops(broker)))
    assert reply["id"] == "1"
    return reply


def error_of(reply):
    assert reply["ok"] is False, reply
    return reply["error"]["code"], reply["error"]["message"]


def test_holdings_returns_exact_decimal_strings_sorted_by_symbol(lib, data_dir):
    reply = call(lib, data_dir, "holdings")
    assert reply["ok"] is True
    assert reply["result"] == {
        "as_of": "2026-11-17T21:05:00Z",
        "holdings": [
            {"symbol": "ZZA", "quantity": "2.5", "average_cost": "100", "opened_on": "2025-06-30"},
            {"symbol": "ZZB", "quantity": "10", "average_cost": "12.34", "opened_on": "2025-03-04"},
        ],
    }
    # Only the position without a symbol needed its instrument looked up, and
    # the saved account was the one asked for.
    assert ("positions", fakes.ACCOUNT) in lib.calls
    assert [c for c in lib.calls if c[0] == "instrument"] == [("instrument", fakes.instrument_url(2))]


def test_opened_on_is_the_new_york_date(lib, data_dir):
    lib.positions_data = [
        # 23:30 New York in winter is 04:30 UTC the next day.
        fakes.position(1, "1", "1", "2025-01-11T04:30:00Z", symbol="ZZA"),
        # 00:30 New York in summer is 04:30 UTC the same day.
        fakes.position(2, "1", "1", "2025-07-11T04:30:00+00:00", symbol="ZZB"),
    ]
    held = call(lib, data_dir, "holdings")["result"]["holdings"]
    assert [h["opened_on"] for h in held] == ["2025-01-10", "2025-07-11"]


def test_no_positions_is_an_empty_list(lib, data_dir):
    lib.positions_data = []
    assert call(lib, data_dir, "holdings")["result"]["holdings"] == []


def test_cash_returns_cash_and_buying_power(lib, data_dir):
    reply = call(lib, data_dir, "cash")
    assert reply["result"] == {"as_of": "2026-11-17T21:05:00Z", "cash": "1234.56", "buying_power": "1200"}


def test_cash_may_be_negative_and_zero_is_plain(lib, data_dir):
    lib.accounts_data = [fakes.account(fakes.OTHER_ACCOUNT), fakes.account(fakes.ACCOUNT, "-12.5000", "0.0000")]
    reply = call(lib, data_dir, "cash")
    assert reply["result"]["cash"] == "-12.5"
    assert reply["result"]["buying_power"] == "0"


@pytest.mark.parametrize("op", ["holdings", "cash"])
def test_broker_ops_take_no_arguments(lib, data_dir, op):
    code, _ = error_of(call(lib, data_dir, op, {"account": "x"}))
    assert code == "bad_request"
    assert lib.calls == []


@pytest.mark.parametrize("op", ["holdings", "cash"])
def test_without_a_login_the_reply_says_to_log_in(lib, tmp_path, op):
    code, message = error_of(call(lib, tmp_path, op))
    assert code == "login_needed"
    assert "brokerhelper login" in message
    assert lib.calls == []


@pytest.mark.parametrize("op", ["holdings", "cash"])
def test_an_expired_session_is_not_sent_to_the_broker(lib, tmp_path, op):
    brokerfile.write(tmp_path, fakes.saved_session(expires_at=fakes.NOW))
    code, message = error_of(call(lib, tmp_path, op))
    assert code == "login_needed"
    assert "expired" in message
    assert lib.calls == []


@pytest.mark.parametrize("op", ["holdings", "cash"])
def test_a_refused_session_says_to_log_in_and_is_not_retried(lib, tmp_path, op):
    brokerfile.write(tmp_path, fakes.saved_session(access_token="a-token-the-broker-no-longer-takes"))
    code, _ = error_of(call(lib, tmp_path, op))
    assert code == "login_needed"
    assert len(lib.calls) == 1


@pytest.mark.parametrize("op,step", [("holdings", "positions"), ("holdings", "instrument"), ("cash", "accounts")])
def test_a_failed_call_is_a_broker_error_and_is_not_retried(lib, data_dir, op, step):
    lib.fail[step] = BrokerCallError("the broker answered HTTP 503")
    code, message = error_of(call(lib, data_dir, op))
    assert code == "broker_error"
    assert message == f"{op}: the broker answered HTTP 503"
    assert [c[0] for c in lib.calls].count(step) == 1


@pytest.mark.parametrize("op,step", [("holdings", "positions"), ("cash", "accounts")])
def test_an_unexpected_failure_is_internal_with_only_its_type(lib, data_dir, op, step):
    lib.fail[step] = RuntimeError("something the broker library said")
    code, message = error_of(call(lib, data_dir, op))
    assert code == "internal"
    assert message == f"{op} failed: RuntimeError"


@pytest.mark.parametrize(
    "change,field",
    [
        ({"quantity": 10.5}, "quantity"),
        ({"quantity": "NaN"}, "quantity"),
        ({"quantity": "ten"}, "quantity"),
        ({"quantity": "-1"}, "quantity"),
        ({"average_buy_price": "-0.01"}, "average_buy_price"),
        ({"average_buy_price": None}, "average_buy_price"),
        ({"created_at": "yesterday"}, "created_at"),
        ({"created_at": "2025-03-04T15:30:00"}, "created_at"),
        ({"symbol": "B@D!"}, "symbol"),
        ({"account_number": fakes.OTHER_ACCOUNT}, "another account"),
    ],
)
def test_a_position_in_an_unexpected_shape_is_refused(lib, data_dir, change, field):
    lib.positions_data[0].update(change)
    code, message = error_of(call(lib, data_dir, "holdings"))
    assert code == "bad_broker_reply"
    assert field in message
    # The message names the field, never the value the broker sent.
    for value in change.values():
        if isinstance(value, str):
            assert value not in message


def test_a_missing_field_is_refused(lib, data_dir):
    del lib.positions_data[1]["instrument"]
    code, message = error_of(call(lib, data_dir, "holdings"))
    assert code == "bad_broker_reply"
    assert "instrument is missing" in message


def test_two_positions_with_one_symbol_are_refused(lib, data_dir):
    lib.instruments_data[fakes.instrument_url(2)]["symbol"] = "ZZB"
    code, _ = error_of(call(lib, data_dir, "holdings"))
    assert code == "bad_broker_reply"


@pytest.mark.parametrize("accounts", [[], [fakes.account(fakes.OTHER_ACCOUNT)], [fakes.account(fakes.ACCOUNT)] * 2])
def test_cash_needs_the_saved_account_exactly_once(lib, data_dir, accounts):
    lib.accounts_data = accounts
    code, message = error_of(call(lib, data_dir, "cash"))
    assert code == "bad_broker_reply"
    assert fakes.ACCOUNT not in message


@pytest.mark.parametrize("field", ["cash", "buying_power"])
def test_cash_refuses_a_float(lib, data_dir, field):
    lib.accounts_data[0][field] = 12.5
    code, _ = error_of(call(lib, data_dir, "cash"))
    assert code == "bad_broker_reply"


def test_a_session_file_others_can_read_is_refused(lib, data_dir):
    os.chmod(brokerfile.path_in(data_dir), 0o640)
    code, message = error_of(call(lib, data_dir, "holdings"))
    assert code == "broker_settings"
    assert "chmod 600" in message
    assert lib.calls == []


def test_a_session_file_that_is_a_link_is_refused(lib, tmp_path):
    real = tmp_path / "elsewhere"
    real.mkdir()
    brokerfile.write(real, fakes.saved_session())
    (tmp_path / "broker.yaml").symlink_to(real / "broker.yaml")
    code, _ = error_of(call(lib, tmp_path, "cash"))
    assert code == "broker_settings"
    assert lib.calls == []


def test_the_session_file_is_read_fresh_on_every_call(lib, tmp_path):
    assert error_of(call(lib, tmp_path, "cash"))[0] == "login_needed"
    brokerfile.write(tmp_path, fakes.saved_session())
    assert call(lib, tmp_path, "cash")["ok"] is True
    brokerfile.write(tmp_path, fakes.saved_session(expires_at=fakes.NOW - timedelta(seconds=1)))
    assert error_of(call(lib, tmp_path, "cash"))[0] == "login_needed"
