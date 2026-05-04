package merge

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/yugui/go-beancount/internal/atomicfile"
	"github.com/yugui/go-beancount/internal/formatopt"
	"github.com/yugui/go-beancount/pkg/printer"
)

// mergeNewFile creates plan.Path from scratch, sorting the inserts in
// ascending date order (stable, so same-date inserts retain input
// order) and emitting them with B/N spacing applied between consecutive
// directives. The first insert starts at byte 0 with no leading blank
// lines; the file ends with exactly one newline (printer-supplied).
func mergeNewFile(plan Plan) (Stats, error) {
	inserts := make([]Insert, len(plan.Inserts))
	copy(inserts, plan.Inserts)
	sort.SliceStable(inserts, func(i, j int) bool {
		return inserts[i].Directive.DirDate().Before(inserts[j].Directive.DirDate())
	})

	var buf bytes.Buffer
	for i, ins := range inserts {
		if i > 0 {
			// Inter-insert padding: there is no existing side, so X=0.
			buf.WriteString(paddingFor(plan.InsertBlankLinesBetweenDirectives, plan.BlankLinesBetweenDirectives, 0))
		}
		if err := printInsert(&buf, plan, ins); err != nil {
			return Stats{Path: plan.Path}, err
		}
	}

	if dir := filepath.Dir(plan.Path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Stats{Path: plan.Path}, fmt.Errorf("merge: creating parent directories for %q: %w", plan.Path, err)
		}
	}
	if err := atomicfile.Write(plan.Path, buf.Bytes()); err != nil {
		return Stats{Path: plan.Path}, fmt.Errorf("merge: writing %q: %w", plan.Path, err)
	}
	return Stats{Path: plan.Path, Written: len(inserts)}, nil
}

// printInsert resolves the per-insert format options against
// formatopt.Default(), overrides the two spacing fields with the
// plan's values (per the §4.4 schema rule), and prints the directive
// to w. The printer emits a trailing newline.
func printInsert(w *bytes.Buffer, plan Plan, ins Insert) error {
	eff := formatopt.Resolve(ins.Format)
	eff.BlankLinesBetweenDirectives = plan.BlankLinesBetweenDirectives
	eff.InsertBlankLinesBetweenDirectives = plan.InsertBlankLinesBetweenDirectives
	if err := printer.Fprint(w, ins.Directive, optsAsClosures(eff)...); err != nil {
		return fmt.Errorf("merge: printing directive: %w", err)
	}
	return nil
}
