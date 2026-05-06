# Plan: Re-apply branch's style/contract fixes onto current `main`

## Goal

Land the eight commits currently on
`claude/resolve-merge-conflicts-rWbXp` (post-merge-base `010f053`) on top
of the latest `origin/main` as a clean, linear history. Every
intermediate commit must build (`bazel build //...`), pass tests
(`bazel test //...`), and remain free of issues a Go style review
(per the `go-code-reviewer` skill) would flag.

## Context

- **Merge base:** `010f053` ("Record Phase 7 (pkg/quote) high-level design").
- **Branch tip:** 8 commits ahead of merge base. Their character is
  uniformly **documentation, godoc tightening, cosmetic / test-message
  polish, deterministic ordering, single goroutine-safety fix, and
  internal helper consolidation**. None of the eight commits introduce
  user-visible feature changes.
- **`origin/main` ahead:** ~65 commits since the merge base. They add
  substantive features — `pkg/quote`, `cmd/beanprice`, `cmd/beanfile`,
  `pkg/distribute/{comment,dedup,merge,route}`, `internal/atomicfile`,
  glob include support, the booking pipeline (`pkg/loader/booking`),
  STRICT total-match handling, balance-assertion subtree aggregation,
  parser error de-duplication, line/column population in `pkg/ast/lower`,
  and the doubled tolerance for balance assertions.

A naive `git merge origin/main` reports five content conflicts:

```
cmd/beanfmt/main.go
pkg/ast/lower.go
pkg/validation/balance/plugin.go
pkg/validation/validations/transaction_balances.go
pkg/validation/validations/transaction_balances_test.go
```

Treating these as line-level merge conflicts is wrong. The branch's
commits express **why** each change was made; in several cases the
underlying file has been completely restructured by `main` (e.g.
`atomicWrite` in `cmd/beanfmt/main.go` was extracted to
`internal/atomicfile`, and the silent error returns the branch's commit
wanted to log are now propagated as proper errors). The correct strategy
is therefore: **abandon the merge / rebase approach, drop the eight
commits, and re-apply the still-applicable subset of each commit's
*intent* on top of `main` as a fresh sequence of commits**.

## Strategy

1. Reset the branch to `origin/main` (after confirming the eight commits
   are preserved as backup tags or in another branch — see "Safety"
   below).
2. Re-create eight commits, one per original intent, in the same order
   as the original branch (so each commit's scope stays narrow and the
   subject lines remain meaningful). Some commits will be smaller than
   their original because parts have been obviated by `main`; one
   commit (the `cmd/beanfmt` portion) has been almost entirely obviated
   and will collapse into the "godoc / printer-test panic / errcheck"
   commit.
3. After each commit, run `bazel build //...` and `bazel test //...`.
   If gazelle is needed for newly-touched Go files, run
   `bazel run //:gazelle` before testing and fold the BUILD changes into
   the same commit.
4. Run the `go-code-reviewer` skill on each commit's diff before moving
   on. Any findings get folded back into that commit (`git commit
   --amend` per project convention in `CLAUDE.md`).
5. Force-push the branch when all eight commits land green.

## Safety

Before resetting the branch, capture the current eight commits so they
remain reachable for diffing during re-application:

```sh
git tag style-fix-original claude/resolve-merge-conflicts-rWbXp
git branch style-fix-original-backup claude/resolve-merge-conflicts-rWbXp
```

The original eight commits and their subjects (chronological, oldest
first) are:

| # | Hash       | Subject                                                           |
|---|------------|-------------------------------------------------------------------|
| 1 | `e0c7715`  | Document panic preconditions and tighten option-package tests     |
| 2 | `175bef2`  | Strengthen public API godoc and command-side error handling       |
| 3 | `aa013aa`  | Polish pkg/syntax surface and tighten scanner test diagnostics    |
| 4 | `a83a77d`  | Make AST lowering deterministic and reject malformed dates        |
| 5 | `b53fb71`  | Polish pkg/loader public API and tighten test coverage            |
| 6 | `0639667`  | Tighten pkg/inventory contracts that the docs misrepresented      |
| 7 | `e808e8d`  | Tighten pkg/validation contracts and consolidate tolerance helper |
| 8 | `1344b91`  | Guard the postproc registry with a mutex and harden test isolation|

`git show <hash>` against the backup tag is the canonical reference for
each commit's body and original diff while re-applying.

---

## Sibling branch with overlapping commits

A parallel branch exists that pursued the same style/contract polish
work in a slightly different shape:

