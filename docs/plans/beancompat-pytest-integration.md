# beancompat pytest integration as Bazel `py_test`

## Goal

Stand up a Bazel-native `py_test` framework that runs upstream beancompat's
pytest-based compatibility suite against go-beancount via a subprocess adapter.
Validate the framework by getting `tests/test_fixtures.py` (parse-tier,
`CAP_PARSE` only) to pass for go-beancount on the same three fixtures the
existing Go-side `parse_fixtures_test.go` already covers.

The framework must be extensible to additional `tests/test_*.py` files later
without architectural rework.

## Scope

### Included

- New Go helper binary at `pkg/compat/beancompat/adapter/beanparse/`
  exposing the subprocess contract `beanparse <file.beancount>` → stdout JSON
  in beancompat's portable schema `{directives, errors, options}` (exit 0 even
  when the ledger has beancount-level errors — those go into the JSON `errors`
  array; exit nonzero only for I/O or internal failures). Reuses
  `pkg/compat/beancompat.SerializeParsed`. Parse-tier only: must NOT invoke the
  plugin / validation pipeline. Placed under the Python adapter directory
  because the binary is subordinate to the compatibility test harness — it has
  no use outside it.
- `rules_python` wired into `MODULE.bazel` with a pinned hermetic CPython
  toolchain (Python 3.12). `pip.parse` to lock `pyyaml` and `pytest` (with
  transitives). Checked-in lockfile.
- `pkg/compat/beancompat/adapter/` Python package containing `GoBeancountAdapter`,
  implementing beancompat's `Implementation` protocol (`CAP_PARSE` only),
  invoking `beanparse` via runfiles.
- `pkg/compat/beancompat/tests/` containing the overlay `conftest.py`,
  `allowlist.py` (deny-by-default fixture skip list), and the `py_test`
  entrypoint. Overlay registers `gobeancount` into beancompat's `ADAPTERS`
  dict via top-level mutation.
