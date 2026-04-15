package inventory

import (
	"errors"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

// mkAmount builds an ast.Amount from a decimal string and a currency.
func mkAmount(t *testing.T, num, currency string) ast.Amount {
	t.Helper()
	return ast.Amount{Number: decimalVal(t, num), Currency: currency}
}

// mkAmountPtr is the pointer form used for posting.Amount / posting.Cost fields.
func mkAmountPtr(t *testing.T, num, currency string) *ast.Amount {
	t.Helper()
	a := mkAmount(t, num, currency)
	return &a
}

// mkPosting builds a minimal ast.Posting for booking tests.
func mkPosting(t *testing.T, account string, units ast.Amount, cost *ast.CostSpec, price *ast.PriceAnnotation) *ast.Posting {
	t.Helper()
	a := units
	return &ast.Posting{
		Account: ast.Account(account),
		Amount:  &a,
		Cost:    cost,
		Price:   price,
	}
}

// mkAutoPosting builds an auto-posting (Amount == nil).
func mkAutoPosting(account string) *ast.Posting {
	return &ast.Posting{Account: ast.Account(account)}
}

// --- classify ------------------------------------------------------------

func TestClassify_EmptyInventoryIsAugment(t *testing.T) {
	inv := NewInventory()
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), nil, nil)
	if got := classify(inv, p, ast.BookingStrict); got != kindAugment {
		t.Errorf("classify(empty) = %v, want kindAugment", got)
	}
}

func TestClassify_NilInventoryIsAugment(t *testing.T) {
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), nil, nil)
	if got := classify(nil, p, ast.BookingStrict); got != kindAugment {
		t.Errorf("classify(nil) = %v, want kindAugment", got)
	}
}

func TestClassify_SameSignIsAugment(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "100", "ACME", mkCost(t, "10", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), nil, nil)
	if got := classify(inv, p, ast.BookingStrict); got != kindAugment {
		t.Errorf("classify(same sign) = %v, want kindAugment", got)
	}
}

func TestClassify_OppositeSignIsReduce(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "100", "ACME", mkCost(t, "10", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), nil, nil)
	if got := classify(inv, p, ast.BookingStrict); got != kindReduce {
		t.Errorf("classify(opposite sign) = %v, want kindReduce", got)
	}
}

func TestClassify_BookingNoneAlwaysAugments(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "100", "ACME", mkCost(t, "10", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	// Opposite sign, but BookingNone must still route to augment.
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), nil, nil)
	if got := classify(inv, p, ast.BookingNone); got != kindAugment {
		t.Errorf("classify(NONE, opposite) = %v, want kindAugment", got)
	}
}

func TestClassify_HintFiltersByCostCurrencyUSD(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "10000", "JPY", date, ""))); err != nil {
		t.Fatal(err)
	}
	// -5 ACME @ 110 USD: no cost spec, price hint = USD. USD lot exists
	// with opposite sign, so this should reduce.
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), nil,
		&ast.PriceAnnotation{Amount: mkAmount(t, "110", "USD"), IsTotal: false})
	if got := classify(inv, p, ast.BookingStrict); got != kindReduce {
		t.Errorf("classify(USD hint) = %v, want kindReduce", got)
	}
}

func TestClassify_HintFiltersByCostCurrencyJPY(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "10000", "JPY", date, ""))); err != nil {
		t.Fatal(err)
	}
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), nil,
		&ast.PriceAnnotation{Amount: mkAmount(t, "11000", "JPY"), IsTotal: false})
	if got := classify(inv, p, ast.BookingStrict); got != kindReduce {
		t.Errorf("classify(JPY hint) = %v, want kindReduce", got)
	}
}

func TestClassify_NoHintFallsBackToCommodity(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "10000", "JPY", date, ""))); err != nil {
		t.Fatal(err)
	}
	// +5 ACME, no cost spec, no price. Commodity-only lookup: same-sign
	// lots exist, so augment.
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), nil, nil)
	if got := classify(inv, p, ast.BookingStrict); got != kindAugment {
		t.Errorf("classify(no hint, same sign) = %v, want kindAugment", got)
	}
}

