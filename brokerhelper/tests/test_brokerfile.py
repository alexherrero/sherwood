"""broker.yaml: where it may live, how it is written, and how it is read."""

import os
import stat
from datetime import timedelta

import pytest

import fakes
from brokerhelper import brokerfile


def test_round_trip_and_owner_only(tmp_path):
    saved = fakes.saved_session()
    path = brokerfile.write(tmp_path, saved)
    assert path == tmp_path / "broker.yaml"
    assert stat.S_IMODE(os.stat(path).st_mode) == 0o600
    assert brokerfile.read(tmp_path) == saved
    text = path.read_text()
    assert text.startswith("# Sherwood's broker session")
    assert 'expires_at: "2026-11-18T09:05:00Z"' in text


def test_it_is_owner_only_whatever_the_umask(tmp_path):
    old = os.umask(0)
    try:
        path = brokerfile.write(tmp_path, fakes.saved_session())
    finally:
        os.umask(old)
    assert stat.S_IMODE(os.stat(path).st_mode) == 0o600


def test_rewriting_leaves_only_the_new_file(tmp_path):
    brokerfile.write(tmp_path, fakes.saved_session())
    newer = fakes.saved_session(expires_at=fakes.NOW + timedelta(days=2))
    brokerfile.write(tmp_path, newer)
    assert brokerfile.read(tmp_path) == newer
    assert [p.name for p in tmp_path.iterdir()] == ["broker.yaml"]


def test_a_failed_write_leaves_the_old_file_and_no_temporary(tmp_path, monkeypatch):
    brokerfile.write(tmp_path, fakes.saved_session())
    before = (tmp_path / "broker.yaml").read_text()

    def broken(*args):
        raise OSError("disk full")

    monkeypatch.setattr(brokerfile.os, "replace", broken)
    with pytest.raises(OSError):
        brokerfile.write(tmp_path, fakes.saved_session(access_token="a-newer-token"))
    assert (tmp_path / "broker.yaml").read_text() == before
    assert [p.name for p in tmp_path.iterdir()] == ["broker.yaml"]


def test_reading_a_missing_file_says_so(tmp_path):
    with pytest.raises(brokerfile.BrokerFileMissing):
        brokerfile.read(tmp_path)


@pytest.mark.parametrize(
    "edit,problem",
    [
        (lambda t: t.replace("version: 1", "version: 2"), "version"),
        (lambda t: t.replace("version: 1", "version: true"), "version"),
        (lambda t: t + 'colour: "green"\n', "unknown key colour"),
        (lambda t: t + 'token_type: "Bearer"\n', "token_type appears twice"),
        (lambda t: "\n".join(l for l in t.splitlines() if not l.startswith("account_number")), "account_number is missing"),
        (lambda t: t.replace('token_type: "Bearer"', 'token_type: ""'), "token_type"),
        (lambda t: t.replace('token_type: "Bearer"', "token_type: Bearer"), "not readable"),
        (lambda t: t.replace("2026-11-18T09:05:00Z", "tomorrow"), "expires_at"),
        (lambda t: t + "just some words\n", "not a 'key: value' line"),
    ],
)
def test_a_malformed_file_is_refused_by_key(tmp_path, edit, problem):
    text = edit(brokerfile.render(fakes.saved_session()))
    with pytest.raises(brokerfile.BrokerFileError, match=problem) as caught:
        brokerfile.parse(text)
    assert fakes.ACCESS_TOKEN not in str(caught.value)


def test_a_file_writable_by_others_is_refused(tmp_path):
    path = brokerfile.write(tmp_path, fakes.saved_session())
    os.chmod(path, 0o602)
    with pytest.raises(brokerfile.BrokerFileError, match="chmod 600"):
        brokerfile.read(tmp_path)


def test_the_session_never_prints_its_token():
    saved = fakes.saved_session()
    assert fakes.ACCESS_TOKEN not in repr(saved)
    assert fakes.REFRESH_TOKEN not in str(saved)