- `third_party/beancompat/beancompat.BUILD` extended with filegroups for
  `tests/`, `implementations/`, `strategies/`. No `py_library` in that external
  BUILD (keeps the external repo's BUILD `rules_python`-independent).
- One `py_test` target `//pkg/compat/beancompat/tests:test_fixtures` that runs
  pytest against `tests/test_fixtures.py` for go-beancount only, with the same
  three fixtures (`open_single`, `price`, `transaction_balanced`) the Go-side
  allowlist enables.
- Surgical fixes to `pkg/compat/beancompat/serialize.go` are permitted only if
  they cleanly unblock a fixture in the initial allowlist.
- All existing `bazel test //...` targets remain green.

### Excluded

- CAP_BOOKING and higher capabilities (Plan-C / Plan-A work in repo-root
  `PLAN.md`). Check-tier fixtures stay disabled.
- Other pytest files (`test_parse.py`, `test_cross_impl.py`,
  `test_discrepancies.py`, `test_round_trip.py`, `test_hash.py`,
  `test_summarize.py`, `test_ingest.py`, `test_fava_compat.py`, etc.). The
  framework must allow adding them by an additional `py_test` rule, but no
  current target.
- cgo / Python C-extension in-process integration.
- Any large-scale go-beancount compatibility fixes.
- Upstreaming the `gobeancount` adapter to beancompat (premature: only
  `CAP_PARSE` is supported).
- Installing the reference `beancount` Python package in the hermetic sandbox.

## Architectural decisions (high-level)

- **Subprocess, not cgo.** Matches upstream rustledger / limabean adapters and
  keeps the Bazel toolchain story simple (no `libpython`-linkage requirement).
- **Top-level mutation of `tests.conftest.ADAPTERS` in our overlay conftest.**
  Autouse fixtures cannot achieve this — pytest evaluates the parametrize
  argument at collection time, so a session fixture would run too late and
  produce zero `gobeancount` parametrizations.
- **Filegroup-only exposure of beancompat Python sources.** Keeps
  `third_party/beancompat/beancompat.BUILD` free of `rules_python` load
  statements; the existing parse-fixtures filegroup build path stays
  untouched.
- **`pytest_collection_modifyitems`-based deny-by-default skip list.**
  Mirrors `pkg/compat/beancompat/allowlist.go`'s policy without sharing
  a serialized format at this stage.
- **New `pkg/compat/beancompat/adapter/beanparse` binary, not an extension to `cmd/beancheck`.**
  Parse-tier output must not be contaminated by plugin / validation passes.

## Steps

The plan is six steps. Dependency order: Go binary → Python toolchain →
external sources exposed → adapter → conftest → py_test target. Each step is
independently committable.

---

### Step 1 — Add `beanparse` helper binary under the adapter directory

**Functional requirements.**

- New CLI: `beanparse <file.beancount>` reads the file, runs through the
  parse-only path (`ast.LoadFile` or `ast.Load(string(bytes))`), calls
  `pkg/compat/beancompat.SerializeParsed(ledger)`, marshals the resulting
  `Result` to JSON, writes to stdout.
- Must NOT run the loader's plugin / pad / balance / validation pipeline —
  parse-tier semantics are load-bearing for fixture comparison.
- Exit 0 on success (including when the ledger reports beancount-level errors;
  those go into the JSON `errors` array). Exit nonzero only for I/O failures,
  bad CLI usage, or internal serializer failures.
- One positional arg; no flags initially. Package doc records the contract.
- Lives at `pkg/compat/beancompat/adapter/beanparse/` (not under `cmd/`)
  because it is subordinate to the compatibility test harness.

**Modules / files / targets touched.**

- New: `pkg/compat/beancompat/adapter/beanparse/main.go`,
  `pkg/compat/beancompat/adapter/beanparse/main_test.go`,
  `pkg/compat/beancompat/adapter/beanparse/BUILD.bazel`.
- Run `bazel run //:gazelle` after adding Go sources.

**Verification.**

- `bazel test //pkg/compat/beancompat/adapter/beanparse:beanparse_test`
  passes.
- Test drives a small inline beancount source via a tempfile and asserts the
  JSON matches `SerializeParsed`'s direct output (or compares by containment
  using `pkg/compat/beancompat.Match`).

**Quality requirements.**

- No new module-level deps; reuse `pkg/ast`, `pkg/compat/beancompat`.
- Architectural mirror of `cmd/beancheck`: testable `run()` returning an exit
  code; `main` calls `os.Exit(run(...))`.
- Package doc explicitly states "parse-tier only — no plugin / validation
  pipeline".
- `bazel test //...` remains green.

---

### Step 2 — Wire `rules_python` and the hermetic Python toolchain

**Functional requirements.**

- `MODULE.bazel` adds `bazel_dep(name = "rules_python", version = "…")` at a
  bzlmod-compatible stable version (verify against `MODULE.bazel.lock`).
