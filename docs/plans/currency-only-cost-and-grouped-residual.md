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

### Detailed Design

#### Contract

**Package: `pkg/ast`**

`CostSpec` struct, exact field order (godoc-readability: span first, then the three "what" fields, then the two optional "which lot" fields):

```go
type CostSpec struct {
    Span     Span
    PerUnit  *apd.Decimal
    Total    *apd.Decimal
    Currency string
    Date     *time.Time
    Label    string
}
```

- `PerUnit` and `Total` are **numbers only**; they share the single `Currency` field.
- `Currency == ""` means "currency unspecified". A non-empty `Currency` is valid even when both `PerUnit` and `Total` are nil (this is the `{ CUR }` form Step 2 will introduce; this step's lowerer never produces it, but the type must accept it).
- Either or both number pointers may be nil. The four shapes that existed before (per-unit only / total only / combined / empty) plus the new currency-only shape are all representable.

`CostHolder` interface (existing) keeps `GetCurrency() string` with this locked contract:

```
// GetCurrency returns the cost currency, or "" if unspecified.
// MUST NOT allocate; MUST NOT synthesize an Amount; MUST NOT
// consult PerUnit / Total. For *CostSpec it returns the Currency
// field verbatim. For *Cost it returns the Currency field verbatim.
```

`(*CostSpec).GetPerUnit() *Amount` synthesis contract:

- Returns `nil` when `c.PerUnit == nil`.
- Returns `nil` when `c.Currency == ""` (even if `c.PerUnit != nil`). A partially-constructed spec with a number but no currency is treated as "no per-unit Amount available"; callers needing the raw number can field-access `c.PerUnit` directly.
- When both are set, returns a **freshly allocated** `*Amount{Number: *CloneDecimal(c.PerUnit), Currency: c.Currency}`. No caching; each call allocates. The decimal is cloned so callers may mutate without disturbing the spec.

`(*CostSpec).GetTotal() *Amount` is symmetric.

`(*Cost).GetPerUnit` / `GetTotal` / `GetCurrency` semantics are unchanged — they continue to read the `*Amount` retention fields and the `Currency` field directly on the booked struct.

`(*CostSpec).Clone() *CostSpec` deep-copies independently:
- nil-safe (nil receiver returns nil).
- `PerUnit` and `Total` cloned via `CloneDecimal` (each may be nil).
- `Currency` is a string (value copy).
- `Date` cloned via fresh `*time.Time` allocation when non-nil.
- `Span`, `Label` copied by value.
- Result must not alias the receiver in any decimal coefficient buffer.

**Package: `pkg/inventory`**

`costNumberMissing` semantics: **unchanged**. Confirmed by reading `pkg/inventory/booking.go:32`: it already routes `c.GetPerUnit() == nil && c.GetTotal() == nil`, and post-refactor those return nil whenever the number pointer is nil OR the currency is empty. The desired predicate is "the spec carries no concrete per-unit or total *number*". Two cases need attention:

1. A currency-only spec (`PerUnit == nil && Total == nil && Currency == "JPY"`) → both accessors return nil → predicate returns true. Correct: this is exactly the "deferred, currency known" case Step 3 will route through the per-currency residual logic.
2. A `PerUnit != nil && Currency == ""` spec (only constructible by direct AST authorship, never by the lowerer) → `GetPerUnit()` returns nil under the synthesis rule → predicate returns true. This is acceptable: `costNumberMissing`'s caller treats true as "needs interpolation", which will then run interpolation against this number-having posting and produce a downstream error when the currency cannot be matched. The Contract does not regress for the in-tree call paths.

The Contract therefore locks: `costNumberMissing(c) == c.GetPerUnit() == nil && c.GetTotal() == nil && !c.IsBooked()` after the refactor, and the existing implementation (booking.go lines 44-52) stands without edit.

**Cross-step coupling**

- Step 2 (parser) will produce `CostSpec{Currency: "JPY"}` (no number pointers) for `{ JPY }`. The Contract above guarantees `GetCurrency()` returns `"JPY"` and both `GetPerUnit()` / `GetTotal()` return nil for this value, which is exactly what Step 3 needs from `unknownCandidateCurrency`.
- Step 3's `unknownCandidateCurrency` will read `p.Cost.GetCurrency()`. The "no allocation" guarantee on `GetCurrency` matters: it is called once per posting in a hot path during reducer Pass 2.
- The existing `(*Posting).TotalCost` (`pkg/ast/cost.go:34`) is unchanged in signature. It already consumes via the accessors, so it adapts transparently. The "combined cost currencies differ" defensive branch (line 43) becomes structurally unreachable through the accessors (a single `Currency` field cannot disagree with itself), but the check stays as a guard against direct field manipulation in tests.

**Allocation guarantees recap (locked):**

| Method                              | Allocates? |
|-------------------------------------|------------|
| `(*CostSpec).GetCurrency()`         | No         |
| `(*Cost).GetCurrency()`             | No         |
| `(*CostSpec).GetPerUnit()`          | Yes (Amount + Decimal clone) when non-nil result |
| `(*CostSpec).GetTotal()`            | Yes (Amount + Decimal clone) when non-nil result |
| `(*Cost).GetPerUnit() / GetTotal()` | No (returns retained pointer) |

This **breaks pointer-identity tests** like `pkg/ast/cost_holder_test.go:85-89`: `if got := h.GetPerUnit(); got != tc.spec.PerUnit`. The Contract requires those tests to be rewritten to compare values, not pointers, for `*CostSpec`. They keep the pointer-identity check for the `*Cost` branch.

**Public API surface that is removed:**

- `CostSpec.PerUnit` field of type `*Amount` (becomes `*apd.Decimal`).
- `CostSpec.Total` field of type `*Amount` (becomes `*apd.Decimal`).

**Public API surface that is added:**

- `CostSpec.Currency` field of type `string`.

No other exported names change. `CostHolder.GetCurrency` already exists; only its allocation contract is being locked here.

#### Suggested Internals

These are advisory. The implementer may reorganize freely as long as the Contract above holds and the test suite passes.

**Migration order for read sites that today access `cost.PerUnit.Number` / `cost.PerUnit.Currency` / `cost.Total.Number` / `cost.Total.Currency`:**

1. **`pkg/ast/lower.go::lowerCostSpec`** — must change first; this is the only constructor of `CostSpec` from CST. Replace the current branch structure with one that resolves `Currency` once and assigns `PerUnit` / `Total` as bare `*apd.Decimal` from the lowered amounts.

2. **`pkg/ast/cost.go`** — update `(*CostSpec).GetCurrency` to return `c.Currency` directly (drop the derive-from-PerUnit-or-Total switch). Add the synthesis logic to `GetPerUnit` and `GetTotal`. The `(*Posting).TotalCost` body needs no edit because it already uses the accessors.

3. **`pkg/ast/clone.go::(*CostSpec).Clone`** — switch from `c.PerUnit.Clone()` (Amount-method) to `CloneDecimal(c.PerUnit)` for both number pointers; copy `Currency` by struct assignment.

4. **`pkg/inventory/cost.go::ResolveCost`** — replaces `spec.PerUnit.Number` with `*spec.PerUnit`, `spec.PerUnit.Currency` with `spec.Currency`, etc. The currency-mismatch defensive check (`spec.PerUnit.Currency != spec.Total.Currency`, line 111) is **deleted**; structurally impossible after the refactor. Remove the dead `CodeInternalError` arm.

5. **`pkg/inventory/matcher.go::NewCostMatcher`** — same mechanical rewrite. The two switch arms (`spec.PerUnit != nil && spec.Total == nil` and `spec.Total != nil`) become field-direct reads.

6. **`pkg/inventory/booking.go::classify`** — the `perUnit, total *ast.Amount` locals (lines 147-159) are doing the work `GetCurrency` was designed to do. Replace with `hintCcy := p.Cost.GetCurrency()` etc. Also fix the doc comment on line 106 (`p.Cost.PerUnit.Currency or p.Cost.Total.Currency`) to read `p.Cost.GetCurrency()`.

7. **`pkg/compat/beancompat/serialize.go::costSpecPayload`** (lines 917-945) — drop the dual-source switch (lines 931-936); rewrite as direct field reads. Leave the existing TODO(beancompat) comment.

8. **`pkg/inventory/reducer.go`** at line 1077 — the `if spec, ok := p.Cost.(*ast.CostSpec); ok && spec != nil` block reads `spec.Date` and `spec.Label`, both unchanged. No edit needed.

9. **`pkg/ext/postproc/std/implicitprices/plugin.go::costPerUnit`** — already uses `c.GetPerUnit()` / `c.GetTotal()` accessors and reads `.Number` and `.Currency` off the *returned synthesized Amounts*. After the refactor those Amounts are freshly allocated; the code paths still work, but they each do an extra Decimal clone per call. **Recommendation: leave as-is**; the per-posting allocation is negligible.

10. **Test files** that read fields (`pkg/ast/cost_test.go` family, `pkg/ast/clone_test.go`, `pkg/ast/total_cost_test.go`, `pkg/ast/lower_integration_test.go`, `pkg/loader/booking/plugin_test.go:178`) — mechanical: `cs.PerUnit.Number.String()` becomes `cs.PerUnit.String()`; `cs.PerUnit.Currency` becomes `cs.Currency`. Tests like `cost_holder_test.go:85` that assert pointer identity on `GetPerUnit` against `tc.spec.PerUnit` must change to value comparison.

**Test-fixture migration strategy for `*CostSpec{...}` literals:**

Roughly fifteen test files construct `&ast.CostSpec{PerUnit: amt(...)}` style literals. **Recommended strategy: per-file mechanical rewrite.** Replace `PerUnit: &ast.Amount{Number: dec(...), Currency: "USD"}` with `PerUnit: decPtr(...), Currency: "USD"`. Implementers may opt for a `costSpecPerUnit(...)` helper if they find themselves repeating the literal more than ~5 times in one file (only `pkg/inventory/reducer_test.go` qualifies; it has ~40 such literals).

**Should `(*CostSpec).GetPerUnit` return nil when `Currency == ""` even if `PerUnit != nil`?**

The Contract says yes (return nil). Rationale: a bare `*Amount` with `Currency == ""` is not a valid amount — `lowerAmount` enforces non-empty currency at line 642. Returning a synthesized `*Amount{Currency: ""}` from the accessor would be the only place in the codebase that produces a currency-less Amount, contradicting the package's invariant.

**Suggested ordering for the commit (single PR, single commit per Step 1):**

1. Rewrite `directives.go` (struct shape).
2. Rewrite `cost.go` (accessor synthesis + `GetCurrency` simplification).
3. Rewrite `clone.go::(*CostSpec).Clone`.
4. Rewrite `lower.go::lowerCostSpec`.
5. Update `pkg/ast/*_test.go` so `pkg/ast` builds and tests pass in isolation.
6. Update each downstream package in topological order: `pkg/inventory/cost.go`, `matcher.go`, `booking.go`, `reducer.go` (only the dead doc comment), then `pkg/compat/beancompat/serialize.go`, then test fixtures across `pkg/inventory/`, `pkg/validation/`, `pkg/printer/`, `pkg/distribute/`, `pkg/ext/postproc/std/`, `pkg/loader/`.
7. `bazel run //:gazelle` then `bazel build //...` then `bazel test //...`.

#### Alternatives

**A1. Keep `*Amount` fields and add a parallel `Currency` field.** Rejected: two-source-of-truth problem; every read site has to remember which one is canonical; round-trip between forms requires invariant maintenance the type system cannot enforce. User's stated preference rules this out.

**A2. Make `Amount.Number` a `*apd.Decimal` instead of changing CostSpec.** Rejected: massive blast radius across every Amount user; gain is needed only in cost specs.

**A3. Drop the `Currency` field; derive it from `PerUnit`/`Total` only.** Rejected: cannot represent `{ JPY }` — both `PerUnit` and `Total` are nil so there is nowhere for the currency to live. Fatal for Step 2.

**A4. Pointer-identity contract on `GetPerUnit` synthesis (cache the synthesized Amount).** Rejected: adds mutable per-spec state; cache invalidation if `PerUnit`/`Currency` ever mutate; concurrency surprises. Allocation cost is negligible (cold path).

**A5. Return-non-nil-when-Currency-empty for `GetPerUnit` synthesis.** Rejected: would produce a `*Amount{Currency: ""}`, violating the codebase's "Amount has both Number and Currency" invariant.

#### Recommendation + rationale

Adopt the Contract as stated. A1's smaller blast radius is illusory — every read site already routes through accessors today, so the "fewer call-site edits" claim only applies to the half-dozen places (matcher, cost, booking, beancompat) that field-access directly. Those sites need rewriting anyway to support `{ JPY }`. A4's allocation optimization solves a non-problem; the simplicity of "no caching, no aliasing" outweighs the micro-optimization. A5 would break a codebase-wide invariant.

**One concrete pain point flagged for the implementer's awareness, not blocking:** The combined `{X # Y CUR}` form's lowerer today has a special-case where `lowerAmountOptionalCurrency` permits the per-unit side to omit its currency and inherit from the total side (`lower.go:1107-1116`). With the refactored layout, currency is a single field, so that inheritance becomes trivial. The existing parser also (incorrectly — see Step 2) lets the per-unit side carry its own currency, in which case the lowerer detects mismatch with the total currency. **For Step 1, preserve this lowerer-side check**: until Step 2 fixes the parser, the lowerer must still cope with per-unit-bearing-currency CST input — accept matching currencies, error on mismatch — to keep the existing parser tests passing during this behavior-neutral refactor. Step 2 makes the per-unit-with-currency case unreachable; the lowerer's mismatch check then becomes dead defensive code (which the implementer of Step 2 may either remove or leave as a guard).

### Step 2 — Parser, lower, and printer support for `{ CUR }`

#### Functional requirements

- `parseCostContents` (`pkg/syntax/parser.go:460`) accepts a single
  leading `CURRENCY` token, consuming it as the cost currency. The
  same logic applies inside `{{ ... }}` (total braces) — currency-only
  is a property of the contents, not of the brace flavor.
- **Reject per-unit currency in the combined form.** Today's parser
  uses `parseAmountOptionalCurrency` for the per-unit side of
  `{X # Y CUR}`, which silently accepts `{2.50 USD # 9.95 USD}` and
  `{2.50 USD # 9.95 EUR}`. Both are syntax errors per upstream
  beancount: the per-unit side is a bare expression, never a full
  amount. The parser must emit a diagnostic when a CURRENCY token
  follows the per-unit number before `#`. The existing positive test
  `TestParsePostingWithCombinedCostExplicitPerUnitCurrency`
  (`pkg/syntax/parser_test.go:1240`) is wrong and must be deleted /
  inverted to a negative test.
- `parseCostElement` (trailing comma-separated) is unchanged:
  currency-only is only valid as the first / sole element.
- A currency-only spec may be combined with a date and/or label
  (`{ JPY, 2024-01-01 }`, `{ JPY, 2024-01-01, "lot" }`); the parser
  must not regress these forms.
- `lowerCostSpec` (`pkg/ast/lower.go`) populates `CostSpec.Currency`
  from the `CURRENCY` token when no `Amount` node is present. When
  an `Amount` node is present, `Currency` is set from the amount's
  currency. With the parser fix above, the per-unit side of the
  combined form is guaranteed currency-less, so the Step 1 mismatch
  check becomes structurally unreachable — Step 2 may remove it or
  leave it as a defensive `unreachable` guard.
- The printer round-trips currency-only specs as `{CUR}` (or
  `{{CUR}}` when only `Total` was set) without injecting a numeric
  zero.

#### Modules

- `pkg/syntax/parser.go` — `parseCostContents` switch (CURRENCY-leading
  case + per-unit currency rejection in combined form).
- `pkg/syntax/parser_test.go` — new tests for `{ JPY }`, `{{ USD }}`,
  `{ JPY, 2024-01-01 }`, `{ JPY, 2024-01-01, "lot" }`; invert
  `TestParsePostingWithCombinedCostExplicitPerUnitCurrency`; ensure
  `{X CUR # Y CUR}` and `{X CUR # Y OTHER}` both error.
- `pkg/ast/lower.go` — currency-only path; consolidated `Currency`
  population; mismatch arm cleanup.
- `pkg/ast/lower_test.go` — assert resulting `CostSpec` field shape.
- `pkg/printer/...` — currency-only emission.
- `pkg/printer/..._test.go` — round-trip cases.

#### Verification

- New parser tests assert no errors and the expected `CostSpec`
  shape for `{ CUR }` family.
- Inverted test asserts that `{X CUR # Y CUR}` produces a syntax
  error containing a recognizable substring (e.g. "currency",
  "combined").
- Round-trip test: a source containing `{ JPY }` parses, lowers,
  prints, re-parses identically (same `CostSpec` shape).
- The reducer still rejects the user's full example at this step
  (Pass 2 ambiguity not yet fixed); a focused parser/lower test does
  not assert end-to-end success yet.

#### Quality requirements

- Parser error wording for malformed currency-only specs (e.g.
  `{ JPY, JPY }` — currency in trailing position) keeps the existing
  "expected amount, date, or label" message.
- Parser error wording for per-unit-with-currency in combined form
  states the rule plainly (e.g. "per-unit amount in combined cost
  form must not carry a currency").
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
