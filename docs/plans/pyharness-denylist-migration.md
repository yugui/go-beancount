# pyharness denylist migration and full-pipeline beanparse

## Goal

Bring the Python pytest harness at `pkg/compat/beancompat/pyharness/` up to
parity with the Go-side fixture coverage that landed in PR #65 (added
parse + check fixtures) and PR #66 (two-tier divergence policy plus
denylist migration). The single observable target is that
`bazel test //pkg/compat/beancompat/pyharness:test_fixtures` runs all 13
upstream fixtures (10 parse + 3 check) against the `gobeancount` adapter,
with the same divergence policy the Go side already enforces — two
fixtures (`parse/display_precision_by_currency.json` and
`parse/options_coverage.json`) gated as locally-known divergences, the
rest expected to pass.

To get there, `beanparse` switches from the parse-only path
(`ast.Load` + `SerializeParsed`) to the full pipeline
(`loader.Load(ctx, …)` + `SerializeChecked`). The Python adapter gains a
`CAP_BOOKING` capability declaration so upstream's capability-gated
tests collect against it. The pyharness conftest replaces today's
deny-by-default allowlist with an xfail-marker-driven denylist that
mirrors the Go-side semantics: every collected fixture runs by default,
local divergences are marked `xfail(strict=False)`, and upstream-recorded
`known_divergences["gobeancount"]` continues to be handled inline by
upstream `tests/test_fixtures.py`. Documentation updates close the loop.

## Scope

### Included

- `pkg/compat/beancompat/adapter/beanparse/main.go` and its package
  header — full-pipeline `loader.Load(ctx, …)` + `SerializeChecked`.
- `pkg/compat/beancompat/adapter/beanparse/main_test.go` — reference
  comparison points at the new internal path; the I/O-vs-ledger exit
  contract (`TestRun_MissingFile`, `TestRun_NoArgs`,
  `TestRun_TooManyArgs`, `TestRun_LedgerErrorsInJSON`) remains green.
- `pkg/compat/beancompat/adapter/__init__.py` — adds `_CAP_BOOKING =
  "booking"` and includes both `_CAP_PARSE` and `_CAP_BOOKING` in the
  `capabilities` property. Package docstring updated (parse-tier-only
  language is no longer accurate).
- `pkg/compat/beancompat/pyharness/allowlist.py` → `denylist.py`
  (rename + reshape from `frozenset[str]` to `dict[str, str]`).
- `pkg/compat/beancompat/pyharness/conftest.py` — `pytest_collection_modifyitems`
  flips from `pytest.mark.skip` (deny-by-default) to
  `pytest.mark.xfail(strict=False)` keyed by the local denylist;
  collection-time stale-entry detection added.
- `pkg/compat/beancompat/pyharness/conftest_smoke_test.py` — assertions
  rewritten for `DENIED_FIXTURES: dict[str, str]`.
- `pkg/compat/beancompat/pyharness/BUILD.bazel` — `srcs`, `data`, and
  any in-package label references switched from `allowlist.py` to
  `denylist.py`.
- `docs/architecture/beancompat-pytest-harness.md` — sections "Skip
  Policy (Deny-by-Default Allowlist)", "Subprocess Contract" (parse-tier
  paragraph), and the topology diagram caption updated; "Surgical
  Forward Fix: HTML Escape" section left as-is.

### Excluded

- Other upstream test files (`test_cross_impl.py`, `test_discrepancies.py`,
  `test_round_trip.py`, `test_hash.py`, …).
- Capabilities beyond `CAP_PARSE` + `CAP_BOOKING` (no `CAP_PLUGINS`,
  `CAP_BQL`, `CAP_PRINT`, `CAP_SUMMARIZE`).
- Upstreaming `GoBeancountAdapter` to beancompat.
- Adding `hypothesis` to the pip lock (would be needed for
  `test_discrepancies.py`; deferred).
- Surgical fixes to `pkg/compat/beancompat/serialize.go` are explicitly
  out of scope unless a fixture we expect to pass under the new pipeline
  turns out to require one. In that case the fixture is added to the
  denylist with an "upstream-PR / go-beancount fix pending" annotation
  and the serializer fix is tracked separately.

