# Per-currency display precision

## Concept

Beancount source carries numeric amounts at the precision the author wrote
them — `100.00 USD`, `160000 JPY`, `0.0001 BTC`, and so on. For consistent
display (whether the ledger is rendered back to text, or its numeric shape is
reported to an external tool) it is useful to derive, per currency, the
precision a reader would expect to see.

The derivation is statistical: observe every amount in the input, count how
often each fractional-digit width appears per currency, and choose the most
common width as that currency's display precision. The result is an inferred
mapping `{currency → integer precision}` that drives both formatter output
and external reporting — notably the `display_precision_by_currency` view
asserted by the upstream beancompat fixture suite.

## Upstream beancount reference

This section is a snapshot of upstream's design, recorded here so future work
on related features (option overrides, expanded option serialization, cross-
implementation conformance) does not have to re-investigate from scratch. The
rest of the document describes go-beancount; this section describes only
upstream Python beancount.

### Where it lives

- `beancount/core/display_context.py` — the `DisplayContext`,
  `_CurrencyContext`, `_FixedPrecisionContext`, `DisplayFormatter`,
  `Precision`, and `Align` types.
- `beancount/parser/grammar.py` — the `Builder` class constructs a
  `DisplayContext` while parsing and updates it from every `Amount` it
  observes.
- `beancount/parser/options.py` — defines the `display_precision` option.
- `beancount/parser/printer.py` — the printer's `EntryPrinter` consumes the
  dcontext through a `DisplayFormatter` built at construction time.
- `beancount/loader.py` — aggregates dcontext data across included files.
- `beancount/scripts/doctor.py` — `bean-doctor display_context` prints the
  inferred dcontext for diagnostics.

### How it is populated

1. **Statistical observation.** While parsing, `Builder` calls
   `dcontext.update(decimal, currency)` for every `Amount` it constructs.
   Each `_CurrencyContext` keeps a frequency distribution
   `fractional_dist: dict[int, int]` mapping fractional-digit counts to
   occurrence counts.
2. **Option override.** After parsing, `finalize()` applies the
   `display_precision` option (a `string → Decimal` map). For each
   `(currency, example)` pair it replaces the `_CurrencyContext` with a
   `_FixedPrecisionContext` keyed off the fractional-digit count of the
   example value.
3. **Comma configuration.** `finalize()` threads the `render_commas` option
   into the dcontext as well.
4. **Cross-file aggregation.** `loader.py` merges the dcontext of every
   included file into the primary context.

Queries use the `Precision` enum:

