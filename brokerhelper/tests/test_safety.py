"""The helper's safety promises for this part.

- It offers no order operations: only ping, holdings and cash.
- The tests never import the real broker library or its HTTP client.
- No secret reaches stdout, stderr or an error reply.
"""

import ast
import inspect
import io
import json
import os
import socket
import subprocess
import sys
import tempfile
from pathlib import Path

import pytest

import fakes
from brokerhelper import brokerfile, library, login, ops
from brokerhelper.library import BrokerAuthError, BrokerCallError, RobinStocks
from brokerhelper.ops import Broker, broker_ops
from brokerhelper.server import handle
from conftest import BLOCKED

SRC = Path(__file__).resolve().parent.parent / "src"
GUARD = Path(__file__).resolve().parent / "guard"
PACKAGE = SRC / "brokerhelper"

ORDER_WORDS = ("order", "buy", "sell", "cancel", "trade", "withdraw", "deposit", "transfer")

# --- No order operations ----------------------------------------------------


def test_the_helper_offers_exactly_ping_holdings_and_cash():
    broker = Broker(library=fakes.FakeLibrary(), data_dir=Path("/nonexistent"))
    assert set(broker_ops(broker)) == {"ping", "holdings", "cash"}
    assert set(ops.OPS) == {"ping"}


@pytest.mark.parametrize(
    "op", ["order", "place_order", "buy", "sell", "order_buy_market", "order_sell_stop_loss", "cancel_all_stock_orders"]
)
def test_order_ops_are_unknown(op):
    lib = fakes.FakeLibrary()
    broker = Broker(library=lib, data_dir=Path("/nonexistent"))
    reply = json.loads(handle(json.dumps({"id": "1", "op": op}).encode(), broker_ops(broker)))
    assert reply["error"]["code"] == "unknown_op"
    assert lib.calls == []


def test_the_wrapper_has_no_order_methods():
    names = [n for n, _ in inspect.getmembers(RobinStocks, inspect.isfunction)]
    names += [n for n in vars(library.Library) if not n.startswith("__")]
    for name in names:
        assert not any(word in name.lower() for word in ORDER_WORDS), name


def test_the_wrapper_takes_nothing_from_the_library_but_login_session_and_urls():
    tree = ast.parse((PACKAGE / "library.py").read_text())
    imported = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.ImportFrom) and node.module and node.module.startswith("robin_stocks"):
            imported |= {(node.module, alias.name) for alias in node.names}
        if isinstance(node, ast.Import):
            assert not any(a.name.startswith("robin_stocks") for a in node.names)
    assert imported == {
        ("robin_stocks.robinhood", "authentication"),
        ("robin_stocks.robinhood", "urls"),
        ("robin_stocks.robinhood", "globals"),
    }
    loader = inspect.getsource(library._load_robin_stocks)
    used = {a.attr for a in ast.walk(ast.parse(loader)) if isinstance(a, ast.Attribute)}
    assert used == {"login", "SESSION", "account_profile_url", "positions_url"}


def test_only_the_wrapper_mentions_the_library():
    for path in PACKAGE.glob("*.py"):
        if path.name != "library.py":
            assert "robin_stocks" not in path.read_text(), path.name


def test_no_module_sends_anything_but_a_get():
    text = (PACKAGE / "library.py").read_text()
    for verb in (".post(", ".put(", ".patch(", ".delete(", "request_post", "request_delete"):
        assert verb not in text


# --- The real library is never imported ------------------------------------


def test_the_guard_refuses_the_library():
    for name in BLOCKED:
        with pytest.raises(ImportError):
            __import__(name)


def test_importing_the_helper_loads_no_library_or_http_client():
    code = (
        "import sys\n"
        "import brokerhelper.__main__, brokerhelper.ops, brokerhelper.library, brokerhelper.login\n"
        "bad = sorted(m for m in sys.modules if m.split('.')[0] in %r)\n"
        "print(','.join(bad))\n" % (BLOCKED,)
    )
    # Without the guard on the path: a plain import must not pull them in.
    env = dict(os.environ, PYTHONPATH=str(SRC), PYTHONDONTWRITEBYTECODE="1")
    proc = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, env=env, timeout=30)
    assert proc.returncode == 0, proc.stderr
    assert proc.stdout.strip() == ""


def test_the_running_helper_answers_broker_ops_without_the_library():
    # With the guard on the path, a real helper process asked for holdings and
    # cash with no login answers login_needed and never imports the library.
    data = tempfile.mkdtemp(prefix="bh")
    sock_path = os.path.join(data, "h.sock")
    env = dict(os.environ, PYTHONPATH=os.pathsep.join([str(SRC), str(GUARD)]), PYTHONDONTWRITEBYTECODE="1")
    proc = subprocess.Popen(
        [sys.executable, "-m", "brokerhelper", "--socket", sock_path, "--data-dir", data],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=env,
    )
    try:
        assert proc.stdout.readline() == "ready\n"
        for op in ("holdings", "cash"):
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
                s.settimeout(5)
                s.connect(sock_path)
                s.sendall(json.dumps({"id": op, "op": op}).encode() + b"\n")
                reply = json.loads(s.makefile("rb").readline())
            assert reply["ok"] is False
            assert reply["error"]["code"] == "login_needed"
    finally:
        proc.terminate()
        out, err = proc.communicate(timeout=10)
    assert proc.returncode == 0
    assert out == "" and err == ""
    os.rmdir(data)