## Architectural decisions (high level)

- **One binary, full pipeline always — beanparse switches semantics in
  place.** The user explicitly chose this path over a `--check` flag or
  a sibling `beancheck` binary. Rationale, verified during Phase 1: the
  Python adapter's only entry point that pytest reaches is
  `parse_string`. Upstream's `CAP_BOOKING` design intent is that
  `parse_string` returns post-booking output for adapters that declare
  the capability; `tests/test_cost_inventory.py` asserts
  `posting["cost"]["number"]`, a flat-field access that only exists on
  booked `Cost`, not on `CostSpec`. Upstream `tests/` has zero
  `CostSpec` / `cost_spec` references, so the parse-tier value of
  `CostSpec` is not in beancompat's validation surface. The Go side
  already proves the full pipeline against all 11 non-denylisted
  fixtures via `TestParseFixtures` + `TestCheckFixtures`; net coverage
  loss in `pkg/compat/beancompat` is zero because `costSpecPayload` is
  unit-tested directly in `serialize_test.go`. A `--check` flag would
  preserve a parse-only path nothing in the Python harness consumes;
  pure cost without benefit.

- **xfail (not skip) for the local denylist.** Pytest's
  `xfail(strict=False)` is the canonical idiom for "this is known
  broken; surface a clear XFAIL line, and if it ever passes the run
  produces an XPASS we can audit." Pytest-skip silently masks future
  fixes — the divergence stays denylisted long after the regression
  evaporates. With `strict=False`, an XPASS is reported but does not
  fail the suite, which matches the Go-side `t.Skipf` semantics where a
  fixture briefly passing while still denylisted does not break the
  build. Strict=True would fail the suite on XPASS; rejected because
  the policy is "noisy reminder," not "tripwire" — a denylist entry
  passing is a maintenance signal, not a regression.

- **Two-tier divergence policy mirror.** The Go side runs every
  fixture by default and gates skips on (1) the fixture file's own
  `known_divergences["go-beancount"]` first, then (2) the local
  `denylist.go` map. The Python side mirrors this exactly, but only
  needs to implement tier (2): upstream `tests/test_fixtures.py:67-73`
  already inspects `fixture["known_divergences"]` and calls
  `pytest.xfail(...)` from inside the test body when the active adapter
  is named in the map. Our conftest must NOT also xfail upstream-recorded
  divergences — both markers applying is idempotent for the strict=False
  case (the test ends up XFAIL either way), but duplicating the
  registry inflates maintenance with zero behavioral gain.

- **No parametrize-time tier signaling.** The orchestration question
  "how does the Python harness know fixture X is parse-tier and fixture
  Y is check-tier?" does not need an answer at the harness layer.
  Upstream's `tests/test_fixtures.py` already discovers fixtures from
  `fixtures/parse/` and `fixtures/check/` via filesystem glob and
  parametrizes with the tier-prefixed path; the adapter contract is
  capability-keyed, not tier-keyed (`CAP_BOOKING` advertises that the
  adapter handles check-tier semantics, and upstream's capability-gating
  fixture skips items the adapter cannot serve). The harness is
  transparent to tier — exactly as it should be.

- **Local denylist as Python file, not inline dict.** The Go-side
  `denylist.go` lives in its own file for grep-affinity and for clean
  diff'ing when divergences are added/removed. Mirror that on the
  Python side: `denylist.py` exporting a single `DENIED_FIXTURES:
  dict[str, str]` constant. Inlining into `conftest.py` would save one
  file but blur the boundary between "harness mechanics" and "the
  curated list of known divergences."

- **Drop the `allowlist.py` concept entirely.** The Go side has no
  ad-hoc fixture pinning mechanism (no allowlist, only the denylist).
  Mirror that. Keeping `allowlist.py` as a debugging-time mask would
  diverge the two harness policies and invite drift.

## Steps