- **Branch:** `origin/claude/refactor-go-style-Yeqs8`
- **Merge base with `origin/main`:** `7dc7f3a` (one commit earlier than
  the current branch's merge base `010f053`).
- **Commits not on `origin/main`** (newest first):
  - `54290e6` "Align syntax.Error semantics and tighten CST test
    diagnostics"
  - `089009e` "Surface silent errors and tighten test diagnostics in
    CLI/format/printer"
  - `dd98fe4` "Document panic preconditions and tighten option-package
    tests"

Each of these three commits is **strictly subsumed** by one of the
eight commits on the current branch — i.e. the current branch's
counterpart touches the same files with the same intent and either
matches byte-for-byte or adds a small superset on top. Concretely:

| Sibling | Current branch | Relationship |
|---------|----------------|--------------|
| `dd98fe4` | `e0c7715` (Step 1) | Identical except for the unrelated `docs/phase7-quote.md` file. |
| `089009e` | `175bef2` (Step 2) | Identical in `cmd/beanfmt/main.go` and `pkg/format/option.go`. `175bef2` adds three small extras: explicit `_ = report(...)` discard form in `cmd/beancheck/main_test.go`, a `TestWithInsertBlankLinesBetweenDirectives` test, and one extra message reword in `pkg/printer/printer_test.go`. |
| `54290e6` | `aa013aa` (Step 3) | The four files `pkg/syntax/{error.go,file.go,trivia.go,node_test.go}` are byte-for-byte identical between the two commits. `aa013aa` is a strict superset, additionally rewording `pkg/syntax/scanner_test.go` and `pkg/syntax/scanner_integration_test.go`. |

**Implication for the executor.** The plan's eight-step sequence
already covers everything in the sibling branch. When a step's "Action"
section mentions running `git show <hash>` to crib exact wording, the
canonical hash is the **current branch's** version (column 2 above),
not the sibling — the current branch's version is at least as complete
in every case. The sibling commits are referenced here only so the
executor (or a code reviewer reading later) does not duplicate work
when these hashes surface in a related PR comment, ledger, or
adjacent branch.

The sibling branch can be left alone after this work lands; it has no
content that the rebased current branch will lack.

## Per-commit reapplication plan

Each section below describes:

- **Intent recap** — what the commit was for (a self-contained summary
  so the executor does not need to read the original commit body).
- **Status on current `main`** — which sub-changes still apply, which
  are already done, and which need adjustment.
- **Action** — concrete steps to re-create an equivalent commit on
  `main`.
- **Validation** — what to run / inspect before considering the commit
  done.

### Step 1 — `internal/options` and `internal/formatopt`: panic preconditions and structural tests

**Intent recap.**
The `internal/options.Values` accessors `String`, `Bool`, `Decimal`,
`StringList` panic on unregistered keys to surface programmer errors.
That panic precondition was undocumented. Also: `internal/formatopt`
and `internal/options` had hand-rolled field-by-field equality tests
that silently lose coverage when a new struct field is added — switch
both to whole-struct `cmp.Diff`. For options' decimal field, register
an `apd.Decimal` comparer (decimals do not round-trip cleanly through
`reflect.DeepEqual` across allocations). Finally, drop the explicit
`CommaGrouping: false` from `formatopt.Default()` — leaving some
zero-value fields explicit and others implicit is misleading.

**Status on `main`.** Files unchanged since merge base except
`internal/formatopt/options.go`, which gained a new field
`InsertBlankLinesBetweenDirectives bool`. The `formatopt.Default()`
function still has the explicit `CommaGrouping: false`. The four
options accessors still lack panic-precondition godoc.

**Action.**
- In `internal/options/options.go`: add `// It panics if key is not a
  registered option name.` to the godoc of `String`, `Bool`, `Decimal`,
  and `StringList`. (`Decimal` and `StringList` already have a doc
  paragraph; append the panic note as a separate sentence after the
  existing text.)
- In `internal/formatopt/options.go`: drop the `CommaGrouping: false`
  line from `Default()`.
- In `internal/formatopt/options_test.go`: rewrite `TestDefault`,
  `TestResolveNoOptions`, `TestResolveWithOverrides` to construct an
  expected `Options` literal and compare via `cmp.Diff`. The expected
  `Options` literal **must include** all current fields, including
  `InsertBlankLinesBetweenDirectives` (which was added on `main` after
  the original commit was authored). Failure messages should name the
  function under test (e.g. `Default() mismatch (-want +got):`).
- In `internal/options/options_test.go`: rewrite
  `TestFromRaw_EquivalentToParse` to use a single
  `cmp.Diff(fromParse.values, fromRaw.values, decimalCmp)` with a
  registered `cmp.Comparer` over `*apd.Decimal` that compares by
  `String()`. Keep the `fromParse.reg != fromRaw.reg` pointer check
  separate (cmp cannot recurse into function fields in the registry).
- In `internal/formatopt/BUILD.bazel`: add
  `deps = ["@com_github_google_go_cmp//cmp"]` to the `go_test` target.
  Verify with `bazel run //:gazelle` after the Go edits — gazelle should
  regenerate the same dep automatically.

**Validation.**
- `bazel test //internal/options/... //internal/formatopt/...`
- Confirm the new `cmp.Diff` test fails when a deliberate one-field
  drift is introduced (sanity check of structural coverage). Roll back
  the drift.

### Step 2 — Public API godoc, beancheck errcheck, printer test panics

**Intent recap.**
The original commit had four orthogonal pieces:

1. `pkg/format/option.go`: each `Option` constructor lacked godoc; add
   one-line doc per constructor that explains its effect and any
   interaction with neighbouring options (e.g. `WithAmountColumn` only
   matters when `WithAlignAmounts` is on).
2. `cmd/beanfmt/main.go`: in the local `atomicWrite`, two error returns
   (temp-file `os.Remove` and `os.Chmod`) were silently discarded. Log
   them via the standard logger so silent permission drift / leftover
   dotfiles are observable while keeping cleanup best-effort.
3. `cmd/beancheck/main_test.go`: add an explicit `// exit code not under
   test here` comment on a `report(&buf, ...)` call whose return is
   intentionally discarded. The errcheck warning was load-bearing
   intent, not noise.
4. `pkg/printer/printer_test.go`: the `decimal()` and `date()` test
   helpers swallowed parse errors and returned zero values. Hard-coded
   fixture parses cannot legitimately fail at runtime; a failure is a
   programmer error in the test itself. Panic with the offending input
   so the next debugger sees the cause, not the symptom. While there,
   rename failure messages from `t.Errorf("got %q, want %q", got,
   want)` → `t.Errorf("Fprint() = %q, want %q", got, want)` per Go
   test-comment guidance ("name the function under test").

**Status on `main`.**
- Piece 1 (`pkg/format/option.go`): unchanged — still bare signatures,
  except `main` added a fully-documented `WithInsertBlankLinesBetween-
  Directives`. The other six constructors still lack godoc.
- Piece 2 (`cmd/beanfmt/main.go`): **obsoleted**. `main` extracted the
  whole `atomicWrite` body into `internal/atomicfile.Write`, which
  returns errors for the chmod/stat failures rather than logging them.
  Skip this piece entirely — the underlying issue is fixed more
  thoroughly than the original commit attempted.
- Piece 3 (`cmd/beancheck/main_test.go`): `main` has *more* `report(...)`
  callsites whose return is discarded for the same reason (see lines
  ~164, ~232, ~259 of `main_test.go` on `origin/main`). Add the
  explanatory comment to all of them, not just the original one, so
  intent is uniformly explicit.
- Piece 4 (`pkg/printer/printer_test.go`): unchanged — both helpers
  still discard parse errors, and the assertion messages still use the
  pre-canonical `got %q, want %q` form.

**Action.**
- In `pkg/format/option.go`: add a one-line godoc above each of
  `WithCommaGrouping`, `WithAlignAmounts`, `WithAmountColumn`,
  `WithEastAsianAmbiguousWidth`, `WithIndentWidth`, and
  `WithBlankLinesBetweenDirectives`. The `WithAmountColumn` doc must
  cross-reference `WithAlignAmounts` (it is a no-op without it). The
  `WithBlankLinesBetweenDirectives` doc must cross-reference
  `WithInsertBlankLinesBetweenDirectives`. Match the doc style of the
  already-documented `WithInsertBlankLinesBetweenDirectives` on `main`.
- In `cmd/beancheck/main_test.go`: append the comment
  `// exit code not under test here` (or equivalent) on every
  `report(&buf, diags, ...)` callsite whose return value is discarded.
- In `pkg/printer/printer_test.go`:
  - Replace `decimal`'s body with `apd.NewFromString(s)` plus a `panic`
    on err. Add a leading godoc comment explaining why panic is
    appropriate in test fixtures.
  - Replace `date`'s body similarly.
  - Add `"fmt"` to the import block (needed for the panic message).
  - Globally rewrite the assertion failure messages of the form
    `t.Errorf("got %q, want %q", got, want)` →
    `t.Errorf("Fprint() = %q, want %q", got, want)` (and similar for
    table-driven sub-tests where the existing message includes
    contextual identifiers). Reference `git show 175bef2 --
    pkg/printer/printer_test.go` for the exact set of rewrites the
    original commit performed.

**Validation.**
- `bazel test //pkg/format/... //cmd/beancheck/... //pkg/printer/...`
- Verify the `decimal("not-a-number")` panic path manually if practical
  (e.g., by injecting a deliberately-broken fixture and observing the
  panic message).

### Step 3 — `pkg/syntax` package surface polish

**Intent recap.**
1. `pkg/syntax/file.go`: add a package-level godoc explaining the CST
   contract — every input byte is preserved (comments, whitespace, even
   malformed regions), so the same tree can drive both formatting and
   downstream semantic analysis.
2. `pkg/syntax/error.go`: change `func (e *Error) Error() string` to a
   value receiver. The struct is `{int, string}` — small and read by
   field. The pointer receiver forced unnecessary address-of at call
   sites without buying anything.
3. `pkg/syntax/trivia.go`: change `TriviaKind.String`'s out-of-range
   fallback from `"UnknownTrivia"` to `"UNKNOWN"`, matching the sibling
   enums `TokenKind` and `NodeKind`.
4. `pkg/syntax/scanner_test.go` and `pkg/syntax/node_test.go`: rewrite
   the `t.Errorf("expected X, got Y", ...)` assertions to the canonical
   `t.Errorf("scan: Kind = %s, want EOF", tok.Kind)` form (i.e. report
   the actual value first in both the prose and the format args). Same
   treatment for `scanner_integration_test.go`.

**Status on `main`.**
- Pieces 1–3 (`file.go`, `error.go`, `trivia.go`): unchanged since merge
  base — fully applicable as-is.
- Piece 4: `scanner_test.go` on `main` gained three new test cases
  (`TestIllegalMultiByteCharacter` and additional rows in the existing
  `TestAccountTokens` / `TestCurrencyTokens` / `TestIdentTokens` table
  tests for CJK / long-currency coverage). The new rows already use
  modern phrasing in their identifiers but the table-driven assertion
  bodies still flow through the same `t.Errorf("expected ...")` lines
  that the original commit rewrote, so the rewording is still the
  right operation. `node_test.go` and `scanner_integration_test.go` are
  unchanged.

**Action.**
- Apply the three production-code edits from the original commit
  (`file.go` package doc, `error.go` value receiver, `trivia.go`
  fallback string) verbatim — no merge skew.
- Caller-side: callers that took `&someErr` to satisfy the previous
  pointer-receiver interface will still compile after the receiver
  change (a value-receiver `Error()` is implemented for both `Error`
  and `*Error`). Search the tree (`grep -rn "syntax.Error" pkg cmd
  internal`) and remove now-unnecessary address-of at call sites where
  the original commit's body identified them. (Reference the original
  commit's diff to locate them if needed.)
- Apply the `scanner_test.go`, `node_test.go`,
  `scanner_integration_test.go` rewordings from `git show aa013aa`.
  Where `main`'s newly-added test rows use the old phrasing, reword
  those too for consistency.

**Validation.**
- `bazel test //pkg/syntax/...`
- `grep -RnE 'expected .* got' pkg/syntax/` to confirm no residual
  inverted phrasings (allow exceptions only where the test name itself
  is `Test...Expected...`).

### Step 4 — Make AST lowering deterministic and reject malformed dates

**Intent recap.**
1. `Transaction.Tags` ordering was non-deterministic: active (pushed)
   tags were merged via `for tag := range l.activeTags`, whose iteration
   order is randomized. Anything comparing AST output (golden tests,
   formatter round-trips) flakes. Sort the pushed tags before merging,
   and use a `seen` map for O(n) duplicate detection.
2. `parseDate` accepted dates with mixed separators: it ran
   `strings.ReplaceAll(s, "/", "-")` before `time.Parse`. `"2024-01/15"`
   was silently treated as valid. Beancount's grammar requires either
   `YYYY-MM-DD` or `YYYY/MM/DD` consistently; reject mixed forms with a
   descriptive error.
3. `pkg/ast/ast.go`: move the `MetaValueKind` constant doc comments
   from trailing to leading position so they match the rest of the
   codebase.
4. `pkg/ast/ast_test.go`: drop `TestFileEmpty` — it only verified Go's
   struct literal semantics, no AST-package contract.

**Status on `main`.**
- Piece 1 (transaction tag merging): `main` refactored the merge into a
  helper `mergeActiveTags(tags []string, active map[string]struct{})
  []string` (in `pkg/ast/lower.go`). The helper is **still
  non-deterministic** — its body is `for tag := range active { if
  !slices.Contains(tags, tag) { tags = append(tags, tag) } }`. The fix
  must be applied **inside** the helper, not at every call site. The
  helper is already used by transaction, note, and document lowering,
  so fixing it once covers all three.
- Piece 2 (`parseDate`): `main`'s `parseDate` is byte-for-byte identical
  to the merge-base version (mixed-separator bug intact). Apply the
  branch's fix verbatim.
- Pieces 3 and 4 (`ast.go` constant comments, `ast_test.go`
  `TestFileEmpty`): both files are unchanged on `main`. Apply verbatim.

**Action.**
- Replace the body of `mergeActiveTags` so it iterates over `active` in
  sorted order. Concretely: collect keys into `[]string`, `sort.Strings`,
  then walk in order. Drop the `slices.Contains` linear scan and use a
  `seen` map seeded from the input `tags` for O(n) merging. Update the
  helper's godoc to state the deterministic-order guarantee and why
  (golden-test stability).
  - Verify the `slices` import in `lower.go` is still needed elsewhere
    after removing the `slices.Contains` call — if not, drop it.
- Rewrite `parseDate` to switch on which separator is present:
  YYYY-MM-DD when only `-` is present, YYYY/MM/DD when only `/`, and
  `fmt.Errorf("invalid date %q: separators must be all '-' or all '/'", s)`
  otherwise. Update the function's godoc to state the new contract.
- Move each `MetaValueKind` constant's doc comment from trailing to
  leading position in `ast.go`.
- Delete `TestFileEmpty` from `ast_test.go`.

**Validation.**
- `bazel test //pkg/ast/...` — existing tests must still pass; the only
  behaviour change for valid inputs is deterministic tag ordering, which
  no existing test should depend on directionally.
- Add a unit test (or extend an existing one) that covers
  `parseDate("2024-01/15")` returning a non-nil error. (The original
  commit deliberately did not add such a test in `ast_test.go`, but the
  re-application can — the fix is too quiet otherwise.)
- Run the lowering tests twice (`bazel test //pkg/ast/... --runs_per_test=10`)
  and confirm transaction-tag ordering is stable across runs.

### Step 5 — `pkg/loader` public API polish and coverage

**Intent recap.**
1. `pkg/loader/option.go`: `WithBaseDir` and `WithFilename` were
   re-exported as `var WithBaseDir = ast.WithBaseDir`. `var` re-exports
   hide the call signature in godoc and break the wrapper-function
   pattern used by sibling `pkg/format`. Promote them to thin wrapper
   functions.
2. `pkg/loader/loader.go`: document the non-nil-context requirement on
   the package; matches the convention of `net/http.NewRequestWithContext`.
   The earlier wording promised a panic from "the underlying pipeline" —
   that ties the package contract to an implementation detail it cannot
   enforce, so drop that promise. The contract worth stating is "callers
   must pass a non-nil context"; pass `context.Background` /
   `context.TODO` when no cancellation/deadline is needed.
3. `pkg/loader/loader.go`: replace the magic strings
   `"plugin_processing_mode"` / `"raw"` with package-private constants
   so the option-key contract is greppable.
4. `pkg/loader/loader_test.go`: add `TestLoadCancellation` (canceled
   context propagates to `loader.Load` as `context.Canceled`) and
   `TestLoadRawMode` (raw mode skips built-in pipeline plugins, so an
   unbalanced transaction does NOT produce a validations diagnostic).
   Harden the existing `TestLoadFile_Equivalent` with a
   `len(ledger.Files) == 0` bounds guard before indexing
   `ledger.Files[0]`. Switch `0644` → `0o644` per Go style.

**Status on `main`.**
- Piece 1: `main`'s `option.go` is unchanged from the merge base — both
  re-exports are still `var`s.
- Piece 2: `main`'s package doc has been substantially expanded to
  include a "Return-value contract" section that covers context
  cancellation behaviourally. It does **not** state the non-nil-context
  precondition or warn against the implementation-leaking "panic from
  the underlying pipeline" phrasing — those are gone in `main`'s rewrite
  too. So: add a short "# Context" section after the existing sections
  that states the non-nil-context precondition and points readers at
  `context.Background` / `context.TODO`.
- Piece 3: `main` still has the bare strings at line ~124 of `loader.go`.
- Piece 4: `loader_test.go` is unchanged on `main` (the merge base
  version), so the new tests and 0o-prefix change apply cleanly.

**Action.**
- Rewrite `option.go`'s two `var` re-exports as `func WithBaseDir(dir
  string) Option { return ast.WithBaseDir(dir) }` and analogously for
  `WithFilename`. Add godoc that names what the option configures and
  cross-references the underlying `ast.WithBaseDir` / `ast.WithFilename`.
- Add a `# Context` section to the package doc in `loader.go`. Keep it
  short — three sentences max.
