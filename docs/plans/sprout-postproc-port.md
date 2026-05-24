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

### Detailed Design

#### Contract

##### 1. `api.Input.SourceFilename`

Add to `pkg/ext/postproc/api/plugin.go`, immediately after the `Directive *ast.Plugin` field (i.e. last field of `Input`):

```go
// SourceFilename is the absolute or repository-relative path of the
// root beancount source file that produced the ledger under
// transformation. It is the filename of the first entry of
// [ast.Ledger.Files] — the file the user named when invoking
// [ast.LoadFile] (or the equivalent root for [ast.Load] /
// [ast.LoadReader]). Plugins use it as the anchor directory for
// path-bearing config (e.g. YAML side-files referenced relatively
// from the ledger).
//
// Empty when the ledger was constructed programmatically without an
// associated source file (i.e. len(ledger.Files) == 0) or when the
// runner is invoked on a hand-built &Ledger{} with no Files slice.
// Plugins that require a non-empty value must report a Diagnostic
// rather than returning an error.
SourceFilename string
```

Runner population in `pkg/ext/postproc/apply.go`, computed once per `Apply` call (not per plugin):

```go
var sourceFilename string
if len(ledger.Files) > 0 && ledger.Files[0] != nil {
    sourceFilename = ledger.Files[0].Filename
}
```

Behaviour: `len(ledger.Files) == 0` → empty string. `ledger.Files[0] == nil` (defensive) → empty string. Value computed once before the loop; later `ReplaceAll` does not change it (consistent with how `Options` snapshot is handled). No new error path — `Apply` retains its existing return contract.

##### 2. `ast.ParseAmountExpression` and `ast.ParseBalanceAmount`

New file `pkg/ast/balance_expr.go`:

```go
// ParseAmountExpression parses a single beancount amount expression of
// the form `<arith-expr> <CURRENCY>` (e.g. "100 USD", "1,000 + 500 USD",
// "(100+200)*1.05 USD"). It is the public entry point for callers that
// want to consume amount text outside the directive grammar — most
// notably postproc plugins whose config or Custom directive bodies
// carry user-authored amount strings.
//
// On success the returned Diagnostics slice is empty; on failure the
// returned Amount is the zero value and Diagnostics carry stable codes.
// Diagnostic Spans are anchored at byte offsets within s (Start.Filename
// empty, Line and Column zero); the caller rebases them onto the
// enclosing CST/AST node before surfacing them on the ledger.
//
// Diagnostic codes:
//   - "amount-expr-parse"        underlying syntax parse failure
//   - "amount-expr-eval"         arithmetic evaluation failure (divide by zero, overflow)
//   - "amount-missing-currency"  no CURRENCY token after the expression
//   - "amount-trailing-input"    unconsumed tokens after the currency
func ParseAmountExpression(s string) (Amount, []Diagnostic)

// ParseBalanceAmount parses a balance directive body of the form
// `<arith-expr> [~ <arith-expr>] <CURRENCY>`. Tolerance is non-nil iff
// the input contained a `~ <expr>` clause, in which case the tolerance
// shares Amount.Currency (it has no independent currency in beancount
// syntax).
//
// Adds the diagnostic code:
//   - "balance-tolerance-eval"   tolerance expression failed to evaluate
func ParseBalanceAmount(s string) (Amount, *apd.Decimal, []Diagnostic)
```

Error semantics (locked):
- All ledger-content failures (bad syntax, divide-by-zero, missing currency, trailing garbage, invalid number) are reported as Diagnostics. The functions never panic on user input and never return an `error`.
- Multiple diagnostics may be returned for one call.
- An empty or whitespace-only input string yields exactly one Diagnostic with code `"amount-expr-parse"` and message `"empty amount expression"`.
- `Diagnostic.Span` is `{Start:{Offset: N}, End:{Offset: M}}` where N and M are byte offsets within s. `Filename`, `Line`, and `Column` are zero — caller rebases.

##### 3. `pkg/syntax` thin entry points

