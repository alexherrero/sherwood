"""The helper's safety promises.

- It offers exactly the operations live trading needs: ``ping``; the reads
  ``holdings``, ``cash``, ``account``, ``lookup`` and ``open_orders``; and the
  two calls that change something at the broker, ``place`` and ``cancel``.
  Nothing else, and nothing that moves money.
- The wrapper's methods, and the parts of the library it borrows, are pinned.
- Only the order routes ever receive a POST: the order list (to send an
  order) and one order's cancel link (to call it off). Nothing sends a
  DELETE, a PUT or a PATCH.
- The tests never import the real broker library or its HTTP client.
- No secret reaches stdout, stderr or an error reply.

Every list here is exact on purpose. Adding an operation, a wrapper method, a
library attribute, an HTTP verb or a route fails this file until the list is
changed too, in the same review.
"""

import ast
import inspect
import io
import json
import os
import re
import socket
import subprocess
import sys
import tempfile
from pathlib import Path

import pytest

import fakes
from brokerhelper import brokerfile, library, login, ops
from brokerhelper.library import BrokerAuthError, BrokerCallError, RobinStocks, Session
from brokerhelper.ops import Broker, broker_ops
from brokerhelper.server import handle
from conftest import BLOCKED

SRC = Path(__file__).resolve().parent.parent / "src"
GUARD = Path(__file__).resolve().parent / "guard"
PACKAGE = SRC / "brokerhelper"
LIBRARY = PACKAGE / "library.py"

# --- Exactly these operations -------------------------------------------------

OPERATIONS = {"ping", "holdings", "cash", "account", "lookup", "open_orders", "place", "cancel"}

# The wrapper's public methods, on the protocol and on the real class alike.
LIBRARY_METHODS = {
    "login",
    "accounts",
    "positions",
    "instrument",
    "instruments_for",
    "portfolio",
    "orders",
    "order_by_ref",
    "place_order",
    "cancel_order",
}
PRIVATE_METHODS = {"_lib", "_get", "_post", "_pages", "_page_iter"}

# The only methods that send anything that changes something at the broker.
SENDING_METHODS = {"place_order", "cancel_order"}

# Every route fragment library.py writes. A new route fails this list.
ROUTES = {
    "https://api.robinhood.com/",
    "orders/",
    "cancel/",
    "/cancel/",
    "instruments/",
    "accounts/",
    "portfolios/",
    "/",
    "application/json",
    "https://github.com/alexherrero/sherwood/broker/client-ref",
}


def test_the_helper_offers_exactly_these_operations():
    broker = Broker(library=fakes.FakeLibrary(), data_dir=Path("/nonexistent"))
    assert set(broker_ops(broker)) == OPERATIONS
    assert set(ops.OPS) == {"ping"}


@pytest.mark.parametrize(
    "op",
    [
        "order",
        "place_order",
        "buy",
        "sell",
        "order_buy_market",
        "order_sell_stop_loss",
        "cancel_all",
        "cancel_all_stock_orders",
        "replace",
        "modify",
        "withdraw",
        "deposit",
        "transfer",
        "options",
        "crypto",
    ],
)
def test_any_other_op_is_unknown(op):
    lib = fakes.FakeLibrary()
    broker = Broker(library=lib, data_dir=Path("/nonexistent"))
    reply = json.loads(handle(json.dumps({"id": "1", "op": op}).encode(), broker_ops(broker)))
    assert reply["error"]["code"] == "unknown_op"
    assert lib.calls == []


def test_the_wrappers_methods_are_pinned():
    protocol = {n for n, v in vars(library.Library).items() if not n.startswith("_") and callable(v)}
    assert protocol == LIBRARY_METHODS
    real = {n for n, _ in inspect.getmembers(RobinStocks, inspect.isfunction) if not n.startswith("__")}
    assert real == LIBRARY_METHODS | PRIVATE_METHODS


def test_the_fake_offers_no_more_than_the_wrapper():
    fake = {n for n, _ in inspect.getmembers(fakes.FakeLibrary, inspect.isfunction) if not n.startswith("_")}
    assert fake == LIBRARY_METHODS