- Define unexported constants `pluginProcessingModeOption =
  "plugin_processing_mode"` and `modeRaw = "raw"` near the top of
  `loader.go`, then route the lone callsite through them.
- In `loader_test.go`:
  - Add `import "errors"` if not already present.
  - Replace the file-mode literal `0644` with `0o644`.
  - After `ast.LoadFile(abs)`, add `if len(ledger.Files) == 0 { ... }`
    bounds check.
  - Add `TestLoadCancellation` (canceled context → `errors.Is(err,
    context.Canceled)`).
  - Add `TestLoadRawMode` using a small unbalanced-transaction fixture
    that should produce no error diagnostic in raw mode.

**Validation.**
- `bazel test //pkg/loader/...`
- `go vet ./pkg/loader/...` (or `bazel run //:gazelle` first if needed)
  must show no warnings about the reworded godoc.

### Step 6 — `pkg/inventory` doc–contract alignment

**Intent recap.**
Three docs misrepresented behaviour and would mislead callers:

1. `Inventory.Add`'s doc claimed "merges never move an entry, and
   appends always go to the tail," but a merge that lands at zero
   deletes the slot (so subsequent entries shift up). Document the
   zero-collapse exception.
2. `Reducer.Walk`'s doc claimed "calling Walk twice produces identical
   results" without mentioning that Walk **mutates** the input ledger:
   when a transaction has an auto-posting (`Amount == nil`), Walk fills
   in the resolved residual `Amount` in place. Idempotence in *outcome*,
   not in *input mutation*.
