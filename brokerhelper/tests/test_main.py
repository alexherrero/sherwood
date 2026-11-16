"""Run the helper as a real process, the way the Go supervisor does."""

import json
import os
import shutil
import signal
import socket
import stat
import subprocess
import sys
import tempfile
from pathlib import Path

import pytest

SRC = Path(__file__).resolve().parent.parent / "src"


@pytest.fixture
def sock_path():
    # A short directory: Unix socket paths are limited to about 100 bytes.
    d = tempfile.mkdtemp(prefix="bh")
    yield os.path.join(d, "helper.sock")
    shutil.rmtree(d, ignore_errors=True)


def start(sock_path):
    env = dict(os.environ, PYTHONPATH=str(SRC), PYTHONDONTWRITEBYTECODE="1")
    proc = subprocess.Popen(
        [sys.executable, "-m", "brokerhelper", "--socket", sock_path],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=env,
    )
    return proc


def call(sock_path, request):
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
        s.settimeout(5)
        s.connect(sock_path)
        s.sendall(json.dumps(request).encode() + b"\n")
        data = b""
        while True:
            chunk = s.recv(65536)
            if not chunk:
                break
            data += chunk
    return json.loads(data)


def test_serves_ping_and_stops_cleanly_on_sigterm(sock_path):
    proc = start(sock_path)
    try:
        assert proc.stdout.readline() == "ready\n"
        assert stat.S_IMODE(os.stat(sock_path).st_mode) == 0o600

        first = call(sock_path, {"id": "a", "op": "ping"})
        assert first["ok"] is True
        assert first["result"]["pid"] == proc.pid
        second = call(sock_path, {"id": "b", "op": "nope"})
        assert second["error"]["code"] == "unknown_op"
    finally:
        proc.send_signal(signal.SIGTERM)
        assert proc.wait(timeout=5) == 0
    assert not os.path.exists(sock_path)


def test_replaces_a_stale_socket(sock_path):
    stale = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    stale.bind(sock_path)
    stale.close()
    proc = start(sock_path)
    try:
        assert proc.stdout.readline() == "ready\n"
        assert call(sock_path, {"id": "a", "op": "ping"})["ok"] is True
    finally:
        proc.kill()
        proc.wait(timeout=5)


def test_refuses_to_replace_a_file_that_is_not_a_socket(sock_path):
    Path(sock_path).write_text("keep me")
    proc = start(sock_path)
    out, err = proc.communicate(timeout=5)
    assert proc.returncode != 0
    assert out == ""
    assert "is not a socket" in err
    assert Path(sock_path).read_text() == "keep me"
