"""Run the broker helper.

``python -m brokerhelper --socket PATH`` serves the Go application's calls.
``python -m brokerhelper login`` is the operator's one-time broker login.
"""

from __future__ import annotations

import argparse
import signal
import sys
from types import FrameType

from . import login
from .ops import DEFAULT_DATA_DIR, broker_ops, default_broker
from .server import DEFAULT_READ_TIMEOUT, serve


def _stop(signum: int, frame: FrameType | None) -> None:
    raise SystemExit(0)


def main(argv: list[str] | None = None) -> int:
    argv = sys.argv[1:] if argv is None else argv
    if argv and argv[0] == "login":
        return login.main(argv[1:])

    parser = argparse.ArgumentParser(
        prog="brokerhelper",
        description="Answer Sherwood's broker calls over a Unix socket.",
    )
    parser.add_argument("--socket", required=True, help="path of the Unix socket to listen on")
    parser.add_argument(
        "--read-timeout",
        type=float,
        default=DEFAULT_READ_TIMEOUT,
        help="seconds to wait for a client to finish its request (default %(default)s)",
    )
    parser.add_argument(
        "--data-dir",
        default=DEFAULT_DATA_DIR,
        help="the application-data folder that holds broker.yaml (default %(default)s)",
    )
    args = parser.parse_args(argv)

    signal.signal(signal.SIGTERM, _stop)
    signal.signal(signal.SIGINT, _stop)
    ops = broker_ops(default_broker(args.data_dir))
    serve(args.socket, ops=ops, read_timeout=args.read_timeout)
    return 0


if __name__ == "__main__":
    sys.exit(main())
