"""The wire format between the Go application and the broker helper.

One connection carries exactly one call. The client sends one JSON object
followed by a newline, and the helper answers with one JSON object followed by
a newline, then closes the connection.

Request::

    {"id": "17", "op": "ping", "args": {}}

``id`` is a non-empty string the client chooses and the reply echoes. ``op``
names the operation. ``args`` is an object and may be left out when empty.
No other keys are allowed.

Reply on success::

    {"id": "17", "ok": true, "result": {...}}

Reply on failure::

    {"id": "17", "ok": false, "error": {"code": "unknown_op", "message": "..."}}

``code`` is one of the ``ErrorCode`` values, so the Go side can branch on it;
``message`` is for people. When a request is too broken to read its ``id``, the
reply's ``id`` is null.

Money, prices and share counts travel as strings. JSON numbers with a fraction
or exponent, and NaN or Infinity, are refused, so a decimal value can never be
quietly turned into a float on the way in.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any

# The largest request the helper reads. Anything bigger is refused.
MAX_REQUEST_BYTES = 1 << 20


class ErrorCode:
    """The typed error codes a reply can carry."""

    # The request could not be read: bad JSON, a missing or wrong field, or
    # arguments the operation does not accept.
    BAD_REQUEST = "bad_request"
    # The request named an operation the helper does not offer.
    UNKNOWN_OP = "unknown_op"
    # The operation failed in a way the helper did not expect.
    INTERNAL = "internal"


class HelperError(Exception):
    """An error the helper reports to the caller with a typed code."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


@dataclass(frozen=True)
class Request:
    """One decoded call."""

    id: str
    op: str
    args: dict[str, Any] = field(default_factory=dict)


_ALLOWED_KEYS = frozenset({"id", "op", "args"})


def _refuse_float(text: str) -> Any:
    raise HelperError(
        ErrorCode.BAD_REQUEST,
        "numbers with a fraction or exponent are not accepted; send decimal values as strings",
    )


def _refuse_constant(text: str) -> Any:
    raise HelperError(ErrorCode.BAD_REQUEST, f"{text} is not accepted")


def decode_request(line: bytes) -> Request:
    """Decode one request line, or raise HelperError with BAD_REQUEST."""
    try:
        text = line.decode("utf-8")
    except UnicodeDecodeError:
        raise HelperError(ErrorCode.BAD_REQUEST, "request is not valid UTF-8") from None
    try:
        obj = json.loads(
            text, parse_float=_refuse_float, parse_constant=_refuse_constant
        )
    except json.JSONDecodeError as exc:
        raise HelperError(ErrorCode.BAD_REQUEST, f"request is not valid JSON: {exc.msg}") from None

    if not isinstance(obj, dict):
        raise HelperError(ErrorCode.BAD_REQUEST, "request must be a JSON object")
    extra = sorted(set(obj) - _ALLOWED_KEYS)
    if extra:
        raise HelperError(ErrorCode.BAD_REQUEST, f"unknown request field: {extra[0]}")

    req_id = obj.get("id")
    if not isinstance(req_id, str) or not req_id:
        raise HelperError(ErrorCode.BAD_REQUEST, "id must be a non-empty string")
    op = obj.get("op")
    if not isinstance(op, str) or not op:
        raise HelperError(ErrorCode.BAD_REQUEST, "op must be a non-empty string")
    args = obj.get("args", {})
    if not isinstance(args, dict):
        raise HelperError(ErrorCode.BAD_REQUEST, "args must be a JSON object")
    return Request(id=req_id, op=op, args=args)


def request_id_of(line: bytes) -> str | None:
    """Best-effort: the id of a request that failed to decode, or None."""
    try:
        obj = json.loads(line.decode("utf-8"))
    except (UnicodeDecodeError, ValueError, HelperError):
        return None
    if isinstance(obj, dict):
        req_id = obj.get("id")
        if isinstance(req_id, str) and req_id:
            return req_id
    return None


def _encode(obj: dict[str, Any]) -> bytes:
    return json.dumps(obj, separators=(",", ":"), allow_nan=False).encode("utf-8") + b"\n"


def ok_reply(req_id: str, result: dict[str, Any]) -> bytes:
    """Encode a success reply as one newline-terminated line."""
    return _encode({"id": req_id, "ok": True, "result": result})


def error_reply(req_id: str | None, code: str, message: str) -> bytes:
    """Encode a failure reply as one newline-terminated line."""
    return _encode({"id": req_id, "ok": False, "error": {"code": code, "message": message}})
