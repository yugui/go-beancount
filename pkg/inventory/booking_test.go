package inventory

import (
	"errors"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
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

// decimalPtr returns a fresh *apd.Decimal parsed from num.
func decimalPtr(t *testing.T, num string) *apd.Decimal {
	t.Helper()
	d := decimalVal(t, num)
	return &d
}

// mkPosting builds a minimal ast.Posting for booking tests.
func mkPosting(t *testing.T, account string, units ast.Amount, cost ast.CostHolder, price *ast.PriceAnnotation) *ast.Posting {
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

	_, steps, finding, err := bookOne(inv, p, ast.BookingStrict, date)
	if err != nil {
		t.Fatalf("bookOne errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if len(steps) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(steps))
	}
	if steps[0].Lot.Currency != "USD" {
		t.Errorf("Reductions[0].Lot.Currency = %q, want USD", steps[0].Lot.Currency)
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
	spec := &ast.CostSpec{PerUnit: decimalPtr(t, "100"), Currency: "USD"}
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), spec, nil)

	lot, _, finding, err := bookOne(inv, p, ast.BookingStrict, txnDate)
	if err != nil {
		t.Fatalf("bookOne returned errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if lot == nil {
		t.Fatal("bookOne lot should be set for augmentation with cost")
	}
	want := decimalVal(t, "100")
	if lot.Number.Cmp(&want) != 0 {
		t.Errorf("Lot.Number = %s, want 100", lot.Number.String())
	}
	if lot.Currency != "USD" {
		t.Errorf("Lot.Currency = %q, want USD", lot.Currency)
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
		PerUnit:  decimalPtr(t, "100"),
		Total:    decimalPtr(t, "50"),
		Currency: "USD",
	}
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), spec, nil)

	lot, _, finding, err := bookOne(inv, p, ast.BookingStrict, txnDate)
	if err != nil {
		t.Fatalf("bookOne returned errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if lot == nil {
		t.Fatal("bookOne: Lot should be set")
	}
	want := decimalVal(t, "110")
	if lot.Number.Cmp(&want) != 0 {
		t.Errorf("Lot.Number = %s, want 110", lot.Number.String())
	}
}

func TestBookOne_AugmentTotalCost(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	spec := &ast.CostSpec{Total: decimalPtr(t, "500"), Currency: "USD"}
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), spec, nil)

	lot, _, finding, err := bookOne(inv, p, ast.BookingStrict, txnDate)
	if err != nil {
		t.Fatalf("bookOne returned errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if lot == nil {
		t.Fatal("bookOne: Lot should be set")
	}
	want := decimalVal(t, "100")
	if lot.Number.Cmp(&want) != 0 {
		t.Errorf("Lot.Number = %s, want 100", lot.Number.String())
	}
}

func TestBookOne_AugmentCash(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	p := mkPosting(t, "Assets:Cash", mkAmount(t, "100", "USD"), nil, nil)

	lot, _, finding, err := bookOne(inv, p, ast.BookingStrict, txnDate)
	if err != nil {
		t.Fatalf("bookOne returned errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if lot != nil {
		t.Errorf("Lot should be nil for cash augmentation, got %+v", lot)
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

	_, _, d, err := bookOne(inv, p, ast.BookingStrict, txnDate)
	if err != nil {

		t.Fatalf("system error: %v", err)

	}

	if d == nil {

		t.Fatal("expected finding, got nil")

	}

	if d.Code != CodeAugmentationRequiresCost {
		t.Errorf("error code = %v, want CodeAugmentationRequiresCost", d.Code)
	}
}

func TestBookOne_AugmentDateDefaultsToTxnDate(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	spec := &ast.CostSpec{PerUnit: decimalPtr(t, "100"), Currency: "USD"} // no Date
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), spec, nil)

	lot, _, finding, err := bookOne(inv, p, ast.BookingStrict, txnDate)
	if err != nil {
		t.Fatalf("unexpected errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if !lot.Date.Equal(txnDate) {
		t.Errorf("Lot.Date = %v, want %v (txn date default)", lot.Date, txnDate)
	}
}

func TestBookOne_AugmentLabelCopied(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	spec := &ast.CostSpec{
		PerUnit:  decimalPtr(t, "100"),
		Currency: "USD",
		Label:    "lot-A",
	}
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), spec, nil)

	lot, _, finding, err := bookOne(inv, p, ast.BookingStrict, txnDate)
	if err != nil {
		t.Fatalf("unexpected errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if lot.Label != "lot-A" {
		t.Errorf("Lot.Label = %q, want lot-A", lot.Label)
	}
}

