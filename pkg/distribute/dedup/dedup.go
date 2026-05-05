// Package dedup builds and queries the active+commented equivalence
// index that the beanfile CLI consults to make the three-way
// write/comment/skip decision (design §2, §7). BuildIndex walks the
// ledger via pkg/ast.LoadFile, records every active directive under
// its destination-relative path key, and re-reads each member file's
// raw bytes to recover commented-out directives via
// pkg/distribute/comment.Extract.
package dedup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/distribute/comment"
)

// DefaultOverrideMetaKey is the built-in metadata key whose entry is
// stripped before AST equality. Callers can override it via
// WithOverrideMetaKey when the user has reconfigured the routing-override
// key in [routes.transaction].
const DefaultOverrideMetaKey = "route-account"

// MatchKind identifies which equivalence rule fired for a query.
type MatchKind int

const (
	// MatchNone indicates the query did not match any indexed entry.
	MatchNone MatchKind = iota
	// MatchAST indicates a Span-and-override-key-stripped AST equality match.
	MatchAST
	// MatchMeta indicates a metadata-key equality match against eqKeys.
	MatchMeta
)

// Index is the queryable equivalence index. Every path argument is a
// Config.Root-relative path (the same form route.Decide returns); the
// implementation does not canonicalize call-site inputs.
type Index interface {
	// InDestination reports whether path already contains an equivalent
	// directive — active OR commented-out — to d.
	InDestination(path string, d ast.Directive, eqKeys []string) (matched bool, kind MatchKind)
	// InOtherActive reports whether any active equivalent of d exists
	// under a path other than path. Commented-out entries elsewhere are
	// ignored by this query, per §7.
	InOtherActive(path string, d ast.Directive, eqKeys []string) (matched bool, kind MatchKind)
	// Add records d under path with the given commented flag so that
	// subsequent queries see it.
	Add(path string, d ast.Directive, commented bool)
}

// Option configures BuildIndex. The slot is generic so future tunables
// can land without a signature change.
type Option func(*options)

type options struct {
	overrideMetaKey string
}

// WithOverrideMetaKey overrides the default metadata key
// (DefaultOverrideMetaKey) stripped from AST equality comparisons. The
// empty string is treated as "not set" — passing it leaves the default
// in place. To disable stripping entirely, callers must explicitly opt
// into a future flag; today, stripping always uses some non-empty key.
func WithOverrideMetaKey(key string) Option {
	return func(o *options) {
		if key == "" {
			return
		}
		o.overrideMetaKey = key
	}
}

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
	entries         []indexEntry
	overrideMetaKey string
}

func (m *memoryIndex) InDestination(path string, d ast.Directive, eqKeys []string) (bool, MatchKind) {
	for _, e := range m.entries {
		if e.path != path {
			continue
		}
		if k := equivalent(e.directive, d, m.overrideMetaKey, eqKeys); k != MatchNone {
			return true, k
		}
	}
	return false, MatchNone
}

func (m *memoryIndex) InOtherActive(path string, d ast.Directive, eqKeys []string) (bool, MatchKind) {
	for _, e := range m.entries {
		if e.path == path || e.commented {
			continue
		}
		if k := equivalent(e.directive, d, m.overrideMetaKey, eqKeys); k != MatchNone {
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
// the ledger walk; callers are expected to feed it through the
// CLI's diagnostic policy. The error return is reserved for
// system-level failures (ctx cancellation, I/O on the root file).
func BuildIndex(ctx context.Context, ledgerRoot, configRoot string, opts ...Option) (Index, []ast.Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	o := options{overrideMetaKey: DefaultOverrideMetaKey}
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

	idx := &memoryIndex{overrideMetaKey: o.overrideMetaKey}
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
