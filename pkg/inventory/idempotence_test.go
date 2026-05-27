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
//     the *resolution* helpers ([ResolveLot] / [NewCostMatcher])
//     that bookOne consults.
//   - costNumberMissing's IsBooked short-circuit makes the deferred-
//     unknowns classification deterministically refuse to drag an
//     already-booked posting into the residual pass.

func TestResolveLot_BookedShortCircuit(t *testing.T) {
	// ResolveLot on an already-booked *ast.Cost extracts the
	// provenance-free lot identity: Number/Currency/Date/Label are
	// transferred, PerUnit/Total are dropped. txnDate is ignored
	// because the booked Cost already carries its acquisition date.
	booked := &ast.Cost{
		Number:   mkAmount(t, "1.024390243902439024390243902439024", "JPY").Number,
		Currency: "JPY",
		Date:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Label:    "lot-X",
		Total:    &ast.Amount{Number: mkAmount(t, "4.2", "JPY").Number, Currency: "JPY"},
	}
	got, finding, err := ResolveLot(booked, mkAmount(t, "4.1", "STOCK"), time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ResolveLot(*ast.Cost): %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if got == nil {
		t.Fatal("ResolveLot: got nil, want a *Lot")
	}
	want := &Lot{
		Number:   mkAmount(t, "1.024390243902439024390243902439024", "JPY").Number,
		Currency: "JPY",
		Date:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Label:    "lot-X",
	}
	if diff := cmp.Diff(want, got, astCmpOpts...); diff != "" {
		t.Errorf("ResolveLot: lot identity mismatch (-want +got):\n%s", diff)
	}

	// Deep-copy of Number: mutating the result must not affect the
	// input. The booked input still carries its provenance fields
	// (which Lot intentionally does not expose).
	newNum := mkAmount(t, "99.0", "JPY").Number
	got.Number.Set(&newNum)
	if booked.Number.String() != "1.024390243902439024390243902439024" {
		t.Errorf("ResolveLot: mutating result.Number leaked into input: %s", booked.Number.String())
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
	bookedLot := *LotFromCost(booked)
	if !m.Matches(bookedLot) {
		t.Error("Matches(self) = false; tight matcher must accept the booked lot identity")
	}
	// ...and reject lots that differ in any identity dimension.
	other := bookedLot
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
