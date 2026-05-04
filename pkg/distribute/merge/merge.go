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
// In sub-phase 7.5b only the active form is supported: Commented==true
// is rejected with ErrCommentedNotSupported; Prefix and StripMetaKeys
// are accepted on the type but never read. They are reserved for the
// 7.5c commented-emit path.
type Insert struct {
	// Directive is the directive to render and insert.
	Directive ast.Directive
	// Commented requests a commented-out emit. Reserved for 7.5c; setting
	// it to true in 7.5b causes Merge to return ErrCommentedNotSupported.
	Commented bool
	// Prefix is the comment prefix used by the commented emit. Reserved
	// for 7.5c; ignored in 7.5b.
	Prefix string
	// StripMetaKeys names metadata keys to strip before emit. Reserved
	// for 7.5c; ignored in 7.5b.
	StripMetaKeys []string
	// Format carries body-level printing options. Spacing options
	// (BlankLines* fields) included here are silently overridden by the
	// Plan's spacing fields, matching the schema documented in §4.4.
	Format []format.Option
}

// Options is reserved for future per-call overrides. It currently has
// no fields; pass the zero value.
type Options struct{}

// Stats reports what Merge did to the destination file.
//
// In sub-phase 7.5b Commented and Skipped are always 0; they are
// reserved for the 7.5c (commented emit) and 7.5e (dedup integration)
// sub-phases respectively.
type Stats struct {
	// Path is the destination file the stats describe.
	Path string
	// Written counts inserts that landed in the file.
	Written int
	// Commented counts inserts that landed as commented-out blocks.
	Commented int
	// Skipped counts inserts that were dropped (e.g. by dedup).
	Skipped int
}

// ErrCommentedNotSupported is wrapped in the error Merge returns when an
// Insert has Commented==true. Commented emit is the responsibility of
// sub-phase 7.5c; callers can route around it with errors.Is.
var ErrCommentedNotSupported = errors.New("merge: commented inserts not supported in sub-phase 7.5b (lands in 7.5c)")

// ErrOrderNotSupported is wrapped in the error Merge returns when
// Plan.Order is not route.OrderAscending. Descending and append orders
// land in sub-phase 7.5h; callers can route around it with errors.Is.
var ErrOrderNotSupported = errors.New("merge: only ascending order supported in sub-phase 7.5b (lands in 7.5h)")

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

	// Validate up-front, before any file I/O, so out-of-scope inputs
	// fail loudly without side effects.
	for i, ins := range plan.Inserts {
		if ins.Commented {
			return Stats{Path: plan.Path}, fmt.Errorf("merge: insert %d has Commented=true: %w", i, ErrCommentedNotSupported)
		}
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
