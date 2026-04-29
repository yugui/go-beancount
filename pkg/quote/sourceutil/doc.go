// Package sourceutil provides composable decorators and adapters that
// quote-source authors stack to express their natural batching axis
// and operational policies (concurrency, rate limiting, retry,
// caching) without the orchestrator having to know about any of them.
//
// Phase 7 separates "the source author's natural axis" from "the
// orchestrator's request shape". A real-world quote source typically
// has exactly one shape it serves cheaply — a single (pair, date)
// cell, a row keyed by date, a column keyed by commodity, a full
// matrix, or latest-only. sourceutil bridges that author axis to the
// uniform Source / LatestSource / AtSource / RangeSource interfaces
// the orchestrator consumes:
//
//   - WrapSingleCell turns a (Pair, date) -> Decimal closure into an
//     AtSource. This is the fastest path for a one-off author.
//
//   - DateRangeIter lifts an AtSource into a RangeSource by walking a
//     Calendar (typically WeekdaysOnly for FX reference data, AllDays
//     for crypto-style 24/7 sources).
//
//   - BatchPairs parallelises a non-batch AtSource by query slicing,
//     so a per-pair API can still satisfy a batched orchestrator call
//     without authors hand-rolling goroutines.
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
//   - Cache memoises returned Price values by (Date.UTC, Commodity,
//     Amount.Currency). When a source whose first call returns a
//     many-pair batch (ECB is the canonical example) is hit again
//     for a different pair on the same day, the cache splits the
//     batch into already-known and missing portions and only the
//     missing portion is forwarded.
//
// # Stacking
//
// Each decorator preserves whichever Capability sub-interfaces the
// input source implements: if you pass a value that satisfies both
// LatestSource and AtSource, the returned value also satisfies both.
// This makes the decorators freely stackable. The typical outside-in
// order is:
//
//	Cache(RateLimit(RetryOnError(source)))
//
// with a Concurrency wrap either inside RateLimit or replacing it,
// depending on whether the source has a hard parallel-connection
// limit. Cache sits outermost so identical follow-up queries short-
// circuit the entire stack; RateLimit sits between Cache and Retry so
// retries are themselves rate-limited; Retry sits closest to the
// source so it can observe the underlying transport errors.
//
// # Goroutine safety
//
// All decorators in this package are safe for concurrent use by
// multiple goroutines, provided the wrapped source is also safe for
// concurrent use. BatchPairs and Concurrency intentionally call the
// wrapped source from multiple goroutines.
//
// # Orchestrator independence
//
// The orchestrator does not depend on this package. sourceutil is
// strictly author-facing: an author chooses which decorators to apply
// and the orchestrator sees the resulting Source value as if it were
// any other.
package sourceutil