// TestClassify_EmptyCostSpecWithPriceHint verifies that a posting with
// an empty `{}` cost spec plus a price annotation still routes the
// cost-currency hint (taken from the price annotation) to the matcher.
// It exercises both classify (the hint gate that filters candidate lots
// by cost currency) and bookReduce (which forwards the hint to the
// matcher), so the matcher's empty-spec fallback in matcher.go is
// reachable from bookOne.
func TestClassify_EmptyCostSpecWithPriceHint(t *testing.T) {
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "10000", "JPY", date, ""))); err != nil {
		t.Fatal(err)
	}
	// -5 ACME {} @ 110 USD: empty spec + price hint = USD. Under STRICT
	// booking, classify must route to reduce (the JPY lot is filtered
	// out by the hint) and the matcher must pick only the USD lot.
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"),
		&ast.CostSpec{},
		&ast.PriceAnnotation{Amount: mkAmount(t, "110", "USD"), IsTotal: false})

	if got := classify(inv, p, ast.BookingStrict); got != kindReduce {
		t.Fatalf("classify(empty spec + USD price) = %v, want kindReduce", got)
	}

	bp, errs := bookOne(inv, p, ast.BookingStrict, date, false)
	if len(errs) > 0 {
		t.Fatalf("bookOne errors: %v", errs)
	}
	if len(bp.Reductions) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(bp.Reductions))
	}
	if bp.Reductions[0].Lot.Currency != "USD" {
		t.Errorf("Reductions[0].Lot.Currency = %q, want USD", bp.Reductions[0].Lot.Currency)
	}
	// Verify the USD lot was partially consumed (5 remaining) and the
	// JPY lot is untouched (still 10).
	var usd, jpy string
	for _, pos := range inv.positions {
		switch pos.Cost.Currency {
		case "USD":
			usd = pos.Units.Number.String()
		case "JPY":
			jpy = pos.Units.Number.String()
		}
	}
	if usd != "5" {
		t.Errorf("USD lot remaining = %s, want 5", usd)
	}
	if jpy != "10" {
		t.Errorf("JPY lot remaining = %s, want 10 (untouched)", jpy)
	}
}

// --- bookOne: augmentation ----------------------------------------------

func TestBookOne_AugmentPerUnitCost(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	spec := &ast.CostSpec{PerUnit: mkAmountPtr(t, "100", "USD")}
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), spec, nil)

	bp, errs := bookOne(inv, p, ast.BookingStrict, txnDate, false)
	if len(errs) > 0 {
		t.Fatalf("bookOne returned errors: %v", errs)
	}
	if bp.Source != p {
		t.Errorf("Source = %p, want %p (alias)", bp.Source, p)
	}
	if bp.Lot == nil {
		t.Fatal("BookedPosting.Lot should be set for augmentation with cost")
	}
	want := decimalVal(t, "100")
	if bp.Lot.Number.Cmp(&want) != 0 {
		t.Errorf("Lot.Number = %s, want 100", bp.Lot.Number.String())
	}
	if bp.Lot.Currency != "USD" {
		t.Errorf("Lot.Currency = %q, want USD", bp.Lot.Currency)
	}
	if inv.Len() != 1 {
		t.Fatalf("inventory Len = %d, want 1", inv.Len())
	}
	posUnits := inv.positions[0].Units.Number
	wantUnits := decimalVal(t, "5")
	if posUnits.Cmp(&wantUnits) != 0 {
		t.Errorf("position Units = %s, want 5", posUnits.String())
	}
}

func TestBookOne_AugmentCombinedCost(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	spec := &ast.CostSpec{
		PerUnit: mkAmountPtr(t, "100", "USD"),
		Total:   mkAmountPtr(t, "50", "USD"),
	}
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), spec, nil)

	bp, errs := bookOne(inv, p, ast.BookingStrict, txnDate, false)
	if len(errs) > 0 {
		t.Fatalf("bookOne returned errors: %v", errs)
	}
	if bp.Lot == nil {
		t.Fatal("Lot should be set")
	}
	want := decimalVal(t, "110")
	if bp.Lot.Number.Cmp(&want) != 0 {
		t.Errorf("Lot.Number = %s, want 110", bp.Lot.Number.String())
	}
}

