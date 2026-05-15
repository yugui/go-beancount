# Plan: Currency-only Cost Spec and Per-Weight-Currency Residual Resolution

## Goal

Make `bean-check` accept ledgers that upstream beancount accepts but
ours currently rejects, specifically transactions that use
currency-only cost specs (`{ JPY }`) on multiple postings whose weight
currencies are distinct. Concretely:

```
1970-01-01 * "txn"
  Assets:A          10 A { JPY }
  Assets:B          10 B { USD }
  Assets:Cash       -100 JPY
  Assets:Cash       -1.00 USD
```

today produces five errors (parser rejection of `{ JPY }` plus
multi-unknown ambiguity); after this change it must reduce cleanly
with `Cost(10, JPY)` on `Assets:A` and `Cost(0.10, USD)` on
`Assets:B`.

## Scope

In scope:

- Parser: accept `{ CUR }` / `{{ CUR }}` (currency-only cost specs).
- AST: refactor `CostSpec` to separate numbers from currency, add
  `CostHolder.GetCurrency()`.
- Lower / printer: round-trip currency-only specs through the new AST.
- Reducer: replace global multi-unknown rejection with a
  per-weight-currency residual resolution that allows one committed
  unknown per currency, plus a single free unknown for an unclaimed
  residual.

Out of scope:

- Booking algorithm itself (lot matching, FIFO/LIFO/STRICT semantics)
  — only the deferred-cost / residual pass is touched.
- New error code / new diagnostic categories — error wording reuses
  existing strings; we only re-route which case fires which.
- Wire format changes for `beancompat` or external consumers.

## Background

Two failures stack on the user's example:

1. `pkg/syntax/parser.go::parseCostContents` (line 470 switch) does not
   accept a leading `CURRENCY` token in a cost spec. It fires the
   generic `expected amount, date, or label in cost spec, got CURRENCY`
   error and skips the token. The recovered AST is a `CostSpec{}` —
   indistinguishable from `{}` — so reducer treats both costed
   postings as deferred unknowns.
2. `pkg/inventory/reducer.go::visitTxn` (line 631) rejects
   `len(unknowns) > 1` outright, regardless of whether the unknowns
   sit in different weight currencies. `solveResidual` further assumes
   the entire transaction has a single residual currency.

Upstream beancount's `interpolate_group` allows one unknown per
weight currency (committed via cost-spec currency or price annotation)
plus one auto-posting absorbing the unique remaining residual
currency. We mirror that.

The AST cannot today represent "currency known, number unknown":
`Amount.Number` is a value type, so the Python-style separation of
`number_per`/`number_total`/`currency` does not survive the current
`*Amount` packing. We refactor `CostSpec` to mirror upstream's
NamedTuple shape; the `CostHolder` interface gains `GetCurrency()` so
callers stop discriminating with type assertions.

## Steps

### Step 1 — AST refactor: `CostSpec` shape and `CostHolder.GetCurrency`

#### Functional requirements

- `ast.CostSpec` carries `PerUnit *apd.Decimal`, `Total *apd.Decimal`,
  `Currency string` (in addition to `Span`, `Date`, `Label`). The old
  `*Amount` fields are removed.
- `CostHolder.GetCurrency() string` is added. Implementations:
  - `*CostSpec` returns `c.Currency`.
  - `*Cost` (booked) returns `c.Currency`.
- `(*CostSpec).GetPerUnit()` synthesizes `&Amount{Number: *PerUnit,
  Currency: Currency}` when both `PerUnit != nil` and `Currency != ""`;
  otherwise returns `nil`. `GetTotal()` is symmetric.
- `(*CostSpec).Clone()` deep-copies the new fields independently
  (number pointers and currency string).
- `costNumberMissing` semantics unchanged: returns true when both
  `GetPerUnit()` and `GetTotal()` return nil.
