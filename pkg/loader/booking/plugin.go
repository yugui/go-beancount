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
// [pkg/inventory.Reducer]. This package is a thin adapter: it forwards
// the plugin input's directive iterator to the reducer, returns the
// booked directive slice as the plugin's replacement contents, and
// translates inventory errors to [ast.Diagnostic] entries.
package booking

import (
	"context"
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/inventory"
)

// Apply runs the booking pass over the input directives and returns
// the booked replacement directives plus any inventory-layer
// diagnostics. The reducer treats the input as immutable and clones
// transactions it needs to mutate; the caller's AST is not disturbed.
//
// Reducer-emitted errors surface as [ast.Diagnostic] entries on the
// returned Result so the load pipeline can continue and surface them
// alongside other validation findings, mirroring the contract used by
// the pad and balance plugins.
func Apply(ctx context.Context, in api.Input) (api.Result, error) {
	if err := ctx.Err(); err != nil {
		return api.Result{}, err
	}
	if in.Directives == nil {
		return api.Result{}, nil
	}

	booked, errs := inventory.NewReducer(in.Directives).Walk(nil)

	diags := make([]ast.Diagnostic, 0, len(errs))
	for _, e := range errs {
		diags = append(diags, ast.Diagnostic{
			Code:    diagnosticCode(e.Code),
			Span:    e.Span,
			Message: bookingMessage(e),
		})
	}

	return api.Result{Directives: booked, Diagnostics: diags}, nil
}

// diagnosticCode maps an inventory error code to the diagnostic Code
// string surfaced through the ledger's diagnostics channel. The shape
// matches the validation package's existing codes where one exists,
// so users see a consistent vocabulary.
func diagnosticCode(c inventory.Code) string {
	switch c {
	case inventory.CodeAmbiguousLotMatch:
		return "ambiguous-lot-match"
	case inventory.CodeNoMatchingLot:
		return "no-matching-lot"
	case inventory.CodeReductionExceedsInventory:
		return "reduction-exceeds-inventory"
	case inventory.CodeAugmentationRequiresCost:
		return "augmentation-requires-cost"
	case inventory.CodeMultipleAutoPostings:
		return "multiple-auto-postings"
	case inventory.CodeUnresolvableInterpolation:
		return "unresolvable-interpolation"
	case inventory.CodeInvalidAutoPosting:
		return "invalid-auto-posting"
	case inventory.CodeMixedInventory:
		return "mixed-inventory"
	case inventory.CodeInvalidBookingMethod:
		return "invalid-booking-method"
	default:
		return "internal-error"
	}
}

// bookingMessage formats the inventory error's message with the
// account prefix folded in, matching the convention used by
// validation.FromInventoryError. The Span on the diagnostic carries
// the source location, so we deliberately omit filename:line:col from
// the message text.
func bookingMessage(e inventory.Error) string {
	if e.Account != "" {
		return fmt.Sprintf("%s: %s", e.Account, e.Message)
	}
	return e.Message
}
