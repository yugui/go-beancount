// Package dedup builds and queries an equivalence index over the
// active and commented-out directives of a beancount ledger so a
// caller can decide whether a new directive duplicates one that is
// already filed.
//
// Typical usage: build the index once via [BuildIndex] from the
// caller's ledger root, query each candidate directive with
// [Index.InDestination] (same path) and [Index.InOtherActive] (other
// paths), and call [Index.Add] for every directive accepted into the
// ledger so subsequent queries see it. See [BuildIndex] for the
// index-construction contract and [Index] for query semantics.
//
// # Equivalence
//
// Each query reports the strongest of four layered match rules, applied
// in priority order (see [MatchKind]):
//
//   - id conflict — a key in MatchParams.IDKeys is present on both
//     directives with differing values. This proves the two are
//     distinct events and vetoes every weaker rule, so look-alike
//     directives carrying conflicting stable ids never collapse.
//   - id equality ([MatchID]) — such a key is present on both with
//     equal values. Useful when an upstream importer stamps a stable id
//     like "import-id" that survives reformatting.
//   - structural AST equality ([MatchExact]) — go-cmp equality with
//     every [ast.Span] ignored, ALL metadata ignored (identity-bearing
//     metadata is the id layer's job; everything else is annotation),
//     [apd.Decimal] compared numerically, posting order canonicalized,
//     and Transaction.Narration / Transaction.Payee / Note.Comment
//     NFKC-normalized. Identifier-bearing strings (accounts, currencies,
//     tags, links) stay byte-exact.
//   - structural similarity ([MatchStructural]) — same posting multiset
//     under account + absolute amount + currency (the absolute value
//     absorbs a transfer's sign flip) within MatchParams.DateWindowDays.
//
// [MatchExact] and [MatchStructural] both tolerate the single
// auto-balanced (amount-elided) posting beancount allows per
// transaction: it is matched by account alone, since the balance
// constraint fixes its amount once the other postings line up. This
// bridges the common case where one source states every posting
// explicitly while another leaves a leg for beancount to auto-balance.
//
// [MatchID] and [MatchExact] are equivalence relations and safe to skip
// on. [MatchStructural] is non-transitive (the date window breaks
// transitivity) and is review-only — callers must not skip on it. See
// [MatchKind.SkipCapable].
//
// # Scopes
//
// The two queries differ in what they look at:
//
//	InDestination — active AND commented-out entries at the SAME path
//	InOtherActive — only active entries at OTHER paths
//
// Commented-out entries elsewhere in the ledger never trigger
// InOtherActive — they are notes, not the canonical record. This
// keeps a re-run from cascading commented markers across files that
// have already been marked.
package dedup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/distribute/comment"
)

// MatchKind identifies which match rule fired for a query, ordered by
// strength. A larger value is not "stronger"; use [MatchKind.SkipCapable]
// to tell skip-safe (equivalence-relation) matches from review-only ones.
type MatchKind int

const (
	// MatchNone indicates the query did not match any indexed entry.
	MatchNone MatchKind = iota
	// MatchID indicates an id-key equality match (MatchParams.IDKeys).
	MatchID
	// MatchExact indicates a Span-and-metadata-stripped AST equality match.
	MatchExact
	// MatchStructural indicates an account+absolute-amount posting-multiset
	// match within the configured date window. Non-transitive: review-only.
	MatchStructural
	// MatchFuzzy is reserved for a future similarity-based match. Like
	// MatchStructural it is review-only.
	MatchFuzzy
)

// SkipCapable reports whether a match of this kind is an equivalence
// relation and therefore safe to drop the incoming directive on
// (skip). Non-transitive kinds are review-only: the caller should keep
// the directive but mark it for review rather than skipping it.
func (k MatchKind) SkipCapable() bool {
	return k == MatchID || k == MatchExact
}

// MatchParams carries the per-query tunables that govern matching.
// IDKeys names the metadata keys treated as stable identity (equal
// value ⇒ same; conflicting value ⇒ distinct). DateWindowDays bounds
// the structural rule; zero disables it.
type MatchParams struct {
	IDKeys         []string
	DateWindowDays int
}

