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
// The validator slice is intentionally empty in this step: Step 8b and
// 8c of the plugin-layer refactor append the individual validators.
// Until then the plugin produces only option-parse diagnostics.
func (Plugin) Apply(ctx context.Context, in api.Input) (api.Result, error) {
	// Build per-run state once. The returned BuildResult is not yet
	// consumed because no validators are wired in; the underscore
	// below keeps the package compiling without dropping the work
	// that Step 8b/8c will need.
	build := accountstate.Build(in.Directives)

	// Decode raw options to a typed *options.Values. Malformed values
	// become api.Error entries with code "invalid-option"; unknown keys
	// are silently dropped by FromRaw.
	_, optErrs := options.FromRaw(in.Options)

	validators := []entryValidator{
		// Step 8b/8c will append validators here.
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

	_ = build // silences "declared and not used" until validators consume it
	_ = ctx   // unused today, consume in later steps

	return api.Result{Errors: errs}, nil
}
