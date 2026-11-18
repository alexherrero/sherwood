"""The login command, against the fake library and a scripted terminal."""

import io
import os
import stat
import subprocess
import sys
from datetime import timedelta
from pathlib import Path

import pytest

import fakes
from brokerhelper import brokerfile, login
from brokerhelper.library import BrokerAuthError, BrokerCallError

SRC = Path(__file__).resolve().parent.parent / "src"
GUARD = Path(__file__).resolve().parent / "guard"

PASSWORD = "made-up-password-for-tests"


class Script:
    """A terminal that answers prompts in order and records what was shown."""

    def __init__(self, answers, secrets):
        self.answers = list(answers)
        self.secrets = list(secrets)
        self.prompts = []
        self.out = io.StringIO()
        self.err = io.StringIO()

    def ask(self, prompt):
        self.prompts.append(prompt)
        return self.answers.pop(0)

    def ask_secret(self, prompt):
        self.prompts.append(prompt)
        return self.secrets.pop(0)

    def terminal(self):
        return login.Terminal(ask=self.ask, ask_secret=self.ask_secret, out=self.out, err=self.err)


def run(data_dir, lib, script):
    return login.run(str(data_dir), lib, script.terminal(), now=lambda: fakes.NOW)


def test_login_writes_an_owner_only_session_file(tmp_path):
    lib = fakes.FakeLibrary()
    script = Script(["user@example.com", ""], [PASSWORD])
    assert run(tmp_path, lib, script) == 0

    assert lib.calls[0] == ("login", "user@example.com", PASSWORD, None)
    path = tmp_path / "broker.yaml"
    assert stat.S_IMODE(os.stat(path).st_mode) == 0o600
    saved = brokerfile.read(tmp_path)
    assert saved.account_number == fakes.ACCOUNT
    assert saved.access_token == fakes.ACCESS_TOKEN
    assert saved.refresh_token == fakes.REFRESH_TOKEN
    assert saved.expires_at == fakes.NOW + timedelta(seconds=86400)
    # The password is asked for without echo and is never written.
    assert "password" in script.prompts[1].lower()
    assert PASSWORD not in path.read_text()
    assert "user@example.com" not in path.read_text()
    assert list(tmp_path.iterdir()) == [path]
    assert "Logged in" in script.out.getvalue()
    assert script.err.getvalue() == ""


def test_an_authenticator_code_is_passed_on_and_not_saved(tmp_path):
    lib = fakes.FakeLibrary()
    assert run(tmp_path, lib, Script(["user@example.com", " 123456 "], [PASSWORD])) == 0
    assert lib.calls[0] == ("login", "user@example.com", PASSWORD, "123456")
    assert "123456" not in (tmp_path / "broker.yaml").read_text()


def test_with_several_accounts_the_operator_picks_one(tmp_path):
    lib = fakes.FakeLibrary(
        accounts=[fakes.account(fakes.OTHER_ACCOUNT), fakes.account(fakes.ACCOUNT)]
    )
    script = Script(["user@example.com", "", "2"], [PASSWORD])
    assert run(tmp_path, lib, script) == 0
    assert brokerfile.read(tmp_path).account_number == fakes.ACCOUNT
    shown = script.out.getvalue()
    # Accounts are shown by their last four characters only.
    assert "ending 0001" in shown and "ending 0002" in shown
    assert fakes.ACCOUNT not in shown


def test_a_deactivated_account_is_not_offered(tmp_path):
    closed = fakes.account(fakes.OTHER_ACCOUNT)
    closed["deactivated"] = True
    lib = fakes.FakeLibrary(accounts=[closed, fakes.account(fakes.ACCOUNT)])
    assert run(tmp_path, lib, Script(["user@example.com", ""], [PASSWORD])) == 0
    assert brokerfile.read(tmp_path).account_number == fakes.ACCOUNT


@pytest.mark.parametrize("choice", ["0", "3", "two", ""])
def test_a_wrong_account_choice_writes_nothing(tmp_path, choice):
    lib = fakes.FakeLibrary(accounts=[fakes.account(fakes.OTHER_ACCOUNT), fakes.account(fakes.ACCOUNT)])
    assert run(tmp_path, lib, Script(["user@example.com", "", choice], [PASSWORD])) == 1
    assert list(tmp_path.iterdir()) == []


