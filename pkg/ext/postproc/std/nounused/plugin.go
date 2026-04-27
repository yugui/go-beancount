package nounused

import (
	"context"
	"fmt"
	"sort"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// codeUnusedAccount is the diagnostic code emitted for an account that
// is opened but never referenced by another directive. Upstream uses a
// Python namedtuple UnusedAccountError with no machine-readable
// category; we pick a stable kebab-case code so downstream tooling
// (lsp, log filters) can match on it without parsing the
// human-readable message.
const codeUnusedAccount = "unused-account"

// Dual registration: upstream's Python module path and this package's
// Go import path. See doc.go for the rationale.
func init() {
	postproc.Register("beancount.plugins.nounused", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/nounused", api.PluginFunc(apply))
}

// apply emits one diagnostic per account that is opened but never
// referenced by any other directive. It is diagnostic-only and returns
// nil Result.Directives. See the package godoc for the full behavior
// and upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	// opens maps each opened account to its first Open directive. The
	// directive is retained so the diagnostic can anchor its Span at
	// the Open's source location. Subsequent Opens on the same account
	// are a separate validator's concern; we keep the first to match
	// upstream's `open_map[entry.account] = entry` pattern, which would
	// also overwrite — but since "opened-but-unused" is independent of
	// duplicate-open detection, the choice is immaterial here.
	opens := map[ast.Account]*ast.Open{}

	// referenced is the set of accounts mentioned by any directive
	// other than Open, mirroring upstream's `referenced_accounts`.
	// The directive types that contribute references are listed in
	// the package godoc and match upstream's
	// `getters.get_entry_accounts` exactly.
	referenced := map[ast.Account]struct{}{}

	for _, d := range in.Directives {
		switch x := d.(type) {
		case *ast.Open:
			if _, exists := opens[x.Account]; !exists {
				opens[x.Account] = x
			}
		case *ast.Close:
			addRef(referenced, x.Account)
		case *ast.Balance:
			addRef(referenced, x.Account)
		case *ast.Note:
			addRef(referenced, x.Account)
		case *ast.Document:
			addRef(referenced, x.Account)
		case *ast.Pad:
			addRef(referenced, x.Account)
			addRef(referenced, x.PadAccount)
		case *ast.Transaction:
			for i := range x.Postings {
				addRef(referenced, x.Postings[i].Account)
			}
		}
	}

	// Collect unused accounts and sort alphabetically for deterministic
	// output. Upstream iterates the open map in insertion order (CPython
	// dict ordering); sorting is the closest portable equivalent and
	// matches the convention used by sibling ports.
	var unused []ast.Account
	for acct := range opens {
		if _, ok := referenced[acct]; ok {
			continue
		}
		unused = append(unused, acct)
	}
	if len(unused) == 0 {
		return api.Result{}, nil
	}
	sort.Slice(unused, func(i, j int) bool { return unused[i] < unused[j] })

	diags := make([]ast.Diagnostic, 0, len(unused))
	for _, acct := range unused {
		diags = append(diags, ast.Diagnostic{
			Code:     codeUnusedAccount,
			Span:     diagSpan(opens[acct], in.Directive),
			Message:  fmt.Sprintf("Unused account '%s'", acct),
			Severity: ast.Error,
		})
	}
	return api.Result{Diagnostics: diags}, nil
}

// addRef inserts acct into the set, ignoring the empty Account (a
// sentinel for "no account on this directive"). This matches the
// pattern in autoaccounts/leafonly.
func addRef(set map[ast.Account]struct{}, acct ast.Account) {
	if acct == "" {
		return
	}
	set[acct] = struct{}{}
}

// diagSpan picks the most actionable span for a diagnostic. The Open
// directive is where the user fixes the issue (delete it, or correct
// the account name), so we prefer it when its Span is non-zero. The
// triggering plugin directive's Span is the fallback, matching the
// convention used by checkcommodity and onecommodity.
func diagSpan(o *ast.Open, trigger *ast.Plugin) ast.Span {
	if o != nil {
		var zero ast.Span
		if o.Span != zero {
			return o.Span
		}
	}
	return spanOf(trigger)
}

// spanOf returns the span of the triggering plugin directive, or the
// zero span when no trigger was supplied.
func spanOf(p *ast.Plugin) ast.Span {
	if p == nil {
		return ast.Span{}
	}
	return p.Span
}