3. `Inventory.Equal`'s nil handling was correct but written as a single
   combined guard `if i == nil || o == nil { return i == nil && o ==
   nil }` that obscured the cases. Restructure into two explicit guards
   and document both nil-vs-nil and nil-vs-non-nil.
4. `pkg/inventory/inventory_test.go`: drop the stray `var _ =
   (*apd.Decimal)(nil)` whose stated purpose was self-fulfilling (the
   `apd` import existed only to satisfy the assertion). Drop both the
   line and the import.

**Status on `main`.**
- Piece 1: `main`'s `Add` doc is unchanged — still the misleading
  one-liner. The body's zero-collapse logic is still there too.
- Piece 2: `main`'s `Reducer.Walk` godoc is unchanged.
- Piece 3: `main`'s `Inventory.Equal` is unchanged — still the combined
  guard.
- Piece 4: the stray `var _ = (*apd.Decimal)(nil)` is still in
  `inventory_test.go`.

All four pieces apply cleanly as-is.

**Action.**
- Apply the four edits verbatim from `git show 0639667`.
- Confirm the `apd` import in `inventory_test.go` becomes unused after
  the var is removed. If the file uses `apd` elsewhere (e.g. via the
  `decimalVal` helper) the import must stay; otherwise drop it.

**Validation.**
- `bazel test //pkg/inventory/...`
- The contract changes are doc-only (and one drop-stray-var) — they do
  not change any test outcome.

