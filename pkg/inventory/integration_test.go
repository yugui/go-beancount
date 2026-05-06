package inventory_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/inventory"
)

// invCmpOpts compares inventory.Position values numerically:
// apd.Decimal has unexported fields, and time.Time carries monotonic
// state, so each gets a custom Comparer that defers to the type's own
// equality semantics. EquateEmpty treats a nil inventory snapshot
// (slices.Collect over an empty iter) as equal to an empty want slice.
var invCmpOpts = cmp.Options{
	cmp.Comparer(func(x, y apd.Decimal) bool { return x.Cmp(&y) == 0 }),
	cmp.Comparer(func(x, y time.Time) bool { return x.Equal(y) }),
	cmpopts.EquateEmpty(),
}

// loadInspectionFixture loads testdata/inspection_e2e.beancount via the
// ast layer directly (no loader pipeline) and fails the test on any
// parse or lowering diagnostics. The bypass is deliberate: these tests
// exercise the inventory Reducer over the raw AST shape that comes out
// of parsing, including auto-balanced postings whose Amount is still
// nil. Going through the full loader pipeline would route the input
// through the booking plugin, which yields a separate booked directives
// slice with auto-posting Amounts already filled in; the InferredAuto
// signal these tests assert on lives in the BookedPosting records and
// is reproducible by feeding the raw ledger straight into the reducer.
func loadInspectionFixture(t *testing.T) *ast.Ledger {
	t.Helper()
	path := filepath.Join("testdata", "inspection_e2e.beancount")
	ledger, err := ast.LoadFile(path)
	if err != nil {
		t.Fatalf("ast.LoadFile(%q): %v", path, err)
	}
	var errs []string
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			errs = append(errs, d.Message)
		}
	}
	if len(errs) != 0 {
		t.Fatalf("ast.LoadFile(%q): got %d error-severity diagnostics, want 0:\n  %s",
			path, len(errs), strings.Join(errs, "\n  "))
	}
	return ledger
}

// mustDecimal parses s as an apd.Decimal and fatal-fails on error. The
// returned value is fresh, so callers may mutate it.
func mustDecimal(t *testing.T, s string) apd.Decimal {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString(s); err != nil {
		t.Fatalf("parse decimal %q: %v", s, err)
	}
	return d
}

// decimalEq reports whether two *apd.Decimal values are numerically
// equal. A nil on either side is equal only to a nil on the other side.
func decimalEq(a, b *apd.Decimal) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Cmp(b) == 0
}

// txnByNarration returns the first *ast.Transaction in ledger whose
// Narration matches narration, or nil if none. Identity (not value) of
// the returned pointer must match what Reducer.Inspect sees, which is
// why we look up by narration and return the original pointer.
func txnByNarration(ledger *ast.Ledger, narration string) *ast.Transaction {
	for _, d := range ledger.All() {
		txn, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		if txn.Narration == narration {
			return txn
		}
	}
	return nil
}

// wantPosition is a hand-built expectation for a single Position used
// across the golden assertions. A nil cost field means "cash", matching
// inventory.Position{Cost: nil}.
type wantPosition struct {
	Units    string // decimal string, e.g. "5"
	Currency string
	Cost     *wantCost // nil for cash
}

type wantCost struct {
	Number   string
	Currency string
	Date     time.Time
	Label    string
}

// matchInventory reports whether inv holds exactly the positions
// described by want, in order. Mismatches are reported via t.Errorf.
func matchInventory(t *testing.T, tag string, inv *inventory.Inventory, want []wantPosition) {
	t.Helper()
	var got []inventory.Position
	if inv != nil {
		got = slices.Collect(inv.All())
	}
	wantPositions := make([]inventory.Position, len(want))
	for i, wp := range want {
		wantPositions[i] = inventory.Position{
			Units: ast.Amount{Number: mustDecimal(t, wp.Units), Currency: wp.Currency},
		}
		if wp.Cost != nil {
			wantPositions[i].Cost = &inventory.Cost{
				Number:   mustDecimal(t, wp.Cost.Number),
				Currency: wp.Cost.Currency,
				Date:     wp.Cost.Date,
				Label:    wp.Cost.Label,
			}
		}
	}
	if diff := cmp.Diff(wantPositions, got, invCmpOpts); diff != "" {
		t.Errorf("%s: inventory mismatch (-want +got):\n%s", tag, diff)
	}
}

