// Package validations implements the validations-layer plugin of the
// postprocessing pipeline. It mirrors upstream beancount's
// ops/validation.py: a suite of independent per-directive validators
// sharing an account-lifecycle view built once at the top of Apply.
//
// The plugin is additive in the current refactor step: it is not yet
// wired into the postproc registry and the legacy validation.Check()
// path still runs in parallel. Step 11 of the refactor retires the
// legacy path.
package validations

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/internal/options"
	"github.com/yugui/go-beancount/pkg/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation/internal/accountstate"
)

// Plugin runs the validations-layer checks: open/close accounting,
// active-account enforcement, allowed-currency constraints, and
// transaction balancing. It mirrors upstream beancount's
// ops/validation.py, split into independent entryValidator
// implementations so each check is unit-testable in isolation.
type Plugin struct{}

// Name returns the canonical plugin name. The string matches the Go
// package import path so plugin directives can reference the plugin by
// its fully-qualified identity.
func (Plugin) Name() string {
	return "github.com/yugui/go-beancount/pkg/validation/validations"
}

// Apply constructs per-run account state, fans each directive out to the
// registered entryValidator list, and collects their diagnostics.
// Apply never mutates the ledger; it returns Result.Directives == nil so
// the runner preserves the input verbatim.
//
// Validators populated in this step:
//   - openClose: surfaces duplicate-open diagnostics from the initial
//     Build pass.
//   - activeAccounts: enforces open-window references for every
//     directive type the legacy requireOpen covered.
//   - currencyConstraints: enforces the allowed-currency list declared
//     by each account's open directive.
//   - transactionBalances: verifies each transaction balances per
//     currency and contains at most one auto-posting.
//
// Additional validators (balance, pad) will be appended in subsequent
// steps of the plugin-layer refactor.
func (Plugin) Apply(ctx context.Context, in api.Input) (api.Result, error) {
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
