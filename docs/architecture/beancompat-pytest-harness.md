# beancompat Pytest Harness Architecture

This document describes the steady-state architecture of the Bazel-native
harness that runs upstream beancompat's pytest compatibility suite against
go-beancount via a subprocess adapter. It is the post-decision reference;
the chronological design narrative (alternatives weighed, decisions made)
lives in `docs/plans/beancompat-pytest-integration.md`.

## Overview

The harness lets `bazel test //...` exercise upstream beancompat fixtures
against go-beancount. Today it runs `tests/test_fixtures.py` (parse-tier,
`CAP_PARSE` only) for three fixtures (`open_single`, `price`,
`transaction_balanced`) — the same set the Go-side
`parse_fixtures_test.go` already covers. The framework is designed to be
extended to other `tests/test_*.py` files and to broader fixture
allowlists without architectural rework.

Hermeticity is a hard requirement: no network, no system Python, no
system beancount. All inputs are resolved through Bazel runfiles.

## Component Topology

```
        bazel test //pkg/compat/beancompat/pyharness:test_fixtures
                              │
                              ▼
              ┌─────────────────────────────┐
              │  pytest_main.py             │  resolve test file via runfiles
              │  (pyharness/, ours)         │  pytest.main(plugins=[_conftest])
              └──────┬──────────────────┬───┘
                     │                  │
       loads as      │                  │   positional arg
       plugin        ▼                  ▼
       ┌─────────────────────┐  ┌──────────────────────────┐
       │  conftest.py        │  │  tests/test_fixtures.py  │
       │  + allowlist.py     │  │  (upstream, @beancompat) │
       │  (pyharness/, ours) │  │  parametrize over        │
       │                     │  │  (adapter, fixture)      │
       │  - sys.path setup   │  └────────────┬─────────────┘
       │  - ADAPTERS.clear() │               │
       │  - register adapter │               │  calls ADAPTERS["gobeancount"]
       │  - skip-list hook   │               │
       └─────────────────────┘               ▼
                             ┌────────────────────────────────┐
                             │  GoBeancountAdapter            │  Python adapter
                             │  (adapter/__init__.py, ours)   │  Step 4
                             │  - Implementation protocol     │
                             │  - subprocess.run(beanparse)   │
                             └───────────────┬────────────────┘
                                             │  os.exec, JSON over stdout
                                             ▼
                             ┌────────────────────────────────┐
                             │  beanparse                     │  Go binary
                             │  (adapter/beanparse/, ours)    │  Step 1
                             │  - ast.Load + SerializeParsed  │
                             │  - {directives, errors, options}
                             └────────────────────────────────┘
```

Four owned surfaces:

- **`pkg/compat/beancompat/adapter/beanparse/`** — Go CLI binary. The
  subprocess called by the Python adapter. Lives in the adapter
  directory rather than under `cmd/` because it is subordinate to the
  test harness; nothing else invokes it.
- **`pkg/compat/beancompat/adapter/`** — Python package containing
  `GoBeancountAdapter`, an implementation of beancompat's
  `Implementation` protocol. Locates the binary via the
  `BEANPARSE_BIN` env var (preferred) or Bazel runfiles (fallback).
- **`pkg/compat/beancompat/pyharness/`** — Bazel `py_test` integration
  layer: overlay `conftest.py`, deny-by-default `allowlist.py`, and
  the `pytest_main.py` entrypoint. The seam between Bazel's test
  runner and upstream pytest.
- **`third_party/beancompat/beancompat.BUILD`** — Bazel BUILD overlay
  for the externally-fetched upstream repo. Exposes upstream Python
  sources as `filegroup`s only (no `py_library`), keeping the
  external BUILD `rules_python`-independent.

## Subprocess Contract

The adapter ↔ beanparse boundary is a stable wire protocol:

```
$ beanparse <file.beancount>
        │
        ▼
   stdout: JSON conforming to beancompat's portable schema
           {"directives": [...], "errors": [...], "options": {...}}
   exit:   0  on success, INCLUDING when the ledger has beancount-level
              errors (those go into the JSON `errors` array)
           ≠0 only for I/O failures, bad CLI usage, or internal
              serializer errors
```

Parse-tier semantics are load-bearing: `beanparse` calls `ast.Load` then
`pkg/compat/beancompat.SerializeParsed`, NOT the loader's full plugin /
pad / balance / validation pipeline. Mixing parse-tier output with
post-validation directives would change the JSON shape and break
fixture comparison.

The Python adapter wraps subprocess failures (missing binary, nonzero
exit, invalid JSON, timeout) into a `ParseResult` with a diagnostic
`errors` list. It never raises for subprocess-level conditions. Upstream
`tests/test_fixtures.py` relies on this: it always sees a `ParseResult`,
never an exception, and asserts on its content.

### Subprocess vs cgo