// date is a short-form UTC date constructor.
func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// TestInventoryIntegration_RunProducesFinalState loads the fixture,
// runs the reducer, and checks the post-run per-account inventories
// against hand-built goldens.
func TestInventoryIntegration_RunProducesFinalState(t *testing.T) {
	ledger := loadInspectionFixture(t)

	r := inventory.NewReducer(ledger.All())
	_, errs := r.Run()
	if len(errs) != 0 {
		for _, e := range errs {
			t.Logf("reducer error: %s", e)
		}
		t.Fatalf("Reducer.Run: got %d errors, want 0", len(errs))
	}

	// BrokerA: lot-2025a was fully sold; lot-2025b remains.
	matchInventory(t, "Final BrokerA",
		r.Final(ast.Account("Assets:Investments:BrokerA")),
		[]wantPosition{
			{
				Units:    "5",
				Currency: "ACME",
				Cost: &wantCost{
					Number:   "100",
					Currency: "USD",
					Date:     date(2025, 1, 20),
					Label:    "lot-2025b",
				},
			},
		},
	)

	// BrokerB: 12 GIZMO sold under FIFO — 10 from the first lot (now
	// gone) and 2 from the second lot, leaving 8 GIZMO at 55 USD.
	matchInventory(t, "Final BrokerB",
		r.Final(ast.Account("Assets:Investments:BrokerB")),
		[]wantPosition{
			{
				Units:    "8",
				Currency: "GIZMO",
				Cost: &wantCost{
					Number:   "55",
					Currency: "USD",
					Date:     date(2025, 2, 15),
					Label:    "",
				},
			},
		},
	)

	// Cash: 10000 opening - 500 - 500 - 500 - 550 (four buys) + 600
	// (ACME sale auto leg) + 720 (GIZMO sale) = 9270.00 USD.
	matchInventory(t, "Final Cash",
		r.Final(ast.Account("Assets:Cash")),
		[]wantPosition{
			{Units: "9270.00", Currency: "USD", Cost: nil},
		},
	)
}

