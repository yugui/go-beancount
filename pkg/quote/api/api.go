package api

import (
	"context"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

// Pair is the (commodity, quote currency) addressing unit used to
// identify a price series independently of any particular source.
//
// It corresponds directly to the bean-price meta encoding on a
// commodity directive. For example, a `price: "USD:yahoo/AAPL"` meta
// declares that the AAPL commodity should be priced in USD via the
// "yahoo" source under the symbol "AAPL"; the Pair extracted from
// that meta is Pair{Commodity: "AAPL", QuoteCurrency: "USD"}.
//
// Commodity matches ast.Price.Commodity (and is normally the same
// string as the corresponding ast.Commodity.Currency); QuoteCurrency
// matches ast.Price.Amount.Currency.
type Pair struct {
	// Commodity is the base commodity being priced (e.g. "AAPL").
	Commodity string
	// QuoteCurrency is the currency the price is denominated in
	// (e.g. "USD").
	QuoteCurrency string
}

// SourceRef binds one source name to one symbol on that source. It is
// the unit used inside PriceRequest.Sources to express "ask source X
// for symbol Y".
//
// The bean-price `^` prefix (the 1/X inverted-quote convention) is
// not currently supported.
type SourceRef struct {
	// Source is the registered source name (e.g. "yahoo").
	Source string
	// Symbol is the ticker as the source itself spells it
	// (e.g. "GOOG", "NASDAQ:GOOG").
	Symbol string
}

// PriceRequest is one logical price-fetch unit: "fetch this Pair, and
// if the primary source fails, fall back through the remaining ones
// in order".
//
// Sources[0] is the primary source; Sources[1:] are fallbacks tried
// in priority order if the primary cannot satisfy the request. A
// PriceRequest always concerns exactly one Pair; distinct quote
// currencies for the same commodity (for example AAPL priced in both
// USD and JPY) are represented as separate PriceRequest values, not
// as multiple entries inside one request.
type PriceRequest struct {
	// Pair is the logical (commodity, quote currency) being asked
	// for.
	Pair Pair
	// Sources lists the source candidates in priority order.
	// Sources[0] is the primary; later entries are fallbacks.
	Sources []SourceRef
}

// Mode classifies the time-shape of a fetch call. The orchestrator
// chooses one of the three modes per source based on the entry point
// invoked (FetchLatest / FetchAt / FetchRange) and which sub-interfaces
// the source implements; it then reports the chosen Mode on the
// Event.Mode field passed to observers.
type Mode uint8

const (
	// ModeLatest asks a source for the newest price it can produce.
	ModeLatest Mode = iota
	// ModeAt asks for the price as of a particular calendar date.
	ModeAt
	// ModeRange asks for prices over a half-open calendar-date
	// interval [start, end).
	ModeRange
)

// SourceQuery is the source-physical addressing unit: a (Pair,
// Symbol) pair handed to a single source on a single call.
//
// Pair and Symbol are intentionally separate. Pair is the logical
// request unit — what the ledger asked for, expressed in commodity
// terms — while Symbol is the source-specific ticker the quoter must
// use to actually hit its underlying API. The same commodity may have
// different tickers across sources (e.g. GOOG on Yahoo,
// NASDAQ:GOOG on Google), so the orchestrator carries both: it picks
// up Symbol from the matching SourceRef when issuing a call, and uses
// the Pair to label the resulting ast.Price.
//
// When a quoter constructs an ast.Price from a fetched value, the
// Pair is the authoritative source for ast.Price.Commodity (and for
// the Amount.Currency); the Symbol is purely an input to the fetch
// call and must not appear in the output.
type SourceQuery struct {
	// Pair is the logical pair being fetched. Authoritative for the
	// resulting ast.Price's commodity and quote currency.
	Pair Pair
	// Symbol is the source-specific ticker to query. Used only as
	// an input to the source's underlying API.
	Symbol string
}

// Source is the base interface every quote source must implement. It
// exposes only identification (Name); the actual fetch methods live
// on the optional sub-interfaces LatestSource, AtSource, and
// RangeSource.
//
// The orchestrator detects which sub-interfaces a source supports via
// type assertions on LatestSource / AtSource / RangeSource and uses
// the result to pick which method to invoke. The hybrid design — base
// Source plus optional sub-interfaces — is deliberately preferred
// over both alternatives:
//
//   - A single all-purpose method that returns an "unsupported" error
//     for shapes the source cannot serve forces every caller to
//     handle that error and forces every source to advertise non-
//     support through a runtime channel rather than statically.
//
//   - Three required methods on a single interface forces sources
//     whose natural batching axis is, say, "row by date" to provide
//     stub implementations of the other shapes that immediately
//     return errors.
//
// Each real-world source has one natural batching axis (a single
// cell, a row keyed by date, a column keyed by commodity, a full
// matrix, or latest only). The hybrid lets each source declare only
// what it natively can do.
type Source interface {
	// Name returns the registry name of the source (e.g. "yahoo").
	Name() string
}

// LatestSource is implemented by sources that natively serve "the
// newest price available". The orchestrator calls QuoteLatest only
// when the source satisfies LatestSource via a type assertion.
//
// Returned ast.Prices use each query's Pair (not Symbol) for
// Commodity and Amount.Currency. Diagnostics describe per-query
// problems that did not produce a price; the error return is for
// transport- or source-level failures that affected the whole call.
//
// QuoteLatest MUST accept any-size batch and any mix of quote
// currencies in q; see the "Quoter author obligations" section of
// the package doc and [sourceutil.SplitBatch] for the helper that
// caps per-call query count.
type LatestSource interface {
	Source
	QuoteLatest(ctx context.Context, q []SourceQuery) ([]ast.Price, []ast.Diagnostic, error)
}

// AtSource is implemented by sources that natively serve "the price
// as of a particular calendar date". The orchestrator calls QuoteAt
// only when the source satisfies AtSource via a type assertion.
//
// The at argument is a TZ-naïve calendar date conventionally at 0:00
// UTC; the quoter is responsible for projecting it onto the source's
// native exchange time zone if needed. Output and error semantics
// match LatestSource.
//
// QuoteAt MUST accept any-size batch and any mix of quote currencies
// in q; see the "Quoter author obligations" section of the package
// doc and [sourceutil.SplitBatch] for the helper that caps per-call
// query count.
type AtSource interface {
	Source
	QuoteAt(ctx context.Context, q []SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error)
}

// RangeSource is implemented by sources that natively serve a series
// of prices over a date range. The orchestrator calls QuoteRange
// only when the source satisfies RangeSource via a type assertion.
//
// The interval is half-open: [start, end). Both endpoints are TZ-
// naïve calendar dates conventionally at 0:00 UTC. Output and error
// semantics match LatestSource.
//
// QuoteRange MUST accept any-size batch and any mix of quote
// currencies in q, and MUST accept arbitrarily long ranges; see the
// "Quoter author obligations" section of the package doc, plus
// [sourceutil.SplitBatch] (per-call query count) and
// [sourceutil.SplitRange] (per-call day count) for the helpers that
// cap each axis.
type RangeSource interface {
	Source
	QuoteRange(ctx context.Context, q []SourceQuery, start, end time.Time) ([]ast.Price, []ast.Diagnostic, error)
}
