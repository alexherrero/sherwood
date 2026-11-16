"""The operations the helper offers.

Each operation takes the request's ``args`` object and returns the reply's
``result`` object, or raises HelperError with a typed code. An operation runs
exactly once per request: the helper never retries one, whatever goes wrong.
Later parts add the broker operations here; today there is only ``ping``.
"""

from __future__ import annotations

import os
from typing import Any, Callable, Dict

from . import __version__
from .protocol import ErrorCode, HelperError

Handler = Callable[[Dict[str, Any]], Dict[str, Any]]


def ping(args: dict[str, Any]) -> dict[str, Any]:
    """Answer that the helper is alive, with its version and process id."""
    if args:
        raise HelperError(ErrorCode.BAD_REQUEST, "ping takes no arguments")
    return {"version": __version__, "pid": os.getpid()}


OPS: dict[str, Handler] = {"ping": ping}