Add to `pkg/syntax/balance_expr.go`:

```go
// ParseBalanceAmount parses src as a balance directive body of the form
//   <arith-expr> [~ <arith-expr>] <CURRENCY>
// returning the resulting BalanceAmountNode subtree and any parse errors.
// The returned Node is never nil; trailing input is reported as an error
// and consumed into the node.
func ParseBalanceAmount(src string) (*Node, []Error)

// ParseAmountExpression parses src as a single amount of the form
//   <arith-expr> <CURRENCY>
// returning an AmountNode subtree and any parse errors.
func ParseAmountExpression(src string) (*Node, []Error)
```

Both wrap a single private helper that constructs the `parser` with a fresh scanner over `src`, calls `p.advance()` once, invokes `p.parseBalanceAmount()` (or `p.parseAmount()`), then verifies `p.peek() == EOF` and records a trailing-input error otherwise. The existing private productions are unchanged.

##### 4. Stable diagnostic codes (kebab-case)

| Code | Trigger |
|---|---|
| `amount-expr-parse` | syntax error from `pkg/syntax` (any `Error` not otherwise classified) |
| `amount-expr-eval` | apd arithmetic trap on the main expression |
| `amount-missing-currency` | no CURRENCY token where one was required |
| `amount-trailing-input` | unconsumed tokens after the directive body completed |
| `balance-tolerance-eval` | apd arithmetic trap on the tolerance expression |

`amount-missing-currency` is detected at the ast-layer wrapper (`AmountNode.FindToken(CURRENCY) == nil`) rather than substring-matching the syntax error message; the syntax layer's "expected CURRENCY, got X" error is then suppressed for that exact offset to avoid duplicate diagnostics (mirrors `lowerer.hasParserErrorIn`).

##### 5. `pkg/ext/postproc/sprout/` umbrella

`pkg/ext/postproc/sprout/doc.go` — only file in directory at end of Step 1: `package sprout` with godoc adapted from `std/doc.go`. **No blank imports during Step 1.** The file exists so `cmd/beancheck` has a real package to import; Wave 2 lights up the contents.

##### 6. `cmd/beancheck` activation

`cmd/beancheck/main.go` gains a second side-effect import next to the existing `std` import:

```go
_ "github.com/yugui/go-beancount/pkg/ext/postproc/std"
_ "github.com/yugui/go-beancount/pkg/ext/postproc/sprout"
```

One-line addition to the comment block explaining the umbrella covers both. No new tests required at this step (umbrella has no plugins to verify).

#### Suggested Internals

##### Shared `evalArithExpr` / `parseNumberToken`

Recommendation: **option (a) — extract as package-level free functions in `pkg/ast`** (e.g. `evalArithExpr(n *syntax.Node) (apd.Decimal, *Diagnostic)` and `parseNumberToken(t *syntax.Token) (apd.Decimal, error)`), and update `lowerer.evalExpr` to delegate.

```go
// in balance_expr.go
func evalArithExpr(n *syntax.Node) (apd.Decimal, *Diagnostic) { /* port of lowerer.evalExpr without the *lowerer receiver */ }
func parseNumberToken(t *syntax.Token) (apd.Decimal, error)   { /* identical body to current parseNumber */ }

// in lower.go
func (l *lowerer) evalExpr(n *syntax.Node) (apd.Decimal, bool) {
    d, diag := evalArithExpr(n)
    if diag != nil {
        l.file.Diagnostics = append(l.file.Diagnostics, l.rebaseDiagnostic(*diag))
        return apd.Decimal{}, false
    }
    return d, true
}
```

Rationale: `evalArithExpr` produces diagnostics anchored at the CST node's byte offsets — exactly what the public string-input API exposes; the lowerer needs the same offsets rehydrated through `posAt` into Line/Column under `l.filename`. No new package needed. Option (b) (throwaway lowerer) rejected because it would require fake `filename`/`source`/`lineStarts`. Option (c) (internal subpackage) rejected as overengineering for two helpers.

