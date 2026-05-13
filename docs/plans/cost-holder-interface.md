# Plan: Booked Cost in the AST via `CostHolder` Interface

## Goal

Enable beancompat check-tier fixtures that depend on the booked `Cost`
representation (currently blocked by `transaction_with_cost`, which expects
`{"kind": "cost", "number": ..., "currency": ..., "date": ...}`). The
underlying issue is that after booking, `Posting.Cost` still holds a raw
`*ast.CostSpec` with `Date == nil`, so the serializer cannot emit the
`"cost"` kind nor the resolved acquisition date.

## Non-goals

- Reworking the inventory / lot-matching algorithm. The change is about how
  the AST represents the booking *result*, not how booking is computed.
- Changing the wire format. The serializer keeps emitting beancompat's
  documented shape; only the path that produces it is rationalized.
- Adding new posting metadata fields beyond what upstream beancount itself
  carries on `Cost`.

## Background

Upstream beancount (Python, v3) carries two distinct NamedTuples on
`Posting.cost`:

- `CostSpec` — written by the parser; any of `number_per` / `number_total`
  / `date` / `merge` may be missing.
- `Cost` — written by booking; concrete `number`, `currency`, `date`,
  `label`. Always fully resolved.

The conversion happens in two layered passes inside
`beancount/parser/booking_full.py`:

1. `book_reductions` (`booking_full.py:486-489`) fills the txn date into
   the `CostSpec` of *augmenting* postings whose `date` is nil, and for
   *reducing* postings replaces the cost wholesale with the matched lot's
   `Cost` (`booking_method.py:85, 128` — `posting._replace(cost=match.cost)`).
2. `convert_costspec_to_cost` (`booking_full.py:557-575`) finally rewrites
   each remaining `CostSpec` into a `Cost` of the new type.

The Python adapter in beancompat (`implementations/beancount/_parse_helper.py:100-138`)
discriminates the two by *runtime type* (`hasattr(cost, "number_per")`) and
emits `kind: cost_spec` vs `kind: cost` accordingly — no tier tag, no
serializer-side fallback.

Partial booking (upstream): on `book_reductions` errors the failing
currency group's postings are dropped from the entry but the entry itself
is still emitted; on `interpolate_group` errors the entry is emitted even
with an empty posting list. Both behaviors mean that within a single
emitted entry, some postings may be booked and others may remain
unresolved. This rules out an entry-level "tier" tag as the
discriminator — the discrimination must live on the posting (or its cost)
itself.

## Design

### Layering

Move `inventory.Cost` → `ast.Cost`. Rationale:

- Matches upstream layout (both `Cost` and `CostSpec` live in
  `beancount.core.position`).
- Lets `ast.Posting.Cost` reference a sealed union defined entirely
  within `pkg/ast` (no ast → inventory back-edge).
- `inventory` keeps using the type via re-import; the algorithmic code
  (matcher, inventory, reducer) is unchanged.

### Type: `ast.Cost` (booked counterpart of `CostSpec`)

`ast.Cost` carries both the canonical resolved per-unit number (for
inventory matching/equality) and the original per-unit / surcharge form
the user wrote (for print-fidelity round-trip). Surcharge form
`{X CUR, # CUR}` therefore survives booking:

```go
type Cost struct {
    Number   apd.Decimal  // canonical per-unit, always set (inventory match/eq)
    Currency string       // always set after booking
    Date     time.Time    // always set after booking
    Label    string

    // Original-form retention for printer round-trip.
    PerUnit  *Amount      // user's explicit per-unit, or canonical for lot-driven
    Total    *Amount      // user's explicit surcharge total; nil if not specified
}
```

`Number`/`Currency`/`Date`/`Label` define lot identity (Cost.Equal
ignores PerUnit/Total). The reducer is responsible for keeping
PerUnit/Total consistent with Number when it converts a CostSpec.

### Sealed union: `ast.CostHolder`

```go
// CostHolder is the sealed union of cost representations carried on a
// Posting. *CostSpec is the parse-tier form (any of PerUnit/Total/Date
// may be nil); *Cost is the booked, fully-resolved form.
type CostHolder interface {
    isCostHolder()  // sealed-union marker; only implementations in this package

    GetPerUnit() *Amount     // CostSpec: PerUnit field; Cost: PerUnit field
    GetTotal()   *Amount     // CostSpec: Total field; Cost: Total field
    GetCurrency() string     // CostSpec: derived from PerUnit/Total; Cost: Currency field
    GetDate()    *time.Time  // CostSpec: Date field; Cost: &Cost.Date (copy)
    GetLabel()   string      // shared field
    IsBooked()   bool        // false for *CostSpec, true for *Cost
}
```

Naming: `Get*` prefix is used because `*CostSpec` already has exported
fields named `PerUnit`/`Total`/`Date`/`Label`; a method with the same
name as a field is a Go compile error. The collision rules out the
idiomatic field-named getter, so the explicit `Get` prefix is accepted
as the smallest-impact alternative — direct field access on the
concrete struct is preserved (reducer mutators still write
`spec.PerUnit = ...`), and only the interface boundary uses the
prefixed form.