### Step 7 — `pkg/validation` contracts and tolerance helper consolidation

**Intent recap.**
1. `"invalid-option"` was emitted as a bare string at three callsites
   (`pkg/validation/balance/plugin.go`,
   `pkg/validation/pad/plugin.go`,
   `pkg/validation/validations/plugin.go`) while
   `pkg/validation/errors.go` already declared
   `CodeInvalidOption Code = "invalid-option"`. The duplication meant a
   constant rename would silently desynchronize the wire codes. Route
   every site through the constant via `string(validation.CodeInvalidOption)`.
2. `CodeInvalidBookingMethod` was marked `Deprecated:` with the claim
   "the validation package no longer emits it." This is a half-truth:
   the AST-walk path no longer produces it, but `FromInventoryError`
   still maps `inventory.CodeInvalidBookingMethod` onto it for legacy
   adapter callers. Rewrite the deprecation note to describe what the
   code actually documents.
3. The `withinTolerance(diff, tol *apd.Decimal) (bool, error)` helper
   was duplicated identically in two places: the balance plugin and the
   transaction-balances validator. Lift it to
   `pkg/validation/internal/tolerance` as `Within` and route both
   callers through it.
4. Document the import-time `postproc.Register` side effect on the
   balance / pad / validations subpackages — importing them mutates a
   global registry, which a caller cannot deduce from the package's
   public surface without reading `init()`.
