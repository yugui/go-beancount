# Phase 7 Planning Discussion: Quote Library (`pkg/quote`)

This document records the open design questions raised while planning Phase 7
of go-beancount. PLAN.md describes the intended deliverables at a high level;
this document captures the points that still need to be settled before
implementation begins, the trade-offs identified for each, and any tentative
leanings.

The plan recorded here is a living draft. Decisions are folded back into
PLAN.md once they are settled.

## Scope recap (from PLAN.md)

`pkg/quote` provides a pluggable price-fetching layer on top of Phase 6's
plugin framework. Headline deliverables:

- A `Quoter` interface for fetching prices.
- A `Price` value type carrying commodity pair, value, source, timestamp.
- At least one built-in quoter against a public market data API.
- Plugin-backed and external-process-backed quoters that delegate to Go
  plugins / subprocesses implementing `Quoter`.
- A fan-out / fallback quoter that tries sources in order.
- `pkg/quote/pricedb`, which materializes `Price` values as `price`
  directives and merges them into an `ast.Ledger` with deduplication.

## Open questions

### 1. `Quote` signature: single commodity vs. base/quote pair

PLAN.md currently shows:

```go
Quote(ctx context.Context, commodity string, date time.Time) (Price, error)
```

Beancount prices are inherently pairs: `HOOL` priced in `USD`, `USD` priced
in `JPY`. A single-commodity API forces the implementation to pick a quote
currency from somewhere — typically the ledger's `operating_currency` — which
ties the abstraction to ledger context it doesn't otherwise need and breaks
when more than one operating currency is in use.

**Options.**

- **(a)** Keep the single-commodity signature; document that the quote
  currency is selected by the caller via configuration.
- **(b)** Take an explicit pair: `Quote(ctx, base, quote string, date time.Time)`.
- **(c)** Take a structured `Symbol` value (`type Symbol struct { Base, Quote string }`)
  to leave room for non-currency-pair instruments later (e.g. options, indices).

**Tentative lean:** (b). Honest about what a price actually is, no future
churn for the common case, and `Symbol` can be added later if a need arises.

### 2. Semantics of "now" / latest

Many quote sources have a distinct "current price" path that is much faster
and cheaper than a historical lookup. Overloading `date time.Time` with
`time.Time{}` (zero value) for "latest" is easy to misuse and silently
produces wrong results when callers forget to set the date.

**Options.**

- **(a)** Sentinel zero-value `time.Time` means latest. Simple, error-prone.
- **(b)** Separate method: `Latest(ctx, base, quote) (Price, error)` alongside
  `Quote(ctx, base, quote, date) (Price, error)` for historical.
- **(c)** Sentinel constant exported from `pkg/quote` (e.g. `quote.AsOfNow`)
  passed where `date` is expected.

**Tentative lean:** (b). Two methods make the contract explicit and let
sources optimize the latest-price path independently.

### 3. Caching and rate limits

Quote APIs are slow and rate-limited; users will run `beansprout quote` for
a portfolio of N commodities across M dates. PLAN.md has no caching story.

**Options.**

- **(a)** Leave caching to each `Quoter` implementation.
- **(b)** Ship a `CachingQuoter` decorator backed by SQLite or BoltDB,
  keyed by `(source, base, quote, date)`, with a TTL for "latest" entries.
- **(c)** Make `pricedb` the cache: every `Quote` call is satisfied from the
  ledger's existing `price` directives if a recent-enough one exists.

**Tentative lean:** (b). Orthogonal to source, easy to compose with
fan-out/fallback, and doesn't conflate ledger state with transient cache.
(c) remains useful as a separate "ledger-aware" decorator on top of (b).

### 4. Plugin contract — relation to Phase 6

PLAN.md states that Phase 7 supports plugin-backed and external-process
quoters. Phase 6's `pkg/ext/goplug` and `pkg/ext/extproc` (when delivered)
load plugins implementing `postproc.api.Plugin`. A `Quoter` is a different
contract.

This means Phase 7 implies a Phase 6 follow-up: the loaders must be
generalized to register plugins against multiple interface families, with
the concrete contract being one of `postproc.api.Plugin`, `quote.Quoter`, or
a future `importer.Source`/`importer.Classifier` (Phase 8). The plugin
manifest needs an explicit `kind` field.

**Open sub-questions.**

- Where does the `Quoter` interface live? `pkg/quote` is the obvious home,
  but `pkg/ext/goplug` then needs to know about it. Options: a `pkg/ext/quoter/api`
  shim package mirroring the layout of `pkg/ext/postproc/api`, or an
  inversion where `goplug` exposes a generic "register-by-interface" API
  and `pkg/quote` plugs itself in.
