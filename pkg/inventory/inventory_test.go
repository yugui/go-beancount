package inventory

import (
	"errors"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// mkPosition is a test helper that builds a Position with the given
// units and (optional) cost.
func mkPosition(t *testing.T, num, currency string, cost *Cost) Position {
	t.Helper()
	return Position{
		Units: ast.Amount{Number: decimalVal(t, num), Currency: currency},
		Cost:  cost,
	}
}

// mkCost builds a Cost value for tests.
func mkCost(t *testing.T, num, currency string, date time.Time, label string) *Cost {
	t.Helper()
	return &Cost{
		Number:   decimalVal(t, num),
		Currency: currency,
		Date:     date,
		Label:    label,
	}
}

func TestInventoryAddMergeEqualCost(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()

	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, "lot"))); err != nil {
		t.Fatalf("Add 1: %v", err)
	}
	if err := inv.Add(mkPosition(t, "5", "ACME", mkCost(t, "100", "USD", date, "lot"))); err != nil {
		t.Fatalf("Add 2: %v", err)
	}

	if got := inv.Len(); got != 1 {
		t.Fatalf("Len = %d, want 1 (positions should have merged)", got)
	}
	got := inv.positions[0].Units.Number
	want := decimalVal(t, "15")
	if got.Cmp(&want) != 0 {
		t.Errorf("merged units = %s, want 15", got.String())
	}
}

func TestInventoryAddDistinctLabels(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()

	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, "lot-a"))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "5", "ACME", mkCost(t, "100", "USD", date, "lot-b"))); err != nil {
		t.Fatal(err)
	}

	if got := inv.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2 (labels differ, should not merge)", got)
	}
}

func TestInventoryAddMergesCash(t *testing.T) {
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "100", "USD", nil)); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "50", "USD", nil)); err != nil {
		t.Fatal(err)
	}
	if got := inv.Len(); got != 1 {
		t.Fatalf("Len = %d, want 1", got)
	}
	want := decimalVal(t, "150")
	if got := inv.positions[0].Units.Number; got.Cmp(&want) != 0 {
		t.Errorf("merged cash = %s, want 150", got.String())
	}
}

func TestInventoryAddCashAndLotDoNotMerge(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", nil)); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "5", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	if got := inv.Len(); got != 2 {
		t.Errorf("Len = %d, want 2 (cash and cost should not merge)", got)
	}
}

func TestInventoryAddDropsZeroSum(t *testing.T) {
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "USD", nil)); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "-10", "USD", nil)); err != nil {
		t.Fatal(err)
	}
	if !inv.IsEmpty() {
		t.Errorf("expected inventory to be empty after zero-sum merge, got %d", inv.Len())
	}
}

func TestInventoryAddClonesSource(t *testing.T) {
	inv := NewInventory()
	src := mkPosition(t, "10", "USD", nil)
	if err := inv.Add(src); err != nil {
		t.Fatal(err)
	}
	// Mutating the source decimal must not affect the stored copy.
	newNum := decimalVal(t, "999")
	src.Units.Number.Set(&newNum)

	stored := inv.positions[0].Units.Number
	want := decimalVal(t, "10")
	if stored.Cmp(&want) != 0 {
		t.Errorf("stored position aliased caller's decimal: got %s, want 10", stored.String())
	}
}