5. Replace `%v`-formatted residual currency lists (`"non-zero residual
   in [USD]"`) with `strings.Join(residual, ", ")` (`"non-zero residual
   in USD"`) for human-readable diagnostics. The corresponding
   transaction_balances tests and the validations plugin test pin the
   exact wording, so the test files must be updated in lockstep.

**Status on `main`.**
- Piece 1 (bare `"invalid-option"`): all three callsites still use the
  bare string on `main`.
- Piece 2 (deprecation note): `main`'s `errors.go` still has the
  half-truth wording.
- Piece 3 (`withinTolerance` consolidation): both copies still exist on
  `main`. The helpers are byte-for-byte identical to the merge-base
  versions; the consolidation applies cleanly. Note that
  `main`'s `pkg/validation/balance/plugin.go` has been **substantially
  refactored** for booking-pass interaction (the old auto-posting
  inference is gone, replaced by `CodeAutoPostingUnresolved`-emission;
  `checkBalance` now aggregates over `b.Account.Covers(k.Account)` for
  subtree balance assertions; the tolerance helper is now
  `tolerance.ForBalanceAssertion`, not `tolerance.ForAmount`). The
  branch's commit only changes one line in `checkBalance` (the
  `withinTolerance` call) and the bare-string callsite — both remain
  applicable on top of the refactor without conflict.
