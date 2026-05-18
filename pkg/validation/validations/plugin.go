// Package validations implements the validations-layer plugin of the
// postprocessing pipeline. It mirrors upstream beancount's
// ops/validation.py: a suite of independent per-directive validators
// sharing an account-lifecycle view built once at the top of Apply.
//
// It is the third and final stage of the validation pipeline
// (pad -> balance -> validations); see the pkg/validation package doc
// for the recommended wiring.
//
// Importing this package has the side effect of registering Apply in
// pkg/ext/postproc under the package's import path, so beancount
// `plugin "github.com/yugui/go-beancount/pkg/validation/validations"`
// directives can activate it.
package validations

import (
	"context"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation/internal/accountstate"
)

// Apply runs the validations-layer checks against the input ledger:
// open/close consistency, postings against active accounts, allowed
// currency constraints, and transaction balancing. It never mutates
// the ledger; it returns a Result with a nil Directives field so the
// runner preserves the input verbatim, and reports issues only via
// the Diagnostics slice.
func Apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}

	// Build per-run state once and share it across the validators that
	// need an open/close view of the ledger.
	build := accountstate.Build(in.Directives, in.Options)

	validators := []entryValidator{
		newOpenClose(build),
		newActiveAccounts(build.State),
		newCurrencyConstraints(build.State),
		newTransactionBalances(in.Options),
	}

	diags := append([]ast.Diagnostic(nil), build.Diagnostics...)
	if in.Directives != nil {
		for _, d := range in.Directives {
			for _, v := range validators {
				diags = append(diags, v.ProcessEntry(d)...)
			}
		}
	}
	for _, v := range validators {
		diags = append(diags, v.Finish()...)
	}

	return api.Result{Diagnostics: diags}, nil
}

// init registers Apply in the global registry so that, once this
// package is imported, a beancount `plugin "..."` directive can
// activate it by name.
func init() {
	postproc.Register("github.com/yugui/go-beancount/pkg/validation/validations", api.PluginFunc(Apply))
}
