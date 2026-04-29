// Package pricedb is the small post-processing layer between the
// quote orchestrator and the CLI's stdout: it deduplicates the
// ast.Price values returned from one or more sources and writes them
// out in a deterministic order via pkg/printer.
//
// Scope is intentionally narrow. pricedb does not merge prices into
// an existing ledger file or rewrite source text. Persisting prices
// back to a user's checked-in ledger — preserving comments,
// formatting, and surrounding directives — is Phase 10's
// responsibility (the bean-daemon), where ledger ownership is
// centralised. Phase 7's CLI uses this package to write a fresh
// stream to stdout that the user can redirect, diff, or pipe into
// their own tooling.
package pricedb

import (
	"fmt"
	"io"
	"sort"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/printer"
)

// CodeDuplicate is the diagnostic code emitted by Dedup when two or
// more input prices collapse to the same (Date.UTC, Commodity,
// Amount.Currency) key.
const CodeDuplicate = "quote-duplicate"

// dedupKey is the equality key used by Dedup. Date is normalised to
// UTC and truncated to the calendar day so two timestamps that
// represent the same UTC day collide regardless of their original
// location.
type dedupKey struct {
	year      int
	month     int
	day       int
	commodity string
	currency  string
}

func keyOf(p ast.Price) dedupKey {
	utc := p.Date.UTC()
	return dedupKey{
		year:      utc.Year(),
		month:     int(utc.Month()),
		day:       utc.Day(),
		commodity: p.Commodity,
		currency:  p.Amount.Currency,
	}
}

// Dedup returns prices with duplicates collapsed by
// (Date.UTC, Commodity, Amount.Currency).
//
// Two ast.Price values are duplicates if those three components match
// after normalising Date to UTC. The numeric amount is intentionally
// not part of the key — having two different prices recorded for the
// same (date, pair) is precisely the case the caller should be told
// about, not silently allowed through.
//
// keepFirst selects which entry survives a collision: when true the
// earlier entry in the input slice wins; when false the later one
// wins. Either way, every duplicate after the first is reported as a
// diagnostic with code "quote-duplicate". The returned kept slice
// preserves the relative order of the input.
func Dedup(prices []ast.Price, keepFirst bool) (kept []ast.Price, diags []ast.Diagnostic) {
	if len(prices) == 0 {
		return nil, nil
	}

	// Determine which input index wins for each key.
	winner := make(map[dedupKey]int, len(prices))
	for i, p := range prices {
		k := keyOf(p)
		if _, ok := winner[k]; !ok || !keepFirst {
			winner[k] = i
		}
	}

	// Walk the input in order, keeping the winners and emitting a
	// diagnostic for each loser.
	kept = make([]ast.Price, 0, len(winner))
	for i, p := range prices {
		k := keyOf(p)
		if winner[k] == i {
			kept = append(kept, p)
			continue
		}
		diags = append(diags, ast.Diagnostic{
			Code:     CodeDuplicate,
			Span:     p.Span,
			Severity: ast.Warning,
			Message: fmt.Sprintf(
				"duplicate price for (%s, %s, %s); keeping the %s entry",
				p.Date.UTC().Format("2006-01-02"),
				p.Commodity,
				p.Amount.Currency,
				keepFirstWord(keepFirst),
			),
		})
	}
	return kept, diags
}

// keepFirstWord renders the keepFirst flag as the human-readable
// adjective embedded in duplicate diagnostics.
func keepFirstWord(keepFirst bool) string {
	if keepFirst {
		return "earlier"
	}
	return "later"
}

// FormatStream writes prices to w in canonical price-directive form,
// ordered by (Date asc, Commodity asc, Amount.Currency asc) so the
// output is deterministic across runs.
//
// FormatStream does NOT call Dedup; callers that want deduplicated
// output run Dedup first and feed the kept slice in. Persisting
// prices back to a ledger file (merging into an existing file,
// preserving surrounding text) is Phase 10's responsibility —
// pricedb stays out of that scope.
func FormatStream(w io.Writer, prices []ast.Price) error {
	if len(prices) == 0 {
		return nil
	}
	out := make([]ast.Price, len(prices))
	copy(out, prices)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.Date.Equal(b.Date) {
			return a.Date.Before(b.Date)
		}
		if a.Commodity != b.Commodity {
			return a.Commodity < b.Commodity
		}
		return a.Amount.Currency < b.Amount.Currency
	})
	for i := range out {
		if err := printer.Fprint(w, &out[i]); err != nil {
			return err
		}
	}
	return nil
}