def test_the_wrapper_takes_nothing_from_the_library_but_login_session_and_urls():
    tree = ast.parse(LIBRARY.read_text())
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


# --- Only the order routes receive a POST ---------------------------------------


def _functions(tree):
    return {n.name: n for n in ast.walk(tree) if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))}


def _attribute_calls(node):
    return [c for c in ast.walk(node) if isinstance(c, ast.Call) and isinstance(c.func, ast.Attribute)]


def test_the_session_is_used_only_for_get_and_post():
    tree = ast.parse(LIBRARY.read_text())
    verbs = {
        c.func.attr
        for c in _attribute_calls(tree)
        if isinstance(c.func.value, ast.Attribute) and c.func.value.attr == "session"
    }
    assert verbs == {"get", "post"}


def test_exactly_one_post_and_it_is_in_post():
    funcs = _functions(ast.parse(LIBRARY.read_text()))
    posts = {name for name, f in funcs.items() for c in _attribute_calls(f) if c.func.attr == "post"}
    assert posts == {"_post"}
    assert sum(1 for c in _attribute_calls(funcs["_post"]) if c.func.attr == "post") == 1


def test_only_the_sending_methods_call_post_and_only_with_an_order_route():
    funcs = _functions(ast.parse(LIBRARY.read_text()))
    callers = {}
    for name, f in funcs.items():
        for c in _attribute_calls(f):
            if c.func.attr == "_post":
                callers.setdefault(name, []).append(c)
    assert set(callers) == SENDING_METHODS
    [send] = callers["place_order"]
    assert isinstance(send.args[1], ast.Name) and send.args[1].id == "ORDERS_URL"
    [stop] = callers["cancel_order"]
    assert isinstance(stop.args[1], ast.Call) and stop.args[1].func.id == "cancel_url"


def test_the_post_guard_admits_only_the_order_list_and_cancel_links():
    assert library.ORDERS_URL == "https://api.robinhood.com/orders/"
    good = library.cancel_url(fakes.order_id(1))
    assert good == f"https://api.robinhood.com/orders/{fakes.order_id(1)}/cancel/"
    assert library._CANCEL_URL.pattern == re.escape(library.ORDERS_URL) + library._ORDER_ID.pattern + "/cancel/"


@pytest.mark.parametrize(
    "url",
    [
        # Every address the wrapper reads, and the obvious money routes.
        library.INSTRUMENTS_URL,
        library.account_url(fakes.ACCOUNT),
        library.portfolio_url(fakes.ACCOUNT),
        library.order_url(fakes.order_id(1)),
        "https://api.robinhood.com/accounts/?default_to_all_accounts=true",
        f"https://api.robinhood.com/positions/?account_number={fakes.ACCOUNT}",
        "https://api.robinhood.com/ach/transfers/",
        "https://api.robinhood.com/ach/relationships/",
        "https://api.robinhood.com/options/orders/",
        "https://nummus.robinhood.com/orders/",
        "https://api.robinhood.com/orders/?account_numbers=FAKE0001",
    ],
)
def test_a_post_to_any_other_address_is_refused_before_it_is_sent(url):
    class NoNetwork:
        def post(self, *a, **k):
            raise AssertionError("a POST went out")

    wrapper = RobinStocks(loader=lambda: type("Parts", (), {"session": NoNetwork()})())
    with pytest.raises(BrokerCallError, match="order routes"):
        wrapper._post(Session("Bearer", fakes.ACCESS_TOKEN), url, {})


def test_no_module_sends_a_delete_put_or_patch_or_uses_the_librarys_requests():
    for path in PACKAGE.glob("*.py"):
        text = path.read_text()
        for verb in (".delete(", ".put(", ".patch(", ".request(", "request_post", "request_delete", "request_get"):
            assert verb not in text, (path.name, verb)
        if path.name != "library.py":
            assert ".post(" not in text, path.name