// TestInventoryIntegration_InspectReductionTransaction exercises the
// STRICT label-selected ACME sale, which also drives the auto-posting
// inference path. The test matches against a hand-built before/after
// view and verifies the realized-gain enrichment.
func TestInventoryIntegration_InspectReductionTransaction(t *testing.T) {
	ledger := loadInspectionFixture(t)
	r := inventory.NewReducer(ledger.All())

	sale := txnByNarration(ledger, "Sell ACME lot-2025a")
	if sale == nil {
		t.Fatalf("could not find the ACME sale transaction in the fixture")
	}

	insp, errs := r.Inspect(sale)
	if len(errs) != 0 {
		for _, e := range errs {
			t.Logf("inspect error: %s", e)
		}
		t.Fatalf("Inspect: got %d errors, want 0", len(errs))
	}
	if insp == nil {
		t.Fatalf("Inspect returned nil inspection")
	}

	// Before: BrokerA carries both 5 ACME lots; Cash holds the
	// running balance after the four prior purchases: 10000 - 500 -
	// 500 - 500 - 550 = 7950.00 USD. (GIZMO second-lot buy on
	// 2025-02-15 precedes the ACME sale on 2025-03-10 in canonical
	// order.)
	matchInventory(t, "Inspect Before BrokerA",
		insp.Before[ast.Account("Assets:Investments:BrokerA")],
		[]wantPosition{
			{
				Units: "5", Currency: "ACME",
				Cost: &wantCost{Number: "100", Currency: "USD", Date: date(2025, 1, 5), Label: "lot-2025a"},
			},
			{
				Units: "5", Currency: "ACME",
				Cost: &wantCost{Number: "100", Currency: "USD", Date: date(2025, 1, 20), Label: "lot-2025b"},
			},
		},
	)
	matchInventory(t, "Inspect Before Cash",
		insp.Before[ast.Account("Assets:Cash")],
		[]wantPosition{
			{Units: "7950.00", Currency: "USD", Cost: nil},
		},
	)

	// After: only lot-2025b remains on BrokerA; cash has absorbed the
	// explicit +600 USD proceeds leg, so the balance is 7950 + 600 =
	// 8550.00 USD.
	matchInventory(t, "Inspect After BrokerA",
		insp.After[ast.Account("Assets:Investments:BrokerA")],
		[]wantPosition{
			{
				Units: "5", Currency: "ACME",
				Cost: &wantCost{Number: "100", Currency: "USD", Date: date(2025, 1, 20), Label: "lot-2025b"},
			},
		},
	)
	matchInventory(t, "Inspect After Cash",
		insp.After[ast.Account("Assets:Cash")],
		[]wantPosition{
			{Units: "8550.00", Currency: "USD", Cost: nil},
		},
	)

	// Booked: one reducing ACME posting with RealizedGain of 100 USD
	// (the cost-basis difference between 100 USD per-unit cost and the
	// 120 USD per-unit sale price), one explicit Cash posting with
	// +600.00 USD proceeds, and one inferred auto Income:Gains posting
	// absorbing the realized gain (-100.00 USD).
	if len(insp.Booked) != 3 {
		t.Fatalf("len(Booked) = %d, want 3", len(insp.Booked))
	}

	var acmeBP, cashBP, gainsBP *inventory.BookedPosting
	for i := range insp.Booked {
		bp := &insp.Booked[i]
		switch bp.Account {
		case "Assets:Investments:BrokerA":
			acmeBP = bp
		case "Assets:Cash":
			cashBP = bp
		case "Income:Gains":
			gainsBP = bp
		}
	}
	if acmeBP == nil || cashBP == nil || gainsBP == nil {
		t.Fatalf("missing booked postings: acme=%v cash=%v gains=%v", acmeBP, cashBP, gainsBP)
	}

	// ACME posting: units -5, one reduction of 5 units against lot-2025a
	// with realized gain (120 - 100) * 5 = 100 USD.
	if acmeBP.InferredAuto {
		t.Errorf("acmeBP.InferredAuto = true, want false")
	}
	if got, want := acmeBP.Units.Currency, "ACME"; got != want {
		t.Errorf("acmeBP.Units.Currency = %q, want %q", got, want)
	}
	wantNeg5 := mustDecimal(t, "-5")
	if acmeBP.Units.Number.Cmp(&wantNeg5) != 0 {
		t.Errorf("acmeBP.Units.Number = %s, want -5", acmeBP.Units.Number.Text('f'))
	}
	if len(acmeBP.Reductions) != 1 {
		t.Fatalf("acmeBP.Reductions: got %d steps, want 1", len(acmeBP.Reductions))
	}
	step := acmeBP.Reductions[0]
	if step.Lot.Label != "lot-2025a" {
		t.Errorf("step.Lot.Label = %q, want lot-2025a", step.Lot.Label)
	}
	if step.Lot.Currency != "USD" {
		t.Errorf("step.Lot.Currency = %q, want USD", step.Lot.Currency)
	}
	want100 := mustDecimal(t, "100")
	if step.Lot.Number.Cmp(&want100) != 0 {
		t.Errorf("step.Lot.Number = %s, want 100", step.Lot.Number.Text('f'))
	}
	if !step.Lot.Date.Equal(date(2025, 1, 5)) {
		t.Errorf("step.Lot.Date = %s, want 2025-01-05", step.Lot.Date)
	}
	want5 := mustDecimal(t, "5")
	if step.Units.Cmp(&want5) != 0 {
		t.Errorf("step.Units = %s, want 5", step.Units.Text('f'))
	}
	want120 := mustDecimal(t, "120")
	if !decimalEq(step.SalePricePer, &want120) {
		var got string
		if step.SalePricePer != nil {
			got = step.SalePricePer.Text('f')
		}
		t.Errorf("step.SalePricePer = %s, want 120", got)
	}
	wantGain := mustDecimal(t, "100")
	if !decimalEq(step.RealizedGain, &wantGain) {
		var got string
		if step.RealizedGain != nil {
			got = step.RealizedGain.Text('f')
		}
		t.Errorf("step.RealizedGain = %s, want 100", got)
	}
	if step.GainCurrency != "USD" {
		t.Errorf("step.GainCurrency = %q, want USD", step.GainCurrency)
	}

	// Cash posting: explicit +600.00 USD proceeds, no lot, no reductions.
	if cashBP.InferredAuto {
		t.Errorf("cashBP.InferredAuto = true, want false (cash leg is explicit)")
	}
	if cashBP.Units.Currency != "USD" {
		t.Errorf("cashBP.Units.Currency = %q, want USD", cashBP.Units.Currency)
	}
	want600 := mustDecimal(t, "600.00")
	if cashBP.Units.Number.Cmp(&want600) != 0 {
		t.Errorf("cashBP.Units.Number = %s, want 600.00", cashBP.Units.Number.Text('f'))
	}
	if cashBP.Lot != nil {
		t.Errorf("cashBP.Lot = %+v, want nil", cashBP.Lot)
	}
	if len(cashBP.Reductions) != 0 {
		t.Errorf("len(cashBP.Reductions) = %d, want 0", len(cashBP.Reductions))
	}

	// Income:Gains posting: auto-inferred, absorbs the realized gain
	// (-100.00 USD), no lot, no reductions.
	if !gainsBP.InferredAuto {
		t.Errorf("gainsBP.InferredAuto = false, want true")
	}
	if gainsBP.Units.Currency != "USD" {
		t.Errorf("gainsBP.Units.Currency = %q, want USD", gainsBP.Units.Currency)
	}
	wantNeg100 := mustDecimal(t, "-100.00")
	if gainsBP.Units.Number.Cmp(&wantNeg100) != 0 {
		t.Errorf("gainsBP.Units.Number = %s, want -100.00", gainsBP.Units.Number.Text('f'))
	}
	if gainsBP.Lot != nil {
		t.Errorf("gainsBP.Lot = %+v, want nil", gainsBP.Lot)
	}
	if len(gainsBP.Reductions) != 0 {
		t.Errorf("len(gainsBP.Reductions) = %d, want 0", len(gainsBP.Reductions))
	}
}