- All existing call sites that read `cost.PerUnit` / `cost.Total`
  directly switch to `GetPerUnit()` / `GetTotal()` accessor calls so
  the synthesis layer is the single source of truth. Call sites that
  inspected `cost.PerUnit.Currency` to discover the cost currency are
  rewritten to `holder.GetCurrency()`, eliminating the `*CostSpec`
  type assertion at those sites.

#### Modules

- `pkg/ast/directives.go` — `CostSpec` struct definition.
- `pkg/ast/cost.go` — `CostHolder` interface, `*CostSpec` and `*Cost`
  method set, `(*Posting).TotalCost`.
- `pkg/ast/clone.go` — `(*CostSpec).Clone`.
- `pkg/ast/lower.go` — `lowerCostSpec` updated to populate the new
  field shape (currency-only path is still parser-rejected at this
  step; lower handles the existing forms with the new layout).
- `pkg/ast/*_test.go` — fixture `*CostSpec{}` literals updated to the
  new shape; round-trip / clone / accessor tests cover the synthesis.
- All read sites in `pkg/inventory/`, `pkg/printer/`, `pkg/validation/`,
  `pkg/loader/`, `cmd/` that touch `*CostSpec` fields directly.

#### Verification

- `bazel build //...` and `bazel test //...` are both green after this
  step alone (no behavior change end-to-end).
- New `pkg/ast/cost_holder_test.go` cases assert `GetCurrency()`
  values for parser-output `CostSpec` (with PerUnit / with Total /
  with combined / with neither) and for booked `Cost`.
- Existing `TestCostHolder_*`, `TestCostSpecClone*`, `TestPosting_TotalCost_*`
  pass after fixture migration.

#### Quality requirements

- Doc comments on the new `GetCurrency` method state its contract:
  returns the cost currency, empty string if unspecified, never
  causes synthesis or allocation.
- The new `CostSpec` fields' godoc spells out the four shapes
  (per-unit / total / combined / currency-only / empty) and the
  redundancy contract between `PerUnit.Currency`-style synthesis and
  the standalone `Currency` field.
- No type assertion on `*CostSpec` remains in non-`pkg/ast` packages
  for the purpose of reading the cost currency. (Type assertions
  remain legitimate where the caller needs to *write* CostSpec
  fields, e.g. reducer mutators.)

### Step 2 — Parser, lower, and printer support for `{ CUR }`

#### Functional requirements

- `parseCostContents` (`pkg/syntax/parser.go:460`) accepts a single
  leading `CURRENCY` token, consuming it as the cost currency. The
  same logic applies inside `{{ ... }}` (total braces) — currency-only
  is a property of the contents, not of the brace flavor.
- `parseCostElement` (trailing comma-separated) is unchanged:
  currency-only is only valid as the first / sole element.
- A currency-only spec may be combined with a date and/or label
  (`{ JPY, 2024-01-01 }`, `{ JPY, 2024-01-01, "lot" }`); the parser
  must not regress these forms.