func TestInventoryReduceFIFO(t *testing.T) {
	older := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", newer, ""))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", older, ""))); err != nil {
		t.Fatal(err)
	}

	steps, err := inv.Reduce(
		ast.Amount{Number: decimalVal(t, "-12"), Currency: "ACME"},
		CostMatcher{},
		ast.BookingFIFO,
	)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}

	if len(steps) != 2 {
		t.Fatalf("len(steps) = %d, want 2", len(steps))
	}
	// Oldest lot (2024-01-01) must be consumed first and fully.
	if !steps[0].Lot.Date.Equal(older) {
		t.Errorf("steps[0] date = %v, want %v", steps[0].Lot.Date, older)
	}
	want10 := decimalVal(t, "10")
	if steps[0].Units.Cmp(&want10) != 0 {
		t.Errorf("steps[0] units = %s, want 10", steps[0].Units.String())
	}
	// Then 2 taken from the newer lot.
	if !steps[1].Lot.Date.Equal(newer) {
		t.Errorf("steps[1] date = %v, want %v", steps[1].Lot.Date, newer)
	}
	want2 := decimalVal(t, "2")
	if steps[1].Units.Cmp(&want2) != 0 {
		t.Errorf("steps[1] units = %s, want 2", steps[1].Units.String())
	}

	// Residual inventory: the older lot is fully gone, the newer lot
	// has 8 units left.
	if inv.Len() != 1 {
		t.Fatalf("residual Len = %d, want 1", inv.Len())
	}
	wantResid := decimalVal(t, "8")
	if got := inv.positions[0].Units.Number; got.Cmp(&wantResid) != 0 {
		t.Errorf("residual units = %s, want 8", got.String())
	}
	if !inv.positions[0].Cost.Date.Equal(newer) {
		t.Errorf("residual lot date = %v, want %v", inv.positions[0].Cost.Date, newer)
	}

	// ReductionStep.RealizedGain et al must be untouched.
	for i, s := range steps {
		if s.SalePricePer != nil || s.RealizedGain != nil || s.GainCurrency != "" {
			t.Errorf("step %d: gain fields must be zero, got %+v", i, s)
		}
	}
}

func TestInventoryReduceLIFO(t *testing.T) {
	older := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	// Insert older first, newer second.
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", older, ""))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", newer, ""))); err != nil {
		t.Fatal(err)
	}

	steps, err := inv.Reduce(
		ast.Amount{Number: decimalVal(t, "-3"), Currency: "ACME"},
		CostMatcher{},
		ast.BookingLIFO,
	)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}

	if len(steps) != 1 {
		t.Fatalf("len(steps) = %d, want 1", len(steps))
	}
	// LIFO: newest lot consumed first.
	if !steps[0].Lot.Date.Equal(newer) {
		t.Errorf("steps[0] date = %v, want newer %v", steps[0].Lot.Date, newer)
	}
	// Residual: older still full, newer has 7 left; insertion order preserved.
	if inv.Len() != 2 {
		t.Fatalf("residual Len = %d, want 2", inv.Len())
	}
	if !inv.positions[0].Cost.Date.Equal(older) {
		t.Errorf("positions[0] date = %v, want older %v", inv.positions[0].Cost.Date, older)
	}
	wantOlder := decimalVal(t, "10")
	if got := inv.positions[0].Units.Number; got.Cmp(&wantOlder) != 0 {
		t.Errorf("older residual = %s, want 10", got.String())
	}
	wantNewer := decimalVal(t, "7")
	if got := inv.positions[1].Units.Number; got.Cmp(&wantNewer) != 0 {
		t.Errorf("newer residual = %s, want 7", got.String())
	}
}

func TestInventoryReduceStrictSingleMatch(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	// A second lot with a different cost number is filtered out.
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "200", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}

	matcher := CostMatcher{HasPerUnit: true, PerUnit: decimalVal(t, "100"), Currency: "USD"}
	steps, err := inv.Reduce(
		ast.Amount{Number: decimalVal(t, "-4"), Currency: "ACME"},
		matcher,
		ast.BookingStrict,
	)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("len(steps) = %d, want 1", len(steps))
	}
	want4 := decimalVal(t, "4")
	if steps[0].Units.Cmp(&want4) != 0 {
		t.Errorf("consumed = %s, want 4", steps[0].Units.String())
	}
	// The $100 lot should now have 6 remaining; the $200 lot is untouched.
	if inv.Len() != 2 {
		t.Fatalf("Len = %d, want 2", inv.Len())
	}
}

func TestInventoryReduceStrictAmbiguous(t *testing.T) {
	d1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", d1, "a"))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", d2, "b"))); err != nil {
		t.Fatal(err)
	}

	// An empty matcher sees both lots; STRICT must reject that.
	_, err := inv.Reduce(
		ast.Amount{Number: decimalVal(t, "-1"), Currency: "ACME"},
		CostMatcher{},
		ast.BookingStrict,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var invErr Error
	if !errors.As(err, &invErr) {
		t.Fatalf("error type = %T, want inventory.Error", err)
	}
	if invErr.Code != CodeAmbiguousLotMatch {
		t.Errorf("Code = %v, want CodeAmbiguousLotMatch", invErr.Code)
	}
}