def test_the_routes_library_py_writes_are_pinned():
    tree = ast.parse(LIBRARY.read_text())
    written = {
        n.value
        for n in ast.walk(tree)
        if isinstance(n, ast.Constant) and isinstance(n.value, str) and "/" in n.value and " " not in n.value
    }
    assert written == ROUTES


def test_no_module_but_the_wrapper_names_a_route():
    for path in PACKAGE.glob("*.py"):
        if path.name != "library.py":
            assert "robinhood.com" not in path.read_text(), path.name


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


BROKER_OP_ARGS = {
    "holdings": {},
    "cash": {},
    "account": {},
    "open_orders": {},
    "lookup": {"ref": fakes.KEY_1},
    "cancel": {"ref": fakes.KEY_1},
    "place": {"ref": fakes.KEY_1, "symbol": "ZZA", "side": "buy", "type": "day_limit_buy", "quantity": "1", "price": "1"},
}


def test_every_broker_op_is_tested_in_a_running_helper():
    assert set(BROKER_OP_ARGS) == OPERATIONS - {"ping"}


def test_the_running_helper_answers_broker_ops_without_the_library():
    # With the guard on the path, a real helper process asked for every broker
    # op with no login answers login_needed and never imports the library.
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
        for op, args in sorted(BROKER_OP_ARGS.items()):
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
                s.settimeout(5)
                s.connect(sock_path)
                s.sendall(json.dumps({"id": op, "op": op, "args": args}).encode() + b"\n")
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

    def instruments_for(self, session, symbol):
        if "instruments_for" in self.fail_at:
            self._raise()
        return super().instruments_for(session, symbol)

    def portfolio(self, session, account_number):
        if "portfolio" in self.fail_at:
            self._raise()
        return super().portfolio(session, account_number)

    def orders(self, session, account_number):
        if "orders" in self.fail_at:
            self._raise()
        return super().orders(session, account_number)

    def order_by_ref(self, session, account_number, ref):
        if "order_by_ref" in self.fail_at:
            self._raise()
        return super().order_by_ref(session, account_number, ref)

    def place_order(self, session, payload):
        if "place_order" in self.fail_at:
            self._raise()
        return super().place_order(session, payload)

    def cancel_order(self, session, order_id):
        if "cancel_order" in self.fail_at:
            self._raise()
        return super().cancel_order(session, order_id)


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
@pytest.mark.parametrize(
    "op,step",
    [
        ("holdings", "positions"),
        ("holdings", "instrument"),
        ("cash", "accounts"),
        ("account", "accounts"),
        ("account", "portfolio"),
        ("open_orders", "orders"),
        ("lookup", "order_by_ref"),
        ("cancel", "order_by_ref"),
        ("cancel", "cancel_order"),
        ("place", "instruments_for"),
        ("place", "place_order"),
    ],
)
def test_a_failed_broker_op_replies_and_prints_no_secret(tmp_path, capfd, failure, op, step):
    brokerfile.write(tmp_path, fakes.saved_session(access_token=ACCESS))
    lib = LeakyLibrary(failure)
    lib.fail_at = {step}
    broker = Broker(library=lib, data_dir=tmp_path, now=lambda: fakes.NOW)
    reply = handle(json.dumps({"id": "1", "op": op, "args": BROKER_OP_ARGS[op]}).encode(), broker_ops(broker))
    assert json.loads(reply)["ok"] is False
    captured = capfd.readouterr()
    assert_clean(reply.decode(), captured.out, captured.err)


@pytest.mark.parametrize("op", sorted(BROKER_OP_ARGS))
def test_a_good_broker_op_replies_with_no_secret(tmp_path, capfd, op):
    brokerfile.write(tmp_path, fakes.saved_session(access_token=ACCESS))
    lib = LeakyLibrary(None)
    lib.fail_at = set()
    broker = Broker(library=lib, data_dir=tmp_path, now=lambda: fakes.NOW)
    reply = handle(json.dumps({"id": "1", "op": op, "args": BROKER_OP_ARGS[op]}).encode(), broker_ops(broker))
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