The `arithCtx` constant stays where it is in `lower.go` (or migrates to `balance_expr.go`; either works, just one location).

##### `syntax.ParseBalanceAmount` wrapping

The existing private `parseBalanceAmount` is already a clean unit. The public wrapper only needs:

```go
func ParseBalanceAmount(src string) (*Node, []Error) {
    p := &parser{scanner: newScanner(src), src: src}
    p.advance()
    node := p.parseBalanceAmount()
    if p.peek() != EOF {
        p.errorf("unexpected trailing input")
        for p.peek() != EOF {
            tok := p.advance()
            node.AddToken(&tok)
        }
    }
    return node, p.errors
}
```

No new top-level production needed. `parseAmount` gets identical treatment for `ParseAmountExpression`. The `isAtNextLine()` check inside `parseBalanceAmount` keeps working: a freestanding amount string has no newlines, so the production runs end-to-end on one logical line.

##### File layout

- `pkg/ast/balance_expr.go` — public `ParseAmountExpression`, `ParseBalanceAmount`, and the extracted free helpers `evalArithExpr`, `parseNumberToken`.
- `pkg/ast/balance_expr_test.go` — table-driven coverage of both public functions plus targeted tests for diagnostic codes and span offsets.
- `pkg/ast/lower.go` — `evalExpr` / `parseNumber` shrink to thin delegators; existing `arithCtx` stays in `lower.go`.
- `pkg/syntax/balance_expr.go` — public `ParseBalanceAmount` and `ParseAmountExpression` wrappers (kept separate from `parser.go` so the public surface is greppable).
- `pkg/syntax/balance_expr_test.go` — CST-level smoke tests for the two wrappers.
- `pkg/ext/postproc/sprout/doc.go` — umbrella.
- `pkg/ext/postproc/sprout/BUILD.bazel` — hand-written, identical shape to `std/BUILD.bazel` minus the deps list.

##### BUILD.bazel changes

- `pkg/ast/BUILD.bazel`: add `"balance_expr.go"` to `go_library.srcs` (alphabetical: between `ast.go` and `booking.go`); add `"balance_expr_test.go"` to `go_test.srcs`. No new deps.
- `pkg/syntax/BUILD.bazel`: add `"balance_expr.go"` to `go_library.srcs` (between `error.go` and `file.go`); add `"balance_expr_test.go"` to `go_test.srcs`. No new deps.
- `pkg/ext/postproc/sprout/BUILD.bazel` (new): `go_library` named `sprout`, `srcs = ["doc.go"]`, empty deps.
- `cmd/beancheck/BUILD.bazel`: add `"//pkg/ext/postproc/sprout"` to both `go_binary.deps` and `go_test.deps` (alphabetical, after the existing `std` entry).
- `pkg/ext/postproc/BUILD.bazel`: no source changes. If `apply_test.go` grows additional deps for the new test, update accordingly (likely no change).

##### Diagnostic-code dispatch from syntax errors

In `ast.ParseBalanceAmount`:
1. Call `syntax.ParseBalanceAmount(s)`.
2. Validate structural expectations (currency token present, second `ArithExprNode` after TILDE if any).
3. For each `syntax.Error`, classify: if `Pos` falls inside a byte range where a structural failure was already detected (missing currency, trailing input), emit the structural code only — suppress the generic one (mirrors `lowerer.hasParserErrorIn`). Otherwise emit `amount-expr-parse`.
4. Call `evalArithExpr` on the main `ArithExprNode`; stamp `amount-expr-eval` on any returned diagnostic.
5. Call `evalArithExpr` on the tolerance `ArithExprNode` if present; stamp `balance-tolerance-eval`.

`evalArithExpr` produces a `*Diagnostic` with empty `Code`; the caller stamps the code based on which expression it was evaluating. Keeps `evalArithExpr` agnostic to its calling context.

#### Verification Plan (Step 1)

**`pkg/ast/balance_expr_test.go`** — table-driven for `ParseBalanceAmount`:

