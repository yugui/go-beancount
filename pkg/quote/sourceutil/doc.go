// Package sourceutil provides composable decorators and adapters that
// quote-source authors stack to express their natural batching axis
// and operational policies (concurrency, rate limiting, retry,
// caching) without the orchestrator having to know about any of them.
//
// Phase 7 separates "the source author's natural axis" from "the
// orchestrator's request shape". The orchestrator dispatches one
// call per (level, source) carrying every query for that source and
// the full requested range. A Source implementer is obliged to
// accept arbitrary-size batches, mixed quote currencies in one
// batch, and (for RangeSource) arbitrary ranges; the helpers in
// this package are how an implementer that cannot natively serve
// those shapes adapts to the contract. See the "Quoter author
// obligations" section of pkg/quote/api for the contract itself.
//
// A real-world quote source typically has exactly one shape it
// serves cheaply — a single (pair, date) cell, a row keyed by date,
// a column keyed by commodity, a full matrix, or latest-only.
// sourceutil bridges that author axis to the uniform Source /
// LatestSource / AtSource / RangeSource interfaces the orchestrator
// consumes:
//
//   - WrapSingleCell turns a (Pair, date) -> Decimal closure into an
//     AtSource. This is the fastest path for a one-off author.
//
//   - DateRangeIter lifts an AtSource into a RangeSource by walking a
//     Calendar (typically WeekdaysOnly for FX reference data, AllDays
//     for crypto-style 24/7 sources).
//
//   - SplitBatch caps per-call query count; use it when your source
//     cannot natively handle arbitrary-size batches. Generic over
//     LatestSource / AtSource / RangeSource.
//
//   - SplitRange caps per-call day count for a RangeSource; use it
//     when the underlying API cannot natively span arbitrary ranges.
//
//   - GroupByQuoteCurrency partitions an inbound batch by
//     Pair.QuoteCurrency and dispatches one downstream call per
//     quote currency in parallel. Use it when the source can only
//     batch pairs that share a single quote currency.
//
//   - Concurrency caps the number of in-flight calls to one source
//     independent of the orchestrator's global concurrency window.
//
//   - RateLimit imposes a token-bucket rate (fractional rps allowed)
//     so authors can match a documented per-source quota exactly.
//
//   - RetryOnError applies exponential-backoff retry on transient
//     HTTP errors. The 429-recovery path doubles as a back-stop
//     behind RateLimit when the limiter's calibration is loose.
//
//   - QuoteCache is a storage primitive — not a decorator — that a
//     source uses internally to memoise per-quote values keyed on
//     the source-physical addressing units (QuoteCurrency, Symbol,
//     plus a time qualifier). Use it when your underlying API
//     returns more data than the caller asked for (a "windfall"):
//     a single ECB feed download, for example, contains every
//     currency on every covered date, so caching each entry as it
//     is parsed lets a follow-up call for a different pair on the
//     same day skip the network entirely. See [QuoteCache] for the
//     rationale on why caching cannot live as an outer decorator.
//
// # Stacking
//
// Each decorator preserves whichever Capability sub-interfaces the
// input source implements: if you pass a value that satisfies both
// LatestSource and AtSource, the returned value also satisfies both.
// This makes the decorators freely stackable. The typical outside-in
// order is:
//
//	RateLimit(RetryOnError(source))
//
// with a Concurrency wrap either inside RateLimit or replacing it,
// depending on whether the source has a hard parallel-connection
// limit. RateLimit sits outside Retry so retries are themselves
// rate-limited; Retry sits closest to the source so it can observe
// the underlying transport errors. Caching is not a decorator in
// this stack — it lives inside the source via [QuoteCache] so
// windfall data returned by the underlying API can be stored and
// reused without losing the source-physical (QuoteCurrency, Symbol)
// addressing units that a decorator would not see.
//
// When both partitioning and per-call batch capping apply, the
// typical stack is:
//
//	GroupByQuoteCurrency → SplitBatch → wrapped source
//
// because partitioning by quote currency may produce per-partition
// slices of varying length that each then want batch-size capping.
//
// # Goroutine safety
//
// All decorators in this package are safe for concurrent use by
// multiple goroutines, provided the wrapped source is also safe for
// concurrent use. SplitBatch and Concurrency intentionally call the
// wrapped source from multiple goroutines.
//
// # Orchestrator independence
//
// The orchestrator does not depend on this package. sourceutil is
// strictly author-facing: an author chooses which decorators to apply
// and the orchestrator sees the resulting Source value as if it were
// any other.
package sourceutil