// TestInventoryReduceStrictTotalMatch pins upstream beancount's
// "total match" rule: when a STRICT reduction matches more than one
// lot but the requested magnitude equals the sum of all candidate
// magnitudes, the booking is unambiguous (every matching lot is
// consumed in full) and must succeed rather than be rejected as
// CodeAmbiguousLotMatch.
func TestInventoryReduceStrictTotalMatch(t *testing.T) {
	d1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", d1, "first"))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", d2, "second"))); err != nil {
		t.Fatal(err)
	}

	// `{ 100 USD }` matcher: per-unit cost matches both lots; magnitudes
	// (10 + 10) sum to exactly the requested 20.
	matcher := CostMatcher{HasPerUnit: true, PerUnit: decimalVal(t, "100"), Currency: "USD"}
	steps, err := inv.Reduce(
		ast.Amount{Number: decimalVal(t, "-20"), Currency: "ACME"},
		matcher,
		ast.BookingStrict,
	)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("len(steps) = %d, want 2", len(steps))
	}
	want10 := decimalVal(t, "10")
	for i, s := range steps {
		if s.Units.Cmp(&want10) != 0 {
			t.Errorf("step[%d].Units = %s, want 10", i, s.Units.String())
		}
	}
	if !inv.IsEmpty() {
		t.Errorf("inventory not empty after total match: Len = %d", inv.Len())
	}
}

// TestInventoryReduceStrictTotalMatchEmptyMatcher mirrors the bug
// repro's `{}` variant: an empty matcher accepts every lot, so under
// STRICT a multi-lot inventory is normally ambiguous; total match
// still applies when |reduction| equals the sum of all candidate
// magnitudes.
func TestInventoryReduceStrictTotalMatchEmptyMatcher(t *testing.T) {
	d1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", d1, "first"))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", d2, "second"))); err != nil {
		t.Fatal(err)
	}

	steps, err := inv.Reduce(
		ast.Amount{Number: decimalVal(t, "-20"), Currency: "ACME"},
		CostMatcher{},
		ast.BookingStrict,
	)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("len(steps) = %d, want 2", len(steps))
	}
	if !inv.IsEmpty() {
		t.Errorf("inventory not empty after total match: Len = %d", inv.Len())
	}
}

// TestInventoryReduceDefaultTotalMatch confirms BookingDefault gets
// the same total-match treatment as BookingStrict, matching the
// "DEFAULT behaves like STRICT for lot selection" rule.
func TestInventoryReduceDefaultTotalMatch(t *testing.T) {
	d1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "7", "ACME", mkCost(t, "100", "USD", d1, "a"))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "13", "ACME", mkCost(t, "100", "USD", d2, "b"))); err != nil {
		t.Fatal(err)
	}

	steps, err := inv.Reduce(
		ast.Amount{Number: decimalVal(t, "-20"), Currency: "ACME"},
		CostMatcher{HasPerUnit: true, PerUnit: decimalVal(t, "100"), Currency: "USD"},
		ast.BookingDefault,
	)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("len(steps) = %d, want 2", len(steps))
	}
	// FIFO order: 7 from lot a, then 13 from lot b.
	want7 := decimalVal(t, "7")
	want13 := decimalVal(t, "13")
	if steps[0].Units.Cmp(&want7) != 0 {
		t.Errorf("step[0].Units = %s, want 7", steps[0].Units.String())
	}
	if steps[1].Units.Cmp(&want13) != 0 {
		t.Errorf("step[1].Units = %s, want 13", steps[1].Units.String())
	}
	if !inv.IsEmpty() {
		t.Errorf("inventory not empty after total match: Len = %d", inv.Len())
	}
}

func TestInventoryReduceExceedsInventory(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}

	_, err := inv.Reduce(
		ast.Amount{Number: decimalVal(t, "-15"), Currency: "ACME"},
		CostMatcher{},
		ast.BookingFIFO,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var invErr Error
	if !errors.As(err, &invErr) {
		t.Fatalf("error type = %T", err)
	}
	if invErr.Code != CodeReductionExceedsInventory {
		t.Errorf("Code = %v, want CodeReductionExceedsInventory", invErr.Code)
	}
	// The inventory must not have been partially mutated.
	want10 := decimalVal(t, "10")
	if got := inv.positions[0].Units.Number; got.Cmp(&want10) != 0 {
		t.Errorf("inventory partially mutated: units = %s, want 10", got.String())
	}
}