def test_this_test_process_never_loaded_the_library():
    assert not [m for m in sys.modules if m.split(".")[0] in BLOCKED]


# --- No secret reaches stdout, stderr or an error reply -----------------------

PASSWORD = "SENTINEL-PASSWORD-1"
CODE = "SENTINEL-CODE-2"
ACCESS = "SENTINEL-ACCESS-3"
REFRESH = "SENTINEL-REFRESH-4"
SECRETS = (PASSWORD, CODE, ACCESS, REFRESH)


def assert_clean(*texts):
    for text in texts:
        for secret in SECRETS:
            assert secret not in text


class LeakyLibrary(fakes.FakeLibrary):
    """A library whose every failure message carries the secrets."""

    def __init__(self, failure_type):
        super().__init__(login_reply=dict(fakes.LOGIN_REPLY, access_token=ACCESS, refresh_token=REFRESH))
        self.failure_type = failure_type
        self.leak = f"{PASSWORD} {CODE} {ACCESS} {REFRESH}"

    def _check(self, session):
        pass

    def _raise(self):
        raise self.failure_type(self.leak)

    def login(self, username, password, mfa_code):
        super().login(username, password, mfa_code)
        if self.failure_type is not None and "login" in self.fail_at:
            self._raise()
        return dict(self.login_reply)

    def accounts(self, session):
        if "accounts" in self.fail_at:
            self._raise()
        return super().accounts(session)

    def positions(self, session, account_number):
        if "positions" in self.fail_at:
            self._raise()
        return super().positions(session, account_number)

    def instrument(self, session, url):
        if "instrument" in self.fail_at:
            self._raise()
        return super().instrument(session, url)


FAILURES = [BrokerAuthError, BrokerCallError, RuntimeError, KeyError, ValueError]


def run_login(data_dir, lib, answers):
    out, err = io.StringIO(), io.StringIO()
    answers = list(answers)
    term = login.Terminal(
        ask=lambda prompt: answers.pop(0), ask_secret=lambda prompt: PASSWORD, out=out, err=err
    )
    code = login.run(str(data_dir), lib, term, now=lambda: fakes.NOW)
    return code, out.getvalue(), err.getvalue()


@pytest.mark.parametrize("failure", FAILURES)
@pytest.mark.parametrize("step", ["login", "accounts"])
def test_a_failed_login_prints_no_secret(tmp_path, capfd, failure, step):
    lib = LeakyLibrary(failure)
    lib.fail_at = {step}
    code, out, err = run_login(tmp_path, lib, ["user@example.com", CODE])
    assert code == 1
    captured = capfd.readouterr()
    assert_clean(out, err, captured.out, captured.err)


def test_a_good_login_prints_no_secret(tmp_path, capfd):
    lib = LeakyLibrary(None)
    lib.fail_at = set()
    code, out, err = run_login(tmp_path, lib, ["user@example.com", CODE])
    assert code == 0
    captured = capfd.readouterr()
    assert_clean(out, err, captured.out, captured.err)
    saved = brokerfile.read(tmp_path)
    assert saved.access_token == ACCESS
    assert_clean(repr(saved), str(saved))


@pytest.mark.parametrize("failure", FAILURES)
@pytest.mark.parametrize("op,step", [("holdings", "positions"), ("holdings", "instrument"), ("cash", "accounts")])
def test_a_failed_broker_op_replies_and_prints_no_secret(tmp_path, capfd, failure, op, step):
    brokerfile.write(tmp_path, fakes.saved_session(access_token=ACCESS))
    lib = LeakyLibrary(failure)
    lib.fail_at = {step}
    broker = Broker(library=lib, data_dir=tmp_path, now=lambda: fakes.NOW)
    reply = handle(json.dumps({"id": "1", "op": op}).encode(), broker_ops(broker))
    assert json.loads(reply)["ok"] is False
    captured = capfd.readouterr()
    assert_clean(reply.decode(), captured.out, captured.err)


@pytest.mark.parametrize("op", ["holdings", "cash"])
def test_a_good_broker_op_replies_with_no_secret(tmp_path, capfd, op):
    brokerfile.write(tmp_path, fakes.saved_session(access_token=ACCESS))
    lib = LeakyLibrary(None)
    lib.fail_at = set()
    broker = Broker(library=lib, data_dir=tmp_path, now=lambda: fakes.NOW)
    reply = handle(json.dumps({"id": "1", "op": op}).encode(), broker_ops(broker))
    assert json.loads(reply)["ok"] is True
    captured = capfd.readouterr()
    assert_clean(reply.decode(), captured.out, captured.err)


@pytest.mark.parametrize(
    "text",
    [
        f'version: 1\naccess_token: "{ACCESS}"\n',
        f"version: 1\naccess_token: {ACCESS}\n",
        f'version: 1\naccess_token: "{ACCESS}"\naccess_token: "{REFRESH}"\n',
        f'{ACCESS}\n',
    ],
)
def test_a_malformed_session_file_error_quotes_no_value(tmp_path, capfd, text):
    path = brokerfile.path_in(tmp_path)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT, 0o600)
    with os.fdopen(fd, "w") as f:
        f.write(text)
    broker = Broker(library=fakes.FakeLibrary(), data_dir=tmp_path, now=lambda: fakes.NOW)
    reply = handle(b'{"id":"1","op":"holdings"}', broker_ops(broker))
    assert json.loads(reply)["error"]["code"] == "broker_settings"
    captured = capfd.readouterr()
    assert_clean(reply.decode(), captured.out, captured.err)