// TestInventoryIntegration_InspectFIFOReduction verifies that a FIFO
// sale crossing a lot boundary emits one ReductionStep per consumed lot
// and that per-step realized gains sum to the expected total.
func TestInventoryIntegration_InspectFIFOReduction(t *testing.T) {
	ledger := loadInspectionFixture(t)
	r := inventory.NewReducer(ledger.All())

	sale := txnByNarration(ledger, "Sell GIZMO FIFO crossing lot boundary")
	if sale == nil {
		t.Fatalf("could not find the GIZMO FIFO sale transaction in the fixture")
	}

	insp, errs := r.Inspect(sale)
	if len(errs) != 0 {
		for _, e := range errs {
			t.Logf("inspect error: %s", e)
		}
		t.Fatalf("Inspect: got %d errors, want 0", len(errs))
	}
	if insp == nil {
		t.Fatalf("Inspect returned nil inspection")
	}

	// After: only 8 GIZMO at 55 USD on the second lot.
	matchInventory(t, "Inspect After BrokerB",
		insp.After[ast.Account("Assets:Investments:BrokerB")],
		[]wantPosition{
			{
				Units: "8", Currency: "GIZMO",
				Cost: &wantCost{Number: "55", Currency: "USD", Date: date(2025, 2, 15), Label: ""},
			},
		},
	)

	var gizmoBP *inventory.BookedPosting
	for i := range insp.Booked {
		if insp.Booked[i].Account == "Assets:Investments:BrokerB" {
			gizmoBP = &insp.Booked[i]
		}
	}
	if gizmoBP == nil {
		t.Fatalf("no BookedPosting for Assets:Investments:BrokerB")
	}
	if gizmoBP.InferredAuto {
		t.Errorf("gizmoBP.InferredAuto = true, want false")
	}
	if len(gizmoBP.Reductions) != 2 {
		t.Fatalf("gizmoBP.Reductions: got %d steps, want 2", len(gizmoBP.Reductions))
	}

	// Reductions[0]: FIFO consumes the entire 10 units from the 2025-01-10
	// lot at cost 50 USD. Gain = (60 - 50) * 10 = 100 USD.
	s0 := gizmoBP.Reductions[0]
	want50 := mustDecimal(t, "50")
	if s0.Lot.Number.Cmp(&want50) != 0 {
		t.Errorf("step[0].Lot.Number = %s, want 50", s0.Lot.Number.Text('f'))
	}
	if !s0.Lot.Date.Equal(date(2025, 1, 10)) {
		t.Errorf("step[0].Lot.Date = %s, want 2025-01-10", s0.Lot.Date)
	}
	want10 := mustDecimal(t, "10")
	if s0.Units.Cmp(&want10) != 0 {
		t.Errorf("step[0].Units = %s, want 10", s0.Units.Text('f'))
	}
	wantGain0 := mustDecimal(t, "100")
	if !decimalEq(s0.RealizedGain, &wantGain0) {
		var got string
		if s0.RealizedGain != nil {
			got = s0.RealizedGain.Text('f')
		}
		t.Errorf("step[0].RealizedGain = %s, want 100", got)
	}
	if s0.GainCurrency != "USD" {
		t.Errorf("step[0].GainCurrency = %q, want USD", s0.GainCurrency)
	}

	// Reductions[1]: remainder of 2 units consumed from the 2025-02-15 lot at
	// cost 55 USD. Gain = (60 - 55) * 2 = 10 USD.
	s1 := gizmoBP.Reductions[1]
	want55 := mustDecimal(t, "55")
	if s1.Lot.Number.Cmp(&want55) != 0 {
		t.Errorf("step[1].Lot.Number = %s, want 55", s1.Lot.Number.Text('f'))
	}
	if !s1.Lot.Date.Equal(date(2025, 2, 15)) {
		t.Errorf("step[1].Lot.Date = %s, want 2025-02-15", s1.Lot.Date)
	}
	want2 := mustDecimal(t, "2")
	if s1.Units.Cmp(&want2) != 0 {
		t.Errorf("step[1].Units = %s, want 2", s1.Units.Text('f'))
	}
	wantGain1 := mustDecimal(t, "10")
	if !decimalEq(s1.RealizedGain, &wantGain1) {
		var got string
		if s1.RealizedGain != nil {
			got = s1.RealizedGain.Text('f')
		}
		t.Errorf("step[1].RealizedGain = %s, want 10", got)
	}

	// Sum of per-step realized gains: 100 + 10 = 110 USD, which
	// matches (60 - avg_cost) * 8 = 110 when you solve for avg_cost.
	var sum apd.Decimal
	if _, err := apd.BaseContext.Add(&sum, s0.RealizedGain, s1.RealizedGain); err != nil {
		t.Fatalf("sum gains: %v", err)
	}
	wantTotal := mustDecimal(t, "110")
	if sum.Cmp(&wantTotal) != 0 {
		t.Errorf("sum of realized gains = %s, want 110", sum.Text('f'))
	}
}