- Does `extproc` need a separate wire schema per kind, or one umbrella
  protocol with a discriminator?

This is the single biggest cross-phase coupling point in Phase 7 and is
worth resolving before any Phase 7 code lands.

### 5. Choice of built-in source

PLAN.md's example, Yahoo Finance, is problematic: Yahoo's terms forbid
scraping, the unofficial v7/v8 endpoints break with no warning, and the
official endpoint requires a paid plan. Better candidates:

- **Stooq.** Free CSV endpoints, broad coverage of equities and FX,
  no API key. Latency is fine. Terms allow personal use.
- **exchangerate.host.** FX-only, free, no key. Recently moved to a paid
  tier for some endpoints — needs re-verification.
- **ECB reference rates.** EUR-base FX, daily only, official, no key.
  Narrow but rock-solid.
- **Alpha Vantage.** Broad coverage including crypto, requires a free key,
  rate-limited (5 req/min).

The chosen default should be license-clean, not require a key, and have
documented limitations. Stooq looks like the best candidate; ECB is a good
fallback for FX-only users.

**Tentative lean:** ship a `stooq` quoter as the default reference
implementation, document its scope, and provide an `ecb` quoter for FX as a
second example so users see how to compose two sources via the
fallback quoter.

### 6. Conflict policy in `pricedb`

PLAN.md says `pricedb` "deduplicates by date and commodity". What if an
existing `price` directive on the same date for the same pair has a
different value than the fetched one?

**Options.**

- **(a)** Skip the new value; leave the ledger unchanged.
- **(b)** Overwrite the existing directive with the new value.
- **(c)** Emit a diagnostic (`CodeQuoteConflict` or similar) and skip;
  let the user decide.
- **(d)** Make the policy configurable per call, defaulting to (c).

**Tentative lean:** (d) with a default of (c). The user is unlikely to want
silent overwrite, but the choice belongs to the calling tool, not buried in
`pricedb`.

### 7. Bulk fetch and concurrency

The realistic workload is "fetch N commodities × M dates". Doing this as
N×M serial `Quote` calls is too slow for any non-trivial portfolio.

**Options.**

- **(a)** Add a batch method to the interface:
  `QuoteMany(ctx, requests []Request) ([]Result, error)`.
  Sources that have a bulk endpoint (Stooq does) can implement it natively;
  others fall back to a default helper that fans out via goroutines with
  bounded concurrency.
- **(b)** Keep the interface single-shot and have the framework run
  goroutines around it. Simpler interface, but loses bulk-endpoint speedups.

**Tentative lean:** (a). The default helper means single-shot sources don't
have to write their own concurrency code, while bulk-capable sources get the
speedup. Bounded concurrency is configurable on the helper.

### 8. Testability

`pkg/quote` and downstream tools (`cmd/beansprout quote`) need to be
testable without hitting the network.

**Plan.** Ship two test helpers under `pkg/quote/quotetest`:

- A **recording quoter** that wraps a real quoter, captures responses to
  disk, and on replay returns recorded responses for matching requests.
  Pattern modeled after `httptest`.
- A **stub quoter** built from a static map for unit tests that don't need
  realistic data.

Fixtures live in `testdata/` next to each test that uses them.

## Cross-phase implications

- **Phase 6 follow-up.** Multi-interface plugin loading (see §4) is a
  prerequisite for Phase 7's plugin-backed quoter and for Phase 8's
  plugin-backed sources/classifiers. It is best designed once, generically.
- **Phase 4 / `pricedb`.** The conflict-diagnostic flow (§6) needs a new
  diagnostic code in `pkg/ast`. Worth checking whether the existing
  `ast.Diagnostic` channel is the right home or whether `pricedb` should
  return its own structured result.
- **`operating_currency` option.** Already registered in
  `pkg/validation/options.go` but not yet consumed. Phase 7 may consume it
  as a default quote currency for tools that prefer the single-commodity
  ergonomics (see §1 (a)).

## Status

Discussion only — no code yet. Implementation is gated on:

1. Resolving the `Quoter` signature (§1) and "latest" semantics (§2).
2. Picking a default built-in source (§5).
3. Designing the multi-interface plugin registration (§4) — likely as a
   small Phase 6 follow-up that lands before Phase 7 implementation begins.

Once these are settled, the rest of Phase 7 (caching decorator, fan-out
quoter, `pricedb`, test helpers) is straightforward.