func TestBookOne_AugmentTotalCost(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	spec := &ast.CostSpec{Total: mkAmountPtr(t, "500", "USD")}
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), spec, nil)

	bp, errs := bookOne(inv, p, ast.BookingStrict, txnDate, false)
	if len(errs) > 0 {
		t.Fatalf("bookOne returned errors: %v", errs)
	}
	if bp.Lot == nil {
		t.Fatal("Lot should be set")
	}
	want := decimalVal(t, "100")
	if bp.Lot.Number.Cmp(&want) != 0 {
		t.Errorf("Lot.Number = %s, want 100", bp.Lot.Number.String())
	}
}

func TestBookOne_AugmentCash(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	p := mkPosting(t, "Assets:Cash", mkAmount(t, "100", "USD"), nil, nil)

	bp, errs := bookOne(inv, p, ast.BookingStrict, txnDate, false)
	if len(errs) > 0 {
		t.Fatalf("bookOne returned errors: %v", errs)
	}
	if bp.Lot != nil {
		t.Errorf("Lot should be nil for cash augmentation, got %+v", bp.Lot)
	}
	if inv.Len() != 1 {
		t.Fatalf("inventory Len = %d, want 1", inv.Len())
	}
	if inv.positions[0].Cost != nil {
		t.Errorf("stored position Cost should be nil for cash")
	}
}

func TestBookOne_AugmentEmptyCostSpecErrors(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	spec := &ast.CostSpec{} // empty "{}"
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), spec, nil)

	_, errs := bookOne(inv, p, ast.BookingStrict, txnDate, false)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if errs[0].Code != CodeAugmentationRequiresCost {
		t.Errorf("error code = %v, want CodeAugmentationRequiresCost", errs[0].Code)
	}
}

func TestBookOne_AugmentDateDefaultsToTxnDate(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	spec := &ast.CostSpec{PerUnit: mkAmountPtr(t, "100", "USD")} // no Date
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), spec, nil)

	bp, errs := bookOne(inv, p, ast.BookingStrict, txnDate, false)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !bp.Lot.Date.Equal(txnDate) {
		t.Errorf("Lot.Date = %v, want %v (txn date default)", bp.Lot.Date, txnDate)
	}
}

func TestBookOne_AugmentLabelCopied(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	spec := &ast.CostSpec{
		PerUnit: mkAmountPtr(t, "100", "USD"),
		Label:   "lot-A",
	}
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), spec, nil)

	bp, errs := bookOne(inv, p, ast.BookingStrict, txnDate, false)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if bp.Lot.Label != "lot-A" {
		t.Errorf("Lot.Label = %q, want lot-A", bp.Lot.Label)
	}
}

func TestBookOne_InferredAutoFlagPropagates(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	p := mkPosting(t, "Assets:Cash", mkAmount(t, "-500", "USD"), nil, nil)

	bp, errs := bookOne(inv, p, ast.BookingStrict, txnDate, true)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !bp.InferredAuto {
		t.Errorf("InferredAuto = false, want true")
	}
}

// --- bookOne: reduction --------------------------------------------------

func TestBookOne_ReduceFIFO(t *testing.T) {
	oldDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", oldDate, ""))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "120", "USD", newDate, ""))); err != nil {
		t.Fatal(err)
	}

	spec := &ast.CostSpec{} // empty matcher
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), spec, nil)

	bp, errs := bookOne(inv, p, ast.BookingFIFO, newDate, false)
	if len(errs) > 0 {
		t.Fatalf("bookOne errors: %v", errs)
	}
	if len(bp.Reductions) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(bp.Reductions))
	}
	// FIFO consumes the oldest lot (100 USD).
	want := decimalVal(t, "100")
	if bp.Reductions[0].Lot.Number.Cmp(&want) != 0 {
		t.Errorf("reduced lot cost = %s, want 100", bp.Reductions[0].Lot.Number.String())
	}
	wantUnits := decimalVal(t, "5")
	if bp.Reductions[0].Units.Cmp(&wantUnits) != 0 {
		t.Errorf("reduced units = %s, want 5", bp.Reductions[0].Units.String())
	}
}