// TestInventoryIntegration_AutoPostingInference re-inspects the ACME
// sale (the fixture's only transaction with an auto-balanced posting)
// and asserts the auto posting's InferredAuto flag plus the inferred
// amount. With cost-and-price postings, upstream Beancount uses cost
// for the balancing weight and price only for the prices database, so
// the auto leg lands on Income:Gains and absorbs the realized gain
// (-100.00 USD), not the cash proceeds (which is recorded explicitly).
// This overlaps with the reduction test above but keeps the
// auto-posting contract asserted in isolation.
func TestInventoryIntegration_AutoPostingInference(t *testing.T) {
	ledger := loadInspectionFixture(t)
	r := inventory.NewReducer(ledger.All())

	// The ACME sale is the only transaction in the fixture that uses
	// an auto-balanced posting. Locate it by narration so the test
	// document itself pins that assumption.
	sale := txnByNarration(ledger, "Sell ACME lot-2025a")
	if sale == nil {
		t.Fatalf("could not find the auto-posting transaction in the fixture")
	}
	if len(sale.Postings) != 3 {
		t.Fatalf("sale has %d postings, want 3", len(sale.Postings))
	}

	insp, errs := r.Inspect(sale)
	if len(errs) != 0 {
		for _, e := range errs {
			t.Logf("inspect error: %s", e)
		}
		t.Fatalf("Inspect: got %d errors, want 0", len(errs))
	}
	if insp == nil {
		t.Fatalf("Inspect returned nil inspection")
	}

	var autoBP *inventory.BookedPosting
	for i := range insp.Booked {
		if insp.Booked[i].InferredAuto {
			autoBP = &insp.Booked[i]
		}
	}
	if autoBP == nil {
		t.Fatalf("no BookedPosting with InferredAuto=true; booked=%+v", insp.Booked)
	}
	if autoBP.Account != "Income:Gains" {
		t.Errorf("autoBP.Account = %q, want Income:Gains", autoBP.Account)
	}
	if autoBP.Units.Currency != "USD" {
		t.Errorf("autoBP.Units.Currency = %q, want USD", autoBP.Units.Currency)
	}
	wantNeg100 := mustDecimal(t, "-100.00")
	if autoBP.Units.Number.Cmp(&wantNeg100) != 0 {
		t.Errorf("autoBP.Units.Number = %s, want -100.00", autoBP.Units.Number.Text('f'))
	}
	// The auto-posting should not carry a lot or reductions.
	if autoBP.Lot != nil {
		t.Errorf("autoBP.Lot = %+v, want nil", autoBP.Lot)
	}
	if len(autoBP.Reductions) != 0 {
		t.Errorf("len(autoBP.Reductions) = %d, want 0", len(autoBP.Reductions))
	}
}