- `Precision.MOST_COMMON` returns the modal fractional-digit width.
  Upstream's tie-break is insertion order, which is brittle; go-beancount
  tightens this — see [Tie-break](#tie-break).
- `Precision.MAXIMUM` returns the largest observed width.

### Known upstream issues

- **[#678][issue-678] — parser-time include resolution.** Because dcontext
  is populated by the per-file parser, amounts inside included files can be
  missed before include resolution merges directives. The aggregation step
  in `loader.py` partially compensates but the behaviour is fragile.
- **[#716][issue-716] — price-driven precision blowup.** Price directives
  can carry many more fractional digits than ordinary transactions, which
  can dominate the statistics if priced amounts share a currency with
  transacted amounts.

[issue-678]: https://github.com/beancount/beancount/issues/678
[issue-716]: https://github.com/beancount/beancount/issues/716

### The naming trap

Two upstream names look alike and are easy to confuse:

| Name | Kind | Type | Settable by user? |
|---|---|---|---|
| `display_precision` | **option** | `string → Decimal` (example value per currency, e.g. `"USD:0.01"`) | Yes, via `option "display_precision" "USD:0.01"` |
| `display_precision_by_currency` | **derived view** | `string → int` (integer fractional-digit count per currency) | No, computed from the dcontext |

The derived view is produced by walking `DisplayContext.build().fmtstrings`
and projecting each format string down to its fractional-digit count. The
name comes from `fava`, which exposes this dict to its templates — there is
no upstream **option** by this name.

The rest of this document keeps the distinction visible by saying "the
option" or "the derived view" wherever ambiguity is possible.

## go-beancount architecture

### Responsibility split

```
internal/formatopt.DisplayContext   — interface (formatter consumer contract)
                ▲ implements
pkg/ast.PrecisionProfile             — struct (observed-amount statistics)
                ▲ wraps (future)
some.OverrideContext (not yet)       — DisplayContext implementation that
                                       decorates a PrecisionProfile with
                                       per-currency overrides supplied by
                                       option "display_precision" or another
                                       directive.
```

The split exists so a future override implementation can be added without
rewiring the formatter or the beancompat serializer. Consumers depend on the
`DisplayContext` interface; the bookkeeping-side concrete type can be
replaced or wrapped freely.

`pkg/ast.PrecisionProfile` carries no override or fixed-precision semantics.
Its job is only to count fractional-digit widths per currency and answer the
most-common query.

### Population path

`PrecisionProfile` is populated by an AST post-pass in `loader.finish()`,
after option parsing. The pass walks `ledger.All()` and feeds amounts to
`(*PrecisionProfile).Update`.

This deliberately differs from upstream's parser-integrated callback. The
post-pass is the canonical place because:

- By the time `loader.finish()` runs, every included file is merged into the
  directive stream. Walking `ledger.All()` naturally observes each amount
  once, regardless of file boundaries — sidestepping upstream issue #678 by
  construction.
- The parser and the lowerer stay free of statistics concerns.
- The cost is a single linear walk at load time, dominated by parse cost at
  every realistic ledger size.

The field is populated once, at load. It is **not** refreshed by `Insert`,
`InsertAll`, or `ReplaceAll`. This mirrors `Ledger.Options`. Hand-built
`&ast.Ledger{}` literals have a nil `PrecisionProfile`; every read method on
`*PrecisionProfile` is nil-safe.

### Observation set

The post-pass observes amounts from exactly three positions:

| Position | Source | Observed? |
|---|---|---|
| `Transaction.Postings[i].Amount` | posting amount | Yes |
| `Balance.Amount` | balance amount | Yes |
| `Price.Amount` | price directive amount | Yes |
| `Posting.Price` annotation (`@` / `@@`) | per-posting price | **No** |
| Cost-spec amounts (`CostSpec.PerUnit`, `CostSpec.Total`, `Cost.Number`) | cost basis | **No** |
| Metadata-value NUMBER tokens | arbitrary metadata | **No** |

The included set matches upstream beancount's documented dcontext observation
set. The excluded set is excluded deliberately:

- **Cost amounts** sit on a separate scale. A cost basis quoted in USD
  against a non-USD unit is not a per-currency display statistic for USD;
  mixing it in would skew the modal distribution.
- **Posting price annotations** can carry very high precision for narrow
  technical reasons (e.g. `0.0001 BTC` rates) that would otherwise dominate
  display precision for a currency that ordinary transactions use at low
  precision.
- **Metadata** values are not currency-denominated.

The governing rule for any future fixture that wants to widen the set:
**match upstream's observation set exactly**, rather than do something that
seems independently defensible. Cross-implementation parity against
beancompat is the whole point.

### Tie-break

`(*PrecisionProfile).MostCommon` ties on equal frequency by returning the
**higher** precision. This deliberately tightens upstream's insertion-order
tie-break: the higher-precision form is the one that does not silently
truncate user-authored decimals when chosen.

## Formatter integration

### Consumer contract

`internal/formatopt.DisplayContext` is a single-method interface:

```go
type DisplayContext interface {
    MostCommon(currency string) (int, bool)
}
```

`*ast.PrecisionProfile` satisfies it structurally. The interface lives next
to `formatopt.Options` (which carries the field that points to it) so no
package crosses an import boundary to satisfy the type.

`pkg/format.WithDisplayContext` is the only public option. Passing `nil` is
a no-op equivalent to never calling the option.

### Pipeline placement

When a `DisplayContext` is set, the formatter rewrites the relevant NUMBER
tokens during `formatDirective`, immediately before `formatCommaGrouping`.
The ordering matters: comma insertion and amount alignment both read the
rewritten `tok.Raw`, so quantize-first is sufficient — alignment requires no
awareness of the quantization pass.

When no `DisplayContext` is set, the pass is a single nil check at the top
of `applyDisplayContext`. Every existing format consumer sees byte-identical
output.

### Quantization semantics

For each NUMBER token in an eligible CST position whose adjacent CURRENCY
token names a currency in the configured `DisplayContext`:

- Pad with trailing zeros when the source has fewer fractional digits.
- Round half-even (banker's rounding) when the source has more. Negative
  numbers preserve their sign; the magnitude is quantized.
- Integer results at zero fractional digits never carry a trailing `.`.
- The output never uses scientific notation.

Half-even matches upstream beancount (which inherits it from Python
`Decimal.quantize`). The arithmetic context (`apd.BaseContext.WithPrecision(34)`
with `apd.RoundHalfEven`) matches the precision the loader uses for its own
arithmetic, so the formatter and the loader cannot disagree about how the
same number rounds.

When the currency is not in the `DisplayContext`, or when the lookup returns
`ok=false`, the token passes through unchanged.

### Eligible CST positions (symmetry invariant)

The set of NUMBER tokens the formatter quantizes mirrors the observation set
above. Concretely:

| CST node containing the NUMBER | Quantize? |
|---|:---:|
| `AmountNode` (posting / price directive / custom value amount) | Yes |
| `BalanceAmountNode` (main + tolerance) | Yes |
| `PriceAnnotNode` (`@` / `@@`) | No |
| `CostSpecNode` amount children | No |
| `MetadataLineNode` value NUMBER | No |
| Bare NUMBER inside `ErrorNode` / `UnrecognizedLineNode` | No |

The invariant: **quantize only positions whose currency precision the
profile observed**. Quantizing a position that was not observed would impose
a precision derived elsewhere onto a number the statistics never accounted
for — silently truncating, for example, a `0.0001 BTC` price annotation to
`0.00`. Symmetry is what keeps the formatter and the observation pipeline
honest.

## Beancompat serializer

`pkg/compat/beancompat` emits `display_precision_by_currency` as a derived
view inside the `options` envelope of its `Result`. The data flows from
`Ledger.PrecisionProfile` directly; there is no option directive involved.

Three concrete rules:

- Currencies are listed alphabetically. Precisions are bare JSON integers
  (`{"USD": 2, "JPY": 0}`), never strings or decimals — that is what the
  beancompat fixture matcher expects.
- When the profile is nil or has no observations, the entire `options`
  envelope is omitted from the JSON (`omitempty` on `Result.Options`). The
  matcher uses containment semantics, so omitting the envelope is preferable
  to emitting an empty `display_precision_by_currency: {}` — the latter
  would suggest the implementation observed something and found zero, when
  in fact it observed nothing.
- The same emission path runs from both `SerializeParsed` and
  `SerializeChecked`; the check-tier ledger carries the same profile
  populated at load time.

## Future extension points

### Option-driven override (`option "display_precision"`)

Adding support for `option "display_precision" "USD:0.01"` does not require
any consumer-side changes. The new behaviour fits as a separate
`DisplayContext` implementation that decorates a `PrecisionProfile`:

- Parse the option value into `map[string]int` (fractional-digit count
  derived from the example Decimal).
- Wrap the underlying `PrecisionProfile` with a struct whose `MostCommon`
  returns the override when the currency is in the map and delegates to
  the wrapped profile otherwise.
- Plumb the wrapped value into `formatopt.Options.DisplayContext` (already
  an interface field) and through the beancompat serializer.

The bookkeeping/override split documented above is what makes this addition
local: the formatter and the serializer stay untouched.

### `options_coverage.json` and wider option serialization

A larger task than this document covers. It requires:

- Adding map-typed entries to the `OptionValues` registry — `string →
  Decimal` for `inferred_tolerance_default`, `string → Decimal` for the
  `display_precision` *option*, integer scalars for `long_string_maxlines`,
  and so on.
- Wiring approximately 30 BeancountOptions keys through the beancompat
  serializer.
- Distinguishing **the option** (settable via `option` directives) from
  **the derived view** (computed from the ledger) at every emit point.

The [naming-trap](#the-naming-trap) section above is the single most
important piece of context for that work.
