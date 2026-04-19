// Package validations implements the validations-layer plugin of the
// postprocessing pipeline. It mirrors upstream beancount's
// ops/validation.py: a suite of independent per-directive validators
// sharing an account-lifecycle view built once at the top of Apply.
//
// It is the third and final stage of the validation pipeline
// (pad -> balance -> validations); see the pkg/validation package doc
// for the recommended wiring.
package validations

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/postproc"
	"github.com/yugui/go-beancount/pkg/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation/internal/accountstate"
)

// Plugin runs the validations-layer checks against the input ledger:
// open/close consistency, postings against active accounts, allowed
// currency constraints, and transaction balancing. It never mutates
// the ledger; it returns a Result with a nil Directives field so the
// runner preserves the input verbatim, and reports issues only via
// the Errors slice.
var Plugin api.PluginFunc = func(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}

	// Build per-run state once and share it across the validators that
	// need an open/close view of the ledger.
	build := accountstate.Build(in.Directives)

	// Decode raw options to a typed *options.Values. Malformed values
	// become api.Error entries with code "invalid-option"; unknown keys
	// are silently dropped by FromRaw.
	opts, optErrs := options.FromRaw(in.Options)

	validators := []entryValidator{
		newOpenClose(build),
		newActiveAccounts(build.State),
		newCurrencyConstraints(build.State),
		newTransactionBalances(opts),
	}

	var errs []api.Error
	for _, perr := range optErrs {
		errs = append(errs, api.Error{
			Code:    "invalid-option",
			Span:    perr.Span,
			Message: fmt.Sprintf("invalid option %q: %v", perr.Key, perr.Err),
		})
	}

	if in.Directives != nil {
		for _, d := range in.Directives {
			for _, v := range validators {
				errs = append(errs, v.ProcessEntry(d)...)
			}
		}
	}
	for _, v := range validators {
		errs = append(errs, v.Finish()...)
	}

	return api.Result{Errors: errs}, nil
}

// init registers Plugin in the global registry so that, once this
// package is imported, a beancount `plugin "..."` directive can
// activate it by name.
func init() {
	postproc.Register("github.com/yugui/go-beancount/pkg/validation/validations", Plugin)
}
