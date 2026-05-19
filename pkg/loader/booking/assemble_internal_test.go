package booking

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
	"github.com/yugui/go-beancount/pkg/inventory"
)

// TestAssembleResult_NoErrorPassesThrough pins the happy path: when
// the reducer halts cleanly, the booked directives and collected
// findings flow into the [api.Result] verbatim.
func TestAssembleResult_NoErrorPassesThrough(t *testing.T) {
	open := &ast.Open{Account: "Assets:A"}
	directives := []ast.Directive{open}
	diags := []ast.Diagnostic{{Code: inventory.CodeNoMatchingLot, Message: "boom"}}

	got := assembleResult(directives, diags, nil)
	want := api.Result{Directives: directives, Diagnostics: diags}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("assembleResult diff (-want +got):\n%s", diff)
	}
}

// TestAssembleResult_SystemErrorClearsPartialState pins the bug-halt
// path: a non-Diagnostic error from the reducer discards every
// partial output and surfaces as the sole [inventory.CodeInternalError]
// diagnostic. The Directives slice is non-nil but empty so downstream
// plugins see "ledger zeroed", not "no rewrite", and therefore avoid
// cascading false positives from a truncated booking.
func TestAssembleResult_SystemErrorClearsPartialState(t *testing.T) {
	// Partial state the reducer might have collected before halting.
	partialBooked := []ast.Directive{&ast.Open{Account: "Assets:A"}}
	partialDiags := []ast.Diagnostic{{Code: inventory.CodeNoMatchingLot, Message: "ignored"}}
	err := errors.New("inventory.bookOne: classify returned an unknown kind")

	got := assembleResult(partialBooked, partialDiags, err)

	if got.Directives == nil {
		t.Error("Directives = nil, want non-nil empty slice (downstream must see 'ledger zeroed', not 'no rewrite')")
	}
	if len(got.Directives) != 0 {
		t.Errorf("Directives len = %d, want 0 (partial booking must be discarded)", len(got.Directives))
	}
	if len(got.Diagnostics) != 1 {
		t.Fatalf("Diagnostics len = %d, want 1 (sole bug report)", len(got.Diagnostics))
	}
	d := got.Diagnostics[0]
	if d.Code != inventory.CodeInternalError {
		t.Errorf("Diagnostics[0].Code = %q, want %q", d.Code, inventory.CodeInternalError)
	}
	if want := "booking pass halted: " + err.Error(); d.Message != want {
		t.Errorf("Diagnostics[0].Message = %q, want %q", d.Message, want)
	}
}

// TestAssembleResult_SystemErrorDiscardsPriorFindings exists to make
// the policy explicit: partial findings collected before the halt
// look legitimate but originate from an aborted pass; keeping them
// would let the bug report (CodeInternalError) compete with — and
// be buried under — what looks like a long list of fixable user
// errors. The single bug report is the deliverable.
func TestAssembleResult_SystemErrorDiscardsPriorFindings(t *testing.T) {
	priorDiags := []ast.Diagnostic{
		{Code: inventory.CodeAmbiguousLotMatch, Message: "looks real but originates from aborted pass"},
		{Code: inventory.CodeNoMatchingLot, Message: "same"},
		{Code: inventory.CodeUnresolvableInterpolation, Message: "same"},
	}
	got := assembleResult(nil, priorDiags, errors.New("invariant violation"))
	for _, d := range got.Diagnostics {
		if d.Code != inventory.CodeInternalError {
			t.Errorf("found non-bug diagnostic in halt output: %+v", d)
		}
	}
}
