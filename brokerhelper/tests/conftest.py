"""Shared test set-up: no test may load the real broker library."""

import importlib.util
from pathlib import Path

GUARD = Path(__file__).resolve().parent / "guard"

_spec = importlib.util.spec_from_file_location(
    "brokerhelper_test_guard", GUARD / "sitecustomize.py"
)
_guard = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_guard)
BLOCKED = _guard.BLOCKED
