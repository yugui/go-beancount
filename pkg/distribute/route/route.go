package route

import (
	"fmt"
	"strings"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/format"
)

// OrderKind selects how new directives are positioned relative to
// existing dated directives in a destination file.
type OrderKind int

const (
	// OrderAscending inserts new directives so that older dates precede
	// newer ones.
	OrderAscending OrderKind = iota
	// OrderDescending inserts new directives so that newer dates precede
	// older ones.
	OrderDescending
	// OrderAppend inserts new directives at the end of the file.
	OrderAppend
)

// Decision is the routing outcome for a single directive. PassThrough
// is true when the directive's kind is not routable; in that case the
// remaining fields are zero and the caller decides what to do with the
// directive (typically: error out, or echo to stdout).
type Decision struct {
	Path          string
	Order         OrderKind
	StripMetaKeys []string
	EqMetaKeys    []string
	Format        []format.Option
	PassThrough   bool
}

// Config holds the resolved routing configuration. In sub-phase 7.5a it
// only carries Root for forward compatibility; later sub-phases extend
// it with section-level and override-level fields. Decide accepts a nil
// Config and treats it as the zero value.
type Config struct {
	// Root is the destination root directory. Decide does not consult
	// it in 7.5a (Path is returned as a Root-relative template
	// expansion); downstream code such as the CLI uses it to resolve
	// the path on disk.
	Root string
}

// Decide resolves d to a Decision under the standard convention.
//
// Returns an error only when the directive is structurally unable to
// supply a routing key — currently only a Transaction with no postings.
// Other directives are assumed to have been validated upstream.
func Decide(d ast.Directive, _ *Config) (Decision, error) {
	switch v := d.(type) {
	case *ast.Open:
		return accountDecision(v.Account, v.Date), nil
	case *ast.Close:
		return accountDecision(v.Account, v.Date), nil
	case *ast.Balance:
		return accountDecision(v.Account, v.Date), nil
	case *ast.Note:
		return accountDecision(v.Account, v.Date), nil
	case *ast.Document:
		return accountDecision(v.Account, v.Date), nil
	case *ast.Pad:
		return accountDecision(v.Account, v.Date), nil
	case *ast.Transaction:
		if len(v.Postings) == 0 {
			return Decision{}, fmt.Errorf("route: transaction on %s has no postings", v.Date.Format("2006-01-02"))
		}
		return accountDecision(v.Postings[0].Account, v.Date), nil
	case *ast.Price:
		return Decision{
			Path:  expandCommodityTemplate(v.Commodity, v.Date),
			Order: OrderAscending,
		}, nil
	case *ast.Option, *ast.Plugin, *ast.Include,
		*ast.Event, *ast.Query, *ast.Custom, *ast.Commodity:
		return Decision{PassThrough: true}, nil
	}
	return Decision{}, fmt.Errorf("route: unsupported directive type %T", d)
}

// accountDecision builds the Decision for directives keyed by account.
func accountDecision(a ast.Account, date time.Time) Decision {
	return Decision{
		Path:  expandAccountTemplate(a, date),
		Order: OrderAscending,
	}
}

// expandAccountTemplate fills "transactions/{account}/{date}.beancount".
func expandAccountTemplate(a ast.Account, date time.Time) string {
	return "transactions/" + strings.Join(a.Parts(), "/") + "/" + formatDateYYYYmm(date) + ".beancount"
}

// expandCommodityTemplate fills "quotes/{commodity}/{date}.beancount".
func expandCommodityTemplate(commodity string, date time.Time) string {
	return "quotes/" + commodity + "/" + formatDateYYYYmm(date) + ".beancount"
}

// formatDateYYYYmm formats date under the YYYYmm pattern. Calendar
// fields are read directly from the time value to avoid any timezone
// conversion (beancount dates are date-only; see design §2).
func formatDateYYYYmm(date time.Time) string {
	return fmt.Sprintf("%04d%02d", date.Year(), int(date.Month()))
}
