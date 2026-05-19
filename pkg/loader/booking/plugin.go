// Package booking adapts the inventory layer's booking pass to the
// post-processor plugin interface. The plugin is the Go equivalent of
// upstream beancount's `booking.book` step: it routes the ledger
// through the inventory reducer so reductions resolve against existing
// lots and auto-balanced postings receive their inferred amounts before
// user plugins, balance assertions, and validations observe the AST.
//
// The booking work — including cloning transactions whose postings the
// reducer needs to mutate, filling auto-posting Amounts, interpolating
// deferred per-unit costs, and synthesizing a multi-lot reduction's
// Cost.Total when the user wrote no concrete number — lives in
// [inventory.Reducer]. This package is a thin adapter: it forwards
// the plugin input's directive iterator to the reducer and surfaces
// the reducer's [ast.Diagnostic] outputs as the plugin's Result.
//
// One translation does happen here: a non-Diagnostic error from the
// reducer (a booking-pass implementation bug or invariant violation)
// halts the pass mid-iteration, which means both the partial
// booked-directive prefix and any user findings collected before the
// halt would mislead downstream plugins (a truncated ledger produces
// cascading false positives that bury the actual bug). The adapter
// therefore discards the partial state and returns a Result whose
// Directives is empty and whose Diagnostics is the single
// [ast.Diagnostic] with [inventory.CodeInternalError] reporting the
// bug — so the user gets one unambiguous "please report this"
// finding and downstream plugins run on an empty ledger that has
// nothing to complain about.
package booking

import (
	"context"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/inventory"
)

// Apply runs the booking pass over the input directives and returns
// the booked replacement directives plus any inventory-layer
// diagnostics. The reducer treats the input as immutable and clones
// transactions it needs to mutate; the caller's AST is not disturbed.
//
// Reducer-emitted diagnostics surface on the returned Result so the
// load pipeline can continue and report them alongside other
// validation findings, mirroring the contract used by the pad and
// balance plugins. A booking-pass implementation bug (non-Diagnostic
// error from [inventory.Reducer.Walk]) instead clears the partial
// state and reports only an [inventory.CodeInternalError] Diagnostic;
// see the package doc for the rationale.
func Apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	booked, diags, err := inventory.NewReducerWithOptions(in.Directives, in.Options).Walk(nil)
	return assembleResult(booked, diags, err), nil
}

// assembleResult shapes the booking adapter's [api.Result] from the
// reducer's three-way [inventory.Reducer.Walk] return.
//
// In the no-error path the partial outputs pass through verbatim. On
// a non-nil err — always a booking-pass implementation bug, never a
// user-input finding — the partial directives and diagnostics are
// discarded and the bug surfaces as the sole
// [inventory.CodeInternalError] [ast.Diagnostic]. The returned
// Directives is a non-nil empty slice so a downstream plugin that
// distinguishes "no rewrite" (nil) from "ledger zeroed" (empty) sees
// the latter and operates on an empty ledger rather than the original
// un-booked input.
//
// The function is package-internal but exercised directly by tests
// because the reducer's system-error paths are all invariant
// violations or apd arithmetic that valid grammar inputs cannot
// trigger; the public Apply API offers no realistic way to exercise
// the system-error branch.
func assembleResult(booked []ast.Directive, diags []ast.Diagnostic, err error) api.Result {
	if err != nil {
		return api.Result{
			Directives: []ast.Directive{},
			Diagnostics: []ast.Diagnostic{{
				Code:    inventory.CodeInternalError,
				Message: "booking pass halted: " + err.Error(),
			}},
		}
	}
	return api.Result{Directives: booked, Diagnostics: diags}
}
