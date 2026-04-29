// Package api defines the stable interface shared between the
// orchestrator in pkg/quote, source authors writing in-tree quoters,
// and out-of-tree goplug (the mechanism for loading out-of-tree
// quoters from `.so` files at runtime) plugins that contribute
// additional sources.
//
// It contains only declarative types — addressing units (Pair,
// SourceRef, SourceQuery), the request shape (PriceRequest, Mode),
// and the Source base interface together with the optional
// LatestSource, AtSource, and RangeSource sub-interfaces. Concrete
// fetching logic, registry management, and orchestration live in
// other packages that import this one.
//
// This package is part of the goplug ABI surface: any incompatible
// change to the types or interfaces declared here requires a
// corresponding goplug APIVersion bump so that older plugins fail
// to load against newer hosts (and vice versa) instead of binding
// against an incompatible interface at runtime.
//
// # Quoter author obligations
//
// Which sub-interfaces a source implements is the only declarative
// signal it sends about its shape support. The shape of the inputs
// each Quote* method must accept is fixed by the contract below; it
// is not negotiable per source. A source that cannot natively handle
// one of these shapes is responsible for adapting itself to the
// contract — typically by stacking a decorator from
// pkg/quote/sourceutil at registration time.
//
//  1. Batch size. A Quote* call may receive a []SourceQuery of
//     any length ≥ 1. The source must serve the whole slice or
//     fan it out internally. Use [sourceutil.SplitBatch] to cap
//     per-call query count.
//
//  2. Mixed quote currencies in one batch. A single batch may
//     contain queries with different Pair.QuoteCurrency values.
//     The source must serve them all, or pre-partition by quote
//     currency. Use [sourceutil.GroupByQuoteCurrency] to
//     guarantee a single quote currency per downstream call.
//
//  3. Long ranges (RangeSource only). A QuoteRange call may span
//     any number of days. The source must serve any length, or
//     chunk internally. Use [sourceutil.SplitRange] to cap
//     per-call day count.
//
// Sources whose underlying API returns more data than the caller
// asked for (a "windfall" — e.g. a daily reference-rate feed that
// covers every currency in one download) should memoise the parsed
// values internally with [sourceutil.QuoteCache] keyed on the
// source-physical (QuoteCurrency, Symbol) addressing units, so a
// follow-up Quote* call for a different pair on the same day can
// short-circuit the network.
package api
