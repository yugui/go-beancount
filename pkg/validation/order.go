package validation

import (
	"sort"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

// directiveKind assigns a canonical same-day ordering priority to each
// directive type. Lower values sort earlier.
//
// Beancount processes same-day directives in a specific order so that, for
// example, a balance assertion is evaluated against the opening balance of
// the day (before any transactions posted that day). The ordering used here
// matches Beancount's canonical order:
//
//  1. open
//  2. balance
//  3. pad
//  4. transaction
//  5. note / document / event / commodity / query / custom
//  6. close
//  7. price
//
// Directives without a date (option, plugin, include) are given a dedicated
// sentinel kind and sort before everything else with a zero time, so that
// they are processed first in a stable fashion.
type directiveKind int

const (
	kindFileHeader directiveKind = iota // option/plugin/include (no date)
	kindOpen
	kindBalance
	kindPad
	kindTransaction
	kindOther // note, document, event, commodity, query, custom
	kindClose
	kindPrice
)

// kindOf returns the canonical directiveKind for the given directive.
func kindOf(d ast.Directive) directiveKind {
	switch d.(type) {
	case *ast.Option, *ast.Plugin, *ast.Include:
		return kindFileHeader
	case *ast.Open:
		return kindOpen
	case *ast.Balance:
		return kindBalance
	case *ast.Pad:
		return kindPad
	case *ast.Transaction:
		return kindTransaction
	case *ast.Close:
		return kindClose
	case *ast.Price:
		return kindPrice
	case *ast.Note, *ast.Document, *ast.Event, *ast.Commodity, *ast.Query, *ast.Custom:
		return kindOther
	// default: unknown directives also sort as kindOther
	default:
		return kindOther
	}
}

// dateOf returns the date associated with the directive. Directives without
// a date (option, plugin, include) return the zero time.Time.
func dateOf(d ast.Directive) time.Time {
	switch v := d.(type) {
	case *ast.Open:
		return v.Date
	case *ast.Close:
		return v.Date
	case *ast.Commodity:
		return v.Date
	case *ast.Balance:
		return v.Date
	case *ast.Pad:
		return v.Date
	case *ast.Note:
		return v.Date
	case *ast.Document:
		return v.Date
	case *ast.Event:
		return v.Date
	case *ast.Query:
		return v.Date
	case *ast.Price:
		return v.Date
	case *ast.Transaction:
		return v.Date
	case *ast.Custom:
		return v.Date
	default:
		// Option, Plugin, Include, and any unknown directive types.
		return time.Time{}
	}
}

// orderedDirective is a directive paired with its sort key metadata.
type orderedDirective struct {
	dir    ast.Directive
	date   time.Time
	kind   directiveKind
	srcIdx int // original index in ledger.Directives
}

// orderDirectives returns a stable canonical ordering of the ledger's
// directives: primarily by date, then by directive kind, then by original
// source index. The returned slice has the same length as l.Directives.
func orderDirectives(l *ast.Ledger) []orderedDirective {
	if l == nil {
		return nil
	}
	out := make([]orderedDirective, len(l.Directives))
	for i, d := range l.Directives {
		out[i] = orderedDirective{
			dir:    d,
			date:   dateOf(d),
			kind:   kindOf(d),
			srcIdx: i,
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.date.Equal(b.date) {
			return a.date.Before(b.date)
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.srcIdx < b.srcIdx
	})
	return out
}
