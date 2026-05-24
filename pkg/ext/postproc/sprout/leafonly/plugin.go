package leafonly

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

const codeNonLeafAccount = "non-leaf-account"

// Dual registration: beansprout's Python module path and this package's
// Go import path. See doc.go for the rationale.
func init() {
	postproc.Register("beansprout.plugins.leafonly", api.PluginFunc(apply))
	postproc.Register("github.com/yugui/go-beancount/pkg/ext/postproc/sprout/leafonly", api.PluginFunc(apply))
}

// apply emits one diagnostic per [ast.Transaction] posting or [ast.Pad]
// directive whose account is non-leaf. It is diagnostic-only and returns
// nil Result.Directives. See the package godoc for the full behavior,
// upstream attribution, and deviations.
func apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	// referenced is the set of every account mentioned by any directive.
	// Mirroring upstream's `realization.realize`, the account hierarchy is
	// built from references rather than from Open directives alone — a
	// transaction posting on Assets:Cash:USD makes Assets:Cash non-leaf
	// even when Assets:Cash:USD is never opened.
	referenced := make(map[ast.Account]struct{})
	var transactions []*ast.Transaction
	var pads []*ast.Pad

	for _, d := range in.Directives {
		collectReferences(d, referenced)
		switch x := d.(type) {
		case *ast.Transaction:
			transactions = append(transactions, x)
		case *ast.Pad:
			pads = append(pads, x)
		}
	}

	if len(referenced) == 0 {
		return api.Result{}, nil
	}

	// nonLeaf holds every account in `referenced` that has at least one
	// strict descendant also in `referenced`.
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
				Span:     postingDiagSpan(p, tx, in.Directive),
				Message:  fmt.Sprintf("non-leaf account '%s' has transactions or pad directives on it", p.Account),
				Severity: ast.Error,
			})
		}
	}

	for _, pad := range pads {
		if _, bad := nonLeaf[pad.Account]; !bad {
			continue
		}
		diags = append(diags, ast.Diagnostic{
			Code:     codeNonLeafAccount,
			Span:     padDiagSpan(pad, in.Directive),
			Message:  fmt.Sprintf("non-leaf account '%s' has transactions or pad directives on it", pad.Account),
			Severity: ast.Error,
		})
	}

	if len(diags) == 0 {
		return api.Result{}, nil
	}
	return api.Result{Diagnostics: diags}, nil
}

// collectReferences records every account name d mentions in referenced.
// The set of contributing directive types matches the per-directive use
// map upstream's `realization.realize` builds (Open, Close, Balance, Pad,
// Note, Document, Transaction postings, and MetaAccount-typed values inside
// a Custom).
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

// add inserts acct into the set, ignoring the empty Account sentinel.
func add(set map[ast.Account]struct{}, acct ast.Account) {
	if acct == "" {
		return
	}
	set[acct] = struct{}{}
}

// postingDiagSpan picks the most specific available span for an offending
// posting: posting span → enclosing transaction span → plugin directive span.
func postingDiagSpan(p *ast.Posting, tx *ast.Transaction, plug *ast.Plugin) ast.Span {
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

// padDiagSpan picks the most specific available span for an offending pad
// directive: pad span → plugin directive span.
func padDiagSpan(pad *ast.Pad, plug *ast.Plugin) ast.Span {
	var zero ast.Span
	if pad.Span != zero {
		return pad.Span
	}
	if plug != nil {
		return plug.Span
	}
	return zero
}