// Index is the queryable equivalence index. Every path argument is a
// Config.Root-relative path (the same form route.Decide returns); the
// implementation does not canonicalize call-site inputs.
type Index interface {
	// InDestination reports whether path already contains an equivalent
	// directive — active OR commented-out — to d.
	InDestination(path string, d ast.Directive, p MatchParams) (matched bool, kind MatchKind)
	// InOtherActive reports whether any active equivalent of d exists
	// under a path other than path. Commented-out entries elsewhere are
	// ignored — they are notes, not the canonical record.
	InOtherActive(path string, d ast.Directive, p MatchParams) (matched bool, kind MatchKind)
	// Add records d under path with the given commented flag so that
	// subsequent queries see it.
	Add(path string, d ast.Directive, commented bool)
}

// Option configures BuildIndex. The slot is generic so future tunables
// can land without a signature change.
type Option func(*options)

type options struct{}

// indexEntry is one directive recorded in the index.
type indexEntry struct {
	path      string
	directive ast.Directive
	commented bool
}

// memoryIndex is the in-memory implementation. A linear scan per query
// is acceptable at the expected scale (tens of files, thousands of
// directives).
type memoryIndex struct {
	entries []indexEntry
}

func (m *memoryIndex) InDestination(path string, d ast.Directive, p MatchParams) (bool, MatchKind) {
	for _, e := range m.entries {
		if e.path != path {
			continue
		}
		if k := equivalent(e.directive, d, p); k != MatchNone {
			return true, k
		}
	}
	return false, MatchNone
}

func (m *memoryIndex) InOtherActive(path string, d ast.Directive, p MatchParams) (bool, MatchKind) {
	for _, e := range m.entries {
		if e.path == path || e.commented {
			continue
		}
		if k := equivalent(e.directive, d, p); k != MatchNone {
			return true, k
		}
	}
	return false, MatchNone
}

func (m *memoryIndex) Add(path string, d ast.Directive, commented bool) {
	m.entries = append(m.entries, indexEntry{path: path, directive: d, commented: commented})
}

// BuildIndex walks the ledger rooted at ledgerRoot and returns an
// Index containing every active directive plus every commented-out
// directive recovered from raw bytes. configRoot is the destination
// root: every member file's absolute filename is converted to a
// configRoot-relative key via filepath.Rel before being recorded, so
// Index queries (which take configRoot-relative paths from
// route.Decide) match against the same key space. Files outside
// configRoot yield a `..`-prefixed key — they never match an in-root
// query, which is intentional.
//
// ctx is checked at member-file boundaries: BuildIndex calls
// ctx.Err() before each new file's read+parse+comment-extract pass.
//
// The []ast.Diagnostic return carries every diagnostic produced by
// the ledger walk; the caller decides whether each is fatal. The
// error return is reserved for system-level failures (ctx
// cancellation, I/O on the root file).
func BuildIndex(ctx context.Context, ledgerRoot, configRoot string, opts ...Option) (Index, []ast.Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	// I/O on the root file is a system-level failure surfaced via the
	// error return; missing or unreadable include files surface as
	// diagnostics from ast.LoadFile and flow through the second return.
	if _, err := os.Stat(ledgerRoot); err != nil {
		return nil, nil, fmt.Errorf("dedup: %w", err)
	}
	ledger, err := ast.LoadFile(ledgerRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("dedup: loading ledger %q: %w", ledgerRoot, err)
	}

	idx := &memoryIndex{}
	for _, file := range ledger.Files {
		if err := ctx.Err(); err != nil {
			return nil, ledger.Diagnostics, err
		}
		key := canonicalize(configRoot, file.Filename)
		for _, d := range file.Directives {
			idx.Add(key, d, false)
		}
		raw, err := os.ReadFile(file.Filename)
		if err != nil {
			return nil, ledger.Diagnostics, fmt.Errorf("dedup: reading %q: %w", file.Filename, err)
		}
		for _, b := range comment.Extract(string(raw), file.Filename) {
			if b.Directive == nil {
				continue
			}
			idx.Add(key, b.Directive, true)
		}
	}
	return idx, ledger.Diagnostics, nil
}

// canonicalize converts an absolute member-file path into a
// configRoot-relative key. When filepath.Rel fails (e.g. mismatched
// volumes on Windows) the absolute path is returned verbatim — such a
// key cannot match a route.Decide destination (which is always a
// forward-slash subpath), so the directive simply fails to dedup,
// which is the safe default.
func canonicalize(configRoot, abs string) string {
	rel, err := filepath.Rel(configRoot, abs)
	if err != nil {
		return abs
	}
	return rel
}
