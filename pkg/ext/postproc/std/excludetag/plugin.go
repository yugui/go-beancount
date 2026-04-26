package excludetag

import (
	"context"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// defaultTag is the tag used when the plugin directive carries an
// empty Config string. The literal "virtual" is the upstream default
// (`EXCLUDED_TAG = "virtual"` in beancount/plugins/exclude_tag.py),
// stored without the leading `#` since [ast.Transaction.Tags] is the
// bare-name form.
const defaultTag = "virtual"

// Dual registration: upstream's Python module path (with underscore)
// and this package's Go import path (no underscore, since Go package
// identifiers cannot contain underscores). See doc.go for the
// rationale.
func init() {
	postproc.Register("beancount.plugins.exclude_tag", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/excludetag", api.PluginFunc(apply))
}

// apply walks every directive once, dropping any [ast.Transaction]
// whose Tags slice contains the configured tag and copying every other
// directive through unchanged. See the package godoc for the full
// behavior, configuration, and upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	tag := in.Config
	if tag == "" {
		tag = defaultTag
	}

	// Materialize the input once so we can detect the no-op case
	// (nothing dropped) without committing to an output allocation up
	// front, and so that — when we do drop — we can size the output
	// slice exactly. Mirrors the materialize-then-walk shape used by
	// closetree and implicitprices.
	var all []ast.Directive
	for _, d := range in.Directives {
		// Poll cancellation per directive so large ledgers observe
		// context cancellation promptly rather than only at entry.
		if err := ctx.Err(); err != nil {
			return api.Result{}, err
		}
		all = append(all, d)
	}

	dropCount := 0
	for _, d := range all {
		if hasTag(d, tag) {
			dropCount++
		}
	}
	if dropCount == 0 {
		// Nothing was filtered: signal "no change" so the runner does
		// not replace the ledger. Mirrors the convention used by
		// closetree and implicitprices.
		return api.Result{}, nil
	}

	// Non-nil even when every directive was dropped: the
	// Result.Directives contract distinguishes nil "no change" from
	// non-nil empty "clear", and after filtering we always intend the
	// "clear" sense.
	out := make([]ast.Directive, 0, len(all)-dropCount)
	for _, d := range all {
		if hasTag(d, tag) {
			continue
		}
		out = append(out, d)
	}
	return api.Result{Directives: out}, nil
}

// hasTag reports whether d is a transaction whose Tags slice contains
// tag as a whole-string member. Non-transaction directives always
// return false (they are unaffected by tag filtering). Comparison is
// case-sensitive and full-string — substrings do not match.
func hasTag(d ast.Directive, tag string) bool {
	tx, ok := d.(*ast.Transaction)
	if !ok {
		return false
	}
	for _, t := range tx.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