// TestInventoryReduceCashOverflowAllowed pins the lot-identity rule
// for cash candidates: a reducing posting that exceeds the available
// units must NOT be rejected with CodeReductionExceedsInventory when
// every matched candidate is cash (Cost == nil). Currency units are
// fungible — they have no lot identity to consume that does not exist
// — so an overdraft is the balance assertion's concern, not booking's.
// See package doc "# Lot identity" for the full rationale.
func TestInventoryReduceCashOverflowAllowed(t *testing.T) {
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "500", "JPY", nil)); err != nil {
		t.Fatal(err)
	}

	steps, err := inv.Reduce(
		ast.Amount{Number: decimalVal(t, "-1000"), Currency: "JPY"},
		CostMatcher{},
		ast.BookingFIFO,
	)
	if err != nil {
		t.Fatalf("Reduce returned unexpected error: %v", err)
	}
	if len(steps) == 0 {
		t.Fatalf("Reduce returned no steps; want at least one cash consumption step")
	}
	// Each step is a cash step: the zero-value Lot signals "consumed
	// from a cash position". Currency must be empty for cash steps.
	for i, s := range steps {
		if s.Lot.Currency != "" {
			t.Errorf("Reduce step[%d].Lot.Currency = %q, want \"\" (cash)", i, s.Lot.Currency)
		}
	}
}

func TestInventoryReduceNoMatchingLot(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}

	matcher := CostMatcher{HasPerUnit: true, PerUnit: decimalVal(t, "999"), Currency: "USD"}
	_, err := inv.Reduce(
		ast.Amount{Number: decimalVal(t, "-1"), Currency: "ACME"},
		matcher,
		ast.BookingFIFO,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var invErr Error
	if !errors.As(err, &invErr) {
		t.Fatalf("error type = %T", err)
	}
	if invErr.Code != CodeNoMatchingLot {
		t.Errorf("Code = %v, want CodeNoMatchingLot", invErr.Code)
	}
}

func TestInventoryReduceAverageRejected(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	_, err := inv.Reduce(
		ast.Amount{Number: decimalVal(t, "-1"), Currency: "ACME"},
		CostMatcher{},
		ast.BookingAverage,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var invErr Error
	if !errors.As(err, &invErr) {
		t.Fatalf("error type = %T", err)
	}
	if invErr.Code != CodeInvalidBookingMethod {
		t.Errorf("Code = %v, want CodeInvalidBookingMethod", invErr.Code)
	}
}

func TestInventoryReduceNoneRejected(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	_, err := inv.Reduce(
		ast.Amount{Number: decimalVal(t, "-1"), Currency: "ACME"},
		CostMatcher{},
		ast.BookingNone,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var invErr Error
	if !errors.As(err, &invErr) {
		t.Fatalf("error type = %T", err)
	}
	if invErr.Code != CodeInternalError {
		t.Errorf("Code = %v, want CodeInternalError", invErr.Code)
	}
}

func TestInventoryReduceFullyConsumesLot(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	steps, err := inv.Reduce(
		ast.Amount{Number: decimalVal(t, "-10"), Currency: "ACME"},
		CostMatcher{},
		ast.BookingFIFO,
	)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("len(steps) = %d, want 1", len(steps))
	}
	if !inv.IsEmpty() {
		t.Errorf("inventory should be empty after full consumption, Len = %d", inv.Len())
	}
}

func TestInventoryClone(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}

	clone := inv.Clone()
	if !clone.Equal(inv) {
		t.Fatal("clone not equal to original")
	}

	// Mutate the clone's only position in place.
	newNum := decimalVal(t, "5")
	clone.positions[0].Units.Number.Set(&newNum)
	clone.positions[0].Cost.Label = "mutated"

	// Original must remain untouched.
	want10 := decimalVal(t, "10")
	if got := inv.positions[0].Units.Number; got.Cmp(&want10) != 0 {
		t.Errorf("original mutated: units = %s, want 10", got.String())
	}
	if inv.positions[0].Cost.Label != "" {
		t.Errorf("original label mutated: %q", inv.positions[0].Cost.Label)
	}
}