`isCostHolder()` is a private marker method so external packages cannot
extend the union. `*CostSpec` and `*Cost` are the only implementations.

`Posting.Cost` becomes `CostHolder` (interface, nil-able). The
field-vs-method collision does not arise on `Posting` itself because
`Posting.Cost` (the interface-typed field) does not get a getter
method.

### How callers stay booking-status-agnostic

Surcharge form preservation collapses the original "polymorphic
Total/Format/Hash methods on the interface" idea into plain getters:
because `*ast.Cost` carries `PerUnit` and `Total` as fields parallel to
`CostSpec`, every read site can dispatch on `(GetPerUnit, GetTotal,
IsBooked)` without a type assertion. The previously-proposed
`AppendFormat`/`HashInto` methods on the interface are dropped — format
stays in `pkg/printer` and dedup hashing stays in `pkg/distribute/dedup`,
each dispatching on the agnostic getters.

`(*Posting).TotalCost` (`pkg/ast/cost.go:33`) keeps its existing
signature `(*Amount, error)`. Its branches read `p.Cost.GetPerUnit()` /
`p.Cost.GetTotal()` via the interface and perform the `apd` arithmetic
locally; error reporting remains on `TotalCost`, not on the getters.

Sites that legitimately cannot be hidden behind the interface stay
typed on the concrete variant and document why:

- `validation/internal/tolerance.tolerance()`: tolerance is a parse-tier
  concept — it uses the precision of the user's literals to bound
  interpolation. After booking the concept does not apply. Takes
  `*CostSpec` explicitly.
- `inventory/reducer.go` mutators (`fillMissingCostFromReductions`,
  `fillDeferredCost`): these mutate `CostSpec.PerUnit` / `CostSpec.Total`
  by field assignment. They guard against the post-booking state with a
  type-assertion-with-ok (`spec, ok := p.Cost.(*ast.CostSpec); if !ok
  { continue }`) so a second reducer run over its own output (the
  fixed-point contract exercised by `TestReducerRun_OutputIsFixedPoint`
  in `pkg/inventory/integration_test.go:524`) is a no-op on
  already-booked postings.
- `pkg/printer` surcharge branch: when both `GetPerUnit()` and
  `GetTotal()` are non-nil, the renderer prints both Amounts; this
  works for `*CostSpec` and `*ast.Cost` symmetrically and stays
  agnostic.

### Reducer pipeline addition

After existing booking steps complete, add a terminal pass that converts
each posting's cost in line with upstream. The pass is idempotent: if a
posting's cost is already `*ast.Cost` (second-pass case) it is skipped.

Conversion rules (consistent with surcharge-form retention):

| Original syntax | ast.Cost fields produced |
|---|---|
| `{X CUR}` | `PerUnit={X,CUR}`, `Total=nil`, `Number=X` |
| `{{Y CUR}}` | `PerUnit=nil`, `Total={Y,CUR}`, `Number=Y/units` |
| `{X CUR, # CUR}` (surcharge) | `PerUnit={X,CUR}`, `Total={#,CUR}`, `Number=(X*units+#)/units` |
| `{}` (lot-driven, reducing) | `PerUnit={matched.Number,CUR}`, `Total=nil`, `Number=matched.Number` |

In all cases `Date = spec.Date` if set, else `entry.Date` for augmenting;
or `matched.Date` for reducing. `Label` is copied from the spec (or
matched lot for lot-driven).

Postings inside an entry that survived `book_reductions` errors (per
upstream semantics) but did not get fully booked retain their `*CostSpec`.
This is the partial-booking case and the serializer/printer renders it as
`kind: cost_spec`, matching upstream.

### Serializer unification

The check-tier and parse-tier cost helpers collapse into one:

```go
func serializeCost(h ast.CostHolder) any {
    switch c := h.(type) {
    case *ast.CostSpec: ...  // kind: "cost_spec"
    case *ast.Cost:     ...  // kind: "cost"
    }
}
```

The only place we keep the type switch is here: it is the boundary where
the variant determines the wire `kind` field. This matches upstream's
adapter exactly (`hasattr` discrimination).

`SerializeChecked` and `SerializeParsed` share this helper; the only
remaining tier-specific difference is which other AST fields are emitted
(unrelated to cost).

## Implementation slices

Each slice is independently reviewable and leaves the tree green.

### Slice 1 — introduce `ast.Cost` and `ast.CostHolder`

- Move `inventory.Cost` (type, methods `Equal` / `Clone`, `Lot` alias)
  into `pkg/ast/cost.go`. Extend `ast.Cost` with new `PerUnit *Amount` /
  `Total *Amount` fields for surcharge-form retention.
- Replace `pkg/inventory/cost.go`'s type declarations with type aliases:
  `type Cost = ast.Cost`, `type Lot = ast.Cost`. `inventory.ResolveCost`
  and `inventory.quoContext` stay in `pkg/inventory` and now return
  `*ast.Cost` via the alias (signature unchanged at call sites).
