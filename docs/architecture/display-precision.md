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

`(*PrecisionProfile).Precision` ties on equal frequency by returning the
**higher** precision. This deliberately tightens upstream's insertion-order
tie-break: the higher-precision form is the one that does not silently
truncate user-authored decimals when chosen.

## Formatter integration

### Consumer contract

`internal/formatopt.DisplayContext` is a single-method interface:

```go
type DisplayContext interface {
    Precision(currency string) (int, bool)
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

## Printer integration

`pkg/printer` is the AST-side renderer (sibling to `pkg/format`, which walks
the CST). It accepts the same `format.Option` set, including
`WithDisplayContext`, and applies the same symmetry invariant — but at AST
positions rather than CST node kinds:

| AST position | Quantize? |
|---|:---:|
| `Transaction.Postings[i].Amount` (posting amount) | Yes |
| `Balance.Amount` and `Balance.Tolerance` (sharing the balance amount's currency) | Yes |
| `Price.Amount` (price directive) | Yes |
| Top-level `ast.Amount` passed to `Fprint` | Yes |
| `Posting.Price.Amount` (posting price annotation) | No |
| `CostSpec.PerUnit` / `Total` (cost basis) | No |
| `MetaValue.Amount` and `MetaValue.Number` (metadata) | No |

Implementation note: the printer keeps the plain `formatAmount` /
`formatDecimal` helpers for the unquantized positions and routes the
quantize-eligible positions through `formatDisplayAmount` /
`formatDisplayNumber`. The split is structural rather than parametric so
each call site declares which path it wants without a boolean argument.

The same `format.Quantize` helper backs both renderers, so they cannot
disagree about half-even rounding for the same input.

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

## Implemented features

### Option-driven override (`option "display_precision"`) — IMPLEMENTED

`option "display_precision" "USD:0.01"` is fully wired. The implementation:

- Registers `display_precision` as `KindIntMap`; parser `parseDisplayPrecisionEntry`
  converts an example decimal to a fractional-digit count (`"0.01"` → 2).
- `DisplayPrecisionContext` in `pkg/ast/precision_profile.go` wraps a
  `*PrecisionProfile` and overrides `Precision(currency)` from the map. The
  struct structurally satisfies `formatopt.DisplayContext`; callers (including
  tests) construct it inline from `ledger.PrecisionProfile` and
  `ledger.Options.IntMap("display_precision")` and pass it to
  `format.WithDisplayContext`.
- The beancompat serializer emits `display_precision` from the generic `KindIntMap`
  path; `display_precision_by_currency` remains a separately-derived view from
  `PrecisionProfile`. The option-vs-derived-view split is preserved at every emit point.

A `pkg/ast`-side helper that wraps the inline construction was deliberately
not added: no production caller exists today to dictate the right shape,
and a helper layer can be introduced when the first real consumer (e.g. a
ledger-aware CLI) lands.

### `options_coverage` fixture — IMPLEMENTED

The upstream `parse/options_coverage` beancompat fixture passes (both Go and Python
denylists are empty). All ~30 `BeancountOptions` keys are registered in the
default registry and emitted by `serializeOptions` via `Snapshot()`.

### Comma rendering (`option "render_commas"`) — IMPLEMENTED (storage only)

`render_commas` is registered as `KindBool` (default `false`). Callers read
it via `ledger.Options.Bool("render_commas")` and pass the result to
`format.WithCommaGrouping`. No production caller exists yet; the canonical
wiring is exercised in tests.

### `tolerance_multiplier` / `inferred_tolerance_multiplier` alias — IMPLEMENTED

`tolerance_multiplier` is the canonical key; `inferred_tolerance_multiplier`
is registered as its deprecated alias with `aliasOf: "tolerance_multiplier"`.
Writes via either name reach the canonical slot through a one-directional
write redirect in `OptionValues.set`; the deprecated slot is never written to
after init, so it always reports its registered default in `Snapshot()`.
This mirrors upstream beancount's `grammar.py:393-396`. Validation consumers
(`pkg/validation/internal/tolerance`) read the canonical key.

## Known divergences / future work

The following options are registered with upstream defaults for cross-tool
emission parity (so `Snapshot()` and the beancompat serializer produce the
expected keys), but their consumers do not yet exist in Go.

| Option | Upstream consumer | Deferral reason |
|---|---|---|
| `name_assets`, `name_liabilities`, `name_equity`, `name_income`, `name_expenses` | `get_account_types()` — account-type classification | Account-type classification subsystem not present. |
| `account_previous_balances`, `account_previous_earnings`, `account_previous_conversions`, `account_current_earnings`, `account_current_conversions` | `get_previous_accounts()` / `get_current_accounts()` | No derived-account computation subsystem. |
| `account_unrealized_gains` | `get_unrealized_account()` — unrealized-gains plugin | Unrealized-gains plugin not present. |
| `account_rounding` | Rounding-error plugin | Plugin not present. Note: upstream default is Python `None`; Go uses `""`. The `options_coverage` fixture sets this option explicitly, so the default divergence is not exercised by containment matching. |
| `conversion_currency` | Zero-rate conversion / currency conversion logic | No conversion logic in Go. |
| `commodities` | Auto-populated by the parser (output key) | Parse-time commodity enumeration deferred; emits `[]`. |
| `plugin` (option form) | Plugin runner (v2 captured form) | v3 directive form is the supported path; option form emits `[]`. |
| `documents` | Document-discovery search paths | Document discovery not present. |
| `pythonpath`, `insert_pythonpath` | Python sys.path manipulation | Python-specific; no Go analog. |
| `allow_pipe_separator`, `allow_deprecated_none_for_tags_and_links` | Deprecated parser flags | Go parser does not implement these; stored as inert booleans. |
| `long_string_maxlines` | Parser warning threshold | No warning channel for this in Go yet. |

### Deliberate behavioral divergences

- **`tolerance_multiplier` / `inferred_tolerance_default` vs upstream `get_balance_tolerance`.**
  Upstream consults `inferred_tolerance_default` for balance assertions whose asserted
  amount has zero fractional digits, even when posting-level inference is computable.
  Go applies a simpler precedence chain (posting-level > per-currency default) uniformly
  across all three computation functions (`Infer`, `ForAmount`, `ForBalanceAssertion`).
  The divergence is documented in `pkg/validation/internal/tolerance/doc.go`. It is
  reopenable if a real consumer encounters the integer-assertion nuance.

- **No deprecation diagnostic on `inferred_tolerance_multiplier` writes.**
  Upstream beancount's `grammar.py:387-391` emits a `DeprecatedError` warning
  when the deprecated key is used. Go silently accepts and redirects the write
  (the alias mechanism itself matches upstream). A warning-severity diagnostic
  channel for option parsing is the prerequisite; once it exists, the deprecation
  warning is a small addition.

### Option-to-format wiring

Callers wire option values into formatter/printer options inline at the
call site:

- `format.WithDisplayContext(&ast.DisplayPrecisionContext{Profile: ledger.PrecisionProfile, Overrides: ledger.Options.IntMap("display_precision")})`
- `format.WithCommaGrouping(ledger.Options.Bool("render_commas"))`

No `pkg/ast`-side helper wraps these patterns today. A helper layer can be
introduced when a production caller (e.g. a ledger-aware CLI) defines the
right shape.
