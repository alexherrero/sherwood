import json

import pytest

from brokerhelper.protocol import (
    ErrorCode,
    HelperError,
    Request,
    decode_request,
    error_reply,
    ok_reply,
    request_id_of,
)


def test_decodes_a_full_request():
    req = decode_request(b'{"id":"7","op":"ping","args":{"a":"1.50"}}')
    assert req == Request(id="7", op="ping", args={"a": "1.50"})


def test_args_may_be_left_out():
    assert decode_request(b'{"id":"7","op":"ping"}').args == {}


def test_whole_numbers_are_accepted():
    assert decode_request(b'{"id":"7","op":"x","args":{"n":3}}').args == {"n": 3}


@pytest.mark.parametrize(
    "line, fragment",
    [
        (b"not json", "not valid JSON"),
        (b"\xff\xfe", "not valid UTF-8"),
        (b"[]", "must be a JSON object"),
        (b'{"op":"ping"}', "id must be"),
        (b'{"id":"","op":"ping"}', "id must be"),
        (b'{"id":7,"op":"ping"}', "id must be"),
        (b'{"id":"7"}', "op must be"),
        (b'{"id":"7","op":3}', "op must be"),
        (b'{"id":"7","op":"ping","args":[]}', "args must be"),
        (b'{"id":"7","op":"ping","extra":1}', "unknown request field: extra"),
        (b'{"id":"7","op":"x","args":{"price":1.5}}', "send decimal values as strings"),
        (b'{"id":"7","op":"x","args":{"price":1e3}}', "send decimal values as strings"),
        (b'{"id":"7","op":"x","args":{"price":NaN}}', "NaN is not accepted"),
    ],
)
def test_refuses_bad_requests(line, fragment):
    with pytest.raises(HelperError) as caught:
        decode_request(line)
    assert caught.value.code == ErrorCode.BAD_REQUEST
    assert fragment in caught.value.message


def test_request_id_of_recovers_the_id_when_it_can():
    assert request_id_of(b'{"id":"9","op":3}') == "9"
    assert request_id_of(b"not json") is None
    assert request_id_of(b'{"id":9}') is None


def test_replies_are_one_json_line():
    ok = ok_reply("1", {"pong": True})
    assert ok.endswith(b"\n") and ok.count(b"\n") == 1
    assert json.loads(ok) == {"id": "1", "ok": True, "result": {"pong": True}}

    err = error_reply(None, ErrorCode.UNKNOWN_OP, "unknown op: x")
    assert json.loads(err) == {
        "id": None,
        "ok": False,
        "error": {"code": "unknown_op", "message": "unknown op: x"},
    }
