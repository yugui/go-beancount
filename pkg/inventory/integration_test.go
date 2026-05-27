package inventory_test

import (
	"fmt"
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

// lotIdentityCmpOpts extends invCmpOpts with IgnoreFields options for
// fields that exist on a booked record solely as presentation
// provenance and are intentionally excluded from lot identity:
//
//   - ast.Cost.PerUnit / ast.Cost.Total — retained by the booked Cost
//     so the printer can round-trip the user's original syntax. Lot
//     identity per ast.Cost.Equal is Number / Currency / Date / Label.
//   - inventory.ReductionStep.SalePricePer — populated only when the
//     reducing posting carries a price annotation; not part of the
//     per-lot consumption record that lot-identity tests assert.
var lotIdentityCmpOpts = append(append(cmp.Options(nil), invCmpOpts...),
	cmpopts.IgnoreFields(ast.Cost{}, "PerUnit", "Total"),
	cmpopts.IgnoreFields(inventory.ReductionStep{}, "SalePricePer"))

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
	var diags []string
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			diags = append(diags, d.Message)
		}
	}
	if len(diags) != 0 {
		t.Fatalf("ast.LoadFile(%q): got %d error-severity diagnostics, want 0:\n  %s",
			path, len(diags), strings.Join(diags, "\n  "))
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
			wantPositions[i].Cost = &inventory.Lot{
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
	_, diags, _ := r.Run()
	if len(diags) != 0 {
		for _, e := range diags {
			t.Logf("reducer error: %s", e)
		}
		t.Fatalf("Reducer.Run: got %d errors, want 0", len(diags))
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

	insp, diags, _ := r.Inspect(sale)
	if len(diags) != 0 {
		for _, e := range diags {
			t.Logf("inspect error: %s", e)
		}
		t.Fatalf("Inspect: got %d errors, want 0", len(diags))
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
	if acmeBP.Reduction == nil {
		t.Fatal("acmeBP.Reduction is nil, want a single step")
	}
	step := *acmeBP.Reduction
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
	if cashBP.Reduction != nil {
		t.Errorf("cashBP.Reduction = %+v, want nil", cashBP.Reduction)
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
	if gainsBP.Reduction != nil {
		t.Errorf("gainsBP.Reduction = %+v, want nil", gainsBP.Reduction)
	}
}

// TestInventoryIntegration_InspectFIFOReduction verifies that a FIFO
// sale crossing a lot boundary is expanded into one BookedPosting per
// consumed lot, each carrying its own single Reduction step and
// per-step realized gain. Summed across the expanded postings the
// realized gains reproduce the per-share basis the inventory layer
// computes.
func TestInventoryIntegration_InspectFIFOReduction(t *testing.T) {
	ledger := loadInspectionFixture(t)
	r := inventory.NewReducer(ledger.All())

	sale := txnByNarration(ledger, "Sell GIZMO FIFO crossing lot boundary")
	if sale == nil {
		t.Fatalf("could not find the GIZMO FIFO sale transaction in the fixture")
	}

	insp, diags, _ := r.Inspect(sale)
	if len(diags) != 0 {
		for _, e := range diags {
			t.Logf("inspect error: %s", e)
		}
		t.Fatalf("Inspect: got %d errors, want 0", len(diags))
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

	var gizmoBPs []*inventory.BookedPosting
	for i := range insp.Booked {
		if insp.Booked[i].Account == "Assets:Investments:BrokerB" {
			gizmoBPs = append(gizmoBPs, &insp.Booked[i])
		}
	}
	if len(gizmoBPs) != 2 {
		t.Fatalf("BrokerB BookedPostings: got %d, want 2 (FIFO sale expanded into two per-lot postings)", len(gizmoBPs))
	}

	// Per-lot expectations in FIFO order: first the 2025-01-10 lot
	// fully consumed (10 units @ 50 USD basis), then the 2025-02-15
	// lot partially consumed (2 units @ 55 USD basis).
	wantLots := []struct {
		basis    string
		date     time.Time
		units    string
		signed   string
		gain     string
		currency string
	}{
		{basis: "50", date: date(2025, 1, 10), units: "10", signed: "-10", gain: "100", currency: "USD"},
		{basis: "55", date: date(2025, 2, 15), units: "2", signed: "-2", gain: "10", currency: "USD"},
	}
	for i, want := range wantLots {
		t.Run(fmt.Sprintf("lot[%d]", i), func(t *testing.T) {
			bp := gizmoBPs[i]
			if bp.InferredAuto {
				t.Errorf("Inspect: InferredAuto = true, want false")
			}
			wantUnits := ast.Amount{Number: mustDecimal(t, want.signed), Currency: "GIZMO"}
			if diff := cmp.Diff(wantUnits, bp.Units, invCmpOpts); diff != "" {
				t.Errorf("Inspect: BookedPosting.Units mismatch (-want +got):\n%s", diff)
			}
			if bp.Reduction == nil {
				t.Fatal("Inspect: Reduction is nil, want a single step (expanded child is a single-lot reduction)")
			}
			wantGain := mustDecimal(t, want.gain)
			wantStep := inventory.ReductionStep{
				Lot:          inventory.Lot{Number: mustDecimal(t, want.basis), Currency: "USD", Date: want.date},
				Units:        mustDecimal(t, want.units),
				RealizedGain: &wantGain,
				GainCurrency: want.currency,
			}
			step := *bp.Reduction
			if diff := cmp.Diff(wantStep, step, lotIdentityCmpOpts); diff != "" {
				t.Errorf("Inspect: Reduction mismatch (-want +got):\n%s", diff)
			}

			// Each expanded child carries its own *ast.Cost rendering
			// the matched lot's identity. This is the witness for the
			// kind:"cost" output the beancompat serializer needs.
			if bp.Source == nil {
				t.Fatalf("Inspect: Source = nil")
			}
			cost, ok := bp.Source.Cost.(*ast.Cost)
			if !ok || cost == nil {
				t.Fatalf("Inspect: Source.Cost type = %T, want *ast.Cost", bp.Source.Cost)
			}
			wantCost := &ast.Cost{
				Number:   mustDecimal(t, want.basis),
				Currency: "USD",
				Date:     want.date,
			}
			if diff := cmp.Diff(wantCost, cost, lotIdentityCmpOpts); diff != "" {
				t.Errorf("Inspect: Source.Cost mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// Sum of per-step realized gains: 100 + 10 = 110 USD, which
	// matches (60 - avg_cost) * 8 = 110 when you solve for avg_cost.
	// Guard against a nil Reduction here: a per-lot subtest above can
	// observe and t.Fatal on it, but t.Fatal inside t.Run only
	// terminates the subtest, so this outer-body sum would still run.
	if gizmoBPs[0].Reduction == nil || gizmoBPs[1].Reduction == nil {
		t.Fatal("Inspect: gizmoBPs Reduction is nil on an expanded child; cannot sum realized gains")
	}
	var sum apd.Decimal
	if _, err := apd.BaseContext.Add(&sum, gizmoBPs[0].Reduction.RealizedGain, gizmoBPs[1].Reduction.RealizedGain); err != nil {
		t.Fatalf("sum gains: %v", err)
	}
	wantTotal := mustDecimal(t, "110")
	if sum.Cmp(&wantTotal) != 0 {
		t.Errorf("sum of realized gains = %s, want 110", sum.Text('f'))
	}
}

// loadIdempotencyFixture loads testdata/idempotency_e2e.beancount via
// the ast layer directly (no loader pipeline), mirroring
// loadInspectionFixture's bypass rationale: these tests need to drive
// the Reducer over the raw AST shape that comes out of parsing,
// including the auto-balanced posting whose Amount is still nil and
// the `{}` cost specs that Pass 2 must resolve.
func loadIdempotencyFixture(t *testing.T) *ast.Ledger {
	t.Helper()
	path := filepath.Join("testdata", "idempotency_e2e.beancount")
	ledger, err := ast.LoadFile(path)
	if err != nil {
		t.Fatalf("ast.LoadFile(%q): %v", path, err)
	}
	var diags []string
	for _, d := range ledger.Diagnostics {
		if d.Severity == ast.Error {
			diags = append(diags, d.Message)
		}
	}
	if len(diags) != 0 {
		t.Fatalf("ast.LoadFile(%q): got %d error-severity diagnostics, want 0:\n  %s",
			path, len(diags), strings.Join(diags, "\n  "))
	}
	return ledger
}

// TestReducerRun_OutputIsFixedPoint asserts that re-running the Reducer
// over its own output yields the same booked directives, the same
// per-account final inventory, and no errors. The Reducer's rewrites —
// Pass-1 augment Cost install, Pass-2 single-lot Cost install and
// multi-lot reduction expansion, Pass-3 auto-posting Amount inference
// and deferred per-unit cost solving — are designed so that once the
// AST has been enriched with the inferred values (and expanded into
// per-lot postings), a second pass routes those postings through the
// explicit-value path instead of the rewrite path and converges on
// the same numbers. The fixture (testdata/idempotency_e2e.beancount)
// packs every rewrite into one ledger so a single round-trip
// exercises every path.
//
// The two snapshots compared:
//
//   - Booked directives (Walk's []ast.Directive output) — these are
//     deep-cloned by Walk before mutation, so equality across runs
//     means the second pass produced no further mutation.
//   - Final per-account Inventory (Reducer.Final) — equality across
//     runs means the second pass routed the same units through the
//     same lots in the same order.
//
// Errors are required to be empty on both runs; an error on either
// run would invalidate the comparison.
func TestReducerRun_OutputIsFixedPoint(t *testing.T) {
	ledger := loadIdempotencyFixture(t)

	r1 := inventory.NewReducer(ledger.All())
	out1, diags1, _ := r1.Run()
	if len(diags1) != 0 {
		for _, e := range diags1 {
			t.Logf("1st-run error: %s", e)
		}
		t.Fatalf("1st Run: got %d errors, want 0", len(diags1))
	}

	// Witness the terminal CostSpec→Cost pass: after the first run
	// at least one cost-bearing posting must have been converted to
	// *ast.Cost so the second Run engages the already-booked path
	// somewhere. The branch-specific coverage of the resolution
	// helpers (ResolveLot / NewCostMatcher with *ast.Cost input)
	// is pinned by focused unit tests in idempotence_test.go; here
	// we only assert that the integration glue connects the
	// terminal pass to the next run.
	costConverted := 0
	costRetained := 0
	for _, d := range out1 {
		txn, ok := d.(*ast.Transaction)
		if !ok {
			continue
		}
		for i := range txn.Postings {
			p := &txn.Postings[i]
			if p.Cost == nil {
				continue
			}
			if _, ok := p.Cost.(*ast.Cost); ok {
				costConverted++
			} else {
				costRetained++
			}
		}
	}
	if costConverted == 0 {
		t.Errorf("terminal pass converted no postings to *ast.Cost (retained=%d); 2nd Run cannot exercise the already-booked path", costRetained)
	}

	r2 := inventory.NewReducer(slices.All(out1))
	out2, diags2, _ := r2.Run()
	if len(diags2) != 0 {
		for _, e := range diags2 {
			t.Logf("2nd-run error: %s", e)
		}
		t.Fatalf("2nd Run: got %d errors, want 0", len(diags2))
	}

	if diff := cmp.Diff(out1, out2, invCmpOpts); diff != "" {
		t.Errorf("directives differ between 1st and 2nd Run (-1st +2nd):\n%s", diff)
	}

	// Compare per-account final inventories. The accounts to check are
	// taken from the ledger's Open directives, so adding a new account
	// to the fixture automatically extends the assertion. Positions are
	// materialized through Inventory.All so cmp.Diff prints the diverging
	// entries directly when the assertion fails.
	for _, d := range ledger.All() {
		op, ok := d.(*ast.Open)
		if !ok {
			continue
		}
		var pos1, pos2 []inventory.Position
		if a := r1.Final(op.Account); a != nil {
			pos1 = slices.Collect(a.All())
		}
		if b := r2.Final(op.Account); b != nil {
			pos2 = slices.Collect(b.All())
		}
		if diff := cmp.Diff(pos1, pos2, invCmpOpts); diff != "" {
			t.Errorf("Final(%s) inventory differs between runs (-1st +2nd):\n%s",
				op.Account, diff)
		}
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

	insp, diags, _ := r.Inspect(sale)
	if len(diags) != 0 {
		for _, e := range diags {
			t.Logf("inspect error: %s", e)
		}
		t.Fatalf("Inspect: got %d errors, want 0", len(diags))
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
	if autoBP.Reduction != nil {
		t.Errorf("autoBP.Reduction = %+v, want nil", autoBP.Reduction)
	}
}

// TestInventoryIntegration_MultiLotExpansionWithAutoPosting pins the
// load-bearing ordering contract between Pass 2 (expandReductions)
// and Pass 3 (residual). The "Sell STOCK at gain" transaction in
// idempotency_e2e.beancount is a multi-lot reduction (15 STOCK
// crossing two FIFO lots) co-located with an auto-balanced
// Income:Capital leg. Pass 3 reads PostingWeight on every booked
// Source to compute the residual the auto-posting absorbs; with the
// parent's parse-tier *ast.CostSpec carrying no synthesized Total,
// the only way Pass 3 sees a well-defined weight is for Pass 2 to
// have already expanded the parent into per-lot children whose
// *ast.Cost provides PostingWeight's Total branch directly. If
// expansion were ever reordered after Pass 3 the auto-posting would
// fail to resolve and this test would fail loudly.
func TestInventoryIntegration_MultiLotExpansionWithAutoPosting(t *testing.T) {
	ledger := loadIdempotencyFixture(t)
	r := inventory.NewReducer(ledger.All())

	sale := txnByNarration(ledger, "Sell STOCK at gain")
	if sale == nil {
		t.Fatalf("could not find the multi-lot + auto-posting sale transaction in the fixture")
	}
	if len(sale.Postings) != 3 {
		t.Fatalf("input sale has %d postings, want 3 (reducing, cash, auto)", len(sale.Postings))
	}

	insp, diags, _ := r.Inspect(sale)
	if len(diags) != 0 {
		for _, e := range diags {
			t.Logf("inspect error: %s", e)
		}
		t.Fatalf("Inspect: got %d errors, want 0", len(diags))
	}
	if insp == nil {
		t.Fatalf("Inspect returned nil inspection")
	}

	// Booked layout after expansion: two reducing children on
	// Assets:Stock followed by the explicit Cash leg and the
	// inferred Income:Capital auto-posting.
	if got, want := len(insp.Booked), 4; got != want {
		t.Fatalf("len(Booked) = %d, want %d (2 expanded children + cash + auto)", got, want)
	}

	var children []*inventory.BookedPosting
	var cashBP, autoBP *inventory.BookedPosting
	for i := range insp.Booked {
		bp := &insp.Booked[i]
		switch bp.Account {
		case "Assets:Stock":
			children = append(children, bp)
		case "Assets:Cash":
			cashBP = bp
		case "Income:Capital":
			autoBP = bp
		}
	}
	if len(children) != 2 || cashBP == nil || autoBP == nil {
		t.Fatalf("Booked dispatch: stock=%d cash=%v auto=%v", len(children), cashBP, autoBP)
	}

	// Children carry the FIFO lots in order: 10 @ 5.00 USD from the
	// 2025-01-10 deferred-cost lot, then 5 @ 6.00 USD from the
	// 2025-02-15 explicit lot. Each child's *ast.Cost is the witness
	// for the per-lot expansion the beancompat serializer requires.
	wantLots := []struct {
		signed string
		number string
		date   time.Time
	}{
		{signed: "-10", number: "5.00", date: date(2025, 1, 10)},
		{signed: "-5", number: "6.00", date: date(2025, 2, 15)},
	}
	for i, want := range wantLots {
		t.Run(fmt.Sprintf("child[%d]", i), func(t *testing.T) {
			bp := children[i]
			wantUnits := ast.Amount{Number: mustDecimal(t, want.signed), Currency: "STOCK"}
			if diff := cmp.Diff(wantUnits, bp.Units, invCmpOpts); diff != "" {
				t.Errorf("Inspect: BookedPosting.Units mismatch (-want +got):\n%s", diff)
			}
			if bp.Source == nil {
				t.Fatalf("Inspect: Source = nil")
			}
			cost, ok := bp.Source.Cost.(*ast.Cost)
			if !ok || cost == nil {
				t.Fatalf("Inspect: Source.Cost type = %T, want *ast.Cost", bp.Source.Cost)
			}
			wantCost := &ast.Cost{
				Number:   mustDecimal(t, want.number),
				Currency: "USD",
				Date:     want.date,
			}
			if diff := cmp.Diff(wantCost, cost, lotIdentityCmpOpts); diff != "" {
				t.Errorf("Inspect: Source.Cost mismatch (-want +got):\n%s", diff)
			}
			if !cost.IsBooked() {
				t.Errorf("Inspect: Source.Cost reports IsBooked()=false")
			}
		})
	}

	// Pass 3 absorbed the residual onto Income:Capital. The
	// transaction's expected residual is
	//   -((-10 × 5.00) + (-5 × 6.00) + 100.00) = -(−80 + 100) = -20
	// which is the realized gain (the user *received* 100 but
	// only "earned" 80 in cost basis terms). The auto-posting
	// records this on Income:Capital as -20.00 USD.
	if !autoBP.InferredAuto {
		t.Errorf("Inspect: Income:Capital.InferredAuto = false, want true")
	}
	wantAutoUnits := ast.Amount{Number: mustDecimal(t, "-20.00"), Currency: "USD"}
	if diff := cmp.Diff(wantAutoUnits, autoBP.Units, invCmpOpts); diff != "" {
		t.Errorf("Inspect: Income:Capital.Units mismatch — residual computed against expanded per-child weights (-want +got):\n%s", diff)
	}
}
