# beancompat Pytest Harness Architecture

This document describes the steady-state architecture of the Bazel-native
harness that runs upstream beancompat's pytest compatibility suite against
go-beancount via a subprocess adapter. It is the post-decision reference;
the chronological design narrative (alternatives weighed, decisions made)
lives in `docs/plans/beancompat-pytest-integration.md` and, for the
denylist + full-pipeline migration that brought coverage to all 13
upstream fixtures, `docs/plans/pyharness-denylist-migration.md`.

## Overview

The harness lets `bazel test //...` exercise upstream beancompat fixtures
against go-beancount. It runs `tests/test_fixtures.py` against all 13
upstream fixtures (10 parse + 3 check). The Python adapter advertises
`{CAP_PARSE, CAP_BOOKING}`, so check-tier fixtures parametrize
alongside parse-tier ones. Per-fixture divergences are tracked through
a two-tier xfail policy (see "Divergence Policy" below); the framework
is designed to be extended to other `tests/test_*.py` files without
architectural rework.

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
       │  + denylist.py      │  │  (upstream, @beancompat) │
       │  (pyharness/, ours) │  │  parametrize over        │
       │                     │  │  (adapter, fixture)      │
       │  - sys.path setup   │  └────────────┬─────────────┘
       │  - ADAPTERS.clear() │               │
       │  - register adapter │               │  calls ADAPTERS["gobeancount"]
       │  - xfail-mark hook  │               │
       └─────────────────────┘               ▼
                             ┌──────────────────────────────────┐
                             │  GoBeancountAdapter              │  Python adapter
                             │  (adapter/__init__.py, ours)     │
                             │  - Implementation protocol       │
                             │  - {CAP_PARSE, CAP_BOOKING}      │
                             │  - subprocess.run(beanparse)     │
                             └────────────────┬─────────────────┘
                                              │  os.exec, JSON over stdout
                                              ▼
                             ┌──────────────────────────────────┐
                             │  beanparse                       │  Go binary
                             │  (adapter/beanparse/, ours)      │
                             │  - loader.Load + SerializeChecked│
                             │  - {directives, errors, options} │
                             └──────────────────────────────────┘
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
  layer: overlay `conftest.py`, local divergence registry `denylist.py`,
  and the `pytest_main.py` entrypoint. The seam between Bazel's test
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

`beanparse` runs the full loader pipeline: `loader.Load(ctx, src)`
followed by `pkg/compat/beancompat.SerializeChecked`. The output is
the check-tier shape — directives reflect parse + plugins + pad +
balance + validations. This matches upstream's `CAP_BOOKING` contract,
which expects `parse_string` to return post-booking output (e.g.
`posting["cost"]["number"]` as a flat field on a booked `Cost`, the
shape `tests/test_cost_inventory.py` asserts). Parse-tier-only output
(`SerializeParsed`) is reachable only through the Go library, not
through this binary; nothing on the Python side consumes it because
parse-tier `CostSpec` is not in beancompat's validation surface
(verified: zero references in upstream `tests/`).

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
invisible at this scale (13 fixtures × ~10ms each).

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

## Divergence Policy (Two-Tier xfail Denylist)

Every collected `(adapter, fixture)` pair runs by default. A fixture
that diverges from its expected envelope is recorded as a known
divergence at one of two tiers; the test still runs, but its failure
is reported as XFAIL and an inadvertent fix surfaces as XPASS in the
test output.

**Tier 1 — upstream `known_divergences`.** A fixture file may carry
`known_divergences: {adapter_name: "reason"}` in its JSON.
Upstream `tests/test_fixtures.py` reads this inside the test body and
calls `pytest.xfail(...)` when the active adapter is listed. This is
the right tier once a divergence is accepted upstream.

**Tier 2 — local `pyharness/denylist.py`.** Divergences not yet
reflected upstream live in a `DENIED_FIXTURES: dict[str, str]` mapping
tier-prefixed fixture path → reason. The overlay
`pytest_collection_modifyitems` hook applies
`pytest.mark.xfail(strict=False, reason=...)` to each matching
collected item.

```python
# pyharness/denylist.py
DENIED_FIXTURES: dict[str, str] = {
    "parse/display_precision_by_currency.json": (
        "go-beancount fix pending: parse-tier serializer does not yet "
        "emit the options envelope (display_precision_by_currency expected)."
    ),
    ...
}
```