- Piece 4 (Register-side-effect docs): all three subpackages still lack
  the warning. `pkg/validation/pad/plugin.go` was modified on `main`
  (cost-posting-poison gate added) but its package doc and import
  block are untouched in the relevant area.
- Piece 5 (`%v` → `strings.Join`): `main`'s `transaction_balances.go`
  still has the `%v`-formatted message with two callsites (one for
  multi-currency-auto-posting, one for plain unbalanced-transaction);
  the corresponding tests in `transaction_balances_test.go` and
  `pkg/validation/validations/plugin_test.go` pin the
  bracket-formatted wording. All apply cleanly.

**Action.**
- In `pkg/validation/internal/tolerance/tolerance.go`: add a `Within`
  function that takes `(diff, tol *apd.Decimal) (bool, error)` and
  returns whether `|diff| <= tol`. Doc-comment it with the expectation
  that the error is non-nil only on pathological apd inputs (an
  overflowing exponent), not on tolerance-exceedance.
- In `pkg/validation/balance/plugin.go`:
  - Add the package-doc paragraph noting the import-time `postproc.Register`
    side effect.
  - Replace `Code: "invalid-option"` with
    `Code: string(validation.CodeInvalidOption)` at the lone callsite
    in `parseOptions`.
  - Replace `withinTolerance(diff, tol)` with `tolerance.Within(diff, tol)`
    at its sole callsite in `checkBalance`.
  - Delete the local `withinTolerance` function at the bottom of the
    file.
- In `pkg/validation/pad/plugin.go`:
  - Add the package-doc paragraph (same wording, adjusted import path).
  - Replace `Code: "invalid-option"` at the lone callsite.
  - No `withinTolerance` to remove (pad does not have a local copy).
- In `pkg/validation/validations/plugin.go`:
  - Add the package-doc paragraph.
  - Add `"github.com/yugui/go-beancount/pkg/validation"` to the import
    block.
  - Replace `Code: "invalid-option"` with the constant form.
- In `pkg/validation/validations/transaction_balances.go`:
  - Replace `withinTolerance(sums[cur], tolerances[cur])` with
    `tolerance.Within(...)` (the `tolerance` package is already imported).
  - Delete the local `withinTolerance` helper at the bottom of the
    file.
  - Add `"strings"` to the import block.
  - Replace both `fmt.Sprintf("... %v", residual)` callsites with
    `fmt.Sprintf("... %s", strings.Join(residual, ", "))`.
- In `pkg/validation/validations/transaction_balances_test.go`: update
  the two pinned wordings to the un-bracketed form (e.g.
  `"transaction does not balance: non-zero residual in USD"` and
  `"cannot infer auto-posting amount: residual has 2 non-zero
  currencies (EUR, USD)"`). Drop the "Legacy wording" comment.
- In `pkg/validation/validations/plugin_test.go`: update the pinned
  wording at the line that asserts the unbalanced-transaction message.
- In `pkg/validation/errors.go`: rewrite the `Deprecated:` note on
  `CodeInvalidBookingMethod` to describe both call paths (AST-walk no
  longer emits it; `FromInventoryError` adapter still does).
- Run `bazel run //:gazelle` — no Go-source path changes, but the
  `transaction_balances.go` import additions warrant a regeneration
  pass.

