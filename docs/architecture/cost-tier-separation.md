# Cost tier separation: `inventory.Lot` vs `ast.Cost`

## Motivation

A partial-reduction bug motivated the design. A ledger with `{{ T CCY }}`
augmenting postings and `-N CCY {}` reducing postings would fail balance
checking: the reducing posting was inheriting the augmenting lot's `Total`
presentation provenance, and `(*ast.Posting).TotalCost`'s
`sign(units) × |Total|` rule was firing on it. For an augmenting posting that
rule is correct and division-free; for a reducing posting it overstates the
weight (e.g. `-100 JPY` instead of the correct `-50 JPY`). With two lots in
different currencies the residuals spanned two currencies and
`Income:Gain`'s single-unknown auto-posting could not resolve, producing
`unresolvable-interpolation` and `unbalanced-transaction` errors.

Upstream Beancount's `Cost` type carries only `(number, currency, date, label)`
and computes weight as `units × number` unconditionally, so it never has this
issue.

## The separation

`pkg/inventory` holds `inventory.Lot` — provenance-free: `Number`, `Currency`,
`Date`, `Label` only. `pkg/ast` holds `*ast.Cost` — the booked posting form
that optionally carries `PerUnit` and `Total` presentation provenance for
round-tripping `{{ T CCY }}` and `{X # T CCY}` source forms.

The boundary is crossed by exactly three named helpers:

- `inventory.CostToLot(*ast.Cost) *inventory.Lot` — strips provenance.
- `(*inventory.Lot).ToCost() *ast.Cost` — materializes provenance absence
  (PerUnit and Total are always nil in the result).
- `costSpecToBookedCost(*ast.CostSpec, *inventory.Lot) *ast.Cost` — builds the
  AST-tier Cost from a parse-tier spec, copying PerUnit/Total from the spec and
  Number/Date from the resolved Lot.

Any new code path that converts between the tiers is forced through one of these
named sites.

## Install-path discipline

Reducing-side installers — `addSingleLotReduction`, `addMultiLotReduction`,
`promoteSingleLotReduction` in `pkg/inventory/reducer.go` — install cost via
`step.Lot.ToCost()`, which guarantees `PerUnit == nil && Total == nil`.
`PostingWeight` therefore reaches the `units × Number` fallback for every
reducing posting.

Augmenting-side installers — `addLotAugmentation`, `promoteLotAugmentation`,
`addAlreadyBooked` — install a `*ast.Cost` built by `costSpecToBookedCost` (or,
for the already-booked path, the input pointer forwarded unchanged), so the
round-trip form survives.

## Why both weight rules must coexist

Augmenting `{{ T CCY }}` weight is `sign(units) × |Total|` — divide-free,
preserving bit-exact balance for ledgers like the one pinned by
`TestLoad_TotalCostAugmentationBalances`. Reducing weight is `units × Number`,
using the canonical per-unit value post-booking regardless of the surface form
the augmenting lot was originally written in. The two rules are correct at their
respective sites; the bug was applying the augmenting rule at a reducing site.

## Sealed-union membership

`inventory.Lot` deliberately does **not** implement `ast.CostHolder`. The
`CostHolder` interface (sealed via `isCostHolder()`) is the AST-tier vocabulary
for parse-form and booked-form costs. Inventory values are outside that
vocabulary. Adding a `CostHolder` implementation on `Lot` would re-merge the
tiers at the interface level and defeat the type-identity enforcement.

## Rejected alternative

**Keep a single AST-tier Cost type; discipline reducer-side installs to clear
PerUnit/Total.** The fix would work but is enforced only by convention: a missed
install site re-introduces the bug silently. Type identity makes the rule
physical — inventory-tier code cannot construct or read provenance fields because
the fields are not on the type.