// InferredAuto is no longer populated by bookOne — the reducer owns
// that flag — so a focused unit test is no longer expressible here.
// End-to-end propagation through Reducer.Walk is covered by
// TestReducerWalk_AutoPostingInference in reducer_test.go.

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

	_, steps, finding, err := bookOne(inv, p, ast.BookingFIFO, newDate)
	if err != nil {
		t.Fatalf("bookOne errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if len(steps) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(steps))
	}
	// FIFO consumes the oldest lot (100 USD).
	want := decimalVal(t, "100")
	if steps[0].Lot.Number.Cmp(&want) != 0 {
		t.Errorf("reduced lot cost = %s, want 100", steps[0].Lot.Number.String())
	}
	wantUnits := decimalVal(t, "5")
	if steps[0].Units.Cmp(&wantUnits) != 0 {
		t.Errorf("reduced units = %s, want 5", steps[0].Units.String())
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

	_, steps, finding, err := bookOne(inv, p, ast.BookingStrict, date)
	if err != nil {
		t.Fatalf("bookOne errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if len(steps) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(steps))
	}
	if steps[0].Lot.Label != "lot-a" {
		t.Errorf("reduced lot label = %q, want lot-a", steps[0].Lot.Label)
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

	_, steps, finding, err := bookOne(inv, p, ast.BookingStrict, date)
	if err != nil {
		t.Fatalf("bookOne errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if len(steps) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(steps))
	}
	step := steps[0]
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

	_, steps, finding, err := bookOne(inv, p, ast.BookingStrict, date)
	if err != nil {
		t.Fatalf("bookOne errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if len(steps) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(steps))
	}
	step := steps[0]
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

	_, steps, finding, err := bookOne(inv, p, ast.BookingStrict, date)
	if err != nil {
		t.Fatalf("bookOne errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if len(steps) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(steps))
	}
	step := steps[0]
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

// TestBookOne_ReduceTotalMatchPerStepRealizedGain pins the per-step
// realized-gain calculation under upstream beancount's "total match"
// rule: when a STRICT reduction exhausts multiple lots that share a
// matcher but carry different per-unit costs, each emitted step must
// carry its own RealizedGain derived from its own Lot.Number.
//
// Setup: 10 ACME at 100 USD ("a") and 10 ACME at 110 USD ("b"); sell
// all 20 at @@ 2500 USD (per-unit sale price 125). Steps must carry
// gains (125-100)*10 = 250 and (125-110)*10 = 150 respectively.
// matcher with no per-unit constraint admits both lots; sum (20)
// equals the requested magnitude (20) so total-match accepts under
// STRICT.
func TestBookOne_ReduceTotalMatchPerStepRealizedGain(t *testing.T) {
	d1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", d1, "a"))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "110", "USD", d2, "b"))); err != nil {
		t.Fatal(err)
	}
	// `{}` cost spec hints to USD via the price annotation; matcher
	// admits both lots regardless of per-unit cost.
	p := mkPosting(t, "Assets:A", mkAmount(t, "-20", "ACME"), &ast.CostSpec{},
		&ast.PriceAnnotation{Amount: mkAmount(t, "2500", "USD"), IsTotal: true})

	_, steps, finding, err := bookOne(inv, p, ast.BookingStrict, d2)
	if err != nil {
		t.Fatalf("bookOne: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if len(steps) != 2 {
		t.Fatalf("bookOne Reductions len = %d, want 2", len(steps))
	}
	wantSP := decimalVal(t, "125")
	wantGains := []apd.Decimal{decimalVal(t, "250"), decimalVal(t, "150")}
	for i, step := range steps {
		switch {
		case step.SalePricePer == nil:
			t.Errorf("step[%d].SalePricePer = nil, want 125", i)
		case step.SalePricePer.Cmp(&wantSP) != 0:
			t.Errorf("step[%d].SalePricePer = %s, want 125", i, step.SalePricePer.String())
		}
		switch {
		case step.RealizedGain == nil:
			t.Errorf("step[%d].RealizedGain = nil, want %s", i, wantGains[i].String())
		case step.RealizedGain.Cmp(&wantGains[i]) != 0:
			t.Errorf("step[%d].RealizedGain = %s, want %s", i, step.RealizedGain.String(), wantGains[i].String())
		}
		if step.GainCurrency != "USD" {
			t.Errorf("step[%d].GainCurrency = %q, want USD", i, step.GainCurrency)
		}
	}
}

func TestBookOne_ReduceWithoutPriceLeavesGainZero(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "ACME", mkCost(t, "100", "USD", date, ""))); err != nil {
		t.Fatal(err)
	}
	p := mkPosting(t, "Assets:A", mkAmount(t, "-5", "ACME"), &ast.CostSpec{}, nil)

	_, steps, finding, err := bookOne(inv, p, ast.BookingStrict, date)
	if err != nil {
		t.Fatalf("bookOne errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if len(steps) != 1 {
		t.Fatalf("Reductions len = %d, want 1", len(steps))
	}
	step := steps[0]
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

	_, _, d, err := bookOne(inv, p, ast.BookingFIFO, date)
	if err != nil {

		t.Fatalf("system error: %v", err)

	}

	if d == nil {

		t.Fatal("expected finding, got nil")

	}

	if d.Code != CodeReductionExceedsInventory {
		t.Errorf("code = %v, want CodeReductionExceedsInventory", d.Code)
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

	_, _, d, err := bookOne(inv, p, ast.BookingStrict, date)
	if err != nil {

		t.Fatalf("system error: %v", err)

	}

	if d == nil {

		t.Fatal("expected finding, got nil")

	}

	if d.Code != CodeNoMatchingLot {
		t.Errorf("code = %v, want CodeNoMatchingLot", d.Code)
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

	_, _, d, err := bookOne(inv, p, ast.BookingStrict, date)
	if err != nil {

		t.Fatalf("system error: %v", err)

	}

	if d == nil {

		t.Fatal("expected finding, got nil")

	}

	if d.Code != CodeAmbiguousLotMatch {
		t.Errorf("code = %v, want CodeAmbiguousLotMatch", d.Code)
	}
}

// TestBookOne_ReduceStrictTotalCostDisambiguates verifies that a
// total-only cost spec on a reducing posting (`{{ T CUR }}`) implicitly
// pins the per-unit cost to T/|units| for STRICT lot selection,
// mirroring upstream beancount. With two lots of the same commodity at
// different per-unit costs, the reduction must select only the lot
// whose per-unit cost equals T/|units| instead of being rejected as
// ambiguous.
func TestBookOne_ReduceStrictTotalCostDisambiguates(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	if err := inv.Add(mkPosition(t, "10", "STOCK", mkCost(t, "1", "JPY", date, ""))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "STOCK", mkCost(t, "1.5", "JPY", date, ""))); err != nil {
		t.Fatal(err)
	}

	// {{ 10 JPY }} on -10 STOCK implies per-unit 1 JPY, which uniquely
	// identifies the first lot.
	spec := &ast.CostSpec{Total: decimalPtr(t, "10"), Currency: "JPY"}
	p := mkPosting(t, "Assets:A", mkAmount(t, "-10", "STOCK"), spec, nil)

	_, steps, finding, err := bookOne(inv, p, ast.BookingStrict, date)
	if err != nil {
		t.Fatalf("bookOne errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if len(steps) != 1 {
		t.Fatalf("steps len = %d, want 1", len(steps))
	}
	wantPerUnit := decimalVal(t, "1")
	if steps[0].Lot.Number.Cmp(&wantPerUnit) != 0 {
		t.Errorf("bookOne: reduced lot per-unit = %s, want 1", steps[0].Lot.Number.String())
	}
	if steps[0].Lot.Currency != "JPY" {
		t.Errorf("bookOne: reduced lot currency = %q, want JPY", steps[0].Lot.Currency)
	}
	// The 1.5 JPY lot must remain untouched.
	if inv.Len() != 1 {
		t.Fatalf("inventory Len = %d, want 1 (only the 1.5 JPY lot left)", inv.Len())
	}
	// No exported per-lot accessor; read positions directly.
	remaining := inv.positions[0]
	if remaining.Cost == nil {
		t.Fatalf("bookOne: remaining lot has nil Cost; want lot at per-unit 1.5 JPY")
	}
	wantRemainingPerUnit := decimalVal(t, "1.5")
	if remaining.Cost.Number.Cmp(&wantRemainingPerUnit) != 0 {
		t.Errorf("bookOne: remaining lot per-unit = %s, want 1.5", remaining.Cost.Number.String())
	}
}

// TestBookOne_ReduceStrictCombinedCostDisambiguates is the combined-form
// `{per # total CUR}` counterpart of the total-only disambiguation test
// above. The derived per-unit constraint is per + total/|units|.
func TestBookOne_ReduceStrictCombinedCostDisambiguates(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := NewInventory()
	// First lot was augmented with `{1 # 5 JPY}` on 10 units, so its
	// per-unit cost is 1 + 5/10 = 1.5.
	if err := inv.Add(mkPosition(t, "10", "STOCK", mkCost(t, "1.5", "JPY", date, ""))); err != nil {
		t.Fatal(err)
	}
	if err := inv.Add(mkPosition(t, "10", "STOCK", mkCost(t, "2", "JPY", date, ""))); err != nil {
		t.Fatal(err)
	}

	// Reducer uses `{1 # 5 JPY}` on -10 STOCK; derived per-unit is 1.5.
	spec := &ast.CostSpec{
		PerUnit:  decimalPtr(t, "1"),
		Total:    decimalPtr(t, "5"),
		Currency: "JPY",
	}
	p := mkPosting(t, "Assets:A", mkAmount(t, "-10", "STOCK"), spec, nil)

	_, steps, finding, err := bookOne(inv, p, ast.BookingStrict, date)
	if err != nil {
		t.Fatalf("bookOne errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if len(steps) != 1 {
		t.Fatalf("steps len = %d, want 1", len(steps))
	}
	wantPerUnit := decimalVal(t, "1.5")
	if steps[0].Lot.Number.Cmp(&wantPerUnit) != 0 {
		t.Errorf("bookOne: reduced lot per-unit = %s, want 1.5", steps[0].Lot.Number.String())
	}
}

// TestBookOne_NilAmountReturnsSystemError pins that bookOne treats an
// auto-posting reaching it as an invariant violation. The reducer
// resolves auto-postings upstream, so bookOne should never see one;
// the defensive return is a plain `error` (not an [ast.Diagnostic])
// because this is an implementation bug, not a user finding.
func TestBookOne_NilAmountReturnsSystemError(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	p := mkAutoPosting("Assets:A")

	_, _, finding, err := bookOne(inv, p, ast.BookingStrict, txnDate)
	if err == nil {
		t.Fatalf("expected system error, got nil")
	}
	if finding != nil {
		t.Errorf("bookOne returned a Diagnostic for an invariant violation; want non-Diagnostic system error, got %v", finding)
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

	lot, _, finding, err := bookOne(inv, p, ast.BookingNone, txnDate)
	if err != nil {
		t.Fatalf("bookOne errors: %v", err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %v", finding)
	}
	if lot != nil {
		t.Errorf("Lot = %+v, want nil (no cost spec)", lot)
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

// errors.As smoke test: verify that bookOne's returned errors can be
// recovered as inventory's internal ast.Diagnostic so the enrichment path that
// patches Span/Account on errors from lower-level helpers continues to
// work.
func TestBookOne_ErrorAsDiagnostic(t *testing.T) {
	inv := NewInventory()
	txnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	p := mkPosting(t, "Assets:A", mkAmount(t, "5", "ACME"), &ast.CostSpec{}, nil)

	_, _, d, err := bookOne(inv, p, ast.BookingStrict, txnDate)
	if err != nil {

		t.Fatalf("system error: %v", err)

	}

	if d == nil {

		t.Fatal("expected finding, got nil")

	}
}

// TestWrapSystemErr_StampsLocation pins the developer-repro contract:
// a system error gets the triggering posting's source location
// stamped on its message via fmt.Errorf %w, preserving the chain so
// errors.Is/As still see the underlying cause.
func TestWrapSystemErr_StampsLocation(t *testing.T) {
	root := errors.New("apd: invalid operation")
	p := &ast.Posting{
		Account: "Assets:Test",
		Span: ast.Span{Start: ast.Position{
			Filename: "f.beancount",
			Line:     42,
			Column:   5,
		}},
	}
	err := wrapSystemErr(root, p)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, root) {
		t.Errorf("errors.Is lost the underlying chain: %v", err)
	}
	if want := "at f.beancount:42:5: apd: invalid operation"; err.Error() != want {
		t.Errorf("err.Error() = %q, want %q", err.Error(), want)
	}
}

// TestWrapSystemErr_NoLocationPassesThrough pins that an err produced
// without a source location (Span.Start.Filename == "") flows through
// unwrapped so we do not stamp a misleading "at ::0" prefix.
func TestWrapSystemErr_NoLocationPassesThrough(t *testing.T) {
	root := errors.New("boom")
	p := &ast.Posting{Account: "Assets:Test"}
	err := wrapSystemErr(root, p)
	if err != root {
		t.Errorf("wrapSystemErr wrapped err without a source location: %v", err)
	}
}

// TestEnrichDiagnostic_FillsContext pins the user-finding path: a
// finding the lower helper produced with empty Span/Account gets the
// posting's Span filled in and the account folded into the Message.
func TestEnrichDiagnostic_FillsContext(t *testing.T) {
	d := &ast.Diagnostic{Code: CodeNoMatchingLot, Message: "no lot"}
	p := &ast.Posting{
		Account: "Assets:Test",
		Span: ast.Span{Start: ast.Position{
			Filename: "f.beancount",
			Line:     10,
			Column:   1,
		}},
	}
	got := enrichDiagnostic(d, p)
	if got != d {
		t.Errorf("enrichDiagnostic returned a different pointer; want in-place mutation")
	}
	if got.Code != CodeNoMatchingLot {
		t.Errorf("Code = %q, want %q", got.Code, CodeNoMatchingLot)
	}
	if got.Span != p.Span {
		t.Errorf("Span = %v, want %v", got.Span, p.Span)
	}
	if want := "Assets:Test: no lot"; got.Message != want {
		t.Errorf("Message = %q, want %q", got.Message, want)
	}
}