**Validation.**
- `bazel test //pkg/validation/...`
- `grep -RnE '"invalid-option"' pkg/validation/` should return only the
  declaration line in `errors.go`.
- `grep -RnE 'func withinTolerance' pkg/validation/` should return zero
  hits.
- `grep -RnE 'residual in \[' pkg/validation/` should return zero hits.

### Step 8 — Goroutine-safety for `pkg/ext/postproc` registry + test isolation

**Intent recap.**
The postproc registry is a package-level `map[string]api.Plugin`. The
godoc claimed "init time only," which is true when each plugin's `init()`
inserts itself. However, `goplug.Load` invokes `InitPlugin` (which calls
`Register`) at **any** point a host chooses, including from a goroutine
spawned long after `main` started. Add a `sync.RWMutex` guarding
`Register` and the `lookup` read path so they can interleave safely.

The behavioural contract is unchanged: `Register` still panics on
duplicate names — the panic message gains a `"postproc:"` prefix so the
source subsystem is unambiguous when the panic surfaces from a plugin's
init.

`ResetForTest` (the cross-package test handle) and the
package-internal `withCleanRegistry` helper must hold the mutex across
their swap, otherwise the new mutex would have **introduced** a data
race between concurrent `Register` and the swap. Sharpen the
`ResetForTest` godoc: holding the mutex across each swap keeps
`Register`/`lookup` atomic, but does not prevent a concurrent
`Register` from writing into the captured old map and silently
re-appearing when cleanup restores it. Test authors who care about
that hazard need stricter isolation (e.g. `t.Setenv` + subprocess, or
package-level `t.Parallel()` audits).

Rename `pkg/ext/postproc/testhelpers.go` to
`pkg/ext/postproc/export_testonly.go` so the build tag's intent is
visible from a directory listing. Update `pkg/ext/postproc/BUILD.bazel`
accordingly.

**Status on `main`.**
The four files (`registry.go`, `registry_test.go`, `testhelpers.go`,
`BUILD.bazel`) are byte-for-byte unchanged on `main` since the merge
base. The fix applies verbatim.

**Action.**
- Apply the diff from `git show 1344b91 -- pkg/ext/postproc/registry.go
  pkg/ext/postproc/registry_test.go` onto the `main` versions of
  those files. The diff is small and self-contained.
- `git mv pkg/ext/postproc/testhelpers.go
  pkg/ext/postproc/export_testonly.go` (the rename is intentional;
  contents change to add the mutex-aware swap and improved godoc — see
  the original commit's diff under that path).
- Update `pkg/ext/postproc/BUILD.bazel`'s `srcs` (test-only) entry to
  the new filename.
- Run `bazel run //:gazelle` to confirm nothing else regenerates.

**Validation.**
- `bazel test //pkg/ext/postproc/...`
- `bazel test //pkg/ext/postproc/... --runs_per_test=20
  --test_arg=-test.race` (if the project's bazel config supports
  `-race`; otherwise `go test -race ./pkg/ext/postproc/...`) to confirm
  the mutex actually shuts the race window.
- `ls pkg/ext/postproc/` should show `export_testonly.go`, no
  `testhelpers.go`.

---

## Cross-cutting validation (after all eight commits land)

1. `bazel build //...` and `bazel test //...` from a clean checkout.
2. `git rebase -i origin/main` to confirm the eight new commits form a
   linear history with no fixups remaining.
3. For each of the eight commits, run `git checkout <hash> &&
   bazel build //... && bazel test //...` to confirm the **bisectable**
   property — each individual commit on the rebased branch builds and
   passes tests, not just the final tip. (Use a temporary worktree so
   you can return to the branch tip cleanly.)
4. Spawn the `go-code-reviewer` skill against the diff
   `git diff origin/main...HEAD` and address every finding. Per
   `CLAUDE.md`'s "clean commit history" guidance, fold each fix into
   the commit it logically belongs to via `git rebase -i` with
   `fixup`/`squash`, not as a new standalone commit.
5. Force-push:
   `git push --force-with-lease origin claude/resolve-merge-conflicts-rWbXp`.

## Out of scope

- No new feature work. If the `go-code-reviewer` flags a pre-existing
  issue on `main` that is unrelated to one of these eight commits'
  scope, file a follow-up note rather than expanding the commit's diff.
- No commit-message edits to `main`'s 65 commits. The branch's commits
  are the only ones being rewritten.
- The original commit titles are good as-is and should be preserved on
  the re-applied commits, possibly with minor word adjustments where
  the scope shrank (e.g. step 2's title can drop "command-side error
  handling" if the `cmd/beanfmt` portion is fully obviated).
