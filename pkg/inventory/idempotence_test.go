package inventory

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
)

// The reducer's idempotence story has two layers worth pinning:
//   - resolveBookedCosts / resolveCostFromResidual (the install
//     helpers) only ever run on the parse-tier path, so on a re-run
//     they are guarded by the IsBooked checks at their entry and
//     never reach the cost-mutating branch. The check is exercised
//     end-to-end by TestReducerRun_OutputIsFixedPoint; the focused
//     tests below pin down the booked-Cost short-circuit on the
//     *resolution* helpers (ResolveCost / NewCostMatcher) that
//     bookOne consults.
//   - costNumberMissing's IsBooked short-circuit makes the deferred
//     unknowns classification deterministically refuse to drag an
//     already-booked posting into Pass 3.

func TestResolveBookedCosts_Augmentation(t *testing.T) {
	// An augmentation BookedPosting carries the resolved lot on
	// bp.Lot. resolveBookedCosts must clone it onto the rebuilt
	// posting's Cost.
	lot := &ast.Cost{
		Number:   mkAmount(t, "100.00", "USD").Number,
		Currency: "USD",
		Date:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	txn := &ast.Transaction{
		Postings: []ast.Posting{
			{
				Account: "Assets:Stock",
				Amount:  mkAmountPtr(t, "5", "STOCK"),
				Cost:    &ast.CostSpec{PerUnit: mkAmountPtr(t, "100.00", "USD")},
			},
		},
	}
	originalPtr := &txn.Postings[0]
	booked := []BookedPosting{
		{Source: originalPtr, Account: "Assets:Stock", Lot: lot},
	}

	newBooked, _ := resolveBookedCosts(txn, booked, nil)

	if len(newBooked) != 1 {
		t.Fatalf("resolveBookedCosts: len(newBooked) = %d, want 1", len(newBooked))
	}
	// Source must be rebound away from the original (detached) backing
	// array onto the rebuilt txn.Postings, otherwise Pass 3 would read
	// a stale posting whose Cost is still the parse-tier *ast.CostSpec.
	if newBooked[0].Source == originalPtr {
		t.Error("resolveBookedCosts: Source still points into the original Postings backing array; want rebinding into the rebuilt slice")
	}
	if newBooked[0].Source != &txn.Postings[0] {
		t.Errorf("resolveBookedCosts: newBooked[0].Source = %p, want &txn.Postings[0] = %p",
			newBooked[0].Source, &txn.Postings[0])
	}
	got, ok := txn.Postings[0].Cost.(*ast.Cost)
	if !ok || got == nil {
		t.Fatalf("resolveBookedCosts: Cost holder type = %T, want *ast.Cost", txn.Postings[0].Cost)
	}
	// Fresh allocation: pointer identity must differ even when the
	// underlying value is equal. cmp.Diff cannot express this.
	if got == lot {
		t.Error("resolveBookedCosts: installed *ast.Cost shares pointer with bp.Lot; want fresh clone")
	}
	if diff := cmp.Diff(lot, got, astCmpOpts...); diff != "" {
		t.Errorf("resolveBookedCosts: installed Cost mismatch (-want +got):\n%s", diff)
	}
}

func TestResolveBookedCosts_CashAugmentation(t *testing.T) {
	// A cash augmentation has bp.Lot == nil and no reductions — the
	// posting carries no cost spec at all. resolveBookedCosts must
	// leave the rebuilt posting's Cost untouched (nil on entry, nil
	// on exit).
	txn := &ast.Transaction{
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: mkAmountPtr(t, "100.00", "USD")},
		},
	}
	booked := []BookedPosting{
		{Source: &txn.Postings[0], Account: "Assets:Cash"},
	}

	newBooked, _ := resolveBookedCosts(txn, booked, nil)

	if len(newBooked) != 1 {
		t.Fatalf("resolveBookedCosts: len(newBooked) = %d, want 1", len(newBooked))
	}
	if txn.Postings[0].Cost != nil {
		t.Errorf("resolveBookedCosts: Cost = %T, want nil (cash augmentation must not synthesize a holder)",
			txn.Postings[0].Cost)
	}
}

func TestResolveBookedCosts_CashSentinelSingleLot(t *testing.T) {
	// A zero-value Cost in step.Lot is the cash-position sentinel
	// the inventory layer uses for positions originally stored with
	// Cost == nil. resolveBookedCosts must leave the rebuilt
	// posting's Cost holder untouched (preserving the user's
	// parse-tier *ast.CostSpec) rather than installing a degenerate
	// *ast.Cost{Number:0,Currency:""}.
	specBefore := &ast.CostSpec{}
	txn := &ast.Transaction{
		Postings: []ast.Posting{
			{
				Account: "Assets:Cash",
				Amount:  mkAmountPtr(t, "-5", "USD"),
				Cost:    specBefore,
			},
		},
	}
	step := ReductionStep{Lot: ast.Cost{}}
	step.Units.Set(&txn.Postings[0].Amount.Number)
	booked := []BookedPosting{
		{
			Source:     &txn.Postings[0],
			Account:    "Assets:Cash",
			Reductions: []ReductionStep{step},
		},
	}

	resolveBookedCosts(txn, booked, nil)

	if txn.Postings[0].Cost != specBefore {
		t.Errorf("resolveBookedCosts: Cost holder replaced: got %T, want unchanged *ast.CostSpec",
			txn.Postings[0].Cost)
	}
}

func TestResolveBookedCosts_AlreadyBooked(t *testing.T) {
	// A posting that already carries *ast.Cost must not be touched —
	// this is the second-run fixed-point branch. resolveBookedCosts
	// must dispatch into the alreadyBooked pass-through for every
	// non-multi-lot shape (augmentation and single-lot reduction
	// alike).
	bookedBefore := &ast.Cost{
		Number:   mkAmount(t, "100.00", "USD").Number,
		Currency: "USD",
		Date:     time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	txn := &ast.Transaction{
		Postings: []ast.Posting{
			{
				Account: "Assets:Stock",
				Amount:  mkAmountPtr(t, "-1", "STOCK"),
				Cost:    bookedBefore,
			},
		},
	}
	step := ReductionStep{Lot: ast.Cost{
		Number:   mkAmount(t, "999.99", "USD").Number, // different from bookedBefore
		Currency: "USD",
	}}
	step.Units.Set(&txn.Postings[0].Amount.Number)
	booked := []BookedPosting{
		{
			Source:     &txn.Postings[0],
			Account:    "Assets:Stock",
			Reductions: []ReductionStep{step},
		},
	}

	resolveBookedCosts(txn, booked, nil)

	if txn.Postings[0].Cost != bookedBefore {
		t.Errorf("resolveBookedCosts: booked Cost replaced: got %p, want %p (unchanged pointer)",
			txn.Postings[0].Cost, bookedBefore)
	}
}

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
	got, err := ResolveCost(booked, mkAmount(t, "4.1", "STOCK"), time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ResolveCost(*ast.Cost): %v", err)
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
	m := NewCostMatcher(booked, "" /* priceCurrency unused for booked */)
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
