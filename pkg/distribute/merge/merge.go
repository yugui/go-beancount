package merge

import (
	"errors"
	"fmt"
	"os"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/distribute/route"
	"github.com/yugui/go-beancount/pkg/format"
)

// Plan groups all inserts targeted at one destination file together with
// the file-level layout settings that must be uniform across that file.
//
// Order applies to every Insert in the plan: a single file has one
// canonical sort order. The two spacing fields control whether and how
// many blank lines the merger inserts between consecutive directives;
// see the package doc for the B/N rule.
type Plan struct {
	// Path is the destination file. It is created on first write.
	Path string
	// Order selects how new directives are positioned relative to
	// existing dated directives. In sub-phase 7.5b only
	// route.OrderAscending is supported.
	Order route.OrderKind
	// BlankLinesBetweenDirectives is the target N for the spacing rule.
	BlankLinesBetweenDirectives int
	// InsertBlankLinesBetweenDirectives is the B switch: when true the
	// merger contributes blank lines on either side of an insertion to
	// reach BlankLinesBetweenDirectives, never reducing pre-existing ones.
	InsertBlankLinesBetweenDirectives bool
	// Inserts is the list of directives to add. Input order is preserved
	// for stable same-date FIFO behaviour.
	Inserts []Insert
}

// Insert describes one directive to add to a destination file.
//
// When Commented is true the directive is rendered as a commented-out
// block: each line of the printed directive is prefixed with Prefix
// (typically "; "). StripMetaKeys is reserved for 7.5g; it is accepted
// on the type but not read in 7.5e.
type Insert struct {
	// Directive is the directive to render and insert.
	Directive ast.Directive
	// Commented requests a commented-out emit. When true the rendered
	// directive lines are each prefixed with Prefix.
	Commented bool
	// Prefix is the comment prefix used when Commented is true. The
	// recommended value is "; ".
	Prefix string
	// StripMetaKeys names metadata keys to strip before emit.
	StripMetaKeys []string
	// Format carries body-level printing options (comma_grouping,
	// align_amounts, amount_column, east_asian_ambiguous_width,
	// indent_width). File-level spacing is set on Plan.
	Format []format.Option
}

// Options is reserved for future per-call overrides. It currently has
// no fields; pass the zero value.
type Options struct{}

// Stats reports what Merge did to the destination file.
//
// Skipped is reserved for callers that elect to drop inserts entirely
// (e.g. the dedup wiring in sub-phase 7.5e); Merge itself never bumps
// Skipped because every Insert it receives is rendered.
type Stats struct {
	// Path is the destination file the stats describe.
	Path string
	// Written counts inserts that landed in the file as active directives.
	Written int
	// Commented counts inserts that landed as commented-out blocks.
	Commented int
	// Skipped counts inserts that were dropped (e.g. by dedup).
	Skipped int
}

// ErrOrderNotSupported is wrapped in the error Merge returns when
// Plan.Order is not route.OrderAscending. Descending and append orders
// land in sub-phase 7.5h; callers can route around it with errors.Is.
var ErrOrderNotSupported = errors.New("merge: only ascending order is supported")

// tallyInserts splits the insert list into active and commented counts
// for Stats. Same accounting is used by the new-file and existing-file
// paths.
func tallyInserts(inserts []Insert) (written, commented int) {
	for _, ins := range inserts {
		if ins.Commented {
			commented++
		} else {
			written++
		}
	}
	return written, commented
}

// Merge writes the directives in plan into plan.Path, preserving every
// byte of surrounding content in an existing destination file (see the
// package doc for the patch-composition model). When plan.Path does not
// exist Merge creates it (with parent directories) and emits all
// inserts in canonical order.
//
// Empty plan.Inserts is a no-op: Merge returns Stats{Path: plan.Path}
// and does not touch the file.
func Merge(plan Plan, _ Options) (Stats, error) {
	if len(plan.Inserts) == 0 {
		return Stats{Path: plan.Path}, nil
	}

	if plan.Order != route.OrderAscending {
		return Stats{Path: plan.Path}, fmt.Errorf("merge: order %s: %w", orderName(plan.Order), ErrOrderNotSupported)
	}

	_, err := os.Stat(plan.Path)
	switch {
	case err == nil:
		return mergeExistingFile(plan)
	case errors.Is(err, os.ErrNotExist):
		return mergeNewFile(plan)
	default:
		return Stats{Path: plan.Path}, fmt.Errorf("merge: stat %q: %w", plan.Path, err)
	}
}

// orderName returns a human-readable name for an OrderKind, used in
// error messages.
func orderName(o route.OrderKind) string {
	switch o {
	case route.OrderAscending:
		return "ascending"
	case route.OrderDescending:
		return "descending"
	case route.OrderAppend:
		return "append"
	default:
		return fmt.Sprintf("OrderKind(%d)", int(o))
	}
}
