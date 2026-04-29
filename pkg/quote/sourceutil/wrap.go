package sourceutil

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// WrapSingleCell adapts a one-cell function (one Pair, one date) into
// an api.AtSource. The returned source iterates the input query slice
// serially, calling fn once per query, and reports
// Capabilities{SupportsAt: true}. Authors who can produce a price for
// exactly one (pair, date) cell at a time use this as the shortest
// path onto the orchestrator interface; combine with BatchPairs for
// parallelism and with DateRangeIter to additionally serve ranges.
//
// fn returns the per-cell price as an apd.Decimal in the pair's quote
// currency. A nil fn return error produces an ast.Price with that
// number and the query's Pair as Commodity / Amount.Currency. A non-
// nil error from fn is treated as a per-query failure: it is recorded
// as a Diagnostic on the offending query and iteration continues with
// the next query. Context cancellation observed via ctx.Err() between
// queries is the only condition under which QuoteAt itself returns a
// non-nil top-level error; if the caller has whole-call failure modes
// (auth, transport) that should abort the batch as a top-level error,
// it should wrap the returned source's QuoteAt to map the relevant
// per-query errors to a top-level return.
func WrapSingleCell(name string, fn func(ctx context.Context, p api.Pair, at time.Time) (apd.Decimal, error)) api.AtSource {
	return &singleCell{name: name, fn: fn}
}

type singleCell struct {
	name string
	fn   func(ctx context.Context, p api.Pair, at time.Time) (apd.Decimal, error)
}

func (s *singleCell) Name() string { return s.name }

func (s *singleCell) Capabilities() api.Capabilities {
	return api.Capabilities{SupportsAt: true}
}

func (s *singleCell) QuoteAt(ctx context.Context, q []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	prices := make([]ast.Price, 0, len(q))
	var diags []ast.Diagnostic
	for _, query := range q {
		if err := ctx.Err(); err != nil {
			return prices, diags, err
		}
		num, err := s.fn(ctx, query.Pair, at)
		if err != nil {
			diags = append(diags, ast.Diagnostic{
				Code:    "quote-fetch-error",
				Message: fmt.Sprintf("%s: %s/%s: %v", s.name, query.Pair.QuoteCurrency, query.Pair.Commodity, err),
			})
			continue
		}
		prices = append(prices, ast.Price{
			Date:      at,
			Commodity: query.Pair.Commodity,
			Amount: ast.Amount{
				Number:   num,
				Currency: query.Pair.QuoteCurrency,
			},
		})
	}
	return prices, diags, nil
}
