package merge

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/yugui/go-beancount/internal/atomicfile"
	"github.com/yugui/go-beancount/pkg/syntax"
)

// existingDirective records a dated directive in the destination file
// by its parsed date and its byte span (start..end).
//
// startOff is the position of the date token itself (not its leading
// trivia): bytes immediately before startOff are the previous
// content's terminator plus any blank lines; bytes from startOff
// onward are the directive's first significant byte.
//
// endOff is the position immediately past the directive's line
// terminator (the first '\n' after the last token's FullEnd). Bytes
// immediately after endOff are blank lines (if any) followed by the
// next directive or end-of-file. Together these conventions make
// blank-line counting in the patch stage unambiguous on both sides.
type existingDirective struct {
	date     time.Time
	startOff int
	endOff   int
}

// dateNodeKinds is the set of dated-directive node kinds whose
// directive node carries a DATE token directly. Other node kinds
// (option/plugin/include headers, pushtag/poptag, error/unrecognized
// lines) act as fixed anchors and are not indexed.
var dateNodeKinds = map[syntax.NodeKind]struct{}{
	syntax.OpenDirective:        {},
	syntax.CloseDirective:       {},
	syntax.CommodityDirective:   {},
	syntax.BalanceDirective:     {},
	syntax.PadDirective:         {},
	syntax.NoteDirective:        {},
	syntax.DocumentDirective:    {},
	syntax.PriceDirective:       {},
	syntax.EventDirective:       {},
	syntax.QueryDirective:       {},
	syntax.CustomDirective:      {},
	syntax.TransactionDirective: {},
}

// mergeExistingFile inserts each directive in plan.Inserts into an
// existing file at plan.Path, preserving every byte not covered by an
// insertion. See the package doc for the patch-composition model.
func mergeExistingFile(plan Plan) (Stats, error) {
	data, err := os.ReadFile(plan.Path)
	if err != nil {
		return Stats{Path: plan.Path}, fmt.Errorf("merge: reading %q: %w", plan.Path, err)
	}

	f, err := syntax.ParseReader(bytes.NewReader(data))
	if err != nil {
		return Stats{Path: plan.Path}, fmt.Errorf("merge: parsing %q: %w", plan.Path, err)
	}
	if len(f.Errors) > 0 {
		return Stats{Path: plan.Path}, fmt.Errorf("merge: parsing %q: %w", plan.Path, &f.Errors[0])
	}

	existing, err := indexDirectives(f, data, plan.Path)
	if err != nil {
		return Stats{Path: plan.Path}, err
	}

	inserts := make([]Insert, len(plan.Inserts))
	copy(inserts, plan.Inserts)
	sort.SliceStable(inserts, func(i, j int) bool {
		return inserts[i].Directive.DirDate().Before(inserts[j].Directive.DirDate())
	})

	// Group inserts by target offset, preserving stable input order
	// within each group.
	type group struct {
		offset  int
		inserts []Insert
	}
	var groups []group
	for _, ins := range inserts {
		off := targetOffset(existing, ins.Directive.DirDate(), len(data))
		if n := len(groups); n > 0 && groups[n-1].offset == off {
			groups[n-1].inserts = append(groups[n-1].inserts, ins)
			continue
		}
		groups = append(groups, group{offset: off, inserts: []Insert{ins}})
	}

	patches := make([]patch, 0, len(groups))
	for _, g := range groups {
		var body bytes.Buffer
		for i, ins := range g.inserts {
			if i > 0 {
				body.WriteString(paddingFor(plan.InsertBlankLinesBetweenDirectives, plan.BlankLinesBetweenDirectives, 0))
			}
			if err := printInsert(&body, plan, ins); err != nil {
				return Stats{Path: plan.Path}, err
			}
		}

		leading := leadingPatchText(plan, data, g.offset)
		trailing := trailingPatchText(plan, data, g.offset)
		patches = append(patches, patch{
			offset:   g.offset,
			leading:  leading,
			body:     body.String(),
			trailing: trailing,
		})
	}

	out := applyPatches(data, patches)
	if err := atomicfile.Write(plan.Path, out); err != nil {
		return Stats{Path: plan.Path}, fmt.Errorf("merge: writing %q: %w", plan.Path, err)
	}
	written, commented := tallyInserts(plan.Inserts)
	return Stats{Path: plan.Path, Written: written, Commented: commented}, nil
}

