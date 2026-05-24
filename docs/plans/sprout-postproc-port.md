# Plan: Port beansprout plugins to `pkg/ext/postproc/sprout/`

## Goal

Create a new `pkg/ext/postproc/sprout/` tree containing Go ports of all
10 beansprout Python plugins, plus an umbrella `package sprout` so
`cmd/beancheck` activates them with a single blank import. After this
PR a beancount file that opens with `plugin "beansprout.plugins.<name>"`
is honoured by `beancheck` exactly as the Python beansprout binary
honours it today.

## Scope

**Included:**
- 10 plugin ports (leafonly, check_metadata, inherit_metadata,
  commodity_pattern, comprehensive_balance, infer_metadata,
  price_completion, trading_validation, fiscal_income_expense, print).
- Umbrella `package sprout` blank-importing all 10.
- `api.Input.SourceFilename` field (needed by infer_metadata for YAML
  file resolution).
- New public `pkg/ast` API: `ParseAmountExpression` and
  `ParseBalanceAmount`, exposing existing internal arithmetic /
  tolerance parsing (needed by comprehensive_balance and
  fiscal_income_expense).
- `cmd/beancheck` blank-imports the new umbrella by default.

**Excluded:**
- Changes to beansprout repository (read-only source of truth).
- Pull request creation (deferred until user requests).
- Out-of-process / `.so` plugin loaders.

## Context

`go-beancount` already hosts ports of upstream beancount's standard
Python plugins under `pkg/ext/postproc/std/` (14 subpackages, each
registering under `beancount.plugins.<name>` and its Go import path).
The `beansprout` Python project (at `/home/user/beansprout`) ships 10
additional plugins under `beansprout/plugins/` that have **no upstream
beancount equivalent** — they are extra, general-purpose
post-processors. Right now those plugins live only in the Python world;
the Go pipeline cannot honour `plugin "beansprout.plugins.*"`
directives.

## User decisions captured

- **Umbrella name**: `sprout` (mirrors `std`; one directory, `doc.go`
  is the umbrella).
- **`sprout/leafonly`**: keep beansprout's narrower trigger set
  (Transaction + Pad only) but anchor diagnostics at the **posting**,
  matching `std/leafonly`'s LSP-friendly convention. Document both
  deviations.
- **`infer_metadata` YAML lookup**: extend `api.Input` with a
  `SourceFilename string` field populated by the runner from
  `ledger.Files[0].Filename`, then implement YAML loading correctly
  against the root source directory.
- **`print` plugin**: write directly to `os.Stderr` (matches upstream
  `print(..., file=sys.stderr)`). Document as a deliberate, scoped
  deviation from the "no side effects" API convention.