- Define `ast.CostHolder` interface (sealed marker + 6 getters as
  designed above). Implement on `*ast.CostSpec` and `*ast.Cost` with
  compile-time interface assertions
  (`var _ CostHolder = (*CostSpec)(nil)`, `var _ CostHolder = (*Cost)(nil)`).
- `Posting.Cost` stays `*CostSpec` in this slice (no caller migration
  yet). The interface is dormant; no behavior change expected.
- Update the comment in `pkg/ast/clone.go:33` that references
  `inventory.Cost.Clone` to point at the new location.
- Run gazelle, build, test. Existing tests including
  `TestReducerRun_OutputIsFixedPoint` must continue to pass unchanged.

### Slice 2 — switch `Posting.Cost` to `CostHolder` and migrate read sites

- Change `Posting.Cost *CostSpec` → `Posting.Cost CostHolder`.
- Migrate the eight read sites identified in the Phase-3 audit to the
  agnostic accessor methods (`GetPerUnit`, `GetTotal`, etc.).
- `(*Posting).TotalCost` rewrites its branches to read through the
  interface; its `(*Amount, error)` signature is preserved.
- `printer.formatCostSpec` and `dedup.sumHash` are rewritten to dispatch
  on getter values; format / hash logic stays in their own packages.
- Tolerance site stays typed on `*CostSpec` (justified above).
- Reducer mutators add the `, ok := p.Cost.(*ast.CostSpec)` guard so
  they no-op on already-booked postings, with a comment recording the
  idempotence invariant.

### Slice 3 — booking-time `CostSpec → Cost` conversion

- Add the terminal pass to the reducer that walks postings and replaces
  fully-resolved `*CostSpec` with `*ast.Cost`. Uses txn date as the
  fallback for augmenting postings.
- Reducing postings install the matched lot's `*ast.Cost` directly.
- Partial-booking postings keep their `*CostSpec`.

### Slice 4 — unify serializer cost helper and enable fixture

- Collapse the parse-tier and check-tier cost serializers into one helper
  that type-switches on `CostHolder`.
- Enable `transaction_with_cost` (and any sibling fixtures that the
  switch unblocks). Verify other check-tier fixtures still pass.

## Risks and rejected alternatives

- **A (writeback into `CostSpec.Date` only)**: smaller surface, but
  cannot unify serializers (no way to distinguish "user wrote date" from
  "booking filled date"); cannot represent the partial-booking mix; not
  upstream-aligned. Rejected per design discussion.
- **C (serializer-side fallback)**: misaligned with upstream and forces
  every future post-loader consumer to re-implement the fallback.
  Rejected per design discussion.
- **B-variant: separate `Posting.BookedCost` field**: avoids type
  surgery but introduces an invariant ("if BookedCost is set, ignore
  Cost") that has to be enforced by convention. Less type-safe than the
  sealed union. Held in reserve in case slice 2's call-site surgery
  proves intractable.

## Open items for slice-level design

- Whether `inventory.Cost` should remain as a type alias forever or be
  removed once all callers reference `ast.Cost`. Decide at end of
  Slice 2.
- Whether the printer's `formatCostSpec` should rename to e.g.
  `formatCostHolder` to reflect that it now accepts both variants.
  Cosmetic; decide during Slice 2 implementation.
- Whether to keep an `OriginalSpec() *CostSpec` back-reference on
  `ast.Cost` as a future extension to support fixture authoring /
  debugging tools. Currently deferred (YAGNI); the surcharge-form
  preservation via parallel `PerUnit`/`Total` fields removes the
  immediate need.

## Future work surfaced by Slice 4

- **Multi-lot reduction posting expansion.** *Landed.* The reducer's
  Pass 2 (`expandReductions`) replaces every multi-lot reducing
  posting with one child per matched lot, each carrying its own
  resolved `*ast.Cost`; single-lot reductions install the lot in
  place via `installReductionLot`. The synthesized `spec.Total`
  intermediate state that Slice 3 produced is gone — Pass 2 now runs
  between Pass 1 (explicit booking) and Pass 3 (residual / auto-
  posting solving), so Pass 3's `PostingWeight(bp.Source)` reads
  concrete `*ast.Cost` on every booked child without any multi-lot
  branching. Check-tier fixtures exercising multi-lot reductions can
  now serialize as a sequence of `kind:"cost"` envelopes matching
  upstream; enabling such fixtures in
  `pkg/compat/beancompat/serialize.go` is a separate follow-up slice.
- **Display-precision and other check-tier options.** `SerializeChecked`
  currently shares its body with `SerializeParsed`; the only tier-
  specific divergence so far is the cost-discriminator switch handled
  inside `serializeCostHolder`. Fixtures that assert on display
  precision will need a dedicated path once the AST gains an
  options-retention mechanism (tracked separately as "Plan A").
