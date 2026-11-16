"""The Unix-socket server: one connection, one request, one reply.

The server handles one call at a time, in the order they arrive, so two broker
calls never overlap. A client that connects and then says nothing is dropped
after the read timeout without a reply, and a request that never reaches its
closing newline is never run.
"""

from __future__ import annotations

import os
import socket
import stat
import sys
from typing import IO, Mapping

from .ops import OPS, Handler
from .protocol import (
    MAX_REQUEST_BYTES,
    ErrorCode,
    HelperError,
    decode_request,
    error_reply,
    ok_reply,
    request_id_of,
)

# The line the helper prints on stdout once it is listening. The Go supervisor
# waits for it before sending the first call.
READY_LINE = "ready"

DEFAULT_READ_TIMEOUT = 10.0


def handle(line: bytes, ops: Mapping[str, Handler] = OPS) -> bytes:
    """Run one request line and return its reply line.

    The operation runs at most once. An error it raises becomes the reply; it
    is never retried.
    """
    try:
        req = decode_request(line)
    except HelperError as exc:
        return error_reply(request_id_of(line), exc.code, exc.message)

    handler = ops.get(req.op)
    if handler is None:
        return error_reply(req.id, ErrorCode.UNKNOWN_OP, f"unknown op: {req.op}")
    try:
        result = handler(req.args)
    except HelperError as exc:
        return error_reply(req.id, exc.code, exc.message)
    except Exception as exc:  # noqa: BLE001 - every failure becomes a reply
        # Only the exception's type goes back: its text could one day carry
        # something from the broker that does not belong in a log.
        return error_reply(
            req.id, ErrorCode.INTERNAL, f"{req.op} failed: {type(exc).__name__}"
        )
    return ok_reply(req.id, result)


def read_request(conn: socket.socket) -> bytes | None:
    """Read one newline-terminated request.

    Returns the line without its newline, or None when the client closed the
    connection or went quiet before finishing it. Raises HelperError when the
    request is larger than MAX_REQUEST_BYTES.
    """
    buf = bytearray()
    while True:
        newline = buf.find(b"\n")
        if newline >= 0:
            return bytes(buf[:newline])
        if len(buf) > MAX_REQUEST_BYTES:
            raise HelperError(
                ErrorCode.BAD_REQUEST, f"request is larger than {MAX_REQUEST_BYTES} bytes"
            )
        try:
            chunk = conn.recv(65536)
        except (socket.timeout, OSError):
            return None
        if not chunk:
            return None
        buf += chunk


def handle_connection(
    conn: socket.socket,
    ops: Mapping[str, Handler] = OPS,
    read_timeout: float = DEFAULT_READ_TIMEOUT,
) -> None:
    """Serve the single call one connection carries."""
    conn.settimeout(read_timeout)
    try:
        line = read_request(conn)
    except HelperError as exc:
        reply = error_reply(None, exc.code, exc.message)
    else:
        if line is None:
            return
        reply = handle(line, ops)
    try:
        conn.sendall(reply)
    except OSError as exc:
        # The client is gone. The call has already run once; it is not run
        # again, and the client sees the closed socket as an error.
        log(f"could not send reply: {type(exc).__name__}")


def bind(socket_path: str) -> socket.socket:
    """Listen on socket_path, readable and writable by this user only.

    A stale socket left by an earlier run is replaced. Any other kind of file
    at the path is left alone and the helper refuses to start.
    """
    try:
        st = os.lstat(socket_path)
    except FileNotFoundError:
        pass
    else:
        if not stat.S_ISSOCK(st.st_mode):
            raise SystemExit(f"brokerhelper: {socket_path} exists and is not a socket")
        os.unlink(socket_path)

    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    old_umask = os.umask(0o177)
    try:
        sock.bind(socket_path)
    except OSError:
        sock.close()
        raise
    finally:
        os.umask(old_umask)
    sock.listen()
    return sock


def serve(
    socket_path: str,
    ops: Mapping[str, Handler] = OPS,
    read_timeout: float = DEFAULT_READ_TIMEOUT,
    ready_out: IO[str] | None = None,
) -> None:
    """Serve calls on socket_path until the process is told to stop."""
    sock = bind(socket_path)
    try:
        out = ready_out if ready_out is not None else sys.stdout
        print(READY_LINE, file=out, flush=True)
        while True:
            conn, _ = sock.accept()
            with conn:
                handle_connection(conn, ops, read_timeout)
    finally:
        sock.close()
        try:
            os.unlink(socket_path)
        except FileNotFoundError:
            pass


def log(message: str) -> None:
    """Write one diagnostic line to stderr. Never pass request contents."""
    print(f"brokerhelper: {message}", file=sys.stderr, flush=True)
