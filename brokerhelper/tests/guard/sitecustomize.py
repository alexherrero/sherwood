"""Refuse to import the real broker library or an HTTP client.

The tests put this folder on PYTHONPATH (and conftest.py installs the same
guard in the test process), so any attempt to load the broker library's
network code during a test fails loudly instead of reaching the network.
"""

import importlib.abc
import sys

BLOCKED = ("robin_stocks", "requests", "urllib3", "pyotp")


class _Refuse(importlib.abc.MetaPathFinder):
    def find_spec(self, fullname, path=None, target=None):
        if fullname.split(".")[0] in BLOCKED:
            raise ImportError(f"tests must not import {fullname}")
        return None


def install():
    if not any(isinstance(f, _Refuse) for f in sys.meta_path):
        sys.meta_path.insert(0, _Refuse())


install()
