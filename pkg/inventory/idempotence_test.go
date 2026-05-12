package inventory

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

// The idempotence guards in fillMissingCostFromReductions and
// fillDeferredCost are forward-looking: until the reducer's terminal
// CostSpec→Cost conversion lands in a later slice, no production
// path delivers an *ast.Cost into Posting.Cost. The tests below
// construct that state directly to keep the guards from rotting into
// dead code, and to lock down the contract that a re-run of these
// mutators over already-booked input is a true no-op (no panic, no
// mutation, no error appended to the reducer).

func TestFillMissingCostFromReductions_AlreadyBookedIsNoOp(t *testing.T) {
	// Construct a Posting whose Cost is *ast.Cost (the form produced
	// by the future terminal CostSpec→Cost pass) and a synthetic
	// reduction step. The mutator must observe the booked variant,
	// skip the synthesis step entirely, and leave the AST untouched.
	bookedCost := &ast.Cost{
		Number:   mkAmount(t, "10", "USD").Number,
		Currency: "USD",
		Date:     time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		Label:    "lot-1",
	}
	p := &ast.Posting{
		Account: "Assets:Brokerage",
		Amount:  mkAmountPtr(t, "5", "ACME"),
		Cost:    bookedCost,
	}
	step := ReductionStep{
		Lot: ast.Cost{
			Number:   mkAmount(t, "10", "USD").Number,
			Currency: "USD",
			Date:     time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			Label:    "lot-1",
		},
	}
	step.Units.Set(&p.Amount.Number)

	r := &Reducer{}
	r.fillMissingCostFromReductions(p, []ReductionStep{step})

	if got := p.Cost; got != bookedCost {
		t.Errorf("Cost holder replaced: got %v, want unchanged %v", got, bookedCost)
	}
	if !p.Cost.IsBooked() {
		t.Error("Cost lost its booked identity")
	}
	if pu := p.Cost.GetPerUnit(); pu != nil {
		t.Errorf("guard wrote PerUnit: %v, want nil", pu)
	}
	if tot := p.Cost.GetTotal(); tot != nil {
		t.Errorf("guard wrote Total: %v, want nil", tot)
	}
	if len(r.errs) != 0 {
		t.Errorf("guard appended errors: %v", r.errs)
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