func TestBookOne_ReduceStrictWithLabel(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, "lot-a"))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "120", "USD", date, "lot-b"))); err != nil {
		t.Fatal(err)
	}

	spec := &ast.CostSpec{Label: "lot-a"}
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), spec, nil)

	bp, errs := bookOne(inv, p, ast.BookingStrict, date, false)
	if len(errs) > 0 {
		t.Fatalf("bookOne errors: %v", errs)
	}
	if len(bp.Reductions) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(bp.Reductions))
	}
	if bp.Reductions[0].Lot.Label != "lot-a" {
		t.Errorf("reduced lot label = %q, want lot-a", bp.Reductions[0].Lot.Label)
	}
	// The other lot must remain untouched.
	if inv.Len() != 2 {
		t.Errorf("inventory Len = %d, want 2", inv.Len())
	}
	var remainingA, remainingB string
	for _, pos := range inv.positions {
		switch pos.Cost.Label {
		case "lot-a":
			remainingA = pos.Units.Number.String()
		case "lot-b":
			remainingB = pos.Units.Number.String()
		}
	}
	if remainingA != "5" {
		t.Errorf("lot-a remaining = %s, want 5", remainingA)
	}
	if remainingB != "10" {
		t.Errorf("lot-b remaining = %s, want 10", remainingB)
	}
}

func TestBookOne_ReduceWithPerUnitPrice_RealizedGain(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), &ast.CostSpec{},
		&ast.PriceAnnotation{Amount: mkAmount(t, "110", "USD"), IsTotal: false})

	bp, errs := bookOne(inv, p, ast.BookingStrict, date, false)
	if len(errs) > 0 {
		t.Fatalf("bookOne errors: %v", errs)
	}
	if len(bp.Reductions) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(bp.Reductions))
	}
	step := bp.Reductions[0]
	if step.SalePricePer == nil {
		t.Fatal("SalePricePer should be set")
	}
	wantSP := decimalVal(t, "110")
	if step.SalePricePer.Cmp(&wantSP) != 0 {
		t.Errorf("SalePricePer = %s, want 110", step.SalePricePer.String())
	}
	if step.RealizedGain == nil {
		t.Fatal("RealizedGain should be set")
	}
	// (110 - 100) * 5 = 50.
	wantGain := decimalVal(t, "50")
	if step.RealizedGain.Cmp(&wantGain) != 0 {
		t.Errorf("RealizedGain = %s, want 50", step.RealizedGain.String())
	}
	if step.GainCurrency != "USD" {
		t.Errorf("GainCurrency = %q, want USD", step.GainCurrency)
	}
}

func TestBookOne_ReduceWithTotalPrice_RealizedGain(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	// -5 ACME @@ 550 USD: total sale price, per-unit = 110.
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), &ast.CostSpec{},
		&ast.PriceAnnotation{Amount: mkAmount(t, "550", "USD"), IsTotal: true})

	bp, errs := bookOne(inv, p, ast.BookingStrict, date, false)
	if len(errs) > 0 {
		t.Fatalf("bookOne errors: %v", errs)
	}
	if len(bp.Reductions) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(bp.Reductions))
	}
	step := bp.Reductions[0]
	wantSP := decimalVal(t, "110")
	if step.SalePricePer == nil || step.SalePricePer.Cmp(&wantSP) != 0 {
		t.Errorf("SalePricePer = %v, want 110", step.SalePricePer)
	}
	wantGain := decimalVal(t, "50")
	if step.RealizedGain == nil || step.RealizedGain.Cmp(&wantGain) != 0 {
		t.Errorf("RealizedGain = %v, want 50", step.RealizedGain)
	}
}

func TestBookOne_ReduceWithPerUnitPrice_RealizedLoss(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	// -5 ACME @ 90 USD against a 100 USD lot: (90 - 100) * 5 = -50.
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), &ast.CostSpec{},
		&ast.PriceAnnotation{Amount: mkAmount(t, "90", "USD"), IsTotal: false})

	bp, errs := bookOne(inv, p, ast.BookingStrict, date, false)
	if len(errs) > 0 {
		t.Fatalf("bookOne errors: %v", errs)
	}
	if len(bp.Reductions) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(bp.Reductions))
	}
	step := bp.Reductions[0]
	if step.RealizedGain == nil {
		t.Fatal("RealizedGain should be set")
	}
	wantLoss := decimalVal(t, "-50")
	if step.RealizedGain.Cmp(&wantLoss) != 0 {
		t.Errorf("RealizedGain = %s, want -50", step.RealizedGain.String())
	}
	if step.GainCurrency != "USD" {
		t.Errorf("GainCurrency = %q, want USD", step.GainCurrency)
	}
}