@pytest.mark.parametrize(
    "failure",
    [BrokerAuthError("refused"), BrokerCallError("the login failed with ConnectionError"), RuntimeError("boom")],
)
def test_a_failed_login_writes_nothing(tmp_path, failure):
    lib = fakes.FakeLibrary()
    lib.fail["login"] = failure
    script = Script(["user@example.com", ""], [PASSWORD])
    assert run(tmp_path, lib, script) == 1
    assert list(tmp_path.iterdir()) == []
    assert "Nothing was written" in script.err.getvalue()
    assert [c[0] for c in lib.calls] == ["login"]


@pytest.mark.parametrize(
    "reply",
    [
        {},
        {"token_type": "Bearer", "access_token": "", "expires_in": 86400},
        {"token_type": "Bearer", "access_token": "x", "expires_in": "86400"},
        {"token_type": "Bearer", "access_token": "x", "expires_in": 0},
        {"token_type": None, "access_token": "x", "expires_in": 86400},
    ],
)
def test_a_login_answer_in_an_unexpected_shape_writes_nothing(tmp_path, reply):
    lib = fakes.FakeLibrary(login_reply=reply)
    lib.login_reply = reply
    assert run(tmp_path, lib, Script(["user@example.com", ""], [PASSWORD])) == 1
    assert list(tmp_path.iterdir()) == []


def test_no_open_account_writes_nothing(tmp_path):
    lib = fakes.FakeLibrary(accounts=[])
    lib.accounts_data = []
    assert run(tmp_path, lib, Script(["user@example.com", ""], [PASSWORD])) == 1
    assert list(tmp_path.iterdir()) == []


@pytest.mark.parametrize("answers,secrets", [([" "], [PASSWORD]), (["user@example.com"], [""])])
def test_an_empty_username_or_password_stops_before_the_broker(tmp_path, answers, secrets):
    lib = fakes.FakeLibrary()
    assert run(tmp_path, lib, Script(answers, secrets)) == 1
    assert lib.calls == []


def test_it_replaces_an_earlier_session(tmp_path):
    brokerfile.write(tmp_path, fakes.saved_session(access_token="an-older-token"))
    assert run(tmp_path, fakes.FakeLibrary(), Script(["user@example.com", ""], [PASSWORD])) == 0
    assert brokerfile.read(tmp_path).access_token == fakes.ACCESS_TOKEN
    assert [p.name for p in tmp_path.iterdir()] == ["broker.yaml"]


def test_it_refuses_a_missing_or_relative_folder_before_asking_anything(tmp_path):
    lib = fakes.FakeLibrary()
    for folder in (tmp_path / "missing", Path("relative")):
        script = Script([], [])
        assert login.run(str(folder), lib, script.terminal()) == 2
        assert script.prompts == []
    assert lib.calls == []


def test_it_refuses_to_write_through_a_link(tmp_path):
    elsewhere = tmp_path / "elsewhere.txt"
    elsewhere.write_text("not yours\n")
    data = tmp_path / "data"
    data.mkdir()
    (data / "broker.yaml").symlink_to(elsewhere)
    script = Script([], [])
    assert login.run(str(data), fakes.FakeLibrary(), script.terminal()) == 2
    assert elsewhere.read_text() == "not yours\n"
    assert script.prompts == []


def test_it_refuses_a_folder_that_is_a_link(tmp_path):
    real = tmp_path / "real"
    real.mkdir()
    (tmp_path / "data").symlink_to(real)
    script = Script([], [])
    assert login.run(str(tmp_path / "data"), fakes.FakeLibrary(), script.terminal()) == 2
    assert list(real.iterdir()) == []


def test_it_refuses_when_broker_yaml_is_a_folder(tmp_path):
    (tmp_path / "broker.yaml").mkdir()
    script = Script([], [])
    assert login.run(str(tmp_path), fakes.FakeLibrary(), script.terminal()) == 2


def test_the_command_needs_a_terminal(tmp_path):
    env = dict(os.environ, PYTHONPATH=os.pathsep.join([str(SRC), str(GUARD)]), PYTHONDONTWRITEBYTECODE="1")
    proc = subprocess.run(
        [sys.executable, "-m", "brokerhelper", "login", "--data-dir", str(tmp_path)],
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        env=env,
        timeout=30,
    )
    assert proc.returncode == 2
    assert "docker exec -it" in proc.stderr
    assert proc.stdout == ""
    assert list(tmp_path.iterdir()) == []
