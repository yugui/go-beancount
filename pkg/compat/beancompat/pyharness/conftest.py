"""Overlay conftest for go-beancount beancompat test harness.

Inserts GoBeancountAdapter as the sole entry in tests.conftest.ADAPTERS
at module load time (before pytest parametrizes the session fixtures).
Also installs a pytest_collection_modifyitems hook that skips any fixture
not in ALLOWED_FIXTURES (deny-by-default policy).

All exceptions propagate loudly; no silent swallowing of errors.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

import pytest

from python.runfiles import Runfiles

from allowlist import ALLOWED_FIXTURES


def _resolve_beancompat_root() -> str:
    r = Runfiles.Create()
    if r is None:
        raise ImportError("Bazel runfiles unavailable; cannot locate beancompat sources")
    probe = r.Rlocation("beancompat/implementations/__init__.py")
    if not probe or not os.path.exists(probe):
        raise ImportError(
            f"beancompat/implementations/__init__.py not found in runfiles (got {probe!r})"
        )
    return str(Path(probe).parent.parent)


_beancompat_root = _resolve_beancompat_root()

if _beancompat_root not in sys.path:
    sys.path.insert(0, _beancompat_root)

import tests.conftest as _upstream  # noqa: E402

from adapter import GoBeancountAdapter  # noqa: E402

_upstream.ADAPTERS.clear()
_upstream.ADAPTERS["gobeancount"] = GoBeancountAdapter

_FIXTURES_DIR = Path(_beancompat_root) / "fixtures"


def _fixture_id_of(item) -> str | None:
    try:
        params = item.callspec.params
    except AttributeError:
        return None
    path = params.get("fixture_path")
    if path is None:
        return None
    try:
        return str(Path(path).relative_to(_FIXTURES_DIR))
    except ValueError:
        return None


def pytest_collection_modifyitems(config, items):
    skip_mark = pytest.mark.skip(reason="not in ALLOWED_FIXTURES")
    for item in items:
        fid = _fixture_id_of(item)
        if fid is None or fid not in ALLOWED_FIXTURES:
            item.add_marker(skip_mark)
