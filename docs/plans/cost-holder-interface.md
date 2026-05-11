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

### Sealed union: `ast.CostHolder`

```go
// CostHolder is the sealed union of cost representations carried on a
// Posting. *CostSpec is the parse-tier form (some fields may be nil);
// *Cost is the booked, fully-resolved form.
type CostHolder interface {
    isCostHolder()
    // Booking-status-agnostic accessors.
    Currency() string        // "" if not yet known (CostSpec with no PerUnit/Total)
    Date() *time.Time        // nil iff CostSpec.Date is unset and not yet booked
    Label() string

    // Polymorphic operations — each variant implements its own behavior,
    // so callers do not need a type switch.
    Total(units *Amount) (*Amount, error)  // total cost given posting units
    AppendFormat(p *Printer)               // print-tier rendering
    HashInto(h hash.Hash)                  // dedup
}
```

`isCostHolder()` is a private marker method so external packages cannot
extend the union. `*CostSpec` and `*Cost` are the only implementations.

`Posting.Cost` becomes `CostHolder` (interface, nil-able).

### Why polymorphic methods, not type assertions

The audit identified eight read sites and three "behavior differs per
variant" sites (`printer.formatCostSpec`, `dedup.sumHash`, `cost.TotalCost`).
The user constraint is to make callers booking-status-agnostic. Each of
those three sites is naturally a method on the value: the variant knows
how to format / hash / weight itself. Pushing them onto `CostHolder`
eliminates the type-switch.

Sites that legitimately cannot be hidden behind the interface stay
typed on the concrete variant and document why:

- `validation/internal/tolerance.tolerance()`: tolerance is a parse-tier
  concept — it uses the precision of the user's literals to bound
  interpolation. After booking the concept does not apply. Takes
  `*CostSpec` explicitly.
- `inventory/reducer.go` mutators (`fillMissingCostFromReductions`,
  `fillDeferredCost`): these run *before* the `CostSpec → Cost`
  conversion, so the invariant they assume — that `Posting.Cost` is a
  `*CostSpec` — is upheld by ordering, not by the type system. They keep
  type-asserting `Posting.Cost.(*CostSpec)` and document the invariant.

### Reducer pipeline addition

After existing booking steps complete, add a terminal pass that converts
each posting's cost in line with upstream:

- *augmenting posting with `*CostSpec`*: build `*ast.Cost` using the
  spec's resolved per-unit number/currency, `spec.Date` if non-nil
  otherwise `entry.Date`, and `spec.Label`.
- *reducing posting*: the matched lot's `*ast.Cost` is already what we
  want; install it on the posting.
- *posting with no cost*: leave `Posting.Cost = nil`.

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

- Move `inventory.Cost` (type, methods, `Lot` alias) → `ast.Cost`.
  Keep `inventory.Cost = ast.Cost` as a type alias for source compatibility
  during transition.
- Define `ast.CostHolder` interface with `isCostHolder()` + the agnostic
  accessor set. Don't add polymorphic `Total/AppendFormat/HashInto` yet —
  introduce them in slices 2/3/4 as their call sites migrate, to keep
  each slice's surface small.
- Implement `isCostHolder()` and the accessor methods on `*ast.CostSpec`
  and `*ast.Cost`.
- `Posting.Cost` stays `*CostSpec` in this slice (no caller migration
  yet). The interface is dormant.
- Run gazelle, build, test. No behavior change expected.

### Slice 2 — switch `Posting.Cost` to `CostHolder` and migrate read sites

- Change `Posting.Cost *CostSpec` → `Posting.Cost CostHolder`.
- Migrate the eight read sites to the agnostic accessor methods.
- For sites whose behavior differs per variant (`TotalCost`,
  `formatCostSpec`, `sumHash`), add the corresponding polymorphic
  method (`Total`, `AppendFormat`, `HashInto`) to the interface and
  implement on both variants. Callers stop type-switching.
- Tolerance site stays typed on `*CostSpec` (justified above).
- Reducer mutators stay typed on `*CostSpec` via assertion, plus a
  comment recording the ordering invariant.

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

- Exact shape of `Total(units *Amount) (*Amount, error)` for `*CostSpec`
  when `PerUnit` and `Total` are both nil (parser literal `{}`): return
  `nil, nil`? An error? Today `TotalCost` lives at `pkg/ast/cost.go:37` —
  revisit its contract in slice 2.
- Whether `inventory.Cost` should remain as a type alias forever or be
  removed once all callers reference `ast.Cost`. Decide at end of slice 1.
- Whether the printer's existing `formatCostSpec` signature
  (`pkg/printer/printer.go:350`, takes `ast.CostSpec` by value) can move
  to a pointer receiver method on each variant without churning fixtures.