The subprocess model matches upstream's `rustledger` and `limabean`
adapters and avoids `libpython` linkage entirely. `rules_go` produces
the Go binary, `rules_python` runs the test driver, and the two never
share an address space. A cgo-fused binary was considered and rejected:
it would force an ABI between Go and Python types, inflate the sandbox
dependency footprint with `libpython`, and complicate `rules_go` /
`rules_python` interop. The marginal cost of process startup is
invisible at this scale (3 fixtures × ~10ms each).

## Adapter Registration

Upstream's `tests/conftest.py` exposes a module-global `ADAPTERS` dict
keyed by adapter name. Every parametrized test iterates
`ADAPTERS.items()` at collection time, so any registration mechanism
that runs later than collection produces zero `gobeancount`
parametrizations.

Our overlay conftest performs the registration at module top level
(pytest plugin import time, which precedes collection):

```python
import tests.conftest as _upstream
_upstream.ADAPTERS.clear()
_upstream.ADAPTERS["gobeancount"] = GoBeancountAdapter
```

Two notable choices:

1. **Top-level mutation, not autouse fixture or entry-point plugin.**
   Autouse session fixtures run too late — pytest evaluates the
   `parametrize` argument during collection, before any fixture runs.
   Entry-point plugins would require a `setup.py` / `pyproject.toml`
   install dance that fights `rules_python`'s hermetic model.

2. **`clear()` before register.** The hermetic Bazel sandbox has no
   reference `beancount`, no Rust toolchain for `rustledger`, no JVM
   for `limabean`. Their `is_available()` probes all fail. Clearing
   the dict and registering only `gobeancount` eliminates per-test
   subprocess probes that would fail anyway, and removes a latent
   failure class driven by upstream changes to `is_available()`
   semantics.

## Skip Policy (Deny-by-Default Allowlist)

Upstream parametrizes over `(adapter, fixture)` pairs. Without
filtering, the harness would report failures for every fixture we
don't yet support. Instead we apply a deny-by-default skip list via
pytest's `pytest_collection_modifyitems` hook:

```python
# pyharness/allowlist.py
ALLOWED_FIXTURES: frozenset[str] = frozenset({
    "parse/open_single.json",
    "parse/price.json",
    "parse/transaction_balanced.json",
})
```

The hook resolves each item's `fixture_path` parameter to a
tier-prefixed JSON path and applies `pytest.mark.skip(reason="not in
ALLOWED_FIXTURES")` to any item whose identifier is not in the set.
Allowlisted items run normally; if they fail, the test FAILS — the
skip mechanism does not mask failures of allowlisted fixtures.

The fixture identifier is read from `item.callspec.params["fixture_path"]`
and compared against the form upstream's `_fixture_id` emits, so no
normalization layer can drift.

Widening the allowlist is a deliberate, file-local edit.

## Bazel Integration Touch-points