| Input | Expected Amount | Expected Tolerance | Expected diag codes |
|---|---|---|---|
| `"100 USD"` | `{100, "USD"}` | nil | none |
| `"1,000 + 500 USD"` | `{1500, "USD"}` | nil | none |
| `"(100+200)*1.05 USD"` | `{315, "USD"}` | nil | none |
| `"319.020 ~ 0.002 USD"` | `{319.020, "USD"}` | `0.002` | none |
| `"-100 USD"` | `{-100, "USD"}` | nil | none |
| `"100 / 0 USD"` | zero | nil | `["amount-expr-eval"]` |
| `"100"` | zero | nil | `["amount-missing-currency"]` |
| `"100 USD trailing"` | as above | nil | `["amount-trailing-input"]` |
| `"100 USD EUR"` | as above | nil | `["amount-trailing-input"]` |
| `"100 + USD"` | zero | nil | `["amount-expr-parse"]` |
| `""` | zero | nil | `["amount-expr-parse"]` |
| `"100 ~ 0/0 USD"` | zero | nil | `["balance-tolerance-eval"]` |

For each row also assert `Diagnostic.Severity == ast.Error` and `Diagnostic.Span.Start.Offset` is within `len(input)`. Use `apd.Decimal.Cmp` for numeric comparison.

**`ParseAmountExpression`** parallel table (subset, no tolerance):

| Input | Expected | Expected diag codes |
|---|---|---|
| `"100 USD"` | `{100, "USD"}` | none |
| `"1,234.56 EUR"` | `{1234.56, "EUR"}` | none |
| `"100 ~ 1 USD"` | zero | `["amount-trailing-input"]` (tolerance form intentionally rejected) |
| `"100"` | zero | `["amount-missing-currency"]` |
| `"abc USD"` | zero | `["amount-expr-parse"]` |

**`pkg/syntax/balance_expr_test.go`** — CST-level smoke:
- `"100 USD"` round-trips through the wrapper: returned `*Node.Kind == BalanceAmountNode`, contains one `ArithExprNode` and one `CURRENCY` token, `len(errors) == 0`.
- `"100 USD junk"` returns the same node shape plus one `Error` with `Pos` at the offset of `j`.

**`pkg/ext/postproc/apply_test.go`** — new tests:
- `TestApply_SourceFilenamePopulated`: construct a ledger with `Files: []*ast.File{{Filename: "/tmp/main.beancount"}}` + a plugin directive; register a fake plugin that captures `api.Input.SourceFilename`; assert captured value == `/tmp/main.beancount`.
- `TestApply_SourceFilenameEmptyWhenNoFiles`: ledger with empty `Files`; captured value == `""`; no diagnostic.

**`pkg/ext/postproc/api/plugin_test.go`** — one-line check that `api.Input{}.SourceFilename == ""` (zero-value contract).

**Build verification:**
- `bazel build //pkg/ast/... //pkg/syntax/... //pkg/ext/postproc/... //cmd/beancheck/...` passes.
- `bazel test //pkg/ast/... //pkg/syntax/... //pkg/ext/postproc/... //cmd/beancheck/...` passes (existing `lower_test.go` evalExpr-via-balance-directive cases must remain green — delegation must be byte-identical).
- `bazel test //...` shows no regression.

#### Cross-Step Coupling

- **Wave 1 / `infermetadata`** depends on `api.Input.SourceFilename` being non-empty for YAML side-file resolution.
- **Wave 1 / `comprehensivebalance`** depends on `ast.ParseBalanceAmount` to parse each non-empty line of a Custom directive body.
- **Wave 1 / `fiscalincomeexpense`** depends on `ast.ParseBalanceAmount` (and optionally `ast.ParseAmountExpression` for the string-amount path that does not accept tolerance).
- **Wave 2** uses no new types from this step; only wires existing umbrella deps.
- No coupling to other ports/phases — additions are purely additive (new field defaults to zero, lowerer.evalExpr delegation is byte-identical, covered by existing `lower_test.go`).

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