- `lowerCostSpec` (`pkg/ast/lower.go`) populates `CostSpec.Currency`
  from the `CURRENCY` token when no `Amount` node is present. When
  an `Amount` node is present, `Currency` is set from the amount's
  currency (or, in the combined `{X # Y CUR}` form, from the total
  side's currency).
- The printer round-trips currency-only specs as `{CUR}` (or
  `{{CUR}}` when only `Total` was set) without injecting a numeric
  zero.

#### Modules

- `pkg/syntax/parser.go` — `parseCostContents` switch.
- `pkg/syntax/parser_test.go` — new tests for `{ JPY }`, `{{ USD }}`,
  `{ JPY, 2024-01-01 }`, `{ JPY, 2024-01-01, "lot" }`.
- `pkg/ast/lower.go` — currency-only path; consolidated `Currency`
  population.
- `pkg/ast/lower_test.go` — assert resulting `CostSpec` field shape.
- `pkg/printer/...` — currency-only emission.
- `pkg/printer/..._test.go` — round-trip cases.

#### Verification

- New parser tests assert no errors and the expected `CostSpec`
  shape.
- Round-trip test: a source containing `{ JPY }` parses, lowers,
  prints, re-parses identically (same `CostSpec` shape).
- The reducer still rejects the user's full example at this step
  (Pass 2 ambiguity not yet fixed); a focused parser/lower test does
  not assert end-to-end success yet.

#### Quality requirements

- Parser error wording for malformed currency-only specs (e.g.
  `{ JPY, JPY }` — currency in trailing position) keeps the existing
  "expected amount, date, or label" message.
- The new test cases cover `{ CUR }`, `{{ CUR }}`, and the
  currency-with-date / currency-with-label combinations explicitly,
  not relying on a single golden file.

### Step 3 — Reducer per-weight-currency residual resolution

#### Functional requirements

- `unknownCandidateCurrency(p *ast.Posting) string` returns the
  weight currency a still-unknown posting will absorb, or `""` if
  the currency is not yet determinable. The decision precedence is:
  1. `p.Cost != nil` and `p.Cost.GetCurrency() != ""` → that
     currency.
  2. `p.Price != nil` → `p.Price.Amount.Currency`.
  3. otherwise → `""`.
- `postingResolution.addUnknown` records the candidate currency on
  `unknownDesc[i].currency` at insertion time.
- `(*postingResolution).bindAndCollect()` is narrowed to bind
  `Source` pointers on `[]BookedPosting` only and return
  `[]BookedPosting`. It no longer returns the unknown slice.
- New `(*postingResolution).groupForResidual()` returns
  `(committed []unknownGroup, free []*ast.Posting)`. `unknownGroup`
  is `{ currency string; booked []*ast.Posting; unknown []*ast.Posting }`.
  `committed` only includes groups where `len(g.unknown) > 0` and
  `g.currency != ""`. `free` is the slice of unknown postings whose
  candidate currency was `""`.
- `visitTxn` Pass 2 logic, after `bindAndCollect`:
  1. For each `committed` group: if `len(g.unknown) > 1`, call
     `flagAmbiguousUnknowns(g.unknown)` and continue; otherwise sum
     `g.booked` weights in the group's currency, negate, and apply
     the residual to `g.unknown[0]` via `resolveCostFromResidual`
     (or direct `Amount` write for auto-postings).
  2. After committed processing, compute `unclaimed`: non-zero
     residual currencies among booked weights minus currencies
     claimed by committed unknowns.
  3. Free unknown handling:
     - 0 free + any unclaimed → no reducer error (validation layer
       reports unbalanced).
     - 1 free + 1 unclaimed → resolve the free unknown against the
       single unclaimed currency (existing single-unknown path).
     - 1 free + 0 unclaimed or 1 free + multiple unclaimed → reuse
       existing zero-residual / multi-residual diagnostic.
     - >1 free → `flagAmbiguousUnknowns(free)`.
- `BookedPosting` records continue to be assembled inside Pass 1's
  `addX` calls; partial / pre-Source-bind `BookedPosting` values are
  never visible outside `postingResolution`.
- `solveResidual` is split into a per-currency helper that takes the
  group's booked posting pointers and the target currency and
  returns the negated sum.

#### Modules

- `pkg/inventory/reducer.go` — `postingResolution`, `bindAndCollect`,
  `groupForResidual`, `visitTxn` Pass 2, residual helpers,
  `unknownCandidateCurrency`.
- `pkg/inventory/weight.go` — defensively guard `PostingWeight` so
  `cost == nil && Cost.GetCurrency() != ""` (currency-only deferred,
  number unknown) does not silently fall through to Price / units.
- `pkg/inventory/reducer_test.go` — new and updated tests.
- `pkg/loader/loader_test.go` — add an end-to-end test using the
  user's exact ledger.

#### Verification

- New `TestReducerWalk_InterpolatesMultipleDeferred_DistinctWeightCurrency`:
  feeds the user's example, asserts no errors and inferred costs
  `Cost(10, JPY)` / `Cost(0.10, USD)`.
- Updated `TestReducerWalk_InterpolationAmbiguousMultipleDeferred`
  (or sibling): two deferred postings sharing the same weight
  currency still produce two `CodeUnresolvableInterpolation` errors.
- `TestReducerWalk_CommittedPlusFree`: a `{ JPY }` deferred posting
  combined with an auto-posting where the committed absorbs JPY
  residual and the free absorbs the single remaining USD residual.
- All existing `TestReducerWalk_Interpolation*` tests pass after
  fixture migration.
- End-to-end loader test: `loader.Load(ctx, src)` on the user's
  source returns a ledger with no error-severity diagnostics.
- `bazel build //...` and `bazel test //...` are green.

#### Quality requirements

- The new `unknownGroup` type and `groupForResidual` method carry
  godoc that states the contract (committed groups have a known
  currency and at least one unknown; free postings have no
  determinable currency; together they cover every recorded unknown
  exactly once).
- Inline comments inside the new Pass 2 dispatch are limited to
  short tags (`// committed`, `// free`, `// invariant: ...`); the
  switch arms speak for themselves through the case labels.
- No new exported symbols outside what the contract above lists.

### Step 4 — Cleanup

#### Functional requirements

- `docs/plans/currency-only-cost-and-grouped-residual.md` is removed
  from the repository in the final commit (amended into Step 3's
  commit). Plan artifacts must not persist past the work that
  consumes them.

#### Modules

- (none, just file removal)

#### Verification

- `git log -1 --stat` on the final commit shows the plan file
  deletion alongside the Step 3 reducer changes.
- `find docs/plans -name '*currency-only*'` returns nothing after
  the final commit.

#### Quality requirements

- The amended commit's message still reflects Step 3's purpose
  (reducer per-currency resolution); the plan deletion is
  janitorial and does not need its own changelog line.

## Quality Requirements (binding on every step)

These apply to all `.go` source produced or modified by this work.
They sit on top of upstream Go style (Effective Go, Code Review
Comments, Google Go Style); where upstream is silent or permissive,
prefer the more minimal form below. Both `generator` and
`go-code-reviewer` enforce them.

### Doc comments on exported symbols

Required. State the external contract concisely:

- For functions and methods: behavior callers can rely on, error
  semantics where they affect callers, preconditions /
  postconditions.
- For types: role and lifecycle — what the value represents, when it
  becomes valid, when it must be released, what concurrency
  guarantees apply.
- For variables and constants: the invariant the value carries.

Do not narrate the implementation. Exception: when the narration is
itself part of the contract (a complexity bound, an ordering
guarantee, a goroutine-safety claim, a documented allocation
behavior), it stays — but kept tight. Brevity is a feature, not a
side effect; a two-line contract is better than a paragraph that
restates the code.

### Doc comments on unexported symbols

Not required by default. Omit when the name and signature already
make the purpose obvious. Add a doc comment only when:

- the symbol is a non-obvious helper, or carries a subtle invariant a
  reader could plausibly violate; or
- it is a package-internal building block designed for reuse, in
  which case treat it like an exported symbol and document its
  contract with the same brevity.

### Inline comments

Default: none. Code structure, type names, and function names should
carry the meaning. When a comment is genuinely needed, prefer the
shortest form that conveys it — typically 1–3 words that name the
non-obvious property or reference a term defined in the surrounding
godoc. Examples:

```
// unreachable
// avoid aliasing
// pass 1
// invariant: sorted
```

Reserve longer comments for genuinely non-obvious workarounds (cite
the issue or bug). Before writing a long comment, ask whether a
rename, a small extraction, or a clearer type would make the comment
unnecessary; if so, prefer the code change. Never narrate what the
code already says.

### Tests target observable behavior

Tests exercise the package's exported surface. Exported symbols are
the externally observable behavior; unexported symbols are
implementation that must remain free to be reorganized without
rewriting tests.

Exceptions where a direct test on an unexported symbol is justified:

- The symbol is a package-internal building block intentionally
  designed for reuse across multiple call sites, where its contract
  has independent value.
- The code path's coverage via the exported API would require
  disproportionately many tests or fragile fixtures, such that
  testing the helper directly reduces total test surface.

When you take an exception, note the rationale briefly (in a test
comment or in the implementation report) so a reviewer can see why
direct testing was chosen.

### Bazel / Gazelle

After modifying `.go` files or dependencies, run `bazel run //:gazelle`
to regenerate BUILD files before building / testing. `bazel build //...`
and `bazel test //...` must be green at every step's commit.

### Commit messages

Per `CLAUDE.md`: subject in imperative mood; body conveys why /
realized behavior / design rationale. Do not narrate mechanical
edits or reference internal variable names unless they encode a
significant design decision. Do not include the model identifier or
session URL.

## Alternatives considered

### AST representation of currency-only cost

- **Add `Currency` field to current `CostSpec` (PerUnit/Total stay
  `*Amount`).** Smaller blast radius. Rejected: leaves redundancy
  (PerUnit.Currency vs Currency) and forces every caller to
  remember which is canonical. The user explicitly preferred the
  upstream-shaped split.
- **Make `Amount.Number` a `*apd.Decimal`.** Lets us encode "no
  number" inside `Amount` itself. Rejected: blast radius across
  every Amount user is large; semantic gain is small because
  `Amount` is rarely "missing-number" outside cost specs.
- **Synthesize `PerUnit = &Amount{Number: zero, Currency: CUR}` with
  a separate "missing" flag.** Rejected: zero is a valid number;
  encoding "missing" via a flag adjacent to Number bloats Amount
  and replicates work GetCurrency already solves cleanly.

**Selected:** `PerUnit *apd.Decimal`, `Total *apd.Decimal`,
`Currency string` on `CostSpec`, paired with `CostHolder.GetCurrency()`.

### Reducer Pass 2 grouping API

- **Return a single `[]unknownGroup` containing both committed and
  free groups (free as a `currency == ""` group).** Rejected: free
  handling is structurally different (residual disambiguation vs.
  per-currency solve), and a single iterator forces every caller to
  re-discriminate. Splitting return values matches actual usage.
- **Return groups including those with `len(unknown) == 0`.**
  Rejected: such groups are validation-layer concerns; including
  them adds a 0-unknown branch to every consumer for no value.
- **Expose partially-built `BookedPosting` slices from
  `groupForResidual`.** Rejected: would break the invariant that
  partial `BookedPosting` values do not escape `postingResolution`.
  `unknownGroup.booked []*ast.Posting` carries exactly what Pass 2
  needs (Source for weight calculation).

**Selected:** `groupForResidual() (committed []unknownGroup, free []*ast.Posting)`
with `unknownGroup.booked []*ast.Posting`.

### Multi-unknown handling for auto-postings

- **Strict committed/free separation** (recommended and selected): an
  unknown with a determinable weight currency commits to it; a
  truly free unknown can absorb at most one unclaimed currency.
- **Conservative — keep flagging all multi-unknown when any free is
  present.** Rejected: regresses the user's intended behavior in
  realistic ledgers that mix committed and auto-postings.

**Selected:** strict separation per upstream `interpolate_group`.

## Recommended approach + rationale

The four steps form a strict dependency chain: Step 1 changes the
type that Steps 2 and 3 read, so it ships first as a behavior-neutral
refactor. Step 2 unlocks the surface syntax but leaves the reducer
deliberately rejecting multi-unknown so the regression test for
parser/lower/printer is self-contained. Step 3 is where the
end-to-end behavior change lands, with the focused new tests for
distinct-currency multi-unknown plus the user's exact ledger as a
loader test. Step 4 cleans the plan artifact.

The split into committed vs. free unknowns directly mirrors upstream
`interpolate_group` semantics, avoiding a second implementation that
might subtly diverge. The choice to refactor `CostSpec` shape (rather
than bolt on a new field) is forced by the user's preference for the
upstream-shaped layout, and pays for itself by removing
`*CostSpec`-discriminating type assertions across the call sites.
