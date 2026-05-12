"""Go-beancount adapter for beancompat.

Implements beancompat's Implementation protocol for the parse tier only.
Delegates to the beanparse binary via subprocess; never raises out of
parse_string or check_file for subprocess-level failures (missing binary,
nonzero exit, invalid JSON, timeout) -- those produce a ParseResult with a
diagnostic error string.

Beancompat sources (implementations.adapter providing Directive / ParseResult)
are imported lazily on first call to parse_string / check_file. Import failure
at that point raises ImportError loudly rather than being silently masked, so
deployment problems are diagnosable. Module load itself has no side effects
beyond stdlib imports, which keeps name / capabilities introspectable from
ad-hoc debugging contexts.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Optional


# CAP_PARSE is the string literal value defined in beancompat's
# implementations/adapter.py. Duplicated here so the `capabilities` property
# never triggers the heavier beancompat import. Verified against upstream.
_CAP_PARSE = "parse"


def _resolve_beancompat_root() -> str:
    """Return the filesystem path to the beancompat source root.

    Uses Bazel runfiles to locate implementations/__init__.py inside the
    @beancompat external repo and returns its grandparent directory (which is
    the root containing implementations/, strategies/, tests/).

    Raises ImportError if the path cannot be resolved -- callers translate
    this into a ParseResult diagnostic, or let it propagate at lazy-import
    time for deployment debugging.
    """
    from python.runfiles import Runfiles

    r = Runfiles.Create()
    if r is None:
        raise ImportError("Bazel runfiles unavailable; cannot locate beancompat sources")
    probe = r.Rlocation("beancompat/implementations/__init__.py")
    if not probe or not os.path.exists(probe):
        raise ImportError(
            f"beancompat/implementations/__init__.py not found in runfiles (got {probe!r})"
        )
    return str(Path(probe).parent.parent)


def _load_beancompat_types():
    """Lazy-import (Directive, ParseResult) from beancompat.

    Idempotent: ensures the beancompat source root is on sys.path, then
    imports. On failure raises ImportError with a chained cause so the
    underlying problem is visible.
    """
    root = _resolve_beancompat_root()
    if root not in sys.path:
        sys.path.insert(0, root)
    from implementations.adapter import Directive, ParseResult  # noqa: WPS433

    return Directive, ParseResult


def _resolve_binary() -> Optional[str]:
    override = os.environ.get("BEANPARSE_BIN")
    if override:
        return override
    try:
        from python.runfiles import Runfiles

        r = Runfiles.Create()
        if r is not None:
            # rules_go places the go_binary executable under a
            # `<target_name>_/<target_name>` subdirectory in runfiles.
            path = r.Rlocation(
                "go-beancount/pkg/compat/beancompat/adapter/beanparse/beanparse_/beanparse"
            )
            if path and os.path.exists(path):
                return path
    except ImportError:
        pass
    return None


def _runfiles_env() -> dict:
    """Return os.environ merged with Bazel runfiles env vars, if available."""
    env = os.environ.copy()
    try:
        from python.runfiles import Runfiles

        r = Runfiles.Create()
        if r is not None:
            env.update(r.EnvVars())
    except ImportError:
        pass
    return env


class GoBeancountAdapter:
    """Adapter for go-beancount (parse tier only).

    Invokes the beanparse binary via subprocess. Binary is located via the
    BEANPARSE_BIN environment variable or Bazel runfiles.
    """

    @property
    def name(self) -> str:
        return "gobeancount"

    @property
    def capabilities(self) -> set[str]:
        return {_CAP_PARSE}

    def is_available(self) -> bool:
        path = _resolve_binary()
        if path is None or not os.access(path, os.X_OK):
            return False
        try:
            _load_beancompat_types()
        except (ImportError, OSError, RuntimeError):
            # ImportError: beancompat sources not on runfiles; OSError /
            # RuntimeError: Runfiles.Create() failed (e.g. not in a Bazel
            # sandbox). Any of these means "not available" rather than a bug.
            return False
        return True

    def parse_string(self, source: str):
        tmp_path = None
        try:
            with tempfile.NamedTemporaryFile(
                mode="w", suffix=".beancount", delete=False
            ) as f:
                f.write(source)
                f.flush()
                tmp_path = f.name
            return self.check_file(Path(tmp_path))
        finally:
            if tmp_path is not None:
                try:
                    os.unlink(tmp_path)
                except OSError:
                    pass

    def check_file(self, path: Path):
        Directive, ParseResult = _load_beancompat_types()

        binary = _resolve_binary()
        if binary is None:
            return ParseResult(
                directives=[],
                errors=["beanparse binary not found; check BEANPARSE_BIN or Bazel runfiles"],
            )

        try:
            result = subprocess.run(
                [binary, str(path)],
                capture_output=True,
                text=True,
                timeout=30,
                env=_runfiles_env(),
            )
        except subprocess.TimeoutExpired as e:
            return ParseResult(
                directives=[],
                errors=[f"beanparse timed out after 30s: {e}"],
            )
        except OSError as e:
            return ParseResult(
                directives=[],
                errors=[f"beanparse failed to start: {e}"],
            )

        if result.returncode != 0:
            return ParseResult(
                directives=[],
                errors=[
                    f"beanparse exited with code {result.returncode}: {result.stderr.strip()}"
                ],
            )

        try:
            data = json.loads(result.stdout)
        except json.JSONDecodeError as e:
            return ParseResult(
                directives=[],
                errors=[
                    f"beanparse produced invalid JSON: {e}; stdout={result.stdout!r}"
                ],
            )

        directives = [
            Directive(
                type=d["type"],
                date=d["date"],
                meta=d.get("meta", {}),
                data=d.get("data", {}),
            )
            for d in data["directives"]
        ]
        return ParseResult(
            directives=directives,
            errors=data.get("errors", []),
            options=data.get("options", {}),
        )
