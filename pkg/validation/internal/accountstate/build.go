package accountstate

import (
	"iter"

	"github.com/yugui/go-beancount/pkg/ast"
)

// AllowsCurrency reports whether a posting using the given currency is
// permitted by the account's open directive. An open directive that
// omits the currency list allows all currencies.
func (s *State) AllowsCurrency(currency string) bool {
	if len(s.Currencies) == 0 {
		return true
	}
	for _, c := range s.Currencies {
		if c == currency {
			return true
		}
	}
	return false
}

// BuildResult carries the account state constructed from a directive
// stream, along with diagnostics produced by the construction.
// Diagnostics currently cover only duplicate-open directives; other
// per-account diagnostics (close-without-open, etc.) live with the
// validators that consume this state.
type BuildResult struct {
	// State maps each opened account to its canonical lifecycle state.
	// For duplicate opens the first directive wins, matching upstream
	// beancount's open-visit behavior.
	State map[ast.Account]*State

	// DuplicateOpens lists the *ast.Open directives that re-open an
	// account whose first open is already recorded in State. The first
	// open is never included here.
	DuplicateOpens []*ast.Open
}

// Build walks the given directive sequence once and returns a map of
// Account → State plus any directives that re-open an already-open
// account. Build never emits validation.Error values directly —
// callers own diagnostic shaping.
//
// Close directives update the matching State (setting Closed,
// CloseDate, CloseSpan) when the account is already open. Orphan
// Close directives (for accounts not in State) are silently ignored
// here; diagnosing them is the responsibility of a future validator.
func Build(directives iter.Seq2[int, ast.Directive]) BuildResult {
	result := BuildResult{
		State: make(map[ast.Account]*State),
	}
	if directives == nil {
		return result
	}
	for _, d := range directives {
		switch d := d.(type) {
		case *ast.Open:
			if _, ok := result.State[d.Account]; ok {
				result.DuplicateOpens = append(result.DuplicateOpens, d)
				continue
			}
			result.State[d.Account] = &State{
				OpenSpan:   d.Span,
				OpenDate:   d.Date,
				Currencies: d.Currencies,
				Booking:    d.Booking,
			}
		case *ast.Close:
			st, ok := result.State[d.Account]
			if !ok {
				continue
			}
			if st.Closed {
				continue
			}
			st.Closed = true
			st.CloseDate = d.Date
			st.CloseSpan = d.Span
		}
	}
	return result
}