- Register a hermetic Python 3.12 interpreter via `use_extension("@rules_python//python/extensions:python.bzl", "python")` and `python.toolchain(python_version = "3.12")`.
- Use `pip.parse` to lock `pyyaml` (beancompat's only runtime dep) and `pytest`
  (test driver). Pin via a checked-in `requirements.txt` + `requirements_lock.txt`.
- `MODULE.bazel.lock` regenerated and committed.

**Modules / files / targets touched.**

- `MODULE.bazel`, `MODULE.bazel.lock`, new `python/requirements.in` (or
  `requirements.in` at repo root), new `python/requirements_lock.txt`.
- (Location detail: place lockfile under a top-level non-Python-code path —
  acceptable to put requirements files at the repo root rather than under
  `pkg/compat/beancompat/`, since they are toolchain-level, not adapter-level.)

**Verification.**

- `bazel build @<pip-hub>//pyyaml @<pip-hub>//pytest` succeeds (resolves
  hermetic wheels).
- `bazel test //...` still passes (no existing target is impacted, since
  `rules_python` simply adds a new toolchain).

**Quality requirements.**

- Pin both `rules_python` version and the Python interpreter version (3.12.x).
- Lockfile checked in.
- No system-Python fallback. If the interpreter cannot be downloaded, the
  build must fail loudly.

---

### Step 3 — Expose beancompat's Python sources as Bazel filegroups

**Functional requirements.**

- Extend `third_party/beancompat/beancompat.BUILD` with:
  - `filegroup(name = "implementations_py", srcs = glob(["implementations/**/*.py"]), visibility = ["//visibility:public"])`
  - `filegroup(name = "strategies_py", srcs = glob(["strategies/**/*.py"]), visibility = ["//visibility:public"])`
  - `filegroup(name = "tests_py", srcs = glob(["tests/**/*.py"]), visibility = ["//visibility:public"])`
  - Optionally `exports_files(["requirements.txt"])` for documentation /
    diagnostic use (we do not consume it directly; deps are re-pinned in
    Step 2).
- Existing `parse_fixtures` and `check_fixtures` filegroups untouched.
- No `py_library` declarations; the external BUILD stays `rules_python`-free.

**Modules / files / targets touched.**

- `third_party/beancompat/beancompat.BUILD`.

**Verification.**

- `bazel build @beancompat//:implementations_py @beancompat//:tests_py @beancompat//:strategies_py` succeeds.
- `bazel test //...` still green (existing Go fixture tests still locate
  fixture files via the unchanged filegroups).

**Quality requirements.**

- No load statements for Python rules in the external BUILD.
- Glob excludes `__pycache__` and `*.pyc` (rules_python ignores them anyway,
  but explicit exclude reduces noise on local re-fetches).

---

### Step 4 — Add the Python adapter at `pkg/compat/beancompat/adapter/`

**Functional requirements.**

- New package `pkg/compat/beancompat/adapter/` containing:
  - `__init__.py` with `GoBeancountAdapter` implementing beancompat's
    `Implementation` protocol.
  - Methods:
    - `name()` → `"gobeancount"`
    - `is_available()` → True if `beanparse` binary is locatable via runfiles
      (or `BEANPARSE_BIN` env override for debugging).
    - `capabilities()` → `frozenset({CAP_PARSE})` (explicitly only CAP_PARSE).
    - `parse_string(source: str) -> ParseResult` → writes `source` to a
      `tempfile.NamedTemporaryFile`, invokes `beanparse <tmpfile>` via
      `subprocess.run` with a timeout, parses stdout JSON, constructs a
      `ParseResult` per beancompat's adapter contract.
  - `BUILD.bazel` exposing `py_library(name = "adapter", srcs = ["__init__.py"], deps = [<rules_python runfiles>], data = ["//pkg/compat/beancompat/adapter/beanparse:beanparse"], visibility = ["//visibility:public"])`.
- Runfiles resolution: use `python.runfiles` from `rules_python` (label-based
  lookup of the `beanparse` data dep). `BEANPARSE_BIN` env-var override is
  permitted only for out-of-Bazel debugging.

**Modules / files / targets touched.**

- New: `pkg/compat/beancompat/adapter/__init__.py`,
  `pkg/compat/beancompat/adapter/BUILD.bazel`.
- Optional: `pkg/compat/beancompat/adapter/adapter_test.py` — small `py_test`
  smoke-checks `parse_string("2024-01-01 open Assets:Cash USD\n")` and asserts
  the JSON parses with a `directives` key. Validates the subprocess wire
  before touching the full pytest suite.
- Gazelle directive in parent `pkg/compat/beancompat/BUILD.bazel` (or local
  `BUILD.bazel`) to keep gazelle from scribbling on a non-Go directory:
  `# gazelle:exclude adapter` or `# gazelle:ignore` in the subdir.

**Verification.**

- `bazel build //pkg/compat/beancompat/adapter:adapter` succeeds.
- If `adapter_test.py` is included: `bazel test //pkg/compat/beancompat/adapter:adapter_test` passes end-to-end (invokes the real `beanparse` binary).
- `bazel test //...` remains green.

**Quality requirements.**

- The adapter file is the only place the subprocess wire shape maps to Python
  objects. Beancompat's `ParseResult` schema changes upstream → this is the
  one file to update.
- No upstream patches.
- Tempfile cleanup via context manager; no leftover files in the sandbox.

---

### Step 5 — Overlay conftest + skip-list at `pkg/compat/beancompat/tests/`

**Functional requirements.**

- New directory `pkg/compat/beancompat/tests/` containing:
  - `conftest.py`: at module load time, mutates
    `tests.conftest.ADAPTERS["gobeancount"] = GoBeancountAdapter` (importing
    the upstream conftest by module path). Also configures `sys.path` so
    that `import implementations.adapter`, `import strategies.*`, and
    `import tests.conftest` all resolve against the runfiles-resolved beancompat
    source tree.
  - `allowlist.py`: a module exposing
    `ALLOWED_FIXTURES: set[str] = {"open_single", "price", "transaction_balanced"}`
    (mirroring Go-side `allowlist.go`). The conftest's
    `pytest_collection_modifyitems` adds `pytest.mark.skip` to every collected
    item whose fixture identifier (basename of the JSON file) is not in
    `ALLOWED_FIXTURES`. Deny-by-default policy.
  - `BUILD.bazel`: declares the `py_test` (deferred to Step 6) plus a small
    `py_test` smoke-test (`conftest_smoke_test`) that imports the conftest
    module and asserts `"gobeancount" in tests.conftest.ADAPTERS` post-load.
- Handling of upstream `beancount` adapter: at conftest load time, attempt
  `tests.conftest.ADAPTERS["beancount"].is_available()` defensively. If
  `is_available()` returns False AND collection-time inspection of pytest's
  parametrization machinery suggests trouble, pop the entry; otherwise leave
  it (Step 6 verification decides).
- Gazelle directive to prevent gazelle from generating in this dir.

**Modules / files / targets touched.**

- New: `pkg/compat/beancompat/tests/conftest.py`,
  `pkg/compat/beancompat/tests/allowlist.py`,
  `pkg/compat/beancompat/tests/BUILD.bazel`.

**Verification.**

- `bazel test //pkg/compat/beancompat/tests:conftest_smoke_test` passes.
- `bazel test //...` still green.

**Quality requirements.**

- No edits to any file under `@beancompat//`.
- Allowlist policy is "deny by default" — adding a fixture is an explicit
  edit, matching Go-side semantics.
- Lazy import of the `gobeancount` adapter so a collection failure does not
  silently mask the underlying `ImportError`.

---

### Step 6 — Add the `py_test` target and validate end-to-end

**Functional requirements.**

- In `pkg/compat/beancompat/tests/BUILD.bazel`, declare:
  - `py_test(name = "test_fixtures", srcs = ["pytest_main.py"], main = "pytest_main.py", data = ["@beancompat//:tests_py", "@beancompat//:implementations_py", "@beancompat//:strategies_py", "@beancompat//:parse_fixtures", "@beancompat//:check_fixtures", "//pkg/compat/beancompat/adapter/beanparse:beanparse"], deps = ["//pkg/compat/beancompat/adapter", "@<pip-hub>//pyyaml", "@<pip-hub>//pytest"], env = {"BEANPARSE_BIN": "$(rootpath //pkg/compat/beancompat/adapter/beanparse:beanparse)"}, imports = ["…"])`.
  - `pytest_main.py` is a thin wrapper that calls
    `pytest.main(["-x", "<runfiles-path-to>/tests/test_fixtures.py"])` and
    propagates the exit code.
- The test runs pytest only on `tests/test_fixtures.py`, only for the
  `gobeancount` adapter parametrization, only on the three allowlisted
  fixtures (`open_single`, `price`, `transaction_balanced`).
- Test result: green.
- All previously-passing targets still pass: total count grows by the new
  py_test(s) and remains green.

**Modules / files / targets touched.**

- `pkg/compat/beancompat/tests/BUILD.bazel`,
  `pkg/compat/beancompat/tests/pytest_main.py`.
- Possibly small surgical fixes to `pkg/compat/beancompat/serialize.go` if a
  fixture we expect to pass actually fails for a cleanly-fixable reason;
  otherwise pull the fixture from the allowlist and document.

**Verification.**

- `bazel test //pkg/compat/beancompat/tests:test_fixtures` is green, with
  pytest output showing three parametrized cases passing for `gobeancount`,
  remaining cases skipped (capability gating or allowlist policy).
- `bazel test //...` reports all targets green; total count = previous
  count + (1 or more newly-added test targets).
- `bazel query 'kind(test, //...)'` shows the new `py_test`.

**Quality requirements.**

- Hermetic: no network, no system Python, no system beancount.
- Wall-clock under 30 seconds (`size = "medium"`).
- pytest output for a failing fixture must surface the upstream assertion's
  message (not just "subprocess returned nonzero") — the adapter must produce
  a `ParseResult` even when go-beancount reports errors, so the upstream test
  comparison logic runs.
- If `beancount` reference adapter's `is_available()` returning False breaks
  collection rather than gracefully skipping, Step 5's defensive
  `ADAPTERS.pop("beancount", None)` activates. Document the observed behavior
  inline.

---

## Alternatives discussed (high level — see planner output for full rationale)

| Decision | Adopted | Rejected alternatives |
|---|---|---|
| Subprocess vs cgo | subprocess | cgo + libpython linkage (toolchain weight, ABI risk) |
| Adapter registration mechanism | top-level mutation in overlay conftest | autouse session fixture (too late — parametrize is computed at collection), pytest entry-point plugin (overhead), in-place upstream patch (excluded by user) |
| Adapter location | `pkg/compat/beancompat/adapter/` (per user) | `python/beancompat_adapter/` (planner default), inline inside test target, upstreaming to beancompat |
| Helper binary | new `pkg/compat/beancompat/adapter/beanparse` | extending `cmd/beancheck` with `--json` (pipeline contamination risk), `go run` invocation (non-hermetic) |
| `bean-check` absent | trust `is_available()` to skip; fall back to `ADAPTERS.pop("beancount")` if collection breaks | install reference beancount in the sandbox (inflates deps) |
| Skip mechanism | `pytest_collection_modifyitems` + sibling `allowlist.py` | monkey-patch fixture JSON `known_divergences`, JSON allowlist file shared cross-language |
| External BUILD shape | filegroup-only | `py_library` (forces `rules_python` load in external BUILD) |

## Recommended approach + rationale

Six independently-committable steps, dependency order Go binary → toolchain
→ external sources exposed → adapter → conftest → py_test. The high-leverage
choices, in priority order:

1. **One dedicated `pkg/compat/beancompat/adapter/beanparse` for parse-tier serialization.** Avoids the
   plumbing risk of toggling pipeline stages inside `cmd/beancheck`.
2. **Top-level mutation of `tests.conftest.ADAPTERS` in our overlay conftest.**
   The autouse-fixture alternative looks idiomatic but is silently broken
   (pytest evaluates the parametrize argument before the fixture runs).
3. **Filegroup-only exposure of beancompat Python sources** keeps
   `third_party/beancompat/beancompat.BUILD` decoupled from `rules_python`.
4. **`pytest_collection_modifyitems`-based local skip list** in
   `pkg/compat/beancompat/tests/allowlist.py`. Three fixtures don't justify
   cross-language allowlist plumbing.
5. **Reuse `pkg/compat/beancompat.SerializeParsed`.** That serializer is
   already test-covered by the Go side. The new py_test is a second harness
   over the same code path; an asymmetry between the two surfaces a bug in
   `pkg/compat/beancompat/adapter/beanparse` or the adapter, not in the serializer.

Initial enabled fixtures: the same three as Go-side
(`open_single`, `price`, `transaction_balanced`).
