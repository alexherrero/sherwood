"""Run the broker helper: ``python -m brokerhelper --socket PATH``."""

from __future__ import annotations

import argparse
import signal
import sys
from types import FrameType

from .server import DEFAULT_READ_TIMEOUT, serve


def _stop(signum: int, frame: FrameType | None) -> None:
    raise SystemExit(0)


def main(argv: list[str] | None = None) -> int:
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
    args = parser.parse_args(argv)

    signal.signal(signal.SIGTERM, _stop)
    signal.signal(signal.SIGINT, _stop)
    serve(args.socket, read_timeout=args.read_timeout)
    return 0


if __name__ == "__main__":
    sys.exit(main())
