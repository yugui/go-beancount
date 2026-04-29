// This file is the .so entry point. It must be compiled with
// -buildmode=plugin and is loaded by cmd/beanprice's --plugin flag
// in the integration tests. See doc.go for the fixture's role.

package main

import (
	"context"
	"time"

	"github.com/cockroachdb/apd/v3"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/goplug"
	"github.com/yugui/go-beancount/pkg/quote"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// pluginName is the registry key the fixture registers under. The
// integration test hardcodes the same string, so changing it also
// requires updating the test.
const pluginName = "staticquoter"

// Manifest is exported so goplug.Load can read it via plugin.Lookup.
var Manifest = goplug.Manifest{
	APIVersion: goplug.APIVersion,
	Name:       pluginName,
	Version:    "v0.0.0-fixture",
}

// InitPlugin is the goplug entry point. Called once after the
// Manifest checks pass; a non-nil return aborts the load.
func InitPlugin() error {
	quote.Register(pluginName, &source{})
	return nil
}

// source is a minimal api.Source that supports Latest and At and
// returns Number=1 for every query. Range is intentionally not
// implemented: the fixture exercises the loader, not the full
// capability matrix. The source materialises a Price per query in a
// loop, which inherently handles any-size batch, so no SplitBatch
// wrap is needed.
type source struct{}

func (s *source) Name() string { return pluginName }

func (s *source) Capabilities() api.Capabilities {
	return api.Capabilities{
		SupportsLatest: true,
		SupportsAt:     true,
	}
}

func (s *source) QuoteLatest(_ context.Context, qs []api.SourceQuery) ([]ast.Price, []ast.Diagnostic, error) {
	return s.priceFor(qs, time.Now().UTC().Truncate(24*time.Hour)), nil, nil
}

func (s *source) QuoteAt(_ context.Context, qs []api.SourceQuery, at time.Time) ([]ast.Price, []ast.Diagnostic, error) {
	return s.priceFor(qs, at), nil, nil
}

// priceFor returns one Price per query with Number=1, dated day. The
// constant value is what makes the fixture useful for round-trip
// tests: a price line with a known number is easy to assert on.
func (s *source) priceFor(qs []api.SourceQuery, day time.Time) []ast.Price {
	out := make([]ast.Price, 0, len(qs))
	for _, q := range qs {
		var n apd.Decimal
		n.SetInt64(1)
		out = append(out, ast.Price{
			Date:      day,
			Commodity: q.Pair.Commodity,
			Amount:    ast.Amount{Number: n, Currency: q.Pair.QuoteCurrency},
		})
	}
	return out
}

// main is required for buildmode=plugin but is never invoked.
func main() {}
