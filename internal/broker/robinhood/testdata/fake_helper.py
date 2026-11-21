"""Serve the real broker helper with its library faked, for the Go contract test.

    python3 fake_helper.py --repo ROOT [--logged-out] --socket PATH

The helper's own operations, protocol and server run unchanged; only the
library underneath is brokerhelper/tests/fakes.py's FakeLibrary, with its
made-up records. The import guard from brokerhelper/tests/guard is installed
first, so the real broker library and any HTTP client cannot load. Nothing
here reaches a broker or the network.

With --logged-out there is no saved session, so every broker operation
answers login_needed.
"""

import argparse
import importlib.util
import shutil
import signal
import sys
import tempfile
from pathlib import Path


def _stop(signum, frame):
    raise SystemExit(0)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", required=True, help="the repository's root folder")
    parser.add_argument("--logged-out", action="store_true", help="serve with no saved broker session")
    parser.add_argument("--socket", required=True, help="the Unix socket to listen on")
    args = parser.parse_args()

    root = Path(args.repo) / "brokerhelper"
    guard_path = root / "tests" / "guard" / "sitecustomize.py"
    spec = importlib.util.spec_from_file_location("brokerhelper_test_guard", guard_path)
    guard = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(guard)  # installs the guard
    sys.path[:0] = [str(root / "src"), str(root / "tests")]

    import fakes
    from brokerhelper import brokerfile
    from brokerhelper.ops import Broker, broker_ops
    from brokerhelper.server import serve

    data_dir = Path(tempfile.mkdtemp(prefix="bhdata"))
    try:
        if not args.logged_out:
            brokerfile.write(data_dir, fakes.saved_session())
        broker = Broker(library=fakes.FakeLibrary(), data_dir=data_dir, now=lambda: fakes.NOW)
        signal.signal(signal.SIGTERM, _stop)
        signal.signal(signal.SIGINT, _stop)
        serve(args.socket, ops=broker_ops(broker))
    finally:
        shutil.rmtree(data_dir, ignore_errors=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
