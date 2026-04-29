// Package quote fetches prices from external market and FX sources
// and emits them as ast.Price directives ready to feed into the rest
// of the go-beancount pipeline.
//
// pkg/quote is deliberately not a syntactic-compatibility layer with
// upstream Beancount's bean-price tool. It is a Go-native, typed
// orchestrator that happens to accept bean-price-compatible meta
// strings on Commodity directives (parsed by pkg/quote/meta) so that
// existing ledgers using the bean-price `price:` meta convention can
// be quoted without rewriting.
//
// # Locked-in design decisions
//
// The following choices are committed for Phase 7 and shape every
// sub-package below pkg/quote:
//
//   - ast.Price is the wire-out type. There is no parallel "Quote"
//     struct. Per-quoter metadata (source attribution, retrieval
//     time, vendor-specific fields, ...) is the quoter's
//     responsibility on Price.Meta and is not framework-mandated, so
//     the framework never has to grow a side-channel for fields some
//     sources happen to know.
//
//   - The default global concurrency limit on Fetch is 32. Most
//     quote sources rate-limit at 1 req/s, and goroutines blocked on
//     a rate-limit token are cheap, so the conventional CPU-bound
//     heuristic of GOMAXPROCS or 4 is far too conservative for this
//     IO-bound workload. Source-internal limits (sourceutil.RateLimit
//     and sourceutil.Concurrency) stack beneath the global cap to
//     enforce per-source quotas.
//
//   - Quote currency is always explicit in the meta string
//     (e.g. "USD:source/SYM"). There is no CLI --quote override flag.
//     A single commodity may carry several pair entries to be priced
//     in different currencies; each becomes an independent
//     PriceRequest.
//
//   - Date semantics are TZ-naïve calendar dates. The CLI --date
//     argument is interpreted as a calendar date and represented
//     internally at 0:00 UTC. Projecting that calendar date onto a
//     source-native exchange time zone (NYSE close, TSE close, ...)
//     is the responsibility of each individual quoter, not of the
//     orchestrator or the caller.
//
//   - bean-price-compatible meta. pkg/quote/meta parses the same
//     string grammar that bean-price reads from a Commodity
//     directive's "price" meta key. The key name is overridable for
//     ledgers that use a different convention.
//
//   - pricedb formats and dedups output only. Persisting quoted
//     prices back into ledger source files is the territory of
//     Phase 10 (bean-daemon); Phase 7 deliberately stops at producing
//     a printable, deduplicated stream.
//
//   - Plugin loading reuses pkg/ext/goplug directly. There is no
//     quote-specific loader: an out-of-tree quoter ships as a goplug
//     `.so` file whose InitPlugin callback calls quote.Register.
//     External-process quoters (extproc) are deferred until Phase 6c
//     lands.
//
//   - The pkg/quote/std/ecb source is the Phase 7 reference
//     implementation. ECB FX rates are public, unauthenticated,
//     stable, and natively range-capable, which keeps the test suite
//     hermetic and lets CI exercise a real source end-to-end without
//     external credentials.
//
// # Level-by-level scheduling
//
// Fetch (introduced in a later step) walks each PriceRequest's
// fallback chain in synchronised levels: level 0 is everyone's
// primary source, level 1 is fallback-1 for every request whose
// primary failed, and so on. Within a level each source is invoked
// at most once with the union of pending queries that target it,
// which structurally avoids the deadlocks and fallback-explosion
// failure modes of speculative fan-out. The detailed semantics live
// on Fetch itself.
//
// # Plugin authors
//
// Out-of-tree quoters ship as goplug `.so` files. The full workflow
// for authoring one:
//
//  1. Implement an [api.Source] (and any of [api.LatestSource],
//     [api.AtSource], [api.RangeSource] you need to support).
//  2. In a `package main` Go file, export
//     `Manifest [github.com/yugui/go-beancount/pkg/ext/goplug.Manifest]`
//     and `func InitPlugin() error`.
//  3. From InitPlugin, call quote.Register(name, source).
//  4. Build with `go build -buildmode=plugin -o quoter.so ./path/to/plugin`.
//  5. Pass `--plugin /abs/path/to/quoter.so` to beanprice.
//
// Choose the upstream tool's own name when emulating one (e.g.
// "yahoo", "google"); otherwise use the Go fully-qualified package
// path of the implementing package, mirroring the convention used by
// pkg/ext/postproc/std plugins such as checkclosing.
//
// The cmd/beanprice/testdata/staticquoter fixture is the canonical
// reference implementation: every required symbol and the
// quote.Register call from InitPlugin are present in roughly fifty
// lines.
package quote
