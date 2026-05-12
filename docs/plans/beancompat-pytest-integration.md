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

### Detailed Design

#### Contract

The following are locked at design time; Steps 4, 5, and 6 will reference these
values directly.

- **`rules_python` version:** `1.7.0` — promoted from transitive to direct
  `bazel_dep` in `MODULE.bazel`. (Matches the version already pinned by
  `MODULE.bazel.lock`; avoids gratuitous MODULE-graph churn.)
- **Python interpreter version:** `3.12` (registered via the `python` module
  extension's `python.toolchain(python_version = "3.12", is_default = True)`).
  The exact patch level is whatever `rules_python` 1.7.0 ships for 3.12 — not
  pinned at the patch level by the Contract.
- **Pip hub repo label:** `@gobeancount_pip` (NOT `@pip` — explicit name avoids
  collisions with any future `bazel_dep` that creates its own default `pip`
  hub, and namespaces the deps to this project).
- **Wheel set exposed by the hub (Contract-level):**
  - `@gobeancount_pip//pyyaml` (label form; this is the canonical form Steps
    4/5/6 will use in `deps`).
  - `@gobeancount_pip//pytest`.
  - Other transitives (e.g. `iniconfig`, `pluggy`, `packaging`) MAY appear in
    the hub but are not Contract-level — downstream code must not name them.
- **`requirements.in` location:** `third_party/python/requirements.in`. Content
  pins only direct deps with floors:
  - `pyyaml>=6.0,<7`
  - `pytest>=8.0,<9`
- **Lockfile location:** `third_party/python/requirements_lock.txt`
  (single-platform; CI is Linux-only per `.github/workflows/ci.yml`).
- **Files added/modified:**
  - `MODULE.bazel` (add direct `bazel_dep`, `use_extension` for `python` and
    `pip`, `python.toolchain`, `pip.parse`, `use_repo` for the hub).
  - `MODULE.bazel.lock` (regenerated).
  - `third_party/python/requirements.in` (new).
  - `third_party/python/requirements_lock.txt` (new, checked in).
  - `third_party/python/BUILD.bazel` (new, may be empty or `exports_files`).
- **Failure mode:** `bazel build @gobeancount_pip//pyyaml` and
  `@gobeancount_pip//pytest` must succeed offline after a single initial
  fetch. No system-Python fallback: `python.toolchain` must register the
  hermetic interpreter as the only matching toolchain so that Bazel errors
  out loudly rather than silently using `/usr/bin/python3` if the hermetic
  fetch fails.
- **Visibility:** hub-repo targets are public by `rules_python` default; do
  not constrain. Downstream BUILD files in
  `//pkg/compat/beancompat/adapter/...` and
  `//pkg/compat/beancompat/tests/...` will reference them by full label.
- **Non-Contract (explicitly out of scope for this Step):** `hypothesis`,
  `beancount` reference package, any test-only deps beyond `pytest`. Adding
  `hypothesis` is deferred to whichever future step enables a test_*.py file
  that imports it.

#### Suggested Internals

The implementer may adopt, modify, or replace any of the following.

- **Extension call shape.** Suggested:
  ```
  python = use_extension("@rules_python//python/extensions:python.bzl", "python")
  python.toolchain(python_version = "3.12", is_default = True)

  pip = use_extension("@rules_python//python/extensions:pip.bzl", "pip")
  pip.parse(
      hub_name = "gobeancount_pip",
      python_version = "3.12",
      requirements_lock = "//third_party/python:requirements_lock.txt",
  )
  use_repo(pip, "gobeancount_pip")
  ```
  Alternative: the newer `pip.parse(experimental_index_url=...)` flow with
  per-platform locks. Reject for now (single-platform CI).
- **Where to put `requirements.in` / lockfile.** Suggested:
  `third_party/python/` (mirrors `third_party/beancompat/`, signals
  toolchain-level not adapter-level). Alternative: repo root
  (`/requirements.in`, `/requirements_lock.txt`) — rejected as repo-root
  clutter. Alternative: `pkg/compat/beancompat/requirements*.txt` — rejected
  because the deps are toolchain-level (the toolchain is used by the whole
  project, even if today only beancompat consumes it).
- **Lockfile generation.** Suggested: use `rules_python`'s built-in
  `compile_pip_requirements` target — declare in
  `third_party/python/BUILD.bazel`:
  ```
  load("@rules_python//python:pip.bzl", "compile_pip_requirements")
  compile_pip_requirements(
      name = "requirements",
      src = "requirements.in",
      requirements_txt = "requirements_lock.txt",
  )
  ```
  so `bazel run //third_party/python:requirements.update` regenerates the
  lock hermetically. Alternative: run `pip-compile` outside Bazel — rejected
  (non-hermetic, requires devs to have pip-tools installed). Alternative:
  hand-edit the lockfile — rejected (transitive resolution is non-trivial).
- **Single- vs multi-platform lock.** Single-platform (Linux) is sufficient:
  CI is `ubuntu-latest` only, and macOS dev usage is not a stated requirement.
  Implementer may upgrade to multi-platform later by switching to
  `requirements_<os>_<arch>.txt` files if macOS dev support becomes a need.
- **`hypothesis`.** NOT added. Step 6 only enables `test_fixtures.py`, which
  does not import Hypothesis. Adding it speculatively violates YAGNI and
  inflates the wheel set. Defer to the step that enables a Hypothesis-using
  test file.
- **`is_default = True` on the toolchain.** Suggested true (avoids needing
  `--@rules_python//python/config_settings:python_version` flags on every
  test invocation). Alternative: omit and pass the flag — rejected as
  per-invocation friction.

#### Alternatives discussed

- **rules_python version: 1.7.0 vs latest stable vs 0.33.x LTS.**
  - 1.7.0 (recommended): already resolved in `MODULE.bazel.lock`, so no
    extra MODULE-graph churn; current modern API.
  - "latest stable" floating: poor reproducibility; Bazel bzlmod expects
    a pinned `version =` string regardless. Rejected.
  - 0.33.x or earlier: predates the unified `pip` extension; would force a
    legacy `pip.parse_python_versions` shape. Rejected — uses older API.
- **Python interpreter version: 3.12 vs 3.11 vs 3.13.**
  - 3.12 (recommended): matches the plan's explicit decision; broad wheel
    availability for both `pyyaml` and `pytest`; LTS-ish in the rules_python
    toolchain set.
  - 3.11: more conservative but plan already specifies 3.12 and no concrete
    reason to downgrade.
  - 3.13: newer; rules_python 1.7.0 supports it but wheel availability for
    less-common deps (relevant if Hypothesis is added later) is marginally
    worse. Defer.
- **Lockfile management: bazel-driven `compile_pip_requirements` vs external
  `pip-compile` vs manual.**
  - bazel-driven (recommended): hermetic, no host tool requirement,
    rules_python idiomatic.
  - External `pip-compile`: requires every dev to install pip-tools; CI
    diff-check on the lock becomes a "did you install the right pip-tools
    version?" question. Rejected.
  - Manual: transitive resolution is fragile. Rejected.
- **Hub repo naming: `pip` vs `gobeancount_pip` vs `pypi`.**
  - `gobeancount_pip` (recommended): collision-safe, namespaced, signals
    project ownership.
  - `pip`: default; risks collision if a future `bazel_dep` also calls
    `pip.parse` with the default hub_name (rules_python's own pypi__*
    repos already exist in the lock).
  - `pypi`: also fine but less conventional than the `*_pip` suffix used by
    several open-source bzlmod projects.
- **Lockfile scope: single-platform Linux vs multi-platform.**
  - Single-platform (recommended): CI is Linux-only; `pyyaml` and `pytest`
    are pure-Python on the relevant tags so the wheel set is small and
    portable enough for local macOS dev to work opportunistically without
    being a CI commitment.
  - Multi-platform: extra lockfile maintenance with no CI value today.

#### Recommendation

Promote `rules_python` to a direct `bazel_dep` at version **1.7.0** (the
version already pinned transitively in `MODULE.bazel.lock`), register a
hermetic CPython **3.12** toolchain as the default, and use `pip.parse` to
materialize a hub named **`@gobeancount_pip`** from a checked-in
single-platform lockfile at `third_party/python/requirements_lock.txt`
(source `third_party/python/requirements.in` pinning `pyyaml` and `pytest`
only). Wire `compile_pip_requirements` so `bazel run
//third_party/python:requirements.update` regenerates the lock hermetically.

Rationale, by consequence:

1. Pinning rules_python at the lock-resolved 1.7.0 minimizes MODULE-graph
   churn — no other bazel_dep needs re-resolving, and the lockfile delta is
   confined to the new direct-use entries.
2. A project-namespaced hub (`@gobeancount_pip`) is collision-safe against
   future toolchain additions, costs nothing today, and Steps 4/5/6 reference
   the hub by exact label anyway — there is no "cleaner default" gain from
   using bare `@pip`.
3. Locating the lock under `third_party/python/` matches the existing
   `third_party/beancompat/` layout convention and signals correctly that
   these are toolchain inputs, not adapter sources.
4. Deferring `hypothesis` (and any other test-only deps) until a future step
   actually imports them keeps the wheel set tight; the framework's
   extensibility claim is satisfied by the locking machinery, not by
   pre-locking unused deps.
5. `is_default = True` on the toolchain removes per-invocation flag friction
   for downstream `py_test` rules, which is the dominant usage pattern in
   Steps 4–6.

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

### Detailed Design

#### Contract

These items are locked at design time; Step 5's overlay conftest and Step 6's
`py_test` will reference them by exact name.

**Python package layout.**
- Directory: `pkg/compat/beancompat/adapter/` (already established by Step 1
  for the Go subdirectory `beanparse/`; this Step adds Python siblings).
- Module file: `pkg/compat/beancompat/adapter/__init__.py`.
- **Import name downstream code uses:** `from adapter import GoBeancountAdapter`.
  Achieved by `imports = [".."]` on the `py_library` (puts the parent of
  `adapter/` on `sys.path`, so the package importable name is `adapter`).
  This choice is justified in Alternatives below; Step 5 and Step 6 MUST
  use this exact import.

**Class.** `GoBeancountAdapter` (exact name; Step 5's overlay conftest writes
`tests.conftest.ADAPTERS["gobeancount"] = GoBeancountAdapter`).

**Protocol surface implemented** (matching beancompat's `Implementation`
protocol at `implementations/adapter.py` — note that `name` and `capabilities`
are `@property`, not methods; upstream's autouse `_check_capabilities` fixture
reads `impl.capabilities` as an attribute):

- `@property name -> str` → returns the string `"gobeancount"`.
- `@property capabilities -> set[str]` → returns `{CAP_PARSE}`, imported as
  `from implementations.adapter import CAP_PARSE`. Type is plain `set`, not
  `frozenset` (matches upstream rustledger / limabean and the protocol's own
  annotation).
- `is_available(self) -> bool` → returns True iff `_resolve_binary()` produces
  a non-None path AND the resolved binary is executable. MUST NOT raise on
  missing binary; returns False instead.
- `parse_string(self, source: str) -> ParseResult` → writes `source` to a
  tempfile and delegates to `check_file`. Returns a beancompat `ParseResult`
  (imported from `implementations.adapter`).
- `check_file(self, path: Path) -> ParseResult` → invokes the `beanparse`
  binary with `path` as the sole positional argument and reshapes its
  `{directives, errors, options}` JSON into a `ParseResult`. Per-directive
  reshape is `Directive(type=d["type"], date=d["date"], meta=d.get("meta",
  {}), data=d.get("data", {}))`, identical to rustledger/limabean.

**Methods NOT declared on the class.** `execute_query`, `format_source`,
`hash_entries`, `parse_string_with_plugins`, `clamp`, `load_as_fava`, and
`run_importer` are **not declared**. This matches rustledger/limabean.
Rationale: `Implementation` is a structural `Protocol`, so non-declaration is
legal; upstream's `_check_capabilities` autouse fixture gates calls by
`capabilities` membership, so unreachable methods never execute.

**Binary discovery — locked in this order.**
1. If env var `BEANPARSE_BIN` is set and non-empty, treat its value as an
   absolute filesystem path to the binary.
2. Otherwise, construct a `Runfiles` instance via
   `from python.runfiles import Runfiles; r = Runfiles.Create()` and call
   `r.Rlocation("go-beancount/pkg/compat/beancompat/adapter/beanparse/beanparse")`.
   `"go-beancount"` is this project's `module(name = ...)` in `MODULE.bazel`.
3. If both fail, `is_available()` returns False and `parse_string` /
   `check_file` return a `ParseResult` with empty `directives` and a single
   diagnostic error string.

The `Runfiles.Create()` API path is from `rules_python` 1.7.0 (verified in
the materialized `@rules_python+` repo at
`python/runfiles/runfiles.py`; the `__init__.py` re-exports the symbol).

**Subprocess invocation contract.** The adapter MUST produce a well-formed
`ParseResult` for every outcome an upstream test could plausibly exercise —
including beancount-level errors in the source. Specifically:

- Exit 0 with valid JSON → reshape into `ParseResult`.
- Exit nonzero → return `ParseResult(directives=[], errors=[<diagnostic
  including stderr>])`. **MUST NOT raise** out of `parse_string` /
  `check_file`; upstream tests expect a value.
- Invalid JSON on stdout → same: `ParseResult(directives=[], errors=[...])`.
- `subprocess.TimeoutExpired` → same: `ParseResult(directives=[],
  errors=[...])`.

Step 6's quality requirement ("pytest output for a failing fixture must
surface the upstream assertion") depends on the test comparison logic seeing
a `ParseResult`, not a subprocess exception.

**Subprocess env propagation.** When invoking the binary the adapter SHOULD
merge `r.EnvVars()` (where `r` is the `Runfiles` instance) into
`os.environ.copy()` for the subprocess `env=` argument so that a
runfiles-aware subprocess (if `beanparse` ever grew to read runfiles) keeps
working. Today's binary does not read runfiles; this is defensive.

**`BUILD.bazel` target shape (locked names and labels).**

```
load("@rules_python//python:py_library.bzl", "py_library")

py_library(
    name = "adapter",
    srcs = ["__init__.py"],
    imports = [".."],
    data = ["//pkg/compat/beancompat/adapter/beanparse"],
    deps = ["@rules_python//python/runfiles"],
    visibility = ["//visibility:public"],
)
```

- `name = "adapter"` (label `//pkg/compat/beancompat/adapter:adapter`).
- `imports = [".."]` — puts `pkg/compat/beancompat/` on `sys.path`, so
  the import name is `adapter`. Locked; Step 5 depends on this.
- `data` lists the `beanparse` go_binary label so runfiles can locate it.
- `deps` references the rules_python runfiles `py_library`.
- `//visibility:public` — Step 5's conftest and Step 6's py_test depend on
  this target.

**Gazelle directive.** Add `# gazelle:ignore` as the first line of
`pkg/compat/beancompat/adapter/BUILD.bazel`. Matches the convention already
established by the sibling `beanparse/BUILD.bazel`.

**Cross-step references.** Step 5's conftest performs (effectively):

```python
from adapter import GoBeancountAdapter
import tests.conftest
tests.conftest.ADAPTERS["gobeancount"] = GoBeancountAdapter
```

Step 6's `py_test` lists `"//pkg/compat/beancompat/adapter:adapter"` in its
`deps` and (optionally) sets `env = {"BEANPARSE_BIN": "$(rootpath
//pkg/compat/beancompat/adapter/beanparse)"}`.

#### Suggested Internals

- **Tempfile management.** `tempfile.NamedTemporaryFile(mode="w",
  suffix=".beancount", delete=False)` inside a `with` block, then `os.unlink`
  in a `finally`. Reference adapters skip the unlink (OS cleanup handles it).
- **Subprocess timeout.** Suggest **30 seconds**. Reference adapters use 10s
  for `is_available()` probes; same pattern is reasonable.
- **JSON parsing error handling.** `try: data = json.loads(...) except
  json.JSONDecodeError as e: return ParseResult(directives=[],
  errors=[f"beanparse produced invalid JSON: {e}; stdout={result.stdout!r}"])`.
- **`is_available()` implementation.** Just check `_resolve_binary() is not
  None and os.access(path, os.X_OK)`. No defensive subprocess probe — the
  Bazel runfiles path is hermetic.
- **Unimplemented protocol methods.** Omit (rustledger / limabean parity)
  rather than `NotImplementedError` stubs. Switch to stubs only if Step 6
  surfaces an upstream test that calls a method without checking capability.
- **`adapter_test.py` smoke test.** Recommend **including it** as a small
  `py_test` that parses `"2024-01-01 open Assets:Cash USD\n"` and asserts
  the `ParseResult` has at least one directive of type `"open"`. Bisection
  fence for Step 6 debugging.
- **Import block.**
  ```python
  from implementations.adapter import (
      CAP_PARSE,
      Directive,
      ParseResult,
  )
  ```
  Matches rustledger. `Implementation` need not be imported (structural
  Protocol).

#### Alternatives discussed

- **Python import name: `adapter` vs `gobeancount_adapter` vs
  `pkg.compat.beancompat.adapter`.**
  - `adapter` (recommended): shortest, matches upstream pattern (rustledger
    is `implementations.rustledger` exporting `RustledgerAdapter`), one-line
    import in Step 5 conftest. Bazel py_test sandbox isolates sys.path so
    no real collision risk.
  - `gobeancount_adapter`: more self-describing but requires a separate
    Python directory or `imports = ["."]` plus a re-export shim — mismatch
    between Bazel layout and Python package name.
  - `pkg.compat.beancompat.adapter`: mirrors Bazel path but forces
    `imports = ["../../../../"]`. Brittle.
- **Runfiles API: `python.runfiles` (rules_python target) vs
  `bazel-runfiles` pypi vs direct env-var manipulation.**
  - `python.runfiles` from `@rules_python//python/runfiles` (recommended):
    no extra wheel in the lock; covered by rules_python's release versioning.
  - `bazel-runfiles` pypi: extra pip dep for no benefit.
  - Direct env-var manipulation: re-implements manifest parsing; brittle.
- **Unimplemented methods: omit vs `NotImplementedError` vs sentinel returns.**
  - Omit (recommended): rustledger/limabean precedent; `Protocol` is
    structural; capability gating prevents calls.
  - `NotImplementedError`: marginal defensive value.
  - Sentinel returns: advertises capability the class lacks; misleading.
- **Smoke test `adapter_test.py` now vs deferring to Step 6.**
  - Now (recommended): cheap and converts Step 6's multi-layer failure mode
    into a localized bisection.

#### Recommendation

Land the adapter as described under Contract. Use the bare `adapter` import
name with `imports = [".."]` because (1) it minimizes friction for the two
downstream Steps, (2) it matches upstream adapter conventions, and (3) the
rename cost if a collision ever surfaces is one BUILD attribute change.

Surface the unimplemented protocol methods by omission, not by
`NotImplementedError` stubs: parity with rustledger/limabean is worth more
than the marginal defensive value of stubs.

Include the optional `adapter_test.py` smoke test in this Step. It converts
Step 6's potential failure mode "the whole pytest harness is red, where do
I start?" into "adapter_test green → bug in overlay conftest / allowlist /
pytest invocation; adapter_test red → bug in subprocess wire."

Locking `BEANPARSE_BIN` as a first-class override at the Contract level is
deliberate: Step 6's `py_test` can inject the resolved binary path via
`env = {"BEANPARSE_BIN": "$(rootpath ...)"}` and bypass any runfiles lookup
fragility entirely.

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
