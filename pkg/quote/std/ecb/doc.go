// Package ecb fetches Eurozone foreign-exchange reference rates
// published by the European Central Bank, registered as the quote
// source named "ecb".
//
// # Why ECB is the reference source
//
// The criteria for the first real source were: a public feed (no
// authentication, hermetic CI), a stable URL, native range capability
// (a single HTTP request can return many days), and no rate-limit
// concerns under realistic CI use. The ECB FX reference rates feed
// satisfies all four:
//
//   - Public, unauthenticated, no rate limit in practice.
//   - Three stable XML URLs back the three Capability axes:
//     a daily file (most recent business day), a 90-day file, and a
//     full-history file from 1999 onwards.
//   - Each file contains every currency ECB publishes for every date
//     it covers, so a single download serves arbitrarily many pair
//     queries against the same date or date range.
//   - Updated once per business day after the ECB concertation, with
//     no per-IP throttling that we have observed.
//
// # Scope
//
// ECB publishes reference rates only against the euro. As a
// consequence, this source serves only EUR-denominated pairs — that
// is, queries with Pair.Commodity == "EUR". Requests for any other
// base commodity (including cross-rate requests like
// {Commodity: "USD", QuoteCurrency: "JPY"}) emit a
// quote-source-mismatch diagnostic and are skipped within the call.
// Cross-rate synthesis from EUR/X and EUR/Y is something callers can
// compose themselves with two separate Pair requests against this
// source; folding it into the source itself would obscure which datum
// originated from ECB and which was derived.
//
// # Symbol semantics
//
// ECB has no "ticker symbol" concept; every published quote currency
// is its own implicit symbol. SourceQuery.Symbol may be left empty
// (the source uses Pair.QuoteCurrency directly) or set explicitly to
// the same currency code (e.g. "USD"); any other value is treated as
// the requested currency to look up.
//
// # Registration
//
// Importing this package as a side-effect (via its init function)
// registers the source under the name "ecb" with pkg/quote's global
// registry. Callers wiring up the Phase 7 CLI typically do:
//
//	import _ "github.com/yugui/go-beancount/pkg/quote/std/ecb"
//
// # Live-network testing
//
// The hermetic test suite uses local XML fixtures served by
// httptest.NewServer. A single live-network smoke test against
// www.ecb.europa.eu is gated behind the `live` build tag so CI stays
// hermetic; run it locally with `go test -tags live ./...` or the
// equivalent Bazel invocation.
package ecb