- **Balance-expression parsing for `comprehensive_balance` /
  `fiscal_income_expense`**: do NOT hand-roll an arithmetic evaluator.
  Add two new public functions to `pkg/ast` that expose existing
  internal machinery (`pkg/syntax`'s `Parse` + `ArithExprNode`
  precedence parser, `pkg/ast/lower.go`'s `evalExpr` + `parseNumber`):
    - `ast.ParseAmountExpression(s string) (Amount, []Diagnostic)`
    - `ast.ParseBalanceAmount(s string) (Amount, *apd.Decimal, []Diagnostic)`

## Recommended approach

Three orchestration waves on branch `claude/wonderful-babbage-IKhWB` of
`yugui/go-beancount`. Beansprout repo is read-only (source of truth);
no changes pushed there.

```
Wave 0 (sequential, 1 agent) → API extension + empty umbrella scaffold + beancheck wire-up
Wave 1 (parallel, 8 agents)  → 10 plugin ports (batched as below)
Wave 2 (sequential, 1 agent) → enable umbrella imports + full-repo test
```

The orchestration agent drives waves; each wave's agents commit their
own slice. No cross-directory write conflicts inside a wave.

### Rationale

Wave 0 atomically lands every cross-cutting change (new API surface +
empty umbrella + beancheck wire-up) in one commit so every Wave 1
agent works against a single settled base — no agent has to worry
about merging API additions or fighting for the umbrella file.

Wave 1 plugin ports are fully independent (different subdirectories,
no shared files written). Parallelism is safe.

Wave 2 just lights up the umbrella; if any cross-package activation
clash surfaces (duplicate registration, accidental name reuse), it is
caught here before user observation.

### Alternatives considered (and rejected)

- **Hand-roll an arithmetic evaluator** inside
  `comprehensivebalance/` and `fiscalincomeexpense/`: rejected. The
  `pkg/syntax` parser already implements the precedence grammar and
  `pkg/ast/lower.go` already evaluates it. Reusing them via a small
  public-API addition is less code and stays consistent with how
  beancount-syntax-level expressions are evaluated everywhere else.
- **One mega-commit for all 10 plugins**: rejected. Per-plugin commits
  make review and bisect tractable.
- **Skip `print` plugin** because of the os.Stderr side effect:
  rejected after user confirmed os.Stderr direct write is acceptable.
- **Defer YAML support in `infer_metadata`**: rejected after user
  asked for the `api.Input` extension. Doing it now keeps the port
  feature-complete.
- **Umbrella package named `postsprout`** (literal of user's verbal
  request): rejected after clarification — `sprout` mirrors `std`
  exactly, which is structurally cleaner.

## Steps

### Step 1 — Wave 0: scaffold + API extensions

**Functional requirements:**
- `api.Input` carries a new `SourceFilename string` field, populated
  by `postproc.Apply` from `ledger.Files[0].Filename`.
- `pkg/ast` exposes `ParseAmountExpression` and `ParseBalanceAmount`,
  returning `[]Diagnostic` (span-aware) on error.
- `pkg/syntax` exposes a thin entry point used by the new ast API
  (e.g. `ParseBalanceAmount(s) (*Node, []Error)`).
- `pkg/ext/postproc/sprout/doc.go` exists as `package sprout` with
  godoc and an empty blank-import slot.
- `cmd/beancheck` blank-imports the empty sprout umbrella so wiring
  is in place for Wave 2 to light up.

**Modules touched:**
- `pkg/ext/postproc/api/plugin.go` + test
- `pkg/ext/postproc/apply.go` + test
- `pkg/ast/balance_expr.go` (new) + test (new)
- `pkg/ast/lower.go` (refactor `evalExpr` / `parseNumber` to shared helpers)
- `pkg/syntax/` (new public entry point + test)
- `pkg/ast/BUILD.bazel`, `pkg/syntax/BUILD.bazel`
- `pkg/ext/postproc/sprout/doc.go` (new) + `BUILD.bazel` (new)
- `cmd/beancheck/main.go` + `BUILD.bazel`

**Verification:**
- `bazel build //pkg/ast/... //pkg/syntax/... //pkg/ext/postproc/... //cmd/beancheck/...`
- `bazel test //pkg/ast/... //pkg/syntax/... //pkg/ext/postproc/... //cmd/beancheck/...`
- `bazel test //...` (no regression — additive API surface).

**Quality requirements:**
- All new public API has godoc.
- No internal abstractions exposed beyond what the two ast-level
  functions need.
- BUILD.bazel hand-edited (not gazelle-regenerated for the umbrella).

**Commit subject:** `add sprout postproc umbrella; expose balance-expr public API`

### Step 2 — Wave 1: port 10 plugins (parallel execution)

**Functional requirements:** each of the 10 beansprout plugins ported
to a sibling subpackage under `pkg/ext/postproc/sprout/<dir>/` with:

- 4-file layout (`doc.go`, `plugin.go`, `plugin_test.go`, `BUILD.bazel`)
- Canonical template: `pkg/ext/postproc/std/leafonly/`
- Dual registration: `beansprout.plugins.<py_name>` AND Go import path
- Diagnostic codes kebab-case
- Span fallback chain: posting → transaction → plugin directive

Per-plugin specs (see table further below).

**Modules touched:** new subdirectories only. No file overlap between
plugin ports — safe to run all eight agent batches in parallel.

**Batching** (8 parallel agents):
- Agent A: `print/` + `commoditypattern/` (small)
- Agent B: `leafonly/` (medium, deviation docs)
- Agent C: `checkmetadata/` (medium, config DSL)
- Agent D: `inheritmetadata/` (medium, directive rebuild)
- Agent E: `tradingvalidation/` (medium, balance rules)
- Agent F: `comprehensivebalance/` + `fiscalincomeexpense/` (medium, both call Wave-0 `ast.ParseBalanceAmount`)
- Agent G: `infermetadata/` (hard, YAML loading via new `SourceFilename`)
- Agent H: `pricecompletion/` (hard, Dijkstra + temporal weighting)

**Verification per plugin:**
- `bazel test //pkg/ext/postproc/sprout/<dir>/...`
- Table-driven tests covering each upstream `_test.py` case.

**Quality requirements:**
- Comments restricted to non-obvious WHY (per project CLAUDE.md).
- No backwards-compat shims.
- Apply `ctx.Err()` early.
- Never mutate input directives.

**Commit cadence:** up to 10 commits (one per plugin); agents porting
two plugins together may combine or split.

#### Per-plugin spec

| # | beansprout source | Target dir | Python registration | Notable concerns |
|---|---|---|---|---|
| 1 | `leafonly.py` / `leafonly_test.py` | `sprout/leafonly/` | `beansprout.plugins.leafonly` | Coexists with `std/leafonly` (different registry names — no conflict). Apply trigger filter = `Transaction` postings + `Pad` directives only (omit Open/Close/Note/Document/Balance — matches Python). Diagnostic anchored at posting / pad span with the std fallback chain. Document both deviations from beansprout-Python in `doc.go`. |
| 2 | `check_metadata.py` / `_test.py` | `sprout/checkmetadata/` | `beansprout.plugins.check_metadata` | Multi-line config DSL: first line `"<directive> [account_prefix]"`, subsequent lines = metadata-key names. Per-directive-type dispatch. Leaf detection re-derived locally (same approach as std/leafonly's `referenced` set). |
| 3 | `inherit_metadata.py` / `_test.py` | `sprout/inheritmetadata/` | `beansprout.plugins.inherit_metadata` | Mutates Open directives by walking parent hierarchy. Must return a rebuilt `Result.Directives` slice — never mutate input structs. Use `ast.Account.Parent()` for traversal. |
| 4 | `commodity_pattern.py` / `_test.py` | `sprout/commoditypattern/` | `beansprout.plugins.commodity_pattern` | Reads `commodity-pattern:` metadata from Open directives, compiles with stdlib `regexp`, full-match against each transaction posting's currency. Emit `commodity-pattern-mismatch` and `commodity-pattern-invalid-regexp` diagnostics. |
| 5 | `comprehensive_balance.py` / `_test.py` | `sprout/comprehensivebalance/` | `beansprout.plugins.comprehensive_balance` | Custom directive (`"comprehensive_balance"`). Split the multi-line string body on `\n`, parse each non-empty line with the **new `ast.ParseBalanceAmount` public API** (added in Wave 0) — no hand-rolled evaluator. Re-anchor returned diagnostics onto the enclosing Custom directive's span. Generates `*ast.Balance` directives, removes the matching `*ast.Custom` from the result slice. |
| 6 | `infer_metadata.py` / `_test.py` | `sprout/infermetadata/` | `beansprout.plugins.infer_metadata` | Config DSL: one rule per line `<directive> <target_meta> <source>` plus optional `file:<path>.yaml` mapping. Resolve YAML path relative to `filepath.Dir(in.SourceFilename)`. Use `gopkg.in/yaml.v3` (check `go.mod` for existing dependency; if absent, add). Special source tokens `__commodity__`, `__account__` produce the directive's own currency / leaf account. |
| 7 | `price_completion.py` / `_test.py` | `sprout/pricecompletion/` | `beansprout.plugins.price_completion` | Config `temporal_base=<float>,temporal_scale=<float>` (optional, defaults `1.0, 0.1`). Build temporal commodity graph from existing `*ast.Price` directives; per date, run Dijkstra (`container/heap`) from each operating currency; emit derived `*ast.Price` directives only when the path includes a current-date edge. Read operating currencies from `in.Options`. |
| 8 | `trading_validation.py` / `_test.py` | `sprout/tradingvalidation/` | `beansprout.plugins.trading_validation` | Config = single account prefix string, default `"Equity:Trading"`. For each transaction touching the prefix, validate three balance rules using `apd.Decimal` with tolerance from `in.Options`. Read `trading-account: "disabled"` metadata on `*ast.Commodity`. |
| 9 | `fiscal_income_expense.py` / `_test.py` | `sprout/fiscalincomeexpense/` | `beansprout.plugins.fiscal_income_expense` | Custom directive (`"fiscal_income_expense"`). Use `ast.ParseBalanceAmount` (string-amount form) or read pre-parsed `MetaAmount` values directly (typed-amount form). Tolerance inference from `apd.Decimal` exponent when caller did not supply `~`. Removes the matching Custom; emits diagnostic on mismatch. Paired with #5 (same Wave-0 API). |
| 10 | `print.py` (no test) | `sprout/print/` | `beansprout.plugins.print` | Side-effecting: write `ast.OptionValues` + every directive to `os.Stderr` via `printer.Fprint` (`pkg/printer`). Return `api.Result{}` unchanged. Document the os.Stderr direct write as a deviation. Even with no upstream test, write a Go test that captures stderr and asserts non-empty output for a one-directive fixture. |

### Step 3 — Wave 2: light up umbrella + full-repo verification

**Functional requirements:**
- `pkg/ext/postproc/sprout/doc.go` blank-imports all 10 subpackages
  (alphabetical).
- `pkg/ext/postproc/sprout/BUILD.bazel` lists all 10 as `deps`.
- `bazel test //...` passes.
- A `.beancount` file using any `beansprout.plugins.*` directive
  resolves correctly through the registry under `beancheck`.

**Modules touched:**
- `pkg/ext/postproc/sprout/doc.go`
- `pkg/ext/postproc/sprout/BUILD.bazel`

**Verification:**
- `bazel build //...`
- `bazel test //...`
- `bazel test //cmd/beancheck/...`

**Quality requirements:**
- Subpackages listed in stable alphabetical order so future ports can
  insert without reflow.

**Commit subject:** `wire sprout umbrella to ported plugins`

## Critical reference files

Templates (read-only references):
- `pkg/ext/postproc/std/leafonly/plugin.go`
- `pkg/ext/postproc/std/leafonly/plugin_test.go`
- `pkg/ext/postproc/std/leafonly/doc.go`
- `pkg/ext/postproc/std/leafonly/BUILD.bazel`
- `pkg/ext/postproc/std/doc.go`
- `pkg/ext/postproc/std/BUILD.bazel`

Reused utilities (no changes; each plugin imports as needed):
- `pkg/ast` — directives, accounts, metadata, diagnostics, span
- `pkg/ast.OptionValues` accessors — operating currencies, tolerance
- `pkg/printer.Fprint` — directive serialization (used by `print`)
- `github.com/cockroachdb/apd/v3` — decimal arithmetic
- `container/heap` (stdlib) — for `pricecompletion`'s Dijkstra
- `gopkg.in/yaml.v3` — for `infermetadata` (add if absent)

## Final verification

After Wave 2:

1. `bazel build //pkg/ext/postproc/sprout/...` — every subpackage compiles.
2. `bazel test //pkg/ext/postproc/sprout/...` — every plugin's tests pass.
3. `bazel test //pkg/ext/postproc/...` — api + apply tests with the new `SourceFilename` populated.
4. `bazel test //cmd/beancheck/...` — beancheck still green with the additional umbrella import.
5. `bazel test //...` — no regression anywhere.
6. Manual smoke (optional): run `bazel run //cmd/beancheck -- <fixture>` against a `.beancount` file that uses one `beansprout.plugins.*` directive, confirm activation prints the expected diagnostics.

## Commit / branch strategy

Branch `claude/wonderful-babbage-IKhWB` on `yugui/go-beancount`.

- 1 commit for Wave 0.
- Up to 10 commits across Wave 1 (one per plugin).
- 1 commit for Wave 2.

Push after each commit. No PR creation in this task. No changes pushed
to `yugui/beansprout`.
