import json
import os
import socket
import threading

from brokerhelper import __version__
from brokerhelper.protocol import MAX_REQUEST_BYTES, HelperError
from brokerhelper.server import handle, handle_connection


def reply_of(line):
    return json.loads(handle(line))


def test_ping_answers_with_version_and_pid():
    assert reply_of(b'{"id":"1","op":"ping"}') == {
        "id": "1",
        "ok": True,
        "result": {"version": __version__, "pid": os.getpid()},
    }


def test_ping_refuses_arguments():
    reply = reply_of(b'{"id":"1","op":"ping","args":{"x":"1"}}')
    assert reply["ok"] is False
    assert reply["error"]["code"] == "bad_request"


def test_unknown_op_is_a_typed_error():
    reply = reply_of(b'{"id":"2","op":"buy"}')
    assert reply == {
        "id": "2",
        "ok": False,
        "error": {"code": "unknown_op", "message": "unknown op: buy"},
    }


def test_bad_request_keeps_the_id_when_it_can():
    reply = reply_of(b'{"id":"3","op":"ping","args":[]}')
    assert reply["id"] == "3"
    assert reply["error"]["code"] == "bad_request"


class Counting:
    def __init__(self, exc):
        self.calls = 0
        self.exc = exc

    def __call__(self, args):
        self.calls += 1
        raise self.exc


def test_a_typed_error_from_an_op_is_reported_and_not_retried():
    op = Counting(HelperError("broker_error", "the broker said no"))
    reply = json.loads(handle(b'{"id":"4","op":"flaky"}', {"flaky": op}))
    assert reply["error"] == {"code": "broker_error", "message": "the broker said no"}
    assert op.calls == 1


def test_an_unexpected_failure_is_internal_and_not_retried():
    op = Counting(RuntimeError("secret-looking detail"))
    reply = json.loads(handle(b'{"id":"5","op":"flaky"}', {"flaky": op}))
    assert reply["error"] == {"code": "internal", "message": "flaky failed: RuntimeError"}
    assert "secret-looking" not in json.dumps(reply)
    assert op.calls == 1


def serve_pair(ops=None, read_timeout=2.0):
    """A connected socket pair with the helper's side served on a thread."""
    client, server = socket.socketpair()
    kwargs = {"read_timeout": read_timeout}
    if ops is not None:
        kwargs["ops"] = ops
    thread = threading.Thread(target=lambda: _serve(server, kwargs))
    thread.start()
    return client, thread


def _serve(server, kwargs):
    with server:
        handle_connection(server, **kwargs)


def read_all(client):
    data = b""
    while True:
        chunk = client.recv(65536)
        if not chunk:
            return data
        data += chunk


def test_one_connection_carries_one_call():
    client, thread = serve_pair()
    with client:
        client.sendall(b'{"id":"6",')
        client.sendall(b'"op":"ping"}\n')
        data = read_all(client)
    thread.join()
    assert data.count(b"\n") == 1
    assert json.loads(data)["result"]["pid"] == os.getpid()


def test_an_oversized_request_is_refused():
    client, thread = serve_pair()
    with client:
        try:
            client.sendall(b"x" * (MAX_REQUEST_BYTES + 2))
        except OSError:
            pass  # the helper may close before reading it all
        data = read_all(client)
    thread.join()
    assert json.loads(data)["error"]["code"] == "bad_request"


def test_a_request_without_its_newline_is_never_run():
    op = Counting(AssertionError("must not run"))
    client, thread = serve_pair({"op": op})
    with client:
        client.sendall(b'{"id":"7","op":"op"}')
        client.shutdown(socket.SHUT_WR)
        data = read_all(client)
    thread.join()
    assert data == b""
    assert op.calls == 0


def test_a_silent_client_is_dropped_after_the_read_timeout():
    op = Counting(AssertionError("must not run"))
    client, thread = serve_pair({"op": op}, read_timeout=0.05)
    with client:
        thread.join(timeout=5)
        assert not thread.is_alive()
        assert read_all(client) == b""
    assert op.calls == 0
