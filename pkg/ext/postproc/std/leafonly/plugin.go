package leafonly

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// codeNonLeafAccount is the diagnostic code emitted for postings against
// a non-leaf account. Upstream uses a Python namedtuple LeafOnlyError
// with no machine-readable category; we pick a stable kebab-case code so
// downstream tooling (lsp, log filters) can match on it without parsing
// the human-readable message.
const codeNonLeafAccount = "non-leaf-account"

// Dual registration: upstream's Python module path and this package's
// Go import path. See doc.go for the rationale.
func init() {
	postproc.Register("beancount.plugins.leafonly", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/std/leafonly", api.PluginFunc(apply))
}

// apply emits one diagnostic per [ast.Transaction] posting whose account
// is non-leaf — i.e. some other referenced account in the ledger has it
// as a strict ancestor. It is diagnostic-only and returns nil
// Result.Directives. See the package godoc for the full behavior and
// upstream attribution.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	// referenced is the set of every account mentioned by any directive
	// in the ledger. Mirroring upstream's `realization.realize`, the
	// account hierarchy is built from references rather than from Open
	// directives alone — a transaction posting on Assets:Cash:USD makes
	// Assets:Cash non-leaf even when Assets:Cash:USD is never opened.
	referenced := make(map[ast.Account]struct{})
	// Materialize transactions on the first pass so we don't re-iterate
	// Directives.
	var transactions []*ast.Transaction

	for _, d := range in.Directives {
		collectReferences(d, referenced)
		if tx, ok := d.(*ast.Transaction); ok {
			transactions = append(transactions, tx)
		}
	}

	if len(referenced) == 0 {
		return api.Result{}, nil
	}

	// nonLeaf holds every account in `referenced` that has at least one
	// strict descendant also in `referenced`. Computed by walking each
	// referenced account's ancestor chain and marking every proper
	// prefix that is itself referenced; this is O(N * depth) and avoids
	// the N^2 cost of pairwise prefix scans.
	nonLeaf := make(map[ast.Account]struct{})
	for acct := range referenced {
		for parent := acct.Parent(); parent != ""; parent = parent.Parent() {
			if _, ok := referenced[parent]; !ok {
				continue
			}
			nonLeaf[parent] = struct{}{}
		}
	}

	if len(nonLeaf) == 0 {
		return api.Result{}, nil
	}

	var diags []ast.Diagnostic
	for _, tx := range transactions {
		for i := range tx.Postings {
			p := &tx.Postings[i]
			if _, bad := nonLeaf[p.Account]; !bad {
				continue
			}
			diags = append(diags, ast.Diagnostic{
				Code:     codeNonLeafAccount,
				Span:     diagSpan(p, tx, in.Directive),
				Message:  fmt.Sprintf("Non-leaf account '%s' has postings on it", p.Account),
				Severity: ast.Error,
			})
		}
	}

	if len(diags) == 0 {
		return api.Result{}, nil
	}
	return api.Result{Diagnostics: diags}, nil
}

// collectReferences records every account name d mentions in
// referenced. The set of contributing directive types matches the
// per-directive use map upstream's `realization.realize` builds (Open,
// Close, Balance, Pad, Note, Document, Transaction postings, and
// MetaAccount-typed values inside a Custom).
func collectReferences(d ast.Directive, referenced map[ast.Account]struct{}) {
	switch x := d.(type) {
	case *ast.Open:
		add(referenced, x.Account)
	case *ast.Close:
		add(referenced, x.Account)
	case *ast.Balance:
		add(referenced, x.Account)
	case *ast.Pad:
		add(referenced, x.Account)
		add(referenced, x.PadAccount)
	case *ast.Note:
		add(referenced, x.Account)
	case *ast.Document:
		add(referenced, x.Account)
	case *ast.Transaction:
		for i := range x.Postings {
			add(referenced, x.Postings[i].Account)
		}
	case *ast.Custom:
		for _, v := range x.Values {
			if v.Kind == ast.MetaAccount {
				add(referenced, ast.Account(v.String))
			}
		}
	}
}

// add inserts acct into the set, ignoring the empty Account (a sentinel
// for "no account on this directive").
func add(set map[ast.Account]struct{}, acct ast.Account) {
	if acct == "" {
		return
	}
	set[acct] = struct{}{}
}

// diagSpan picks the most specific available span for the offending
// posting. Per the package godoc the preference order is the posting's
// own span, then the enclosing transaction's span, and finally the
// triggering plugin directive's span as a last resort.
func diagSpan(p *ast.Posting, tx *ast.Transaction, plug *ast.Plugin) ast.Span {
	var zero ast.Span
	if p.Span != zero {
		return p.Span
	}
	if tx.Span != zero {
		return tx.Span
	}
	if plug != nil {
		return plug.Span
	}
	return zero
}
