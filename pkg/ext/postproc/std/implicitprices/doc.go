// Package implicitprices is the Go port of upstream beancount's
// implicit_prices plugin — it synthesizes [ast.Price] directives from
// the cost annotations and explicit price annotations carried on
// transaction postings, so that the price database is automatically
// populated as transactions occur.
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/implicit_prices.py
//
// Upstream copyright: Copyright (C) 2014-2017, 2020, 2024-2025 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// The plugin walks every directive once, copying it through to the
// output. For every [ast.Transaction] it inspects each posting and, when
// the posting carries a price or cost annotation, emits one
// [ast.Price] directive on the transaction's date describing the
// observed conversion rate. The generated Prices follow upstream's
// preference order: an explicit per-/total-price annotation
// ([ast.Posting.Price]) takes precedence; otherwise a cost annotation
// ([ast.Posting.Cost]) is used. Postings without either annotation, or
// without a units [ast.Amount], emit nothing.
//
// Cost-derived prices use the cost's per-unit number when present and,
// for total-cost form ({{T CUR}}), the total divided by |units|. The
// division uses the package-wide [apd.BaseContext] with a precision of
// 34 digits so the result fits IEEE-754 decimal128 — the same precision
// chosen by [pkg/inventory] for its cost resolution. Per-unit-priced
// annotations (`@ X CUR`) are copied verbatim; total-priced
// annotations (`@@ X CUR`) are divided by |units| in the same way.
// Postings whose units number is zero are skipped silently to avoid
// division by zero, matching the spirit of upstream's `add_position`
// guard which never produces a price entry for an empty position.
//
// The plugin is synthesizing: it returns a Result with Directives set
// to a fresh slice containing all original directives followed by the
// synthesized Prices. Input directives are never mutated.
//
// # Usage
//
// The plugin takes no Config string; activation alone is enough. Either
// registered name works — the upstream Python module path for ledger
// portability, or the Go import path:
//
//	plugin "beancount.plugins.implicit_prices"
//
// or
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/std/implicitprices"
//
// A buy with a per-unit cost annotation produces a synthesized Price.
// Given the ledger
//
//	plugin "beancount.plugins.implicit_prices"
//
//	2024-01-02 * "Buy"
//	  Assets:Inv          10 AAPL {100.00 USD}
//	  Assets:Cash    -1000.00 USD
//
// the plugin appends one Price directive on the same date describing
// the observed rate, so the effective directive stream becomes:
//
//	2024-01-02 * "Buy"
//	  Assets:Inv          10 AAPL {100.00 USD}
//	  Assets:Cash    -1000.00 USD
//	2024-01-02 price AAPL  100.00 USD
//
// Explicit `@ price` and `@@ total-price` annotations on a posting are
// honoured the same way (they take precedence over a cost annotation
// when both appear).
//
// # Output ordering
//
// Synthesized Prices are appended to the output slice immediately after
// the transaction that produced them, in posting order. This keeps the
// per-transaction provenance readable in the un-canonicalized slice and
// matches upstream's source-order behavior. The runner re-sorts the
// returned slice through [ast.Ledger.ReplaceAll] when it commits, which
// applies the canonical (date, kind, span, sequence) ordering — Price
// directives end up after every other kind on the same day, matching
// upstream's `data.entry_sortkey`.
//
// # Deviation: deduplication of synthesized Prices
//
// Upstream maintains a `(date, base, number, quote)` map and skips
// emission when the same key is already present in this Apply call,
// citing legitimate duplicates from stock splits and split-day reported
// prices. Upstream's comments call this de-duplication tentative; this
// port preserves the same behavior so byte-compatible reports are
// possible. Deduplication keys the price number by its literal textual
// form, so `1` and `1.00` are treated as distinct entries — matching
// upstream's `dict[Decimal]` behavior, where two `Decimal` instances
// with the same value but different exponents are not equal as dict
// keys.
//
// # Provenance
//
// Each synthesized Price carries the originating transaction's Span,
// so downstream consumers can locate the source posting in the absence
// of `__implicit_prices__` metadata.
//
// # Deviation: provenance metadata
//
// Upstream attaches a `__implicit_prices__` posting-meta key tagged
// either "from_price" or "from_cost" so downstream tooling can
// distinguish synthesized prices from user-authored ones. This port
// does not currently emit that metadata; the synthesized Price's
// metadata map is empty. Downstream tooling that needs the provenance
// signal should round-trip the `__implicit_prices__` key through
// upstream rather than relying on this port. A TODO is opened to add
// the key once go-beancount has a stable convention for synthesized
// metadata.
//
// # Diagnostic codes
//
// The plugin returns no errors in the normal case. If an arithmetic
// operation underlying a cost or price conversion fails (which would
// indicate an internal inconsistency in apd or in posting data), an
// [ast.Diagnostic] with code "implicit-price-error" is emitted, anchored at
// the offending transaction's span.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.implicit_prices" — upstream Python module path
//     (with the underscore), so existing ledgers activate the port
//     without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/implicitprices" —
//     Go import path (no underscore, since Go package identifiers
//     cannot contain underscores), matching Phase 6a's convention for
//     Go-native plugins.
package implicitprices
