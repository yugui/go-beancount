"""Thin pytest entrypoint for the beancompat test_fixtures suite.

Resolves the upstream test file via Bazel runfiles and delegates to
in-process pytest.main. Must not mutate sys.path; the overlay conftest
(loaded transitively via deps) handles beancompat root injection.

The overlay conftest (conftest.py) is registered explicitly as a pytest
plugin so that its ADAPTERS mutation and pytest_collection_modifyitems
hook take effect even though it lives outside the test file's directory
tree (conftest auto-discovery is filesystem-based, not sys.path-based).
"""

from __future__ import annotations

import os
import sys

import conftest as _conftest
import pytest
from python.runfiles import Runfiles


def _main() -> int:
    r = Runfiles.Create()
    if r is None:
        raise RuntimeError("Bazel runfiles unavailable; cannot locate test_fixtures.py")
    test_file = r.Rlocation("beancompat/tests/test_fixtures.py")
    if not test_file:
        raise RuntimeError("beancompat/tests/test_fixtures.py not found in runfiles")
    if not os.path.exists(test_file):
        raise RuntimeError(f"Resolved test file does not exist: {test_file!r}")
    return pytest.main(
        ["-v", "--no-header", "--tb=short", str(test_file)],
        plugins=[_conftest],
    )


if __name__ == "__main__":
    sys.exit(_main())