// indexDirectives walks the top-level children of f.Root and records
// every dated directive's parsed date and byte span. See
// existingDirective for the start/end conventions used here.
func indexDirectives(f *syntax.File, data []byte, path string) ([]existingDirective, error) {
	var out []existingDirective
	for _, c := range f.Root.Children {
		if c.Node == nil {
			continue
		}
		if _, ok := dateNodeKinds[c.Node.Kind]; !ok {
			continue
		}
		dateTok := c.Node.FindToken(syntax.DATE)
		if dateTok == nil {
			// Defensive: every dated-directive parser attaches DATE as a
			// direct child token, but skip rather than panic.
			continue
		}
		date, err := time.Parse("2006-01-02", dateTok.Text())
		if err != nil {
			return nil, fmt.Errorf("merge: parsing date %q in %q: %w", dateTok.Text(), path, err)
		}
		last := lastToken(c.Node)
		if last == nil {
			continue
		}
		// Start at the date token's own position (excluding leading
		// trivia). The leading trivia between the previous directive's
		// terminator and this date is then "between content" rather
		// than being absorbed into either directive's span, which makes
		// blank-line counting unambiguous: bytes immediately before
		// startOff are the previous content's terminator + any blank
		// lines, bytes immediately after startOff are the directive
		// itself.
		start := dateTok.Pos
		end := advancePastTerminator(data, last.FullEnd())
		out = append(out, existingDirective{
			date:     date,
			startOff: start,
			endOff:   end,
		})
	}
	return out, nil
}

// lastToken returns the last token in node's subtree (depth-first, in
// source order).
func lastToken(node *syntax.Node) *syntax.Token {
	var last *syntax.Token
	for t := range node.Tokens() {
		last = t
	}
	return last
}

// advancePastTerminator scans data forward from off, skipping any
// whitespace and comment bytes, and returns the offset just past the
// first '\n' found. If end-of-file is reached without a '\n' the
// returned offset is len(data) — the directive's last line is
// unterminated, and the caller treats EOF-without-newline as the
// "file end" boundary case.
//
// The scan is over raw bytes (not trivia) because newlines are stored
// as leading trivia of the *next* token in the parser's CST, so the
// directive's own subtree does not include them.
func advancePastTerminator(data []byte, off int) int {
	i := off
	for i < len(data) {
		if data[i] == '\n' {
			return i + 1
		}
		i++
	}
	return len(data)
}

// targetOffset returns the byte offset at which an insert with the
// given date should land, per the ascending binary-search rule from
// §8: the largest i with existing[i].date <= date, then "just after i"
// (i.e. existing[i].endOff). When no element matches the insertion
// goes just before the first dated directive (existing[0].startOff);
// when there are no dated directives at all it goes at end of file.
func targetOffset(existing []existingDirective, date time.Time, fileLen int) int {
	if len(existing) == 0 {
		return fileLen
	}
	// sort.Search finds the smallest i for which the predicate is
	// true; we want the largest i with existing[i].date <= date, which
	// is one less than the smallest i with existing[i].date > date.
	idx := sort.Search(len(existing), func(i int) bool {
		return existing[i].date.After(date)
	})
	if idx == 0 {
		return existing[0].startOff
	}
	return existing[idx-1].endOff
}

// leadingPatchText returns the bytes the merger contributes before the
// first insert at offset. It folds two concerns into one string:
//
//   - The B/N blank-line padding between the previous existing content
//     and the new insertion (zero when offset==0, since "file start"
//     skips leading padding per §4.4).
//   - The single missing terminator when the insertion lands at EOF in
//     a file that lacks a trailing newline. This is the documented
//     deliberate exception to the "never edit bytes outside its own
//     insertions" invariant: writing past EOF without a terminator
//     would leave a malformed text file.
func leadingPatchText(plan Plan, data []byte, offset int) string {
	if offset == 0 {
		return ""
	}
	var prefix string
	if offset == len(data) && len(data) > 0 && data[len(data)-1] != '\n' {
		// Terminate the previous content line so the patch lands on
		// its own line; the per-side padding then follows as usual.
		prefix = "\n"
	}
	x := countTrailingBlanks(data[:offset])
	return prefix + paddingFor(plan.InsertBlankLinesBetweenDirectives, plan.BlankLinesBetweenDirectives, x)
}

// trailingPatchText returns the bytes the merger contributes after
// the last insert in a same-offset group. At end-of-file the printer's
// own trailing newline is the only terminator, so nothing is appended;
// in the middle of the file the B/N rule applies against the leading
// blank-line run of the bytes that follow.
func trailingPatchText(plan Plan, data []byte, offset int) string {
	if offset >= len(data) {
		return ""
	}
	x := countLeadingBlanks(data[offset:])
	return paddingFor(plan.InsertBlankLinesBetweenDirectives, plan.BlankLinesBetweenDirectives, x)
}