1. **`rules_python` hermetic toolchain.** `MODULE.bazel` pins Python
   3.12 via `rules_python`'s extension; `pip.parse` locks `pyyaml`
   (beancompat's only runtime dep) and `pytest`. Lockfiles
   (`requirements_lock.txt`, `MODULE.bazel.lock`) are checked in.

2. **Filegroup-only exposure of upstream Python sources.**
   `third_party/beancompat/beancompat.BUILD` lists `tests_py`,
   `implementations_py`, `strategies_py`, `scripts_py`,
   `parse_fixtures`, and `check_fixtures` as `filegroup`s with
   `//visibility:public`. No `py_library` in the external BUILD —
   that would require loading `rules_python` symbols into the
   external repo's BUILD file. Consumers wrap the filegroups in
   `py_library` rules on our side.

3. **Explicit pytest plugin registration.** `pytest_main.py` imports
   the overlay conftest as a module and passes it as
   `plugins=[_conftest]` to `pytest.main()`. Pytest's normal conftest
   auto-discovery walks the filesystem from rootdir to test file, but
   our conftest at `_main/pkg/compat/beancompat/pyharness/conftest.py`
   and the upstream test at
   `+http_archive+beancompat/tests/test_fixtures.py` live in separate
   runfiles branches; auto-discovery never crosses the boundary.

4. **`BEANPARSE_BIN` env injection via `$(rootpath ...)`.** The
   `py_test` rule sets
   `env = {"BEANPARSE_BIN": "$(rootpath //pkg/compat/beancompat/adapter/beanparse:beanparse)"}`.
   This locks the binary path at BUILD time and bypasses
   `rules_go`'s `beanparse_/beanparse` intermediate-directory layout
   in runfiles, which the adapter's runfiles fallback would
   otherwise have to navigate. The runfiles fallback exists for
   ad-hoc debugging and for the adapter unit test; it is not the
   primary discovery path.

5. **`Path(probe).resolve()` symlink walk in the conftest.** Bazel's
   runfiles tree contains symlinks pointing to the content-addressed
   cache. The conftest's `Rlocation()` returns the sandbox path,
   while upstream `test_fixtures.py` computes `FIXTURES_DIR` via
   `Path(__file__).resolve()`, which follows all symlinks to the
   cache root. The conftest must use `Path(probe).resolve()` for
   its own `_FIXTURES_DIR` so the two roots agree; otherwise every
   fixture-id comparison against the allowlist fails silently.

## Surgical Forward Fix: HTML Escape

`pkg/compat/beancompat/serialize.go` was modified to use a custom
`marshalNoEscape` helper at every per-directive payload site instead
of plain `json.Marshal`. The fix is +25 net lines across 13 callsites.

The bug:

- Python's `json.dumps()` emits `<`, `>`, `&` as literal characters.
- Go's `encoding/json.Marshal` escapes them to `\u003c`, `\u003e`,
  `\u0026` by default (defensive against XSS in HTML contexts).
- beancompat's portable JSON fixtures are generated from Python, so
  narrations / comments / metadata that legitimately contain those
  characters appear literally.

Go-side `parse_fixtures_test.go` matchers unmarshal both sides before
comparing, so `\u003c` and `<` round-trip to the same Go string and
tests pass regardless of escaping convention. The Python harness is
escape-sensitive in some comparison paths, so any future allowlist
fixture containing `<` / `>` / `&` would diverge.

`marshalNoEscape` uses `json.NewEncoder(&buf)` with
`SetEscapeHTML(false)` and strips the trailing newline `Encode`
unconditionally appends. It is applied to every per-directive
`*DataPayload` function and to the value path (not the key path) of
`marshalSortedObject`. Keys are Go identifiers or beancount keywords
and cannot contain HTML-special characters.

The fix is forward-looking: it does not unblock the current three
allowlist entries, but it eliminates a latent failure mode that would
surface the first time the allowlist grows.

## Extensibility

The harness is designed to grow along three axes; none requires
architectural rework.

**Adding a new `tests/test_*.py` file.** Today's `pytest_main.py`
hardcodes `"beancompat/tests/test_fixtures.py"`. To run
`tests/test_balance.py` add a parallel entrypoint (or refactor the
existing one to accept the target via env var — see Future Work) and
a second `py_test` rule in `pyharness/BUILD.bazel`. Most data flow is
transitive through `:conftest`; only test-file-specific fixtures or
scripts modules need new `data` entries.

**Widening the fixture allowlist.** Add tier-prefixed paths to
`ALLOWED_FIXTURES` in `pyharness/allowlist.py`. Newly-included
fixtures that don't pass yet either get pulled back out (with a
comment documenting the divergence) or motivate a surgical fix to
`pkg/compat/beancompat/serialize.go`.

**Adding capabilities (CAP_CHECK and above).** The current adapter
implements `CAP_PARSE` only. CAP_CHECK requires:

- A `SerializeChecked` (or similarly-named) entry point in
  `pkg/compat/beancompat` emitting the post-validation JSON shape.
- A second Go CLI (or a flag on `beanparse`) that runs the full
  loader pipeline.
- An adapter method (`check_file`) wired to the new subprocess.
- A new `CAP_CHECK` flag in the adapter's `capabilities` set.

The Bazel rules, conftest, and allowlist mechanisms carry over
unchanged.

## Future Work

In rough priority order:

- **Shared env-driven `pytest_main.py`.** When a second test target
  appears, factor the runfiles probe out and pass the target
  rlocation via an env var (e.g. `BEANCOMPAT_TEST_RLOCATION`). One
  entrypoint file serves N test targets.
- **CAP_BOOKING / CAP_CHECK / `SerializeChecked`.** Tracked separately
  under repo-root `PLAN.md` (Plan-C). When that lands, this harness
  extends to check-tier fixtures with no overlay changes.
- **Negative test for "allowlisted-fixture divergence MUST fail".**
  A small `py_test` (gated `tags = ["manual"]`) that monkeypatches
  one allowlisted fixture's expected output and asserts the suite
  fails. Validates that the skip mechanism does not mask failures.
- **`hypothesis` in the pip lock.** Required for
  `tests/test_discrepancies.py`. Pulls in transitive deps; defer
  until that test file is wanted.
- **Cross-implementation comparison (`tests/test_cross_impl.py`).**
  Meaningless with a single registered adapter. Becomes useful only
  if a second adapter is registered (e.g. shelling out to a
  reference Python beancount install) for differential testing.

## Out of Scope

- **Upstreaming `GoBeancountAdapter` to beancompat.** Premature: only
  `CAP_PARSE` is supported. Revisit once CAP_CHECK lands.
- **Reference Python `beancount` package in the sandbox.** Inflates
  pip deps significantly and brings ABI dependencies (the C extension
  is not pip-installable hermetically on all platforms). Out-of-band
  comparisons against reference beancount can be done locally if
  needed; the hermetic harness does not require them.