func TestBookOne_ReduceWithoutPriceLeavesGainZero(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), &ast.CostSpec{}, nil)

	bp, errs := bookOne(inv, p, ast.BookingStrict, date, false)
	if len(errs) > 0 {
		t.Fatalf("bookOne errors: %v", errs)
	}
	if len(bp.Reductions) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(bp.Reductions))
	}
	step := bp.Reductions[0]
	if step.SalePricePer != nil {
		t.Errorf("SalePricePer = %v, want nil", step.SalePricePer)
	}
	if step.RealizedGain != nil {
		t.Errorf("RealizedGain = %v, want nil", step.RealizedGain)
	}
	if step.GainCurrency != "" {
		t.Errorf("GainCurrency = %q, want empty", step.GainCurrency)
	}
}

// --- bookOne: error propagation -----------------------------------------

func TestBookOne_ReduceExceedsInventory(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "3", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), &ast.CostSpec{}, nil)

	_, errs := bookOne(inv, p, ast.BookingFIFO, date, false)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if errs[0].Code != CodeReductionExceedsInventory {
		t.Errorf("code = %v, want CodeReductionExceedsInventory", errs[0].Code)
	}
}

func TestBookOne_ReduceNoMatchingLot(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, "lot-a"))); err != nil {
		t.Fatal(err)
	}
	spec := &ast.CostSpec{Label: "missing"}
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), spec, nil)

	_, errs := bookOne(inv, p, ast.BookingStrict, date, false)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if errs[0].Code != CodeNoMatchingLot {
		t.Errorf("code = %v, want CodeNoMatchingLot", errs[0].Code)
	}
}

func TestBookOne_ReduceStrictAmbiguous(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, "lot-a"))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "120", "USD", date, "lot-b"))); err != nil {
		t.Fatal(err)
	}
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), &ast.CostSpec{}, nil)

	_, errs := bookOne(inv, p, ast.BookingStrict, date, false)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if errs[0].Code != CodeAmbiguousLotMatch {
		t.Errorf("code = %v, want CodeAmbiguousLotMatch", errs[0].Code)
	}
}

func TestBookOne_NilAmountReturnsInternalError(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	// bookOne must never see an auto-posting, but if it does the
	// defensive path returns CodeInternalError rather than panicking.
	p := mkAutoPosting("Assets:A")

	_, errs := bookOne(inv, p, ast.BookingStrict, txnDate, false)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if errs[0].Code != CodeInternalError {
		t.Errorf("code = %v, want CodeInternalError", errs[0].Code)
	}
}

// TestBookOne_BookingNoneShortPosition verifies that under BookingNone
// a sign-opposite posting against an empty inventory augments with a
// negative-units lot (a short position) rather than attempting a
// reduction. BookingNone treats every posting as an augmentation, so
// sign-opposite postings create shorts instead of matching existing
// lots.
func TestBookOne_BookingNoneShortPosition(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	// -5 ACME with no cost spec under BookingNone.
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), nil, nil)

	bp, errs := bookOne(inv, p, ast.BookingNone, txnDate, false)
	if len(errs) > 0 {
		t.Fatalf("bookOne errors: %v", errs)
	}
	if bp.Lot != nil {
		t.Errorf("Lot = %+v, want nil (no cost spec)", bp.Lot)
	}
	if inv.Len() != 1 {
		t.Fatalf("inventory Len = %d, want 1", inv.Len())
	}
	pos := inv.positions[0]
	if pos.Units.Currency != "ACME" {
		t.Errorf("position currency = %q, want ACME", pos.Units.Currency)
	}
	wantUnits := decimalVal(t, "-5")
	if pos.Units.Number.Cmp(&wantUnits) != 0 {
		t.Errorf("position Units = %s, want -5", pos.Units.Number.String())
	}
	if pos.Cost != nil {
		t.Errorf("position Cost = %+v, want nil", pos.Cost)
	}
}

// errors.As smoke test: verify that bookOne's returned errors match
// via inventory.Error.
func TestBookOne_ErrorAsInventoryError(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), &ast.CostSpec{}, nil)

	_, errs := bookOne(inv, p, ast.BookingStrict, txnDate, false)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	var invErr Error
	if !errors.As(errs[0], &invErr) {
		t.Fatal("errors.As failed to convert to inventory.Error")
	}
}
