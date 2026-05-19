package inventory

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
)

// The reducer's idempotence story has two layers worth pinning:
//   - The install helpers ([postingResolution.addAlreadyBooked] /
//     [Reducer.resolveCostFromResidual]) only ever run on the parse-
//     tier path, so on a re-run they are guarded by IsBooked checks
//     and never reach the cost-mutating branch. The end-to-end fixed
//     point is exercised by TestReducerRun_OutputIsFixedPoint; the
//     focused tests below pin down the booked-Cost short-circuit on
//     the *resolution* helpers ([ResolveCost] / [NewCostMatcher])
//     that bookOne consults.
//   - costNumberMissing's IsBooked short-circuit makes the deferred-
//     unknowns classification deterministically refuse to drag an
//     already-booked posting into the residual pass.

func TestResolveCost_BookedShortCircuit(t *testing.T) {
	// ResolveCost on an already-booked *ast.Cost must clone (not
	// re-resolve) the value, so the canonical Number is preserved
	// even if a parse-tier resolution would have produced a
	// numerically different (precision-bound) result.
	booked := &ast.Cost{
		Number:   mkAmount(t, "1.024390243902439024390243902439024", "JPY").Number,
		Currency: "JPY",
		Date:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Total:    &ast.Amount{Number: mkAmount(t, "4.2", "JPY").Number, Currency: "JPY"},
	}
	got, finding, err := ResolveCost(booked, mkAmount(t, "4.1", "STOCK"), time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ResolveCost(*ast.Cost): %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	// Pointer identity: a fresh clone must be returned (the booked
	// input is never reused) and its Total Amount must also be a
	// fresh allocation. cmp.Diff cannot express "different pointer,
	// same value", so these two checks stay independent of the value
	// comparison below.
	if got == booked {
		t.Error("ResolveCost: returned the input pointer; want a fresh clone")
	}
	if got.Total == booked.Total {
		t.Error("ResolveCost: Total Amount pointer shared with input; want deep clone")
	}
	// Value equality: a booked input must round-trip its
	// Number/Currency/Date/Label/Total untouched, with txnDate ignored.
	if diff := cmp.Diff(booked, got, astCmpOpts...); diff != "" {
		t.Errorf("ResolveCost: booked Cost not preserved (-want +got):\n%s", diff)
	}
}

func TestNewCostMatcher_BookedTightMatch(t *testing.T) {
	// NewCostMatcher on a booked *ast.Cost must produce a matcher
	// tight enough that, applied to lots in a fresh inventory, it
	// reselects the exact lot identity recorded on the first run.
	// Lot identity per [ast.Cost.Equal] is Number / Currency /
	// Date / Label, so a tight matcher must constrain all four.
	date := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	booked := &ast.Cost{
		Number:   mkAmount(t, "100.00", "USD").Number,
		Currency: "USD",
		Date:     date,
		Label:    "lot-A",
	}
	m := NewCostMatcher(booked, "" /* priceCurrency unused for booked */, nil)
	if !m.HasPerUnit {
		t.Error("HasPerUnit = false; tight matcher must constrain Number")
	}
	if m.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", m.Currency)
	}
	if !m.HasDate || !m.Date.Equal(date) {
		t.Errorf("Date constraint = (%v, %v), want (%v, true)", m.Date, m.HasDate, date)
	}
	if !m.HasLabel || m.Label != "lot-A" {
		t.Errorf("Label constraint = (%q, %v), want (\"lot-A\", true)", m.Label, m.HasLabel)
	}

	// The matcher must accept the exact lot...
	if !m.Matches(*booked) {
		t.Error("Matches(self) = false; tight matcher must accept the booked Cost")
	}
	// ...and reject lots that differ in any identity dimension.
	other := *booked
	other.Label = "lot-B"
	if m.Matches(other) {
		t.Error("Matches(different Label) = true; tight matcher must reject")
	}
}

func TestCostNumberMissing_BookedCostIsConcrete(t *testing.T) {
	// A booked *ast.Cost with PerUnit and Total both nil could occur
	// on a degenerate construction (the production terminal pass is
	// expected to populate at least one). The IsBooked short-circuit
	// pins down the contract that a *Cost is never reported as
	// "missing", so the deferred-cost interpolation path cannot be
	// triggered for already-booked input.
	bookedNoFields := &ast.Cost{
		Currency: "USD",
		Date:     time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	if costNumberMissing(bookedNoFields) {
		t.Error("costNumberMissing returned true for booked *Cost; want false")
	}

	// CostSpec with both fields nil remains "missing" — this is the
	// `{}` (lot-tracked, cost TBD) case the helper is designed for.
	emptySpec := &ast.CostSpec{}
	if !costNumberMissing(emptySpec) {
		t.Error("costNumberMissing returned false for empty CostSpec; want true")
	}
}