Four independently committable steps. Step 2 (Python adapter
capability) is kept separate from Step 3 (pyharness rewrite) because:
(a) it is a four-line change with its own conceptual unit ("the
adapter now exposes booking capability"); (b) it can land first and
makes the subsequent pyharness change reviewable in isolation
("upstream's capability gating is satisfied → focus on the denylist
shape"); (c) bisection value if Step 3 later regresses. Folding them
would save a commit at the cost of making the Step 3 diff a mix of
adapter and harness concerns.

---

### Step 1 — beanparse full-pipeline migration

**Functional requirements.**

- `beanparse <file.beancount>` reads the file, calls
  `loader.Load(ctx, string(bytes))` (NOT `ast.Load`), then
  `pkg/compat/beancompat.SerializeChecked(ledger)`, marshals to JSON,
  writes stdout. Exit-code contract unchanged.
- Package header (`main.go` doc comment) loses the "Parse-tier only —
  no plugin / validation pipeline" claim; replaced with a description
  of the full-pipeline contract.
- All existing `beanparse_test` cases still pass. Specifically:
  - `TestRun_NoArgs`, `TestRun_TooManyArgs`, `TestRun_MissingFile` keep
    exit code 2 (I/O / usage class unchanged).
  - `TestRun_SuccessExitsZero`, `TestRun_NoHTMLEscape`,
    `TestRun_DirectivesPresent`, `TestRun_LedgerErrorsInJSON` continue
    to pass — they are written against trivially-bookable sources
    (two `open` directives or a balanced single-currency transaction),
    so the full-pipeline output equals the parse-tier output for them.
  - `TestRun_JSONMatchesSerializeParsed` is renamed
    `TestRun_JSONMatchesSerializeChecked` and its reference path
    switches to `loader.Load(t.Context(), string(src))` +
    `SerializeChecked`.

**Modules / files / targets touched.**

- `pkg/compat/beancompat/adapter/beanparse/main.go`
- `pkg/compat/beancompat/adapter/beanparse/main_test.go`
- `pkg/compat/beancompat/adapter/beanparse/BUILD.bazel` — must add
  `//pkg/loader` to the `go_library` `deps` (and the `go_test` `deps`
  for the reference comparison). Run `bazel run //:gazelle`.

**Verification.**

- `bazel test //pkg/compat/beancompat/adapter/beanparse:beanparse_test`
  green.
- `bazel test //...` green (the pyharness target may still pass under
  the old allowlist of three fixtures because all three are balanced
  trivially-bookable transactions; Step 3 is what actually exercises
  the new pipeline against the full fixture set).

**Quality requirements.**

- The `run()` signature stays the same (`run(args []string, stdout,
  stderr io.Writer) int`); injecting a `context.Context` is unnecessary
  since the binary owns its lifetime — use `context.Background()` (or
  inline it in the `loader.Load` call). No CLI flag for context
  cancellation.
- `loader.Load` failures continue to map to exit code 2 with stderr
  diagnostic, exactly like `ast.Load` failures today (`if err != nil`
  branch already exists; no behavioral change there).
- Ledger-level diagnostics that surface as `Result.Errors` continue to
  yield exit 0 — verify by reading `SerializeChecked`'s contract for
  the `errors` slice initialization (already non-nil per the
  serializer; covered by `TestRun_LedgerErrorsInJSON`).

---

### Step 2 — Python adapter advertises `CAP_BOOKING`

**Functional requirements.**

- `pkg/compat/beancompat/adapter/__init__.py` declares
  `_CAP_BOOKING = "booking"` alongside `_CAP_PARSE`. The `capabilities`
  property returns `{_CAP_PARSE, _CAP_BOOKING}`.
- The module docstring's "parse tier only" sentence is removed; replaced
  with language describing the adapter as covering parse + booking
  (i.e. `parse_string` returns post-booking output).
- `parse_string` / `check_file` need no method-body change — they
  already delegate to `beanparse`, which after Step 1 produces
  post-booking output for every call.

**Modules / files / targets touched.**

- `pkg/compat/beancompat/adapter/__init__.py`
- (No BUILD changes; no new deps; the existing `adapter_test.py` does
  not assert against `capabilities` content beyond shape and stays
  green.)

**Verification.**

- `bazel test //pkg/compat/beancompat/adapter:adapter_test` green.
- `bazel test //...` green. After this step, upstream's
  `_check_capabilities` autouse fixture will start parametrizing
  booking-capability tests against `gobeancount`, but the only test
  target wired up today (`test_fixtures`) is still gated by the
  three-fixture allowlist from `allowlist.py`, so net behavior is
  unchanged until Step 3 lands.

**Quality requirements.**

- `_CAP_BOOKING` value is the literal string `"booking"`, matching
  upstream `implementations/adapter.py`. Verify the constant value once
  during implementation; do not import it (the Python adapter already
  duplicates `_CAP_PARSE` as a literal precisely to avoid the heavier
  beancompat import on the cold path).
- Optional but recommended: extend `adapter_test.py` with a single
  `test_capabilities_includes_booking` assertion. Cheap regression
  fence against accidental removal during a future refactor.

---

### Step 3 — pyharness denylist migration

**Functional requirements.**

- `pkg/compat/beancompat/pyharness/denylist.py` (new file, replaces
  `allowlist.py`) exports
  `DENIED_FIXTURES: dict[str, str]` keyed by tier-prefixed JSON path,
  value = reason string. Initial content:
  ```python
  DENIED_FIXTURES: dict[str, str] = {
      "parse/display_precision_by_currency.json":
          "go-beancount fix pending: parse-tier serializer does not yet "
          "emit the options envelope (display_precision_by_currency expected).",
      "parse/options_coverage.json":
          "go-beancount fix pending: parse-tier serializer does not yet "
          "emit the options envelope (~30 BeancountOptions keys expected).",
  }
  ```
- `pyharness/conftest.py` is rewritten:
  - Import the constant: `from denylist import DENIED_FIXTURES`.
  - `pytest_collection_modifyitems` no longer applies `pytest.mark.skip`.
    Instead: for each collected item whose `_fixture_id_of(item)` is in
    `DENIED_FIXTURES`, apply
    `item.add_marker(pytest.mark.xfail(reason=..., strict=False))`.
  - Stale-entry detection added at the bottom of the hook: every key in
    `DENIED_FIXTURES` must appear in the collected ids; otherwise raise
    a `pytest.UsageError` (so collection fails loudly, mirroring
    `runFixtures`' `t.Errorf` for stale denylist entries).
  - The `_fixture_id_of` helper, the `_resolve_beancompat_root` helper,
    the sys.path setup, and the `ADAPTERS.clear() + register` block all
    stay as-is.
- `pyharness/conftest_smoke_test.py` updated:
  - `test_allowlist_shape` renamed `test_denylist_shape`. Asserts
    `DENIED_FIXTURES` is `dict`, every key matches
    `^(parse|check)/.+\.json$`, every value is a non-empty `str`.
  - Adds `test_denylist_initial_entries` asserting the two specific
    parse-tier keys above are present (sanity check on the migration —
    keeps a typo in the rename from accidentally passing the suite).
  - `test_adapter_registered` and `test_other_adapters_removed` are
    unchanged.
- `pyharness/BUILD.bazel`:
  - `py_library(name = "conftest", srcs = [...])` switches
    `"allowlist.py"` → `"denylist.py"`.
  - No other targets in this BUILD reference the file by name.

**Modules / files / targets touched.**

- New: `pkg/compat/beancompat/pyharness/denylist.py`.
- Deleted: `pkg/compat/beancompat/pyharness/allowlist.py` (`git mv`
  preferred so history shows the rename).
- Modified: `pkg/compat/beancompat/pyharness/conftest.py`,
  `pkg/compat/beancompat/pyharness/conftest_smoke_test.py`,
  `pkg/compat/beancompat/pyharness/BUILD.bazel`.

**Verification.**

- `bazel test //pkg/compat/beancompat/pyharness:conftest_smoke_test`
  green.
- `bazel test //pkg/compat/beancompat/pyharness:test_fixtures` green
  with the expected breakdown:
  - 11 fixtures pass for `gobeancount` (8 parse + 3 check).
  - 2 fixtures (`parse/display_precision_by_currency.json`,
    `parse/options_coverage.json`) XFAIL with the locally-recorded
    reason.
  - Any upstream-recorded `known_divergences["gobeancount"]` entries
    XFAIL via upstream's inline `pytest.xfail()` call. Today there are
    none, but the conftest must not crash if upstream adds one later.
- `bazel test //...` green.

**Quality requirements.**

- `strict=False` is the documented default for our xfail marker — change
  surfaces in a comment in `conftest.py` referencing this plan.
- The stale-entry check raises `pytest.UsageError` (not a plain
  `RuntimeError`), so pytest surfaces it as a collection error with the
  appropriate exit code (4 — "pytest was misused").
- Renaming `allowlist.py` → `denylist.py` is a `git mv` so the
  history-blame chain on the original Bazel-integration commit survives.

---

### Step 4 — Documentation refresh

**Functional requirements.**

- `docs/architecture/beancompat-pytest-harness.md` updates:
  - Section "Subprocess Contract": the paragraph beginning "Parse-tier
    semantics are load-bearing" is replaced. New text describes the
    full pipeline (`loader.Load` + `SerializeChecked`) and notes that
    parse-tier output is no longer reachable from this binary; the
    `CAP_BOOKING` declaration on the Python adapter is what advertises
    this to upstream.
  - Section "Skip Policy (Deny-by-Default Allowlist)" is renamed
    "Divergence Policy (Two-Tier xfail Denylist)" and rewritten:
    - Default = run every fixture.
    - Upstream-recorded `known_divergences["gobeancount"]` is handled
      inline by upstream `tests/test_fixtures.py` (xfail at the test
      body).
    - Local-only divergences live in `pyharness/denylist.py`, applied
      by `pytest_collection_modifyitems` as
      `pytest.mark.xfail(strict=False, reason=...)`.
    - Stale entries fail collection.
    - Mirrors `pkg/compat/beancompat/denylist.go` exactly.
  - Topology diagram caption: `+ allowlist.py` → `+ denylist.py`; the
    `- skip-list hook` bullet inside the conftest box → `- xfail-mark
    hook`.
  - Extensibility / Future Work sections: the "Adding capabilities
    (CAP_CHECK and above)" bullet is updated to reflect that
    `CAP_BOOKING` is now in place; the "CAP_BOOKING / CAP_CHECK /
    `SerializeChecked`" item under Future Work is struck.
- The "Surgical Forward Fix: HTML Escape" section is left untouched.

**Modules / files / targets touched.**

- `docs/architecture/beancompat-pytest-harness.md`.
- Optionally a brief reference from the doc back to this plan file
  (`docs/plans/pyharness-denylist-migration.md`).

**Verification.**

- Manual review during code review. No test target.
- `bazel test //...` green (sanity).

**Quality requirements.**

- The doc must continue to read as the post-decision steady-state
  reference; chronological narrative stays in the plan files.
- No stale references to `allowlist.py`, `ALLOWED_FIXTURES`, or
  "parse-tier only" anywhere in the doc after the edit.

## Alternatives discussed

| Decision | Adopted | Alternatives weighed | Why rejected |
|---|---|---|---|
| Pipeline depth in beanparse | full pipeline always (loader.Load + SerializeChecked) | (a) `--check` flag toggling parse vs full; (b) sibling `beancheck` binary | (a) preserves a parse-only path no Python caller exercises (the adapter's `parse_string` is the sole entry); (b) double-binary maintenance cost with no consumer. Net coverage in `serialize.go` is unchanged because `serialize_test.go` covers `costSpecPayload` directly. Locked by user; sanity-validated. |
| xfail vs skip for local denylist | `pytest.mark.xfail(strict=False)` | (a) `pytest.mark.skip`; (b) `pytest.mark.xfail(strict=True)` | (a) Silently masks fixes; a regression that lands and a fix that lands look identical in pytest output. (b) Would fail the build on XPASS, turning a maintenance signal into a tripwire. Go-side `t.Skipf` is semantically equivalent to `strict=False` (briefly-passing denylisted tests don't break CI). Locked by user. |
| Local denylist format | `dict[str, str]` in standalone `denylist.py` | (a) `frozenset[str]` (preserves today's allowlist shape); (b) inline dict in `conftest.py`; (c) JSON file shared with Go side | (a) Loses the reason string — the most valuable per-entry datum for triage. (b) Conflates "harness mechanics" with "curated divergence list"; the latter changes frequently, the former rarely. (c) Premature: two languages, two literal formats, two-entry initial list — cross-language schema is solving a problem that doesn't exist. Go side uses Go map, Python side uses Python dict; mirror with intent, not with a shared file format. |
| Keep `allowlist.py` for ad-hoc fixture pinning | Drop. Mirror the Go side, which has no allowlist. | Keep as debug-only mask alongside the denylist | Two parallel mechanisms invite drift between the Go and Python harness policies. The empty pyharness allowlist would have to grow whenever someone wants to debug a single fixture in isolation; `pytest -k <fixture>` covers that need without a checked-in file. |
| Handle `known_divergences["gobeancount"]` in our conftest | No. Upstream `tests/test_fixtures.py:67-73` already does this inline at the test body via `pytest.xfail()`. | (a) Duplicate the registry on our side; (b) replace the inline xfail with a collection-time xfail in our conftest | (a) Two registries to keep in sync, zero behavioral gain (both produce XFAIL). (b) Requires reading upstream fixture JSONs at collection time — extra runfiles plumbing, plus we lose the test-body `pytest.xfail()`'s call-site precision. Out of scope: we don't fix what upstream already does right. |
| Two adapters registered (gobeancount-parse + gobeancount-check) for dual-tier validation | No. Single `gobeancount` adapter with `{CAP_PARSE, CAP_BOOKING}`. | Register two adapters and use `parse_string` on the parse-tier one for CostSpec verification | Upstream design doesn't support this — `parse_string` semantics are determined by which capabilities the adapter advertises. Parse-tier CostSpec is not in upstream's validation surface (zero tests reference it). Go-side `serialize_test.go` already unit-tests `costSpecPayload`. The dual-registration approach is making up coverage that nobody asked for. |
| Step granularity: fold Step 2 into Step 3 | Keep separate (recommended). | Single "pyharness rewrite + CAP_BOOKING" commit | Step 2 is conceptually distinct (adapter capability declaration vs harness policy); separating them gives bisection value if Step 3's denylist conversion ever regresses, and reviewers can read each diff in its proper context. Cost is one extra commit. |

## Recommended approach + rationale

Land the four steps in order. The dependency edges are tight: Step 2's
`CAP_BOOKING` declaration is meaningless without Step 1's full pipeline
(the adapter would advertise booking semantics that beanparse doesn't
yet produce), Step 3's expanded fixture coverage assumes Step 2 has
opened the capability gate, and Step 4 documents the steady state Steps
1–3 reach. Each step is independently bisectable: Step 1's verification
runs against `beanparse_test` without touching the Python side; Step 2
flips a Python constant and runs only `adapter_test`; Step 3 is where
the new behavior actually surfaces under
`//pkg/compat/beancompat/pyharness:test_fixtures` and where the largest
diff lives; Step 4 is doc-only.

The single highest-risk decision in this plan is Step 1's full-pipeline
switch. The risk is "an 8th, 9th, …, 11th fixture we expect to pass
under `SerializeChecked` doesn't, for reasons not caught by the
existing Go-side `TestCheckFixtures`." The mitigation is the
transitive-coverage argument: the Go side's `TestParseFixtures` (using
`SerializeParsed`) AND `TestCheckFixtures` (using `loader.Load` +
`SerializeChecked`) both pass for the 11 non-denylisted fixtures
today. The Python harness reaches the same code path through the
adapter subprocess; a divergence would have to be in the JSON wire
format or in upstream pytest's comparison logic, not in the
serializer. If a fixture nevertheless fails, the resolution is to add
it to `DENIED_FIXTURES` with an "upstream-PR pending" reason — the
denylist mechanism we just built absorbs this gracefully.

The second decision worth pinning is the choice of `strict=False`. It
matches the Go-side `t.Skipf` semantics (denylisted-but-now-passing
fixtures do not fail CI), and the XPASS report still produces a clear
visual signal in test output. If audit pressure ever wants a tripwire,
flipping to `strict=True` is a single per-marker constructor change.

## Risks / unknowns

- **Will any of the 11 "should-pass" fixtures actually fail under the
  full pipeline?** Transitively safe: Go-side `TestCheckFixtures` runs
  the same code path against the same fixture content and passes
  today. Flagged because a divergence at the JSON wire boundary
  (rather than at `Match`-comparison) is a class of bug Go-side
  testing doesn't exercise. Resolution if it happens: add a denylist
  entry with "upstream-PR pending" and file a follow-up; do not block
  this PR on it.

- **`strict=False` vs `strict=True` for the xfail marker.** Locked at
  `strict=False`. Recommend revisiting only if a future denylist entry
  drifts from "known-divergence" to "known-flaky," at which point
  per-entry `strict` overrides may be warranted — pytest supports
  this via `pytest.mark.xfail(strict=False, reason=...)` per call.

- **Interaction between upstream xfail (test body) and our xfail
  (collection marker).** When both apply (theoretically possible
  if a divergence is recorded both in the fixture JSON and in our
  local denylist), pytest's behavior is: the collection-time marker
  is set, then the test body's `pytest.xfail()` runs and raises the
  internal xfail exception. The item ends up XFAIL either way. Both
  reasons get reported (collection marker reason in the verbose
  output, test-body call as the actual outcome). Idempotent in
  practice; flagged for awareness. The plan's "remove local entries
  once accepted upstream" annotation convention is the steady-state
  guard against this drift.

- **Bazel rename `allowlist.py` → `denylist.py`.** Python rules don't
  use Gazelle for BUILD generation, and `pyharness/BUILD.bazel`
  contains the explicit `srcs = ["allowlist.py", "conftest.py"]`
  list. The BUILD edit in Step 3 is the only Bazel-side touch; no
  `bazel run //:gazelle` invocation is needed for the Python tree.
  (Gazelle still has to run after Step 1's Go-side dep addition, but
  that's a separate concern.) Verified: `pyharness/BUILD.bazel` is
  already tagged `# gazelle:ignore`.

- **Upstream's `CAP_BOOKING` autouse capability gating.** The Python
  adapter's new `_CAP_BOOKING` declaration will cause upstream tests
  that gate on `CAP_BOOKING` to start parametrizing against
  `gobeancount`. Today's wired-up test target is only
  `test_fixtures.py`, which is capability-agnostic (it tests
  parse-tier and check-tier fixtures both, gated by the fixture's
  tier rather than by adapter capabilities). So the only observable
  effect of Step 2 before Step 3 lands is that
  `test_cost_inventory.py` and similar files would parametrize
  against us — but those test files are not wired into any Bazel
  `py_test` target yet (scope-excluded), so no behavioral change
  surfaces. Flagged because anyone adding a second `py_test` target
  in the future inherits this behavior.

- **Skipped vs xfail semantics for upstream's `parse-tier-only`
  fixtures under the new full pipeline.** The 3 check-tier fixtures
  (`check/*.json`) were previously not in the allowlist and so were
  skipped. After Step 3, they are no longer gated; they run. If any
  of them fails, the failure surfaces. Go-side `TestCheckFixtures`
  already passes for all 3, so transitively safe. Flagged.

- **Test-body assertions in upstream that did not previously
  parametrize over `gobeancount`.** Anything in
  `tests/test_fixtures.py` that runs unconditionally against every
  adapter in `ADAPTERS` already saw `gobeancount`. The capability
  gate change in Step 2 only opens new test files to us, which
  remain out of scope. Confirms: no new test invocations land
  unintentionally as a side effect of Step 2.

---

**Slug:** `pyharness-denylist-migration`