`strict=False` mirrors the Go-side `t.Skipf` semantics in
`pkg/compat/beancompat/denylist.go`: a denylisted fixture that briefly
passes surfaces as XPASS without failing the suite. This is the
intended maintenance signal, not a tripwire — flipping to
`strict=True` would re-cast the registry as a regression gate.

Stale entries fail collection. The hook tracks which `DENIED_FIXTURES`
keys matched a collected fixture id; any entry that did not match
raises `pytest.UsageError`, which pytest surfaces as a collection
error (exit code 4). Mirrors `runFixtures`' `t.Errorf` on stale
denylist entries on the Go side. Annotate every entry's reason with
"upstream-PR pending" or "go-beancount fix pending" so the registry
is easy to triage as fixes land.

The fixture identifier is read from
`item.callspec.params["fixture_path"]` and compared against the form
upstream's `_fixture_id` emits, so no normalization layer can drift.

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
   fixture-id comparison against the denylist fails silently — and
   because stale entries fail collection, that silent drift would
   manifest as bogus collection errors rather than passing tests.

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
escape-sensitive in some comparison paths, so any fixture containing
`<` / `>` / `&` would diverge without the fix.

`marshalNoEscape` uses `json.NewEncoder(&buf)` with
`SetEscapeHTML(false)` and strips the trailing newline `Encode`
unconditionally appends. It is applied to every per-directive
`*DataPayload` function and to the value path (not the key path) of
`marshalSortedObject`. Keys are Go identifiers or beancount keywords
and cannot contain HTML-special characters.

The fix is forward-looking: the 11 currently-passing fixtures happen
not to contain HTML-special characters in their expected envelopes,
but the escape-insensitivity it grants is what keeps that property
non-fragile as new fixtures are accepted upstream.

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

**Recording a new divergence.** Add a tier-prefixed path to
`DENIED_FIXTURES` in `pyharness/denylist.py` with a reason annotated
"go-beancount fix pending" or "upstream-PR pending". When the
underlying fix lands, remove the entry; pytest will start producing
XPASS reports immediately if the fixture starts passing while the
entry is still present, and stale entries (no matching fixture) fail
collection. Track the Go-side `pkg/compat/beancompat/denylist.go` for
parity.

**Adding more capabilities.** The current adapter advertises
`{CAP_PARSE, CAP_BOOKING}`. Wiring up a new capability (e.g.
`CAP_PLUGINS`, `CAP_BQL`, `CAP_PRINT`) requires:

- A serializer or query entry point in `pkg/compat/beancompat` (and
  any supporting Go-side machinery — for example, BQL needs a BQL
  evaluator).
- A new Go CLI or method on the adapter that exposes it via
  subprocess.
- A new capability constant in the adapter's `capabilities` set
  (mirroring the upstream literal).
- Optionally a new `py_test` target if the relevant
  `tests/test_*.py` file is not yet wired in.

The Bazel rules, conftest, and denylist mechanisms carry over
unchanged.

## Future Work

In rough priority order:

- **Shared env-driven `pytest_main.py`.** When a second test target
  appears, factor the runfiles probe out and pass the target
  rlocation via an env var (e.g. `BEANCOMPAT_TEST_RLOCATION`). One
  entrypoint file serves N test targets.
- **Negative test for "non-denylisted divergence MUST fail".**
  A small `py_test` (gated `tags = ["manual"]`) that monkeypatches
  one passing fixture's expected output and asserts the suite
  fails. Validates that the xfail mechanism does not mask failures
  for fixtures outside the denylist.
- **`hypothesis` in the pip lock.** Required for
  `tests/test_discrepancies.py`. Pulls in transitive deps; defer
  until that test file is wanted.
- **Cross-implementation comparison (`tests/test_cross_impl.py`).**
  Meaningless with a single registered adapter. Becomes useful only
  if a second adapter is registered (e.g. shelling out to a
  reference Python beancount install) for differential testing.

## Out of Scope

- **Upstreaming `GoBeancountAdapter` to beancompat.** Premature:
  only `{CAP_PARSE, CAP_BOOKING}` are supported. Revisit once
  plugins / BQL / print capabilities land.
- **Reference Python `beancount` package in the sandbox.** Inflates
  pip deps significantly and brings ABI dependencies (the C extension
  is not pip-installable hermetically on all platforms). Out-of-band
  comparisons against reference beancount can be done locally if
  needed; the hermetic harness does not require them.