func TestInventoryCloneAfterReduceIndependent(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	clone := inv.Clone()

	// Reducing the clone must not touch the original.
	if _, err := clone.Reduce(
		ast.Amount{Number: decimalVal(t, "-3"), Currency: "ACME"},
		CostMatcher{},
		ast.BookingFIFO,
	); err != nil {
		t.Fatal(err)
	}
	want10 := decimalVal(t, "10")
	if got := inv.positions[0].Units.Number; got.Cmp(&want10) != 0 {
		t.Errorf("original mutated by clone.Reduce: units = %s, want 10", got.String())
	}
}

func TestInventoryEqual(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	a := NewInventory()
	b := NewInventory()

	if !a.Equal(b) {
		t.Error("two empty inventories should be equal")
	}

	if err := a.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	if a.Equal(b) {
		t.Error("inventories with different length should not be equal")
	}

	if err := b.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	if !a.Equal(b) {
		t.Error("matching inventories should be equal")
	}

	// Differ by cost presence.
	c := NewInventory()
	if err := c.Add(mkPosition(t, "10", "ACME", nil)); err != nil {
		t.Fatal(err)
	}
	if a.Equal(c) {
		t.Error("cash vs. non-cash positions should not be equal")
	}
}

func TestInventoryAllIterator(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, "a"))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "5", "WIDGET", nil)); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "20", "ACME", mkCost(t, "200", "USD", date, "b"))); err != nil {
		t.Fatal(err)
	}

	var labels []string
	var commodities []string
	for p := range inv.All() {
		commodities = append(commodities, p.Units.Currency)
		if p.Cost != nil {
			labels = append(labels, p.Cost.Label)
		} else {
			labels = append(labels, "")
		}
	}
	wantCommodities := []string{"ACME", "WIDGET", "ACME"}
	wantLabels := []string{"a", "", "b"}
	if len(commodities) != len(wantCommodities) {
		t.Fatalf("len(commodities) = %d, want %d; commodities = %v", len(commodities), len(wantCommodities), commodities)
	}
	if len(labels) != len(wantLabels) {
		t.Fatalf("len(labels) = %d, want %d; labels = %v", len(labels), len(wantLabels), labels)
	}
	for i, w := range wantCommodities {
		if commodities[i] != w {
			t.Errorf("commodities[%d] = %q, want %q", i, commodities[i], w)
		}
	}
	for i, w := range wantLabels {
		if labels[i] != w {
			t.Errorf("labels[%d] = %q, want %q", i, labels[i], w)
		}
	}
}

func TestInventoryAllIteratorIsClone(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, "lot"))); err != nil {
		t.Fatal(err)
	}
	for p := range inv.All() {
		// Mutating the yielded clone must not affect the inventory.
		newNum := decimalVal(t, "0")
		p.Units.Number.Set(&newNum)
		if p.Cost != nil {
			p.Cost.Label = "mutated"
		}
	}
	want10 := decimalVal(t, "10")
	if got := inv.positions[0].Units.Number; got.Cmp(&want10) != 0 {
		t.Errorf("inventory mutated via All(): units = %s, want 10", got.String())
	}
	if inv.positions[0].Cost.Label != "lot" {
		t.Errorf("inventory label mutated via All(): %q", inv.positions[0].Cost.Label)
	}
}

func TestInventoryGet(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "5", "WIDGET", nil)); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "20", "ACME", mkCost(t, "200", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}

	got := inv.Get("ACME")
	if len(got) != 2 {
		t.Fatalf("Get(ACME) len = %d, want 2", len(got))
	}
	// Insertion order preserved.
	wantFirst := decimalVal(t, "10")
	if got[0].Units.Number.Cmp(&wantFirst) != 0 {
		t.Errorf("Get[0] = %s, want 10", got[0].Units.Number.String())
	}
	wantSecond := decimalVal(t, "20")
	if got[1].Units.Number.Cmp(&wantSecond) != 0 {
		t.Errorf("Get[1] = %s, want 20", got[1].Units.Number.String())
	}

	// Mutating the returned slice should not touch the inventory.
	zero := decimalVal(t, "0")
	got[0].Units.Number.Set(&zero)
	if inv.positions[0].Units.Number.Cmp(&wantFirst) != 0 {
		t.Errorf("Get returned aliased decimal; inventory mutated")
	}
}

// Compile-time assertion that *apd.Decimal is accessible via the
// decimalVal helper; silences unused-import complaints when the
// production decimal type is the only reason for importing apd.
var _ = (*apd.Decimal)(nil)
