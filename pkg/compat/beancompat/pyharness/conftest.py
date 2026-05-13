"""Overlay conftest for go-beancount beancompat test harness.

Inserts GoBeancountAdapter as the sole entry in tests.conftest.ADAPTERS
at module load time (before pytest parametrizes the session fixtures).

Every collected fixture runs by default. Local divergences are applied
via pytest.mark.xfail(strict=False, reason=...) keyed by DENIED_FIXTURES
from denylist.py — this is the second tier of a two-tier divergence
policy; the first tier is the upstream fixture file's
known_divergences["gobeancount"] entry, which upstream test_fixtures.py
handles inline with pytest.xfail() at the test body. Stale local
entries (denylisted but with no matching collected fixture id) fail
collection so the registry cannot rot silently.

All exceptions propagate loudly; no silent swallowing of errors.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

import pytest

from python.runfiles import Runfiles

from denylist import DENIED_FIXTURES


def _resolve_beancompat_root() -> str:
    r = Runfiles.Create()
    if r is None:
        raise ImportError("Bazel runfiles unavailable; cannot locate beancompat sources")
    probe = r.Rlocation("beancompat/implementations/__init__.py")
    if not probe or not os.path.exists(probe):
        raise ImportError(
            f"beancompat/implementations/__init__.py not found in runfiles (got {probe!r})"
        )
    # Follow all symlinks so _FIXTURES_DIR matches the resolved path that
    # test_fixtures.py computes via Path(__file__).resolve().
    return str(Path(probe).resolve().parent.parent)


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
    # strict=False matches the Go-side t.Skipf semantics: a denylisted
    # fixture briefly passing produces XPASS in the report without failing
    # the suite, surfacing the maintenance signal without acting as a
    # tripwire. See docs/plans/pyharness-denylist-migration.md.
    seen: set[str] = set()
    for item in items:
        fid = _fixture_id_of(item)
        if fid is None:
            continue
        if fid in DENIED_FIXTURES:
            seen.add(fid)
            item.add_marker(
                pytest.mark.xfail(reason=DENIED_FIXTURES[fid], strict=False)
            )
    stale = set(DENIED_FIXTURES) - seen
    if stale:
        # pytest.UsageError surfaces as exit code 4 ("pytest was misused")
        # at collection time — mirrors runFixtures' t.Errorf on stale
        # denylist entries on the Go side.
        listing = ", ".join(sorted(stale))
        raise pytest.UsageError(
            f"denylist entries do not match any collected fixture: {listing}"
        )
