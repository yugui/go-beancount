package inventory

import (
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
)

// astCmpOpts compares AST values structurally while routing apd.Decimal
// and time.Time through their own equality semantics (apd has
// unexported state, time carries monotonic clock data). The same
// option set is used by the loader/booking tests; duplicating it here
// avoids a test-only cross-package import.
var astCmpOpts = cmp.Options{
	// apd.Decimal is embedded by value (e.g. Amount.Number is a value
	// field, not a pointer), so cmp invokes the comparer at value
	// sites; a pointer-receiver form here would not be matched.
	cmp.Comparer(func(x, y apd.Decimal) bool { return x.Cmp(&y) == 0 }),
	cmp.Comparer(func(x, y time.Time) bool { return x.Equal(y) }),
}

// mkLedger builds an ast.Ledger from the given directives using the
// public Insert/InsertAll API, avoiding any dependence on testdata.
func mkLedger(dirs ...ast.Directive) *ast.Ledger {
	l := &ast.Ledger{}
	l.InsertAll(dirs)
	return l
}

// mkOpen constructs an Open directive with a typed booking method.
// Pass ast.BookingDefault to leave the booking unspecified.
func mkOpen(date time.Time, account string, booking ast.BookingMethod) *ast.Open {
	return &ast.Open{
		Date:    date,
		Account: ast.Account(account),
		Booking: booking,
	}
}

// mkClose constructs a Close directive.
func mkClose(date time.Time, account string) *ast.Close {
	return &ast.Close{Date: date, Account: ast.Account(account)}
}

// mkTxn bundles postings into a transaction on the given date. Posting
// values are copied into the transaction so callers can keep using the
// helper builders (which return *ast.Posting).
func mkTxn(date time.Time, postings ...*ast.Posting) *ast.Transaction {
	ps := make([]ast.Posting, len(postings))
	for i, p := range postings {
		ps[i] = *p
	}
	return &ast.Transaction{Date: date, Flag: '*', Postings: ps}
}

// TestReducerWalk_BasicTwoPostings exercises a simple balanced cash
// transaction: an Open for each account followed by a transfer. The
// visitor should receive nil before-state for both newly-touched
// accounts and a non-nil after snapshot for each, with two BookedPosting
// entries.
func TestReducerWalk_BasicTwoPostings(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	pos := mkAmountPtr(t, "100.00", "USD")
	neg := mkAmountPtr(t, "-100.00", "USD")
	txn := mkTxn(txnDate,
		&ast.Posting{Account: "Assets:Cash", Amount: pos},
		&ast.Posting{Account: "Expenses:Food", Amount: neg},
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		txn,
	)

	r := NewReducer(ledger.All())
	var visited int
	_, errs := r.Walk(func(got *ast.Transaction, before, after map[ast.Account]*Inventory, booked []BookedPosting) bool {
		visited++
		if got != txn {
			t.Errorf("visit: txn pointer mismatch")
		}
		if len(booked) != 2 {
			t.Errorf("visit: len(booked) = %d, want 2", len(booked))
		}
		if v, ok := before["Assets:Cash"]; !ok || v != nil {
			t.Errorf("visit: before[Assets:Cash] = %v (ok=%v), want nil snapshot", v, ok)
		}
		if v, ok := before["Expenses:Food"]; !ok || v != nil {
			t.Errorf("visit: before[Expenses:Food] = %v (ok=%v), want nil snapshot", v, ok)
		}
		if v := after["Assets:Cash"]; v == nil {
			t.Errorf("visit: after[Assets:Cash] is nil, want non-nil")
		}
		if v := after["Expenses:Food"]; v == nil {
			t.Errorf("visit: after[Expenses:Food] is nil, want non-nil")
		}
		return true
	})
	if len(errs) != 0 {
		t.Fatalf("Walk returned errors: %v", errs)
	}
	if visited != 1 {
		t.Errorf("visitor called %d times, want 1", visited)
	}
}

// TestReducerWalk_AutoPostingInference verifies that a posting with a
// nil Amount is mutated in place to absorb the transaction residual,
// and that its BookedPosting record has InferredAuto set.
func TestReducerWalk_AutoPostingInference(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	pos := mkAmountPtr(t, "42.50", "USD")
	txn := mkTxn(txnDate,
		&ast.Posting{Account: "Expenses:Food", Amount: pos},
		&ast.Posting{Account: "Assets:Cash"}, // auto
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		txn,
	)

	r := NewReducer(ledger.All())
	var inferredSeen bool
	booked, errs := r.Walk(func(_ *ast.Transaction, _, _ map[ast.Account]*Inventory, booked []BookedPosting) bool {
		if len(booked) != 2 {
			t.Fatalf("len(booked) = %d, want 2", len(booked))
		}
		for _, bp := range booked {
			if bp.Account == "Assets:Cash" {
				if !bp.InferredAuto {
					t.Errorf("Assets:Cash booked posting InferredAuto=false, want true")
				}
				inferredSeen = true
			} else if bp.InferredAuto {
				t.Errorf("Expenses:Food booked posting InferredAuto=true, want false")
			}
		}
		return true
	})
	if len(errs) != 0 {
		t.Fatalf("Walk returned errors: %v", errs)
	}
	if !inferredSeen {
		t.Errorf("no BookedPosting with InferredAuto=true was observed")
	}

	// The reducer must not have mutated the input transaction; the
	// auto-posting on the original txn should still have a nil Amount.
	if txn.Postings[1].Amount != nil {
		t.Errorf("input auto-posting Amount = %v, want nil (input must be immutable)", txn.Postings[1].Amount)
	}

	// The booked output's clone of the auto-posting should carry the
	// inferred residual.
	bookedTxn := findBookedTxn(t, booked, txnDate)
	autoPosting := &bookedTxn.Postings[1]
	if autoPosting.Amount == nil {
		t.Fatalf("booked auto-posting Amount is still nil after Walk")
	}
	if got := autoPosting.Amount.Currency; got != "USD" {
		t.Errorf("auto-posting currency = %q, want USD", got)
	}
	want := decimalVal(t, "-42.50")
	if got := autoPosting.Amount.Number; got.Cmp(&want) != 0 {
		t.Errorf("auto-posting number = %s, want %s", got.Text('f'), want.Text('f'))
	}
}

// findBookedTxn returns the single Transaction in booked whose Date
// matches d, failing the test if zero or multiple match. The helper is
// used by tests that want to inspect the reducer's clone of a specific
// transaction without depending on directive ordering.
func findBookedTxn(t *testing.T, booked []ast.Directive, d time.Time) *ast.Transaction {
	t.Helper()
	var hit *ast.Transaction
	for _, dir := range booked {
		tx, ok := dir.(*ast.Transaction)
		if !ok || !tx.Date.Equal(d) {
			continue
		}
		if hit != nil {
			t.Fatalf("findBookedTxn: multiple Transactions match date %s", d)
		}
		hit = tx
	}
	if hit == nil {
		t.Fatalf("findBookedTxn: no Transaction matches date %s", d)
	}
	return hit
}

// TestReducerWalk_AutoPostingWithCostRejected ensures an auto-posting
// that carries a cost spec is rejected with CodeInvalidAutoPosting and
// emits no BookedPosting for the transaction.
func TestReducerWalk_AutoPostingWithCostRejected(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	pos := mkAmountPtr(t, "100.00", "USD")
	auto := &ast.Posting{
		Account: "Assets:Cash",
		Cost:    &ast.CostSpec{}, // structural violation: cost on an auto-posting
	}
	txn := mkTxn(txnDate,
		&ast.Posting{Account: "Expenses:Food", Amount: pos},
		auto,
	)
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		txn,
	)

	r := NewReducer(ledger.All())
	visited := 0
	_, errs := r.Walk(func(*ast.Transaction, map[ast.Account]*Inventory, map[ast.Account]*Inventory, []BookedPosting) bool {
		visited++
		return true
	})
	// The pre-pass rejects the transaction before any state mutation,
	// so both before and booked are empty and Walk skips the visitor.
	if visited != 0 {
		t.Errorf("visitor called %d times on rejected txn, want 0", visited)
	}
	if len(errs) != 1 || errs[0].Code != CodeInvalidAutoPosting {
		t.Fatalf("Walk errs = %v, want [CodeInvalidAutoPosting]", errs)
	}
}

// TestReducerWalk_AutoPostingWithPriceRejected mirrors the Cost-on-auto
// rejection test but sets Price instead. The pre-pass should reject
// the transaction with CodeInvalidAutoPosting before any inventory
// state mutates.
func TestReducerWalk_AutoPostingWithPriceRejected(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	pos := mkAmountPtr(t, "100.00", "USD")
	auto := &ast.Posting{
		Account: "Assets:Cash",
		Price:   &ast.PriceAnnotation{}, // structural violation: price on an auto-posting
	}
	txn := mkTxn(txnDate,
		&ast.Posting{Account: "Expenses:Food", Amount: pos},
		auto,
	)
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		txn,
	)

	r := NewReducer(ledger.All())
	visited := 0
	_, errs := r.Walk(func(*ast.Transaction, map[ast.Account]*Inventory, map[ast.Account]*Inventory, []BookedPosting) bool {
		visited++
		return true
	})
	if visited != 0 {
		t.Errorf("visitor called %d times on rejected txn, want 0", visited)
	}
	if len(errs) != 1 || errs[0].Code != CodeInvalidAutoPosting {
		t.Fatalf("Walk errs = %v, want [CodeInvalidAutoPosting]", errs)
	}
}

// TestReducerWalk_AutoPostingZeroResidual ensures that an auto-posting
// attached to an already-balanced transaction (explicit postings sum
// to zero) is rejected with CodeUnresolvableInterpolation. The explicit
// postings are booked before the auto pass runs, so they appear in
// booked; the auto posting itself does not.
func TestReducerWalk_AutoPostingZeroResidual(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	pos := mkAmountPtr(t, "100.00", "USD")
	neg := mkAmountPtr(t, "-100.00", "USD")
	txn := mkTxn(txnDate,
		&ast.Posting{Account: "Assets:Cash", Amount: pos},
		&ast.Posting{Account: "Expenses:Food", Amount: neg},
		&ast.Posting{Account: "Equity:Plug"}, // auto with no residual to absorb
	)
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		mkOpen(openDate, "Equity:Plug", ast.BookingDefault),
		txn,
	)

	r := NewReducer(ledger.All())
	var gotBooked []BookedPosting
	_, errs := r.Walk(func(_ *ast.Transaction, _, _ map[ast.Account]*Inventory, booked []BookedPosting) bool {
		gotBooked = append(gotBooked, booked...)
		return true
	})
	if len(errs) != 1 || errs[0].Code != CodeUnresolvableInterpolation {
		t.Fatalf("Walk errs = %v, want [CodeUnresolvableInterpolation]", errs)
	}
	// The two explicit postings were booked in pass 1; the auto
	// posting is rejected in pass 2 without producing a BookedPosting.
	for _, bp := range gotBooked {
		if bp.Account == "Equity:Plug" {
			t.Errorf("auto posting Equity:Plug was booked, want no booking")
		}
	}
}

// TestReducerWalk_AutoPostingMultiCurrencyResidual ensures that an
// auto-posting is rejected when the residual spans more than one
// currency, because the inferred amount must settle exactly one
// currency.
func TestReducerWalk_AutoPostingMultiCurrencyResidual(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	usd := mkAmountPtr(t, "100.00", "USD")
	eur := mkAmountPtr(t, "200.00", "EUR")
	txn := mkTxn(txnDate,
		&ast.Posting{Account: "Assets:USD", Amount: usd},
		&ast.Posting{Account: "Assets:EUR", Amount: eur},
		&ast.Posting{Account: "Equity:Plug"}, // auto cannot absorb two currencies
	)
	ledger := mkLedger(
		mkOpen(openDate, "Assets:USD", ast.BookingDefault),
		mkOpen(openDate, "Assets:EUR", ast.BookingDefault),
		mkOpen(openDate, "Equity:Plug", ast.BookingDefault),
		txn,
	)

	r := NewReducer(ledger.All())
	var gotBooked []BookedPosting
	_, errs := r.Walk(func(_ *ast.Transaction, _, _ map[ast.Account]*Inventory, booked []BookedPosting) bool {
		gotBooked = append(gotBooked, booked...)
		return true
	})
	if len(errs) != 1 || errs[0].Code != CodeUnresolvableInterpolation {
		t.Fatalf("Walk errs = %v, want [CodeUnresolvableInterpolation]", errs)
	}
	for _, bp := range gotBooked {
		if bp.Account == "Equity:Plug" {
			t.Errorf("auto posting Equity:Plug was booked, want no booking")
		}
	}
}

// TestReducerWalk_MultipleAutoPostings ensures two nil-amount postings
// are reported via CodeMultipleAutoPostings.
func TestReducerWalk_MultipleAutoPostings(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	txn := mkTxn(txnDate,
		&ast.Posting{Account: "Assets:Cash"},
		&ast.Posting{Account: "Expenses:Food"},
	)
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		txn,
	)

	r := NewReducer(ledger.All())
	_, errs := r.Walk(nil)
	if len(errs) != 1 || errs[0].Code != CodeMultipleAutoPostings {
		t.Fatalf("Walk errs = %v, want [CodeMultipleAutoPostings]", errs)
	}
}

// TestReducerWalk_StatePersistsAcrossTransactions verifies that the
// second transaction's before-state matches the first transaction's
// after-state for the shared account.
func TestReducerWalk_StatePersistsAcrossTransactions(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d1 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)

	pos1 := mkAmountPtr(t, "100.00", "USD")
	neg1 := mkAmountPtr(t, "-100.00", "USD")
	pos2 := mkAmountPtr(t, "25.00", "USD")
	neg2 := mkAmountPtr(t, "-25.00", "USD")

	txn1 := mkTxn(d1,
		&ast.Posting{Account: "Assets:Cash", Amount: pos1},
		&ast.Posting{Account: "Expenses:Food", Amount: neg1},
	)
	txn2 := mkTxn(d2,
		&ast.Posting{Account: "Assets:Cash", Amount: pos2},
		&ast.Posting{Account: "Expenses:Food", Amount: neg2},
	)
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		txn1,
		txn2,
	)

	r := NewReducer(ledger.All())
	var afterT1 *Inventory
	call := 0
	_, errs := r.Walk(func(txn *ast.Transaction, before, after map[ast.Account]*Inventory, _ []BookedPosting) bool {
		call++
		switch call {
		case 1:
			if before["Assets:Cash"] != nil {
				t.Errorf("txn1 before[Assets:Cash] = %v, want nil", before["Assets:Cash"])
			}
			afterT1 = after["Assets:Cash"]
		case 2:
			got := before["Assets:Cash"]
			if got == nil {
				t.Fatalf("txn2 before[Assets:Cash] is nil, want snapshot matching txn1 after")
			}
			if !got.Equal(afterT1) {
				t.Errorf("txn2 before[Assets:Cash] does not equal txn1 after snapshot")
			}
		}
		return true
	})
	if len(errs) != 0 {
		t.Fatalf("Walk errs = %v", errs)
	}
	if call != 2 {
		t.Errorf("visitor called %d times, want 2", call)
	}
}

// TestReducerWalk_VisitorEarlyReturn confirms that returning false
// from the visitor halts iteration: the second transaction is not
// visited.
func TestReducerWalk_VisitorEarlyReturn(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d1 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)

	pos1 := mkAmountPtr(t, "10.00", "USD")
	neg1 := mkAmountPtr(t, "-10.00", "USD")
	pos2 := mkAmountPtr(t, "20.00", "USD")
	neg2 := mkAmountPtr(t, "-20.00", "USD")

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		mkTxn(d1,
			&ast.Posting{Account: "Assets:Cash", Amount: pos1},
			&ast.Posting{Account: "Expenses:Food", Amount: neg1},
		),
		mkTxn(d2,
			&ast.Posting{Account: "Assets:Cash", Amount: pos2},
			&ast.Posting{Account: "Expenses:Food", Amount: neg2},
		),
	)

	r := NewReducer(ledger.All())
	calls := 0
	_, errs := r.Walk(func(*ast.Transaction, map[ast.Account]*Inventory, map[ast.Account]*Inventory, []BookedPosting) bool {
		calls++
		return false // stop after first
	})
	if len(errs) != 0 {
		t.Fatalf("Walk errs = %v", errs)
	}
	if calls != 1 {
		t.Errorf("visitor called %d times, want 1 (early stop)", calls)
	}
}

// TestReducerWalk_OpenSetsBookingMethod confirms that the booking
// keyword on an Open directive is picked up for later transactions,
// and that a subsequent Close is a no-op for inventory.
func TestReducerWalk_OpenSetsBookingMethod(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	closeDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Stock", ast.BookingFIFO),
		mkClose(closeDate, "Assets:Stock"),
	)

	r := NewReducer(ledger.All())
	_, errs := r.Walk(nil)
	if len(errs) != 0 {
		t.Fatalf("Walk errs = %v", errs)
	}
	if got, want := r.booking["Assets:Stock"], ast.BookingFIFO; got != want {
		t.Errorf("booking[Assets:Stock] = %v, want %v", got, want)
	}
}

// TestReducerWalk_BeforeNilForFirstTouch documents the before-snapshot
// contract: an account that has not been touched before a transaction
// must appear in before with a nil *Inventory rather than an empty
// Inventory.
func TestReducerWalk_BeforeNilForFirstTouch(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	pos := mkAmountPtr(t, "1.00", "USD")
	neg := mkAmountPtr(t, "-1.00", "USD")

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		mkTxn(txnDate,
			&ast.Posting{Account: "Assets:Cash", Amount: pos},
			&ast.Posting{Account: "Expenses:Food", Amount: neg},
		),
	)

	r := NewReducer(ledger.All())
	_, errs := r.Walk(func(_ *ast.Transaction, before, _ map[ast.Account]*Inventory, _ []BookedPosting) bool {
		v, ok := before["Assets:Cash"]
		if !ok {
			t.Errorf("before map missing Assets:Cash entry")
		}
		if v != nil {
			t.Errorf("before[Assets:Cash] = %v, want nil (first touch)", v)
		}
		return true
	})
	if len(errs) != 0 {
		t.Fatalf("Walk errs = %v", errs)
	}
}

// TestReducerWalk_ReusableWalk asserts that calling Walk twice on the
// same Reducer resets state and yields identical results.
func TestReducerWalk_ReusableWalk(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	pos := mkAmountPtr(t, "50.00", "USD")
	neg := mkAmountPtr(t, "-50.00", "USD")
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		mkTxn(txnDate,
			&ast.Posting{Account: "Assets:Cash", Amount: pos},
			&ast.Posting{Account: "Expenses:Food", Amount: neg},
		),
	)

	collect := func(r *Reducer) (visits int, firstAfter map[ast.Account]*Inventory) {
		_, errs := r.Walk(func(_ *ast.Transaction, _, after map[ast.Account]*Inventory, _ []BookedPosting) bool {
			visits++
			if firstAfter == nil {
				firstAfter = after
			}
			return true
		})
		if len(errs) != 0 {
			t.Fatalf("Walk errs = %v", errs)
		}
		return visits, firstAfter
	}

	r := NewReducer(ledger.All())
	v1, a1 := collect(r)
	v2, a2 := collect(r)
	if v1 != v2 {
		t.Errorf("visit count differs between Walks: %d vs %d", v1, v2)
	}
	if len(a1) != len(a2) {
		t.Errorf("after-map size differs between Walks: %d vs %d", len(a1), len(a2))
	}
	for k := range a1 {
		if !a1[k].Equal(a2[k]) {
			t.Errorf("after[%s] differs between Walks", k)
		}
	}
}

// TestReducerRun_RetainsFinalState runs a multi-transaction ledger via
// Run and confirms that Final(account) exposes the final inventory for
// each touched account, matching the per-account state a Walk visitor
// would have seen on the last transaction.
func TestReducerRun_RetainsFinalState(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d1 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)

	pos1 := mkAmountPtr(t, "100.00", "USD")
	neg1 := mkAmountPtr(t, "-100.00", "USD")
	pos2 := mkAmountPtr(t, "25.00", "USD")
	neg2 := mkAmountPtr(t, "-25.00", "USD")
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		mkTxn(d1,
			&ast.Posting{Account: "Assets:Cash", Amount: pos1},
			&ast.Posting{Account: "Expenses:Food", Amount: neg1},
		),
		mkTxn(d2,
			&ast.Posting{Account: "Assets:Cash", Amount: pos2},
			&ast.Posting{Account: "Expenses:Food", Amount: neg2},
		),
	)

	// Collect the walk's final per-account state via Walk for reference.
	refR := NewReducer(ledger.All())
	var refFinal map[ast.Account]*Inventory
	_, refErrs := refR.Walk(func(_ *ast.Transaction, _, after map[ast.Account]*Inventory, _ []BookedPosting) bool {
		refFinal = after
		return true
	})
	if len(refErrs) != 0 {
		t.Fatalf("reference Walk errs = %v", refErrs)
	}

	r := NewReducer(ledger.All())
	_, errs := r.Run()
	if len(errs) != 0 {
		t.Fatalf("Run errs = %v", errs)
	}

	for acct, wantInv := range refFinal {
		got := r.Final(acct)
		if got == nil {
			t.Errorf("Final(%s) is nil, want non-nil", acct)
			continue
		}
		if !got.Equal(wantInv) {
			t.Errorf("Final(%s) does not match last visitor after-snapshot", acct)
		}
	}

	// Errors() should mirror the slice Run returned.
	gotErrs := r.Errors()
	if len(gotErrs) != len(errs) {
		t.Errorf("Errors() len = %d, Run len = %d", len(gotErrs), len(errs))
	}
}

// TestReducerRun_ClonesErrorsSlice confirms that the slice returned by
// Errors is independent of the reducer's internal errs slice; mutating
// it must not be visible on subsequent calls.
func TestReducerRun_ClonesErrorsSlice(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	// Construct a ledger that produces exactly one error
	// (CodeMultipleAutoPostings) so we have a non-empty errs slice to
	// mutate.
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		mkTxn(txnDate,
			&ast.Posting{Account: "Assets:Cash"},
			&ast.Posting{Account: "Expenses:Food"},
		),
	)

	r := NewReducer(ledger.All())
	_, errs := r.Run()
	if len(errs) != 1 {
		t.Fatalf("Run errs len = %d, want 1", len(errs))
	}
	// Mutate the returned slice with a sentinel Code value no real
	// error ever uses, so a leak would be obvious.
	const tampered Code = -999
	errs[0] = Error{Code: tampered, Message: "tampered"}

	fresh := r.Errors()
	if len(fresh) != 1 {
		t.Fatalf("Errors() len = %d, want 1", len(fresh))
	}
	if fresh[0].Code == tampered {
		t.Errorf("Errors() observed caller mutation; internal slice leaked")
	}
	if fresh[0].Code != CodeMultipleAutoPostings {
		t.Errorf("Errors()[0].Code = %v, want %v", fresh[0].Code, CodeMultipleAutoPostings)
	}
}

// TestReducerFinal_Untouched ensures Final returns nil for an account
// the ledger never touched.
func TestReducerFinal_Untouched(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	pos := mkAmountPtr(t, "5.00", "USD")
	neg := mkAmountPtr(t, "-5.00", "USD")
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		mkTxn(txnDate,
			&ast.Posting{Account: "Assets:Cash", Amount: pos},
			&ast.Posting{Account: "Expenses:Food", Amount: neg},
		),
	)

	r := NewReducer(ledger.All())
	if _, errs := r.Run(); len(errs) != 0 {
		t.Fatalf("Run errs = %v", errs)
	}
	if got := r.Final("Assets:Unrelated"); got != nil {
		t.Errorf("Final(Assets:Unrelated) = %v, want nil", got)
	}
	// Sanity: a touched account returns non-nil.
	if got := r.Final("Assets:Cash"); got == nil {
		t.Errorf("Final(Assets:Cash) = nil, want non-nil")
	}
}

// TestReducerInspect_FirstTransaction inspects the first transaction of
// a two-transaction ledger: before should hold nil snapshots for newly
// touched accounts, after should reflect only that transaction's
// effects, and booked should have the expected two entries.
func TestReducerInspect_FirstTransaction(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d1 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)

	pos1 := mkAmountPtr(t, "100.00", "USD")
	neg1 := mkAmountPtr(t, "-100.00", "USD")
	pos2 := mkAmountPtr(t, "25.00", "USD")
	neg2 := mkAmountPtr(t, "-25.00", "USD")

	txn1 := mkTxn(d1,
		&ast.Posting{Account: "Assets:Cash", Amount: pos1},
		&ast.Posting{Account: "Expenses:Food", Amount: neg1},
	)
	txn2 := mkTxn(d2,
		&ast.Posting{Account: "Assets:Cash", Amount: pos2},
		&ast.Posting{Account: "Expenses:Food", Amount: neg2},
	)
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		txn1,
		txn2,
	)

	r := NewReducer(ledger.All())
	insp, errs := r.Inspect(txn1)
	if len(errs) != 0 {
		t.Fatalf("Inspect errs = %v", errs)
	}
	if insp == nil {
		t.Fatalf("Inspect returned nil Inspection for known txn")
	}
	// Before should have nil entries for Assets:Cash and Expenses:Food.
	if v, ok := insp.Before["Assets:Cash"]; !ok || v != nil {
		t.Errorf("Before[Assets:Cash] = %v (ok=%v), want nil snapshot", v, ok)
	}
	if v, ok := insp.Before["Expenses:Food"]; !ok || v != nil {
		t.Errorf("Before[Expenses:Food] = %v (ok=%v), want nil snapshot", v, ok)
	}
	// After should contain both accounts with non-nil inventories.
	if insp.After["Assets:Cash"] == nil {
		t.Errorf("After[Assets:Cash] is nil, want non-nil")
	}
	if insp.After["Expenses:Food"] == nil {
		t.Errorf("After[Expenses:Food] is nil, want non-nil")
	}
	if got := len(insp.Booked); got != 2 {
		t.Errorf("len(Booked) = %d, want 2", got)
	}
}

// TestReducerInspect_MiddleTransaction inspects the middle transaction
// of a three-transaction ledger and verifies that Before reflects the
// state AFTER the first transaction, and After reflects the state AFTER
// the second transaction. The third transaction's effects must not be
// visible on either map.
func TestReducerInspect_MiddleTransaction(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d1 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	d3 := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)

	p1 := mkAmountPtr(t, "100.00", "USD")
	n1 := mkAmountPtr(t, "-100.00", "USD")
	p2 := mkAmountPtr(t, "25.00", "USD")
	n2 := mkAmountPtr(t, "-25.00", "USD")
	p3 := mkAmountPtr(t, "7.00", "USD")
	n3 := mkAmountPtr(t, "-7.00", "USD")

	txn1 := mkTxn(d1,
		&ast.Posting{Account: "Assets:Cash", Amount: p1},
		&ast.Posting{Account: "Expenses:Food", Amount: n1},
	)
	txn2 := mkTxn(d2,
		&ast.Posting{Account: "Assets:Cash", Amount: p2},
		&ast.Posting{Account: "Expenses:Food", Amount: n2},
	)
	txn3 := mkTxn(d3,
		&ast.Posting{Account: "Assets:Cash", Amount: p3},
		&ast.Posting{Account: "Expenses:Food", Amount: n3},
	)
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		txn1,
		txn2,
		txn3,
	)

	// Reference snapshots: collect the after-state of txn1 and txn2 via
	// a regular Walk so we can compare them to Inspect's output. Match
	// visited transactions by pointer identity so this test does not
	// depend on the visitor call order.
	refR := NewReducer(ledger.All())
	var afterT1, afterT2 map[ast.Account]*Inventory
	_, refErrs := refR.Walk(func(got *ast.Transaction, _, after map[ast.Account]*Inventory, _ []BookedPosting) bool {
		switch got {
		case txn1:
			afterT1 = map[ast.Account]*Inventory{}
			for k, v := range after {
				afterT1[k] = v.Clone()
			}
		case txn2:
			afterT2 = map[ast.Account]*Inventory{}
			for k, v := range after {
				afterT2[k] = v.Clone()
			}
		}
		return true
	})
	if len(refErrs) != 0 {
		t.Fatalf("reference Walk errs = %v", refErrs)
	}

	r := NewReducer(ledger.All())
	insp, errs := r.Inspect(txn2)
	if len(errs) != 0 {
		t.Fatalf("Inspect errs = %v", errs)
	}
	if insp == nil {
		t.Fatalf("Inspect returned nil Inspection for txn2")
	}

	// Before should match afterT1 for each touched account.
	for acct, wantInv := range afterT1 {
		got := insp.Before[acct]
		if got == nil {
			t.Errorf("Before[%s] is nil, want snapshot matching txn1 after", acct)
			continue
		}
		if !got.Equal(wantInv) {
			t.Errorf("Before[%s] does not equal txn1 after snapshot", acct)
		}
	}
	// After should match afterT2 for each touched account.
	for acct, wantInv := range afterT2 {
		got := insp.After[acct]
		if got == nil {
			t.Errorf("After[%s] is nil, want snapshot matching txn2 after", acct)
			continue
		}
		if !got.Equal(wantInv) {
			t.Errorf("After[%s] does not equal txn2 after snapshot", acct)
		}
	}
	if got := len(insp.Booked); got != 2 {
		t.Errorf("len(Booked) = %d, want 2", got)
	}
}

// TestReducerInspect_NotFound constructs a ledger with one transaction
// and inspects a separately constructed (pointer-distinct) transaction.
// Inspect must report a miss by returning (nil, errs).
func TestReducerInspect_NotFound(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d1 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	pos := mkAmountPtr(t, "10.00", "USD")
	neg := mkAmountPtr(t, "-10.00", "USD")
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		mkTxn(d1,
			&ast.Posting{Account: "Assets:Cash", Amount: pos},
			&ast.Posting{Account: "Expenses:Food", Amount: neg},
		),
	)

	// A pointer-distinct transaction with equivalent fields — Inspect
	// uses pointer identity, so this must NOT match.
	otherPos := mkAmountPtr(t, "10.00", "USD")
	otherNeg := mkAmountPtr(t, "-10.00", "USD")
	other := mkTxn(d1,
		&ast.Posting{Account: "Assets:Cash", Amount: otherPos},
		&ast.Posting{Account: "Expenses:Food", Amount: otherNeg},
	)

	r := NewReducer(ledger.All())
	insp, errs := r.Inspect(other)
	if insp != nil {
		t.Errorf("Inspect(other) = %v, want nil", insp)
	}
	if len(errs) != 0 {
		t.Errorf("Inspect(other) errs = %v, want none for clean ledger", errs)
	}
}

// TestReducerInspect_SnapshotIndependence confirms that running the
// reducer again after Inspect does not mutate the returned Inspection:
// Before, After, and Booked must remain a frozen view of the target
// transaction's surroundings.
func TestReducerInspect_SnapshotIndependence(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d1 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)

	p1 := mkAmountPtr(t, "100.00", "USD")
	n1 := mkAmountPtr(t, "-100.00", "USD")
	p2 := mkAmountPtr(t, "25.00", "USD")
	n2 := mkAmountPtr(t, "-25.00", "USD")

	txn1 := mkTxn(d1,
		&ast.Posting{Account: "Assets:Cash", Amount: p1},
		&ast.Posting{Account: "Expenses:Food", Amount: n1},
	)
	txn2 := mkTxn(d2,
		&ast.Posting{Account: "Assets:Cash", Amount: p2},
		&ast.Posting{Account: "Expenses:Food", Amount: n2},
	)
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Food", ast.BookingDefault),
		txn1,
		txn2,
	)

	r := NewReducer(ledger.All())
	insp, errs := r.Inspect(txn1)
	if len(errs) != 0 {
		t.Fatalf("Inspect errs = %v", errs)
	}
	if insp == nil {
		t.Fatalf("Inspect returned nil for txn1")
	}

	// Snapshot the inspection so we can detect mutation even if the
	// structures themselves are later altered by reducer internals.
	beforeSnap := map[ast.Account]*Inventory{}
	for k, v := range insp.Before {
		if v == nil {
			beforeSnap[k] = nil
		} else {
			beforeSnap[k] = v.Clone()
		}
	}
	afterSnap := map[ast.Account]*Inventory{}
	for k, v := range insp.After {
		if v == nil {
			afterSnap[k] = nil
		} else {
			afterSnap[k] = v.Clone()
		}
	}
	bookedLen := len(insp.Booked)

	// Re-walk with a no-op visitor so reducer state advances past txn2.
	if _, errs := r.Walk(func(*ast.Transaction, map[ast.Account]*Inventory, map[ast.Account]*Inventory, []BookedPosting) bool {
		return true
	}); len(errs) != 0 {
		t.Fatalf("post-Inspect Walk errs = %v", errs)
	}

	// Inspection must remain unchanged.
	if got := len(insp.Before); got != len(beforeSnap) {
		t.Errorf("Before map size changed: %d, want %d", got, len(beforeSnap))
	}
	for k, want := range beforeSnap {
		got, ok := insp.Before[k]
		if !ok {
			t.Errorf("Before[%s] missing after post-Inspect Walk", k)
			continue
		}
		if want == nil {
			if got != nil {
				t.Errorf("Before[%s] = %v, want nil", k, got)
			}
			continue
		}
		if got == nil || !got.Equal(want) {
			t.Errorf("Before[%s] mutated by post-Inspect Walk", k)
		}
	}
	if got := len(insp.After); got != len(afterSnap) {
		t.Errorf("After map size changed: %d, want %d", got, len(afterSnap))
	}
	for k, want := range afterSnap {
		got, ok := insp.After[k]
		if !ok {
			t.Errorf("After[%s] missing after post-Inspect Walk", k)
			continue
		}
		if want == nil {
			if got != nil {
				t.Errorf("After[%s] = %v, want nil", k, got)
			}
			continue
		}
		if got == nil || !got.Equal(want) {
			t.Errorf("After[%s] mutated by post-Inspect Walk", k)
		}
	}
	if got := len(insp.Booked); got != bookedLen {
		t.Errorf("Booked len changed: %d, want %d", got, bookedLen)
	}
}

// TestReducerWalk_InterpolatesSingleDeferred_DateLabel exercises the
// upstream "transfer with date+label cost spec" pattern: the
// augmenting side carries `{date, "label"}` (no number), the reducing
// side carries the same lot key. Pass 2 fills the missing per-unit
// from the reduction's resolved weight. After Walk, both sides have a
// resolved Lot and no errors are emitted.
func TestReducerWalk_InterpolatesSingleDeferred_DateLabel(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	xferDate := time.Date(2025, 2, 17, 0, 0, 0, 0, time.UTC)

	// Seed Assets:B with 10 STOCK at 100 JPY {date, "label"}.
	buy := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:B",
			Amount:  mkAmountPtr(t, "10", "STOCK"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "JPY",
				Date:     &buyDate,
				Label:    "label",
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-1000", "JPY")},
	)
	// Transfer 5 STOCK out of Assets:B into Assets:A. Assets:A's
	// cost spec is "{date, "label"}" — no number — and the reducer
	// must interpolate 100 JPY from Assets:B's resolved lot.
	xfer := mkTxn(xferDate,
		&ast.Posting{
			Account: "Assets:A",
			Amount:  mkAmountPtr(t, "5", "STOCK"),
			Cost: &ast.CostSpec{
				Date:  &buyDate,
				Label: "label",
			},
		},
		&ast.Posting{
			Account: "Assets:B",
			Amount:  mkAmountPtr(t, "-5", "STOCK"),
			Cost: &ast.CostSpec{
				Date:  &buyDate,
				Label: "label",
			},
		},
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:A", ast.BookingDefault),
		mkOpen(openDate, "Assets:B", ast.BookingDefault),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		buy,
		xfer,
	)

	r := NewReducer(ledger.All())
	var xferBooked []BookedPosting
	_, errs := r.Walk(func(got *ast.Transaction, _, _ map[ast.Account]*Inventory, booked []BookedPosting) bool {
		if got == xfer {
			xferBooked = append([]BookedPosting(nil), booked...)
		}
		return true
	})
	if len(errs) != 0 {
		t.Fatalf("Walk errs = %v, want none", errs)
	}
	if len(xferBooked) != 2 {
		t.Fatalf("xfer booked len = %d, want 2", len(xferBooked))
	}
	want := decimalVal(t, "100")
	for _, bp := range xferBooked {
		switch bp.Account {
		case "Assets:A":
			if bp.Lot == nil {
				t.Fatalf("Walk Assets:A: Lot is nil after interpolation")
			}
			if bp.Lot.Number.Cmp(&want) != 0 {
				t.Errorf("Walk Assets:A: Lot.Number = %s, want 100", bp.Lot.Number.Text('f'))
			}
			if bp.Lot.Currency != "JPY" {
				t.Errorf("Walk Assets:A: Lot.Currency = %q, want JPY", bp.Lot.Currency)
			}
			if !bp.Lot.Date.Equal(buyDate) {
				t.Errorf("Walk Assets:A: Lot.Date = %v, want %v (preserved from spec)", bp.Lot.Date, buyDate)
			}
			if bp.Lot.Label != "label" {
				t.Errorf("Walk Assets:A: Lot.Label = %q, want %q (preserved from spec)", bp.Lot.Label, "label")
			}
		case "Assets:B":
			if bp.Reduction == nil {
				t.Fatalf("Walk Assets:B: Reduction is nil, want a step")
			}
			step := *bp.Reduction
			if step.Lot.Number.Cmp(&want) != 0 {
				t.Errorf("Walk Assets:B: step Lot.Number = %s, want 100", step.Lot.Number.Text('f'))
			}
		}
	}
}

// TestReducerWalk_InterpolatesSingleDeferred_EmptyBraces exercises
// the `{}` form: the augmenting side declares "this is a lot, fill
// the cost from context" and the reducing side picks an existing
// lot. Identical to the date+label case from the reducer's
// perspective; the assertions confirm the empty-braces shape is also
// accepted.
func TestReducerWalk_InterpolatesSingleDeferred_EmptyBraces(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	xferDate := time.Date(2025, 2, 17, 0, 0, 0, 0, time.UTC)

	buy := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:B",
			Amount:  mkAmountPtr(t, "10", "STOCK"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "JPY",
				Date:     &buyDate,
				Label:    "label",
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-1000", "JPY")},
	)
	xfer := mkTxn(xferDate,
		&ast.Posting{
			Account: "Assets:A",
			Amount:  mkAmountPtr(t, "5", "STOCK"),
			Cost:    &ast.CostSpec{}, // bare "{}"
		},
		&ast.Posting{
			Account: "Assets:B",
			Amount:  mkAmountPtr(t, "-5", "STOCK"),
			Cost: &ast.CostSpec{
				Date:  &buyDate,
				Label: "label",
			},
		},
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:A", ast.BookingDefault),
		mkOpen(openDate, "Assets:B", ast.BookingDefault),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		buy,
		xfer,
	)

	r := NewReducer(ledger.All())
	var xferBooked []BookedPosting
	_, errs := r.Walk(func(got *ast.Transaction, _, _ map[ast.Account]*Inventory, booked []BookedPosting) bool {
		if got == xfer {
			xferBooked = append([]BookedPosting(nil), booked...)
		}
		return true
	})
	if len(errs) != 0 {
		t.Fatalf("Walk errs = %v, want none", errs)
	}
	want := decimalVal(t, "100")
	var sawA bool
	for _, bp := range xferBooked {
		if bp.Account == "Assets:A" {
			sawA = true
			if bp.Lot == nil || bp.Lot.Number.Cmp(&want) != 0 {
				t.Errorf("Walk Assets:A: Lot = %+v, want Number=100 JPY", bp.Lot)
			}
			if bp.Lot != nil && bp.Lot.Currency != "JPY" {
				t.Errorf("Walk Assets:A: Lot.Currency = %q, want JPY", bp.Lot.Currency)
			}
			// Bare "{}" omits Date and Label, so the resolved Lot's
			// Date defaults to the transaction date and Label is empty.
			if bp.Lot != nil && !bp.Lot.Date.Equal(xferDate) {
				t.Errorf("Walk Assets:A: Lot.Date = %v, want %v (txnDate fallback)", bp.Lot.Date, xferDate)
			}
			if bp.Lot != nil && bp.Lot.Label != "" {
				t.Errorf("Walk Assets:A: Lot.Label = %q, want \"\" (no label on \"{}\")", bp.Lot.Label)
			}
		}
	}
	if !sawA {
		t.Errorf("Walk: Assets:A booking not observed")
	}
}

// TestReducerWalk_InterpolationAmbiguousMultipleResidualCurrencies
// exercises the multi-currency-residual rejection path with a
// deferred cost spec (rather than an auto-posting). The transaction
// has one deferred augment and two reductions that resolve to two
// different currencies; the residual cannot be expressed in a single
// cost currency.
func TestReducerWalk_InterpolationAmbiguousMultipleResidualCurrencies(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	xferDate := time.Date(2025, 2, 17, 0, 0, 0, 0, time.UTC)

	usd := mkAmountPtr(t, "100.00", "USD")
	eur := mkAmountPtr(t, "200.00", "EUR")
	deferred := &ast.Posting{
		Account: "Assets:A",
		Amount:  mkAmountPtr(t, "5", "STOCK"),
		Cost:    &ast.CostSpec{}, // unknown
	}
	txn := mkTxn(xferDate,
		&ast.Posting{Account: "Assets:USD", Amount: usd},
		&ast.Posting{Account: "Assets:EUR", Amount: eur},
		deferred,
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:A", ast.BookingDefault),
		mkOpen(openDate, "Assets:USD", ast.BookingDefault),
		mkOpen(openDate, "Assets:EUR", ast.BookingDefault),
		txn,
	)

	r := NewReducer(ledger.All())
	_, errs := r.Walk(nil)
	if len(errs) != 1 || errs[0].Code != CodeUnresolvableInterpolation {
		t.Fatalf("Walk errs = %v, want [CodeUnresolvableInterpolation]", errs)
	}
}

// TestReducerWalk_InterpolationAmbiguousMultipleDeferred ensures that
// two deferred postings in the same transaction are both rejected
// with CodeUnresolvableInterpolation, even if a unique solution might
// look possible by inspection: the system explicitly refuses to guess
// among multiple unknowns.
func TestReducerWalk_InterpolationAmbiguousMultipleDeferred(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	xferDate := time.Date(2025, 2, 17, 0, 0, 0, 0, time.UTC)

	buy := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:B",
			Amount:  mkAmountPtr(t, "10", "STOCK"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "JPY",
				Date:     &buyDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-1000", "JPY")},
	)
	// Two deferred augmenting postings + one reducing posting.
	d1 := &ast.Posting{
		Account: "Assets:A",
		Amount:  mkAmountPtr(t, "3", "STOCK"),
		Cost:    &ast.CostSpec{},
	}
	d2 := &ast.Posting{
		Account: "Assets:C",
		Amount:  mkAmountPtr(t, "2", "STOCK"),
		Cost:    &ast.CostSpec{},
	}
	xfer := mkTxn(xferDate, d1, d2, &ast.Posting{
		Account: "Assets:B",
		Amount:  mkAmountPtr(t, "-5", "STOCK"),
		Cost: &ast.CostSpec{
			Date: &buyDate,
		},
	})

	ledger := mkLedger(
		mkOpen(openDate, "Assets:A", ast.BookingDefault),
		mkOpen(openDate, "Assets:B", ast.BookingDefault),
		mkOpen(openDate, "Assets:C", ast.BookingDefault),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		buy,
		xfer,
	)

	r := NewReducer(ledger.All())
	_, errs := r.Walk(nil)
	if len(errs) != 2 {
		t.Fatalf("Walk errs = %v, want 2 entries (one per deferred posting)", errs)
	}
	for _, e := range errs {
		if e.Code != CodeUnresolvableInterpolation {
			t.Errorf("err.Code = %v, want CodeUnresolvableInterpolation", e.Code)
		}
	}
}

// TestReducerWalk_InterpolationAmbiguousDeferredPlusAutoPosting
// rejects the case where a transaction has both a deferred cost spec
// and an auto-posting: each is a separate unknown the residual cannot
// jointly resolve.
func TestReducerWalk_InterpolationAmbiguousDeferredPlusAutoPosting(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	xferDate := time.Date(2025, 2, 17, 0, 0, 0, 0, time.UTC)

	buy := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:B",
			Amount:  mkAmountPtr(t, "10", "STOCK"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "JPY",
				Date:     &buyDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-1000", "JPY")},
	)
	xfer := mkTxn(xferDate,
		&ast.Posting{
			Account: "Assets:A",
			Amount:  mkAmountPtr(t, "5", "STOCK"),
			Cost:    &ast.CostSpec{},
		},
		&ast.Posting{
			Account: "Assets:B",
			Amount:  mkAmountPtr(t, "-5", "STOCK"),
			Cost: &ast.CostSpec{
				Date: &buyDate,
			},
		},
		&ast.Posting{Account: "Equity:Plug"}, // auto-posting
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:A", ast.BookingDefault),
		mkOpen(openDate, "Assets:B", ast.BookingDefault),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		mkOpen(openDate, "Equity:Plug", ast.BookingDefault),
		buy,
		xfer,
	)

	r := NewReducer(ledger.All())
	_, errs := r.Walk(nil)
	if len(errs) != 2 {
		t.Fatalf("Walk errs = %v, want 2 entries", errs)
	}
	for _, e := range errs {
		if e.Code != CodeUnresolvableInterpolation {
			t.Errorf("err.Code = %v, want CodeUnresolvableInterpolation", e.Code)
		}
	}
}

// TestReducerWalk_AutoPostingResidualUsesBookedReductions is a
// regression test for the residual-computation correctness fix: a
// transaction that combines a partial-cost reduction with an
// auto-posting must compute the residual from the reduction's
// resolved weight (a per-unit cost from the matched lot), not from
// the AST's still-partial spec. Without the fix the auto-posting
// would observe zero non-cost-currency residual and report
// CodeUnresolvableInterpolation; with the fix the auto-posting
// correctly absorbs the cost-currency residual.
func TestReducerWalk_AutoPostingResidualUsesBookedReductions(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	sellDate := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)

	buy := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "10", "STOCK"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "JPY",
				Date:     &buyDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-1000", "JPY")},
	)
	// Sell with a partial cost spec (date only); the reducer must
	// resolve the lot. The auto-posting should absorb +500 JPY.
	sell := mkTxn(sellDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "-5", "STOCK"),
			Cost: &ast.CostSpec{
				Date: &buyDate,
			},
		},
		&ast.Posting{Account: "Assets:Cash"}, // auto-posting
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Brokerage", ast.BookingDefault),
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		buy,
		sell,
	)

	r := NewReducer(ledger.All())
	booked, errs := r.Walk(nil)
	if len(errs) != 0 {
		t.Fatalf("Walk errs = %v, want none", errs)
	}
	if sell.Postings[1].Amount != nil {
		t.Errorf("input auto-posting Amount = %v, want nil (input must be immutable)", sell.Postings[1].Amount)
	}
	bookedSell := findBookedTxn(t, booked, sellDate)
	auto := &bookedSell.Postings[1]
	if auto.Amount == nil {
		t.Fatalf("Walk: booked auto-posting Amount is still nil")
	}
	if auto.Amount.Currency != "JPY" {
		t.Errorf("Walk: auto-posting currency = %q, want JPY", auto.Amount.Currency)
	}
	want := decimalVal(t, "500")
	if auto.Amount.Number.Cmp(&want) != 0 {
		t.Errorf("Walk: auto-posting number = %s, want 500", auto.Amount.Number.Text('f'))
	}
}

// TestReducerWalk_InterpolationZeroUnits guards the divide-by-zero
// branch: a deferred posting whose units are zero cannot have its
// per-unit cost interpolated, even if the residual is otherwise
// well-defined. The diagnostic surfaces as
// CodeUnresolvableInterpolation rather than letting an arithmetic
// overflow leak through.
func TestReducerWalk_InterpolationZeroUnits(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	xferDate := time.Date(2025, 2, 17, 0, 0, 0, 0, time.UTC)

	buy := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:B",
			Amount:  mkAmountPtr(t, "10", "STOCK"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "JPY",
				Date:     &buyDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-1000", "JPY")},
	)
	// Reducing posting alone produces -500 JPY of residual; the
	// zero-unit deferred posting cannot absorb it because dividing
	// the residual by zero units is undefined.
	xfer := mkTxn(xferDate,
		&ast.Posting{
			Account: "Assets:A",
			Amount:  mkAmountPtr(t, "0", "STOCK"), // zero-unit deferred
			Cost:    &ast.CostSpec{},
		},
		&ast.Posting{
			Account: "Assets:B",
			Amount:  mkAmountPtr(t, "-5", "STOCK"),
			Cost: &ast.CostSpec{
				Date: &buyDate,
			},
		},
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:A", ast.BookingDefault),
		mkOpen(openDate, "Assets:B", ast.BookingDefault),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		buy,
		xfer,
	)

	r := NewReducer(ledger.All())
	_, errs := r.Walk(nil)
	if len(errs) != 1 || errs[0].Code != CodeUnresolvableInterpolation {
		t.Fatalf("Walk errs = %v, want [CodeUnresolvableInterpolation]", errs)
	}
}

// TestReducerWalk_DoesNotMutateInput exercises every interpolation /
// rewrite path the reducer can take — auto-posting amount fill,
// deferred per-unit cost fill, single-lot reduction Cost install, and
// multi-lot reduction expansion from a bare cost spec — in a single
// Walk and asserts that the caller's directives are byte-for-byte
// identical afterwards. Expansion grows the booked transaction's
// posting count via [postingResolution]; the caller's pre-Walk
// snapshot remains untouched.
//
// Snapshotting via deep clone (`*Transaction.Clone`) and diffing with
// astCmpOpts catches any mutation Walk might leak through, regardless
// of which Posting field it touches; this is broader than the
// single-field assertions sprinkled across the other tests.
func TestReducerWalk_DoesNotMutateInput(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	buy2Date := time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC)
	cashTxnDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	deferredDate := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	sellDate := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)

	openInv := mkOpen(openDate, "Assets:Investments", ast.BookingFIFO)
	openCash := mkOpen(openDate, "Assets:Cash", ast.BookingDefault)
	openOpen := mkOpen(openDate, "Equity:Opening", ast.BookingDefault)

	// Buy 1: seeds the FIFO sale below.
	buy1 := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:Investments",
			Amount:  mkAmountPtr(t, "10", "STOCK"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "JPY",
				Date:     &buyDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-1000", "JPY")},
	)
	// Buy 2: second lot, so the sale below crosses the FIFO boundary.
	buy2 := mkTxn(buy2Date,
		&ast.Posting{
			Account: "Assets:Investments",
			Amount:  mkAmountPtr(t, "10", "STOCK"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "110"),
				Currency: "JPY",
				Date:     &buy2Date,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-1100", "JPY")},
	)
	// Pure cash transaction with an auto-balanced posting (Amount
	// nil) — exercises Reducer.fillAutoPosting on the input clone.
	cashTxn := mkTxn(cashTxnDate,
		&ast.Posting{Account: "Assets:Cash", Amount: mkAmountPtr(t, "42.50", "USD")},
		&ast.Posting{Account: "Equity:Opening"}, // auto
	)
	// Augmentation with a deferred per-unit cost (`{}` form): the
	// reducer infers the per-unit cost from the residual and
	// historically wrote it back to the AST.
	deferred := mkTxn(deferredDate,
		&ast.Posting{
			Account: "Assets:Investments",
			Amount:  mkAmountPtr(t, "5", "STOCK"),
			Cost:    &ast.CostSpec{}, // empty / deferred
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-600", "JPY")},
	)
	// Multi-lot reduction with a bare cost spec: the matcher
	// resolves the lots from inventory and the reducer expands the
	// posting into one child per matched lot on the booked clone.
	sell := mkTxn(sellDate,
		&ast.Posting{
			Account: "Assets:Investments",
			Amount:  mkAmountPtr(t, "-12", "STOCK"),
			Cost:    &ast.CostSpec{},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "1230", "JPY")},
	)

	// Deep-clone every Transaction to capture a frozen snapshot of
	// the input; Open directives are immutable scalar wrappers and
	// are safe to share by reference. Comparing the post-Walk
	// Transactions against these snapshots surfaces any mutation
	// Walk performed on the caller's AST.
	type snap struct {
		txn   *ast.Transaction
		clone *ast.Transaction
	}
	snaps := []snap{
		{buy1, buy1.Clone()},
		{buy2, buy2.Clone()},
		{cashTxn, cashTxn.Clone()},
		{deferred, deferred.Clone()},
		{sell, sell.Clone()},
	}

	ledger := mkLedger(openInv, openCash, openOpen, buy1, buy2, cashTxn, deferred, sell)
	r := NewReducer(ledger.All())
	_, errs := r.Walk(nil)
	if len(errs) != 0 {
		t.Fatalf("Walk errs = %v, want none", errs)
	}

	for _, s := range snaps {
		if diff := cmp.Diff(s.clone, s.txn, astCmpOpts...); diff != "" {
			t.Errorf("Reducer.Walk mutated input transaction (date=%s), diff (-want +got):\n%s",
				s.txn.Date.Format("2006-01-02"), diff)
		}
	}
}

// ---- Currency-group drop: Pass 1 wiring ----

// TestReducerWalk_FailedGroupDroppedAtomically verifies the atomic
// currency-group drop semantics: a failed bookOne in Pass 1 marks its
// weight-currency group as dropped, suppresses the failing posting from
// txn.Postings, and rolls the group's account out of the visitor
// before/after diff. The surviving group's booking and inventory
// mutation are unaffected.
//
// Scenario: buy builds 5 AAPL @ 100 USD. The sell transaction has two
// groups: a USD group (reduce 10 AAPL — CodeReductionExceedsInventory)
// and a EUR group (augment EUR cash — succeeds). The USD group is
// dropped atomically; the EUR group survives.
func TestReducerWalk_FailedGroupDroppedAtomically(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	sellDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)

	// Buy 5 AAPL @ 100 USD into the brokerage account.
	buy := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "5", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "USD",
				Date:     &buyDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-500", "USD")},
	)

	// Sell transaction with two currency groups:
	//   - failing group (USD): attempt to reduce 10 AAPL{buyDate} but only 5 exist
	//     → CodeReductionExceedsInventory → this group is dropped atomically
	//   - surviving group (EUR): a plain EUR cash augmentation → succeeds
	sell := mkTxn(sellDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "-10", "AAPL"), // more than the 5 available
			Cost: &ast.CostSpec{
				Date: &buyDate,
			},
		},
		&ast.Posting{
			Account: "Assets:EurCash",
			Amount:  mkAmountPtr(t, "800", "EUR"),
		},
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Brokerage", ast.BookingFIFO),
		mkOpen(openDate, "Assets:EurCash", ast.BookingDefault),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		buy,
		sell,
	)

	r := NewReducer(ledger.All())
	var (
		sellBefore map[ast.Account]*Inventory
		sellAfter  map[ast.Account]*Inventory
		sellBooked []BookedPosting
	)
	_, errs := r.Walk(func(got *ast.Transaction, before, after map[ast.Account]*Inventory, booked []BookedPosting) bool {
		if got == sell {
			sellBefore = before
			sellAfter = after
			sellBooked = append([]BookedPosting(nil), booked...)
		}
		return true
	})

	// The failing reduction must emit exactly one error.
	if len(errs) != 1 {
		t.Fatalf("Walk errs = %v (len=%d), want exactly 1 error", errs, len(errs))
	}
	if errs[0].Code != CodeReductionExceedsInventory {
		t.Errorf("err.Code = %v, want CodeReductionExceedsInventory", errs[0].Code)
	}

	// Verification is via the sellBooked slice populated by the visitor.

	// The surviving group (EUR) produces exactly one BookedPosting.
	var eurBooked, aaplBooked int
	for _, bp := range sellBooked {
		switch bp.Account {
		case "Assets:EurCash":
			eurBooked++
		case "Assets:Brokerage":
			aaplBooked++
		}
	}
	if eurBooked != 1 {
		t.Errorf("Walk(sell): EUR BookedPosting count = %d, want 1 (surviving group)", eurBooked)
	}
	// The failing AAPL posting produces no BookedPosting (it was dropped).
	if aaplBooked != 0 {
		t.Errorf("Walk(sell): AAPL BookedPosting count = %d, want 0 (failing group gets no BookedPosting)", aaplBooked)
	}

	// The surviving group's account must appear in the after-snapshot.
	if sellAfter["Assets:EurCash"] == nil {
		t.Errorf("Walk(sell): after[Assets:EurCash] is nil; surviving group must touch the account")
	}

	// The dropped group's account (Assets:Brokerage) must NOT appear in
	// before or after: the group was rolled back atomically, leaving the
	// account unchanged, so diff() suppresses it from the visitor output.
	if _, ok := sellBefore["Assets:Brokerage"]; ok {
		t.Errorf("Walk(sell): before[Assets:Brokerage] present; dropped group account must not appear in before")
	}
	if _, ok := sellAfter["Assets:Brokerage"]; ok {
		t.Errorf("Walk(sell): after[Assets:Brokerage] present; dropped group account must not appear in after")
	}

	// The brokerage inventory must still hold the 5 AAPL from buy.
	// (The failing reduction left no mutation; verify via r.Final.)
	brokerageFinal := r.Final("Assets:Brokerage")
	if brokerageFinal == nil {
		t.Fatalf("r.Final(Assets:Brokerage) is nil; buy transaction should have seeded the inventory")
	}
	if brokerageFinal.Len() != 1 {
		t.Errorf("r.Final(Assets:Brokerage) has %d positions, want 1 (5 AAPL{buyDate} lot unaffected)", brokerageFinal.Len())
	}
}

// TestReducerWalk_MultiCurrencyMultiLotReductionGrouping verifies
// that when a single -N COMMODITY {} posting matches lots with two
// different cost currencies, the reducer's multi-lot reduction path
// expands the posting via addMultiLotReduction into one BookedPosting
// per matched lot, and each child BookedPosting carries the correct
// step currency (USD for the USD-cost lot, EUR for the EUR-cost lot).
func TestReducerWalk_MultiCurrencyMultiLotReductionGrouping(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyUSDDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	buyEURDate := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	sellDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)

	// Buy 10 AAPL at 100 USD per share.
	buyUSD := mkTxn(buyUSDDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "10", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "USD",
				Date:     &buyUSDDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-1000", "USD")},
	)

	// Buy 10 AAPL at 90 EUR per share (different cost currency).
	buyEUR := mkTxn(buyEURDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "10", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "90"),
				Currency: "EUR",
				Date:     &buyEURDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-900", "EUR")},
	)

	// Sell 20 AAPL with bare cost spec — matches both lots (multi-lot reduction).
	// The reducer expands this into two child postings, one for each matched lot.
	sell := mkTxn(sellDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "-20", "AAPL"),
			Cost:    &ast.CostSpec{}, // empty: matches all lots
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "2800", "USD")},
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Brokerage", ast.BookingFIFO),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		buyUSD,
		buyEUR,
		sell,
	)

	r := NewReducer(ledger.All())
	var sellBooked []BookedPosting
	_, errs := r.Walk(func(got *ast.Transaction, _, _ map[ast.Account]*Inventory, booked []BookedPosting) bool {
		if got == sell {
			sellBooked = append([]BookedPosting(nil), booked...)
		}
		return true
	})
	if len(errs) != 0 {
		t.Fatalf("Walk errs = %v, want none", errs)
	}

	// The multi-lot reduction expands into two child postings, one per matched
	// lot (AAPL{USD} and AAPL{EUR}). Verify both are present in sellBooked.
	var usdSteps, eurSteps []*ReductionStep
	for i := range sellBooked {
		bp := &sellBooked[i]
		if bp.Account != "Assets:Brokerage" {
			continue
		}
		if bp.Reduction == nil {
			t.Errorf("sellBooked[%d] (Brokerage): Reduction is nil, want a step", i)
			continue
		}
		switch bp.Reduction.Lot.Currency {
		case "USD":
			usdSteps = append(usdSteps, bp.Reduction)
		case "EUR":
			eurSteps = append(eurSteps, bp.Reduction)
		default:
			t.Errorf("sellBooked[%d] (Brokerage): unexpected lot currency %q", i, bp.Reduction.Lot.Currency)
		}
	}

	if len(usdSteps) != 1 {
		t.Errorf("Walk(sell): USD lot BookedPosting count = %d, want 1", len(usdSteps))
	}
	if len(eurSteps) != 1 {
		t.Errorf("Walk(sell): EUR lot BookedPosting count = %d, want 1", len(eurSteps))
	}

	// Each step must report the full lot quantity (10 AAPL).
	wantQty := decimalVal(t, "10")
	for _, step := range usdSteps {
		if step.Units.Cmp(&wantQty) != 0 {
			t.Errorf("Walk(sell): USD step.Units = %s, want 10", step.Units.Text('f'))
		}
	}
	for _, step := range eurSteps {
		if step.Units.Cmp(&wantQty) != 0 {
			t.Errorf("Walk(sell): EUR step.Units = %s, want 10", step.Units.Text('f'))
		}
	}

	// After the sell, the brokerage should be empty (both lots fully consumed).
	brokerageAfter := r.Final("Assets:Brokerage")
	if brokerageAfter == nil || !brokerageAfter.IsEmpty() {
		t.Errorf("r.Final(Assets:Brokerage) = %v, want empty (both lots consumed)", brokerageAfter)
	}
}

// ---- Drop-application: applyDrops rebuilds txn.Postings ----

// TestReducerWalk_DroppedGroupOmittedFromTxnPostings verifies that the
// posting from a failed currency group is absent from txn.Postings in
// the Walk directive output, while the surviving posting is present in
// its original input order. Source pointers on the surviving
// BookedPosting must alias into the rebuilt txn.Postings slice.
func TestReducerWalk_DroppedGroupOmittedFromTxnPostings(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	sellDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)

	// Seed 5 AAPL @ 100 USD.
	buy := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "5", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "USD",
				Date:     &buyDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-500", "USD")},
	)

	// Sell transaction: failing USD group (reduction exceeds inventory)
	// followed by a surviving EUR cash augmentation.
	sell := mkTxn(sellDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "-10", "AAPL"), // fails: only 5 available
			Cost:    &ast.CostSpec{Date: &buyDate},
		},
		&ast.Posting{
			Account: "Assets:EurCash",
			Amount:  mkAmountPtr(t, "800", "EUR"),
		},
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Brokerage", ast.BookingFIFO),
		mkOpen(openDate, "Assets:EurCash", ast.BookingDefault),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		buy,
		sell,
	)

	r := NewReducer(ledger.All())
	var bookedDirectives []ast.Directive
	var sellBooked []BookedPosting
	var walkErrs []Error
	bookedDirectives, walkErrs = r.Walk(func(got *ast.Transaction, _, _ map[ast.Account]*Inventory, booked []BookedPosting) bool {
		if got == sell {
			sellBooked = append([]BookedPosting(nil), booked...)
		}
		return true
	})
	if len(walkErrs) != 1 || walkErrs[0].Code != CodeReductionExceedsInventory {
		t.Fatalf("Walk errs = %v, want [CodeReductionExceedsInventory]", walkErrs)
	}

	// Find the booked clone of the sell transaction in the directive output.
	var bookedSell *ast.Transaction
	for _, d := range bookedDirectives {
		tx, ok := d.(*ast.Transaction)
		if ok && tx.Date.Equal(sellDate) {
			bookedSell = tx
			break
		}
	}
	if bookedSell == nil {
		t.Fatalf("sell transaction not found in Walk directive output")
	}

	// The failed AAPL posting must be absent; only the EUR posting survives.
	if got := len(bookedSell.Postings); got != 1 {
		t.Fatalf("booked sell txn.Postings len = %d, want 1 (AAPL group dropped)", got)
	}
	if got := bookedSell.Postings[0].Account; got != "Assets:EurCash" {
		t.Errorf("booked sell txn.Postings[0].Account = %q, want Assets:EurCash", got)
	}

	// The surviving BookedPosting's Source must point into the rebuilt slice.
	if len(sellBooked) != 1 {
		t.Fatalf("sellBooked len = %d, want 1", len(sellBooked))
	}
	bp := sellBooked[0]
	if bp.Account != "Assets:EurCash" {
		t.Errorf("sellBooked[0].Account = %q, want Assets:EurCash", bp.Account)
	}
	// Source must alias the rebuilt posting in the directive output.
	if bp.Source != &bookedSell.Postings[0] {
		t.Errorf("sellBooked[0].Source does not point into booked txn.Postings[0]")
	}
}

// TestReducerWalk_DroppedGroupRollsBackAugmentation verifies that a
// successful augmentation is reversed when a later posting in the same
// weight-currency group fails.
//
// Scenario: a prior buy seeds Assets:Broker with 5 AAPL @ 100 USD.
// A second transaction has two postings, both weight-currency USD:
//
//   - posting A: augments 5 more AAPL @ 100 USD into Assets:Broker (succeeds)
//   - posting B: reduces 20 AAPL from Assets:Broker (fails: only 10 available)
//
// Because both share weight currency USD, the whole group is dropped.
// applyDrops must reverse the AAPL augmentation from posting A, leaving
// Assets:Broker with only the original 5 AAPL from the buy.
func TestReducerWalk_DroppedGroupRollsBackAugmentation(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	// Seed: buy 5 AAPL @ 100 USD.
	buy := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:Broker",
			Amount:  mkAmountPtr(t, "5", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "USD",
				Date:     &buyDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-500", "USD")},
	)

	// Mixed transaction: posting A augments (succeeds), posting B reduces
	// more than the resulting inventory holds (fails). The cost spec on
	// posting B carries PerUnit so that weightCurrencyFallback returns
	// "USD", placing it in the same group as the augmentation.
	mixed := mkTxn(txnDate,
		&ast.Posting{
			// Augment: adds 5 AAPL to the existing 5 → total 10.
			Account: "Assets:Broker",
			Amount:  mkAmountPtr(t, "5", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "USD",
				Date:     &txnDate,
			},
		},
		&ast.Posting{
			// Reduce: tries to consume 20 AAPL, but only 10 available → fails.
			// PerUnit is set so weightCurrencyFallback → weight currency = USD.
			Account: "Assets:Broker",
			Amount:  mkAmountPtr(t, "-20", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "USD",
				Date:     &buyDate,
			},
		},
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Broker", ast.BookingFIFO),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		buy,
		mixed,
	)

	r := NewReducer(ledger.All())
	_, errs := r.Walk(func(_ *ast.Transaction, _, _ map[ast.Account]*Inventory, _ []BookedPosting) bool {
		// The visitor is called for the buy (booked postings, before-state
		// touches). For the mixed transaction, all postings are dropped —
		// the visitor may or may not be called; we check Final state only.
		return true
	})

	// One error from the failing reduction.
	if len(errs) == 0 {
		t.Fatalf("Walk returned no errors, want at least 1 (reduction failure)")
	}

	// After the walk, Assets:Broker must hold only the original 5 AAPL
	// from the buy: the augmentation from the mixed transaction was reversed
	// by applyDrops, and the reduction never mutated the inventory.
	broker := r.Final("Assets:Broker")
	if broker == nil {
		t.Fatalf("r.Final(Assets:Broker) is nil; buy should have seeded it")
	}
	if broker.Len() != 1 {
		t.Errorf("r.Final(Assets:Broker) positions = %d, want 1 (augmentation rolled back, original 5 AAPL remains)", broker.Len())
	}
}

// TestReducerWalk_DroppedGroupMultipleFailedAccounts verifies that when
// a single weight-currency group has failing postings from two different
// accounts, both accounts are suppressed from the visitor's before/after
// maps. This is a regression test: when a single currency group has
// failing postings across multiple accounts, prepareForRollback must be
// called for every failing account, not just the first one encountered.
func TestReducerWalk_DroppedGroupMultipleFailedAccounts(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)

	// Seed BrokerA with 5 AAPL and BrokerB with 3 AAPL so that both
	// accounts have existing lots (necessary for classify to choose
	// kindReduce on the negative-amount postings below).
	buyA := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:BrokerA",
			Amount:  mkAmountPtr(t, "5", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "USD",
				Date:     &buyDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-500", "USD")},
	)
	buyB := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:BrokerB",
			Amount:  mkAmountPtr(t, "3", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "USD",
				Date:     &buyDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-300", "USD")},
	)

	// Transaction: both postings target different accounts. The cost spec
	// carries only a date (no PerUnit/Total), so PostingWeight → TotalCost
	// returns (nil, nil) and falls back to the plain-amount branch: weight
	// currency = AAPL (the posting commodity). Both postings share the same
	// "AAPL" weight-currency group. Both fail: BrokerA has only 5 (can't
	// reduce 10), BrokerB has only 3 (can't reduce 8). Both call
	// prepareForRollback at the failure site.
	sell := mkTxn(txnDate,
		&ast.Posting{
			Account: "Assets:BrokerA",
			Amount:  mkAmountPtr(t, "-10", "AAPL"), // fails: only 5 available
			Cost:    &ast.CostSpec{Date: &buyDate},
		},
		&ast.Posting{
			Account: "Assets:BrokerB",
			Amount:  mkAmountPtr(t, "-8", "AAPL"), // fails: only 3 available
			Cost:    &ast.CostSpec{Date: &buyDate},
		},
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:BrokerA", ast.BookingFIFO),
		mkOpen(openDate, "Assets:BrokerB", ast.BookingFIFO),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		buyA,
		buyB,
		sell,
	)

	r := NewReducer(ledger.All())
	var sellBefore, sellAfter map[ast.Account]*Inventory
	_, errs := r.Walk(func(got *ast.Transaction, before, after map[ast.Account]*Inventory, _ []BookedPosting) bool {
		if got == sell {
			sellBefore = before
			sellAfter = after
		}
		return true
	})

	// Both reductions fail, so Walk must return exactly two errors, both
	// with CodeReductionExceedsInventory.
	if len(errs) != 2 {
		t.Fatalf("Walk errs = %v (len=%d), want exactly 2 errors (one per failing account)", errs, len(errs))
	}
	for i, err := range errs {
		if err.Code != CodeReductionExceedsInventory {
			t.Errorf("errs[%d].Code = %v, want CodeReductionExceedsInventory", i, err.Code)
		}
	}

	// Both failing accounts must be absent from before and after: each
	// failed posting called prepareForRollback for its account, so diff()
	// suppresses them when they are back to their pre-transaction state.
	for _, acct := range []ast.Account{"Assets:BrokerA", "Assets:BrokerB"} {
		if _, ok := sellBefore[acct]; ok {
			t.Errorf("Walk(sell): before[%s] present; dropped group account must not appear in before", acct)
		}
		if _, ok := sellAfter[acct]; ok {
			t.Errorf("Walk(sell): after[%s] present; dropped group account must not appear in after", acct)
		}
	}

	// Both accounts still hold their original lots (failed reductions
	// produced no mutation).
	brokerA := r.Final("Assets:BrokerA")
	if brokerA == nil || brokerA.Len() != 1 {
		t.Errorf("r.Final(Assets:BrokerA) = %v, want 1 position (5 AAPL unaffected)", brokerA)
	}
	brokerB := r.Final("Assets:BrokerB")
	if brokerB == nil || brokerB.Len() != 1 {
		t.Errorf("r.Final(Assets:BrokerB) = %v, want 1 position (3 AAPL unaffected)", brokerB)
	}
}

// TestReducerWalk_AllPostingsDroppedEmitsEmptyTxn verifies that when
// every posting in a transaction fails (its group is dropped), Walk
// still emits the transaction to the directive output with an empty
// Postings slice. The visitor is not called (len(booked)==0 and
// len(before)==0); this test verifies both properties hold together.
func TestReducerWalk_AllPostingsDroppedEmitsEmptyTxn(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	sellDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)

	// Seed 5 AAPL.
	buy := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "5", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "USD",
				Date:     &buyDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-500", "USD")},
	)

	// Transaction where both postings belong to the same failing group
	// (both are reductions that exceed inventory). All postings are dropped.
	// We use two postings in the same USD group; the second also fails.
	// Simplest: a single-posting transaction whose only posting fails.
	sell := mkTxn(sellDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "-10", "AAPL"), // fails: only 5 available
			Cost:    &ast.CostSpec{Date: &buyDate},
		},
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Brokerage", ast.BookingFIFO),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		buy,
		sell,
	)

	r := NewReducer(ledger.All())
	visitCount := 0
	directives, errs := r.Walk(func(_ *ast.Transaction, _ map[ast.Account]*Inventory, _ map[ast.Account]*Inventory, _ []BookedPosting) bool {
		visitCount++
		return true
	})
	if len(errs) != 1 || errs[0].Code != CodeReductionExceedsInventory {
		t.Fatalf("Walk errs = %v, want [CodeReductionExceedsInventory]", errs)
	}

	// The visitor is not called for the all-dropped transaction (no booked
	// postings, no before-state touches). The buy transaction still calls it.
	if visitCount != 1 {
		t.Errorf("visitor called %d times, want 1 (buy only; all-dropped sell skips visitor)", visitCount)
	}

	// The all-dropped sell transaction must still appear in the directive output.
	var foundSell bool
	for _, d := range directives {
		tx, ok := d.(*ast.Transaction)
		if !ok || !tx.Date.Equal(sellDate) {
			continue
		}
		foundSell = true
		if len(tx.Postings) != 0 {
			t.Errorf("all-dropped sell txn.Postings len = %d, want 0 (all groups dropped)", len(tx.Postings))
		}
	}
	if !foundSell {
		t.Errorf("all-dropped sell transaction not found in Walk directive output")
	}
}

// ---- Pass 2 D6 and bookOne failure regression tests ----

// TestReducerWalk_Pass2UnknownJoinsDroppedGroup is a direct regression test
// for D6 (reducer.go approx line 682): when solveResidual resolves the
// auto-posting's residual to a currency that was already dropped in Pass 1,
// the auto-posting joins the dropped group without touching its Amount/Cost
// and is excluded from txn.Postings by applyDrops. No additional error is
// emitted and no BookedPosting is produced for the auto-posting's account.
//
// Scenario: a prior buy seeds Assets:Brokerage with 5 AAPL @ 100 USD. The
// sell transaction has three postings:
//   - failing USD group: -10 AAPL{buyDate} from Assets:Brokerage
//     (CodeReductionExceedsInventory → USD group dropped)
//   - surviving USD cash: +500 USD to Assets:Cash (same USD group, also dropped)
//   - auto-posting at Expenses:Plug (nil Amount)
//
// After Pass 1, the booked residual from Assets:Cash contributes +500 USD.
// solveResidual → residual = -500 USD (Currency = "USD"). Since USD is
// already dropped (D6), the auto-posting joins the group without being
// booked. applyDrops removes all USD postings, leaving txn.Postings empty.
func TestReducerWalk_Pass2UnknownJoinsDroppedGroup(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	sellDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)

	// Seed 5 AAPL @ 100 USD.
	buy := mkTxn(buyDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "5", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "USD",
				Date:     &buyDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-500", "USD")},
	)

	// Sell transaction: failing USD group (reduction exceeds inventory) +
	// surviving USD cash (same group, also dropped) + auto-posting (D6 join).
	//
	// The AAPL posting carries PerUnit so weightCurrencyFallback returns "USD"
	// (its weight is the USD cost). The 500 USD cash posting is also "USD"
	// group. USD is therefore the only group, and it is dropped when the AAPL
	// reduction fails. The auto-posting's residual resolves to -500 USD (from
	// the surviving cash), which matches the dropped "USD" key → D6 fires.
	sell := mkTxn(sellDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "-10", "AAPL"), // fails: only 5 available
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"), // weight = USD → USD group dropped
				Currency: "USD",
				Date:     &buyDate,
			},
		},
		&ast.Posting{
			Account: "Assets:Cash",
			Amount:  mkAmountPtr(t, "500", "USD"), // USD group, drops with AAPL
		},
		&ast.Posting{Account: "Expenses:Plug"}, // auto-posting: D6 join to USD group
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Brokerage", ast.BookingFIFO),
		mkOpen(openDate, "Assets:Cash", ast.BookingDefault),
		mkOpen(openDate, "Expenses:Plug", ast.BookingDefault),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		buy,
		sell,
	)

	r := NewReducer(ledger.All())
	var sellVisited bool
	var sellBefore, sellAfter map[ast.Account]*Inventory
	var sellBooked []BookedPosting
	dirs, errs := r.Walk(func(got *ast.Transaction, before, after map[ast.Account]*Inventory, booked []BookedPosting) bool {
		if got == sell {
			sellVisited = true
			sellBefore = before
			sellAfter = after
			sellBooked = append([]BookedPosting(nil), booked...)
		}
		return true
	})

	// Only the AAPL reduction error; no additional error from D6 or applyDrops.
	if len(errs) != 1 {
		t.Fatalf("Walk errs = %v (len=%d), want exactly 1 (CodeReductionExceedsInventory)", errs, len(errs))
	}
	if errs[0].Code != CodeReductionExceedsInventory {
		t.Errorf("err.Code = %v, want CodeReductionExceedsInventory", errs[0].Code)
	}

	// All postings belong to the dropped USD group (including the auto-posting
	// via D6), so before is empty and the visitor is not called.
	if sellVisited {
		t.Errorf("sell visitor was called; want no visit (all postings dropped, before is empty)")
	}

	// The sell transaction must still appear in the directive output, but
	// with an empty Postings slice (every group was dropped).
	var bookedSell *ast.Transaction
	for _, d := range dirs {
		tx, ok := d.(*ast.Transaction)
		if ok && tx.Date.Equal(sellDate) {
			bookedSell = tx
			break
		}
	}
	if bookedSell == nil {
		t.Fatalf("sell transaction not found in Walk directive output")
	}
	if len(bookedSell.Postings) != 0 {
		t.Errorf("booked sell txn.Postings len = %d, want 0 (all postings dropped by D6)", len(bookedSell.Postings))
	}

	// Expenses:Plug (the auto-posting account) must not appear in before/after.
	// sellBefore/sellAfter/sellBooked are nil maps/slices because the visitor
	// was never called; the map-range and slice-range below are no-ops, and the
	// key-lookup loop is what enforces the "must not appear" invariant.
	for _, acct := range []ast.Account{"Expenses:Plug", "Assets:Cash", "Assets:Brokerage"} {
		if _, ok := sellBefore[acct]; ok {
			t.Errorf("Walk(sell): before[%s] present; D6 join must not touch account", acct)
		}
		if _, ok := sellAfter[acct]; ok {
			t.Errorf("Walk(sell): after[%s] present; D6 join must not touch account", acct)
		}
	}

	// No BookedPosting should be emitted for Expenses:Plug.
	for _, bp := range sellBooked {
		if bp.Account == "Expenses:Plug" {
			t.Errorf("Walk(sell): BookedPosting for Expenses:Plug present; D6 path must produce no booking")
		}
	}

	// The AAPL position must be unchanged: the failing reduction left no mutation.
	brokerage := r.Final("Assets:Brokerage")
	if brokerage == nil {
		t.Fatalf("r.Final(Assets:Brokerage) is nil; buy should have seeded it")
	}
	if brokerage.Len() != 1 {
		t.Errorf("r.Final(Assets:Brokerage) has %d positions, want 1 (5 AAPL unaffected)", brokerage.Len())
	}
}

// TestReducerWalk_Pass2UnknownBookOneFailsDropsCurrency is a regression test
// for the Pass 2 bookOne failure path (reducer.go approx lines 705–713):
// when solveResidual succeeds and residual.Currency is not yet dropped, but
// the subsequent bookOne for the unknown posting fails, the currency is
// markForDrop'd, the account is prepareForRollback'd, and the unknown is
// excluded from txn.Postings by applyDrops.
//
// This is the only reducer path that adds a NEW currency to pr.dropped in
// Pass 2 (D6 joins an existing dropped group; this path creates one).
//
// Construction: seed Assets:Stock with 2 AAPL (cash, no cost). A transaction
// augments +5 AAPL (cash) to Assets:Exchange (Pass 1, AAPL group), leaving
// a residual of -5 AAPL. The auto-posting targets Assets:Stock. After Pass 2
// fills in Amount=-5 AAPL, bookOne classifies it as a kindReduce against the
// 2 AAPL in Assets:Stock. Since 5 > 2, Reduce returns CodeReductionExceedsInventory
// and the AAPL group is dropped. applyDrops reverses the Assets:Exchange
// augmentation and excludes the auto-posting from txn.Postings.
func TestReducerWalk_Pass2UnknownBookOneFailsDropsCurrency(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	seedDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	// Seed 2 AAPL at 100 USD/share (cost-bearing lot) into Assets:Stock.
	// Cost-bearing lots are required: Inventory.Reduce enforces the
	// over-reduction check only for non-cash positions (cash overdrafts
	// are deferred to the balance assertion layer).
	seed := mkTxn(seedDate,
		&ast.Posting{
			Account: "Assets:Stock",
			Amount:  mkAmountPtr(t, "2", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "USD",
				Date:     &seedDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-200", "USD")},
	)

	// Transaction: +5 AAPL (cash, no cost) to Assets:Exchange (Pass 1, AAPL
	// weight) + auto-posting at Assets:Stock (nil Amount).
	// Pass 2: residual = -5 AAPL. bookOne(-5 AAPL, Assets:Stock) →
	// classify sees 2 AAPL{USD} (positive lot), negative posting → kindReduce.
	// bookReduce: 5 > 2 (lot-bearing check) → CodeReductionExceedsInventory
	// → markForDrop("AAPL") → pr.unknownDesc[0].currency = "AAPL".
	txn := mkTxn(txnDate,
		&ast.Posting{Account: "Assets:Exchange", Amount: mkAmountPtr(t, "5", "AAPL")},
		&ast.Posting{Account: "Assets:Stock"}, // auto-posting: will fail in Pass 2
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Exchange", ast.BookingDefault),
		mkOpen(openDate, "Assets:Stock", ast.BookingDefault),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		seed,
		txn,
	)

	r := NewReducer(ledger.All())
	var txnVisited bool
	var txnBefore, txnAfter map[ast.Account]*Inventory
	dirs, errs := r.Walk(func(got *ast.Transaction, before, after map[ast.Account]*Inventory, booked []BookedPosting) bool {
		if got == txn {
			txnVisited = true
			txnBefore = before
			txnAfter = after
		}
		return true
	})

	// Expect exactly 1 error: CodeReductionExceedsInventory from Pass 2 bookOne.
	if len(errs) != 1 {
		t.Fatalf("Walk errs = %v (len=%d), want exactly 1 (CodeReductionExceedsInventory)", errs, len(errs))
	}
	if errs[0].Code != CodeReductionExceedsInventory {
		t.Errorf("err.Code = %v, want CodeReductionExceedsInventory", errs[0].Code)
	}

	// All postings belong to the AAPL group (now dropped in Pass 2), so the
	// visitor is not called for the failed txn.
	if txnVisited {
		t.Errorf("txn visitor was called; want no visit (all postings in dropped AAPL group)")
	}
	// txnBefore/txnAfter are nil because the visitor was never called for txn.
	if len(txnBefore) != 0 {
		t.Errorf("txnBefore has %d entries, want 0 (visitor not called)", len(txnBefore))
	}
	if len(txnAfter) != 0 {
		t.Errorf("txnAfter has %d entries, want 0 (visitor not called)", len(txnAfter))
	}

	// The transaction must appear in directive output with empty Postings.
	var bookedTxn *ast.Transaction
	for _, d := range dirs {
		tx, ok := d.(*ast.Transaction)
		if ok && tx.Date.Equal(txnDate) {
			bookedTxn = tx
			break
		}
	}
	if bookedTxn == nil {
		t.Fatalf("txn not found in Walk directive output")
	}
	if len(bookedTxn.Postings) != 0 {
		t.Errorf("booked txn.Postings len = %d, want 0 (AAPL group dropped in Pass 2)", len(bookedTxn.Postings))
	}

	// Assets:Stock must still hold its 2 AAPL from the seed transaction.
	// The Pass 2 bookOne failed before mutating the inventory, so Assets:Stock
	// is rolled back to its pre-txn state by prepareForRollback + diff exclusion.
	stock := r.Final("Assets:Stock")
	if stock == nil {
		t.Fatalf("r.Final(Assets:Stock) is nil; seed should have established it")
	}
	if stock.Len() != 1 {
		t.Errorf("r.Final(Assets:Stock) has %d positions, want 1 (2 AAPL from seed, unchanged)", stock.Len())
	}

	// Assets:Exchange was augmented in Pass 1 and then reversed by applyDrops.
	// Its inventory must be empty (it had no pre-transaction state).
	exchange := r.Final("Assets:Exchange")
	if exchange != nil && !exchange.IsEmpty() {
		t.Errorf("r.Final(Assets:Exchange) = non-empty; Pass-1 augment must have been reversed by applyDrops")
	}
}

// TestReducerWalk_MultiLotReductionPartialGroupDrop is a regression test for
// the cross-boundary partial drop path in applyDrops: a single -N COMMODITY {}
// posting expands into multiple child postings (one per matched lot) via
// addMultiLotReduction, and only the children whose weight currency matches a
// dropped group are excluded. Children of surviving groups remain in
// txn.Postings and their inventory mutations are preserved.
//
// Scenario: buy 10 AAPL @ 100 USD and 10 AAPL @ 90 EUR. Also buy 5 STOCK @
// 100 USD. A sell transaction has:
//   - -20 AAPL {} from Assets:Brokerage (expands to USD child + EUR child)
//   - -10 STOCK{buyDate} from Assets:BrokerB (fails: only 5 available)
//     → CodeReductionExceedsInventory → USD group dropped
//
// applyDrops: USD child of AAPL reduction is reversed (10 AAPL{USD} restored);
// EUR child of AAPL reduction survives (10 AAPL{EUR} consumed). The STOCK
// reduction is also reversed. Only the EUR child posting appears in txn.Postings.
func TestReducerWalk_MultiLotReductionPartialGroupDrop(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	buyUSDDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	buyEURDate := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	buySTOCKDate := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	sellDate := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)

	// Buy 10 AAPL @ 100 USD.
	buyUSD := mkTxn(buyUSDDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "10", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "USD",
				Date:     &buyUSDDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-1000", "USD")},
	)

	// Buy 10 AAPL @ 90 EUR (different cost currency).
	buyEUR := mkTxn(buyEURDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "10", "AAPL"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "90"),
				Currency: "EUR",
				Date:     &buyEURDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-900", "EUR")},
	)

	// Buy 5 STOCK @ 100 USD (the other account for the failing USD posting).
	buySTOCK := mkTxn(buySTOCKDate,
		&ast.Posting{
			Account: "Assets:BrokerB",
			Amount:  mkAmountPtr(t, "5", "STOCK"),
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"),
				Currency: "USD",
				Date:     &buySTOCKDate,
			},
		},
		&ast.Posting{Account: "Equity:Opening", Amount: mkAmountPtr(t, "-500", "USD")},
	)

	// Sell: -20 AAPL {} (multi-lot: USD child + EUR child) + failing STOCK
	// reduction (only 5 available → CodeReductionExceedsInventory → USD group
	// dropped). The STOCK posting has a PerUnit cost so weightCurrencyFallback
	// returns "USD", placing it in the same USD group as the AAPL{USD} child.
	sell := mkTxn(sellDate,
		&ast.Posting{
			Account: "Assets:Brokerage",
			Amount:  mkAmountPtr(t, "-20", "AAPL"),
			Cost:    &ast.CostSpec{}, // matches all lots: expands to USD + EUR children
		},
		&ast.Posting{
			Account: "Assets:BrokerB",
			Amount:  mkAmountPtr(t, "-10", "STOCK"), // fails: only 5 available
			Cost: &ast.CostSpec{
				PerUnit:  decimalPtr(t, "100"), // USD weight → same group as AAPL{USD}
				Currency: "USD",
				Date:     &buySTOCKDate,
			},
		},
	)

	ledger := mkLedger(
		mkOpen(openDate, "Assets:Brokerage", ast.BookingFIFO),
		mkOpen(openDate, "Assets:BrokerB", ast.BookingFIFO),
		mkOpen(openDate, "Equity:Opening", ast.BookingDefault),
		buyUSD,
		buyEUR,
		buySTOCK,
		sell,
	)

	r := NewReducer(ledger.All())
	var sellBooked []BookedPosting
	var sellBefore, sellAfter map[ast.Account]*Inventory
	var bookedDirectives []ast.Directive
	var walkErrs []Error
	bookedDirectives, walkErrs = r.Walk(func(got *ast.Transaction, before, after map[ast.Account]*Inventory, booked []BookedPosting) bool {
		if got == sell {
			sellBefore = before
			sellAfter = after
			sellBooked = append([]BookedPosting(nil), booked...)
		}
		return true
	})

	// Exactly one error: the STOCK reduction failure.
	if len(walkErrs) != 1 {
		t.Fatalf("Walk errs = %v (len=%d), want exactly 1 (CodeReductionExceedsInventory)", walkErrs, len(walkErrs))
	}
	if walkErrs[0].Code != CodeReductionExceedsInventory {
		t.Errorf("err.Code = %v, want CodeReductionExceedsInventory", walkErrs[0].Code)
	}

	// The EUR child of the AAPL reduction must survive; the USD child and
	// STOCK posting must be dropped.
	var eurBookings, usdBookings, stockBookings int
	for _, bp := range sellBooked {
		if bp.Account == "Assets:Brokerage" && bp.Reduction != nil {
			switch bp.Reduction.Lot.Currency {
			case "EUR":
				eurBookings++
			case "USD":
				usdBookings++
			}
		}
		if bp.Account == "Assets:BrokerB" {
			stockBookings++
		}
	}
	if eurBookings != 1 {
		t.Errorf("Walk(sell): EUR AAPL BookedPosting count = %d, want 1 (EUR child survives)", eurBookings)
	}
	if usdBookings != 0 {
		t.Errorf("Walk(sell): USD AAPL BookedPosting count = %d, want 0 (USD child dropped)", usdBookings)
	}
	if stockBookings != 0 {
		t.Errorf("Walk(sell): STOCK BookedPosting count = %d, want 0 (STOCK posting dropped)", stockBookings)
	}

	// The sell transaction in directive output must contain only the EUR child.
	var bookedSell *ast.Transaction
	for _, d := range bookedDirectives {
		tx, ok := d.(*ast.Transaction)
		if ok && tx.Date.Equal(sellDate) {
			bookedSell = tx
			break
		}
	}
	if bookedSell == nil {
		t.Fatalf("sell transaction not found in Walk directive output")
	}
	if got := len(bookedSell.Postings); got != 1 {
		t.Fatalf("booked sell txn.Postings len = %d, want 1 (only EUR AAPL child survives)", got)
	}
	if bookedSell.Postings[0].Account != "Assets:Brokerage" {
		t.Errorf("booked sell txn.Postings[0].Account = %q, want Assets:Brokerage (EUR AAPL child)", bookedSell.Postings[0].Account)
	}

	// Assets:BrokerB must not appear in before/after (its STOCK reduction
	// was rolled back; it is back to its pre-transaction state of 5 STOCK).
	if _, ok := sellBefore["Assets:BrokerB"]; ok {
		t.Errorf("Walk(sell): before[Assets:BrokerB] present; dropped USD group must suppress the account")
	}
	if _, ok := sellAfter["Assets:BrokerB"]; ok {
		t.Errorf("Walk(sell): after[Assets:BrokerB] present; dropped USD group must suppress the account")
	}

	// Assets:Brokerage in after must show only the EUR lot consumed.
	// The USD lot (10 AAPL @ 100 USD) must be fully restored by applyDrops.
	// The EUR lot (10 AAPL @ 90 EUR) must be gone (fully consumed).
	brokerage := r.Final("Assets:Brokerage")
	if brokerage == nil {
		t.Fatalf("r.Final(Assets:Brokerage) is nil; buy transactions should have seeded it")
	}
	// Exactly 1 position should remain: the USD lot (10 AAPL @ 100 USD).
	if brokerage.Len() != 1 {
		t.Errorf("r.Final(Assets:Brokerage) has %d positions, want 1 (USD lot restored, EUR lot consumed)", brokerage.Len())
	}
	for p := range brokerage.All() {
		if p.Cost == nil || p.Cost.Currency != "USD" {
			t.Errorf("r.Final(Assets:Brokerage): remaining position has cost %v, want USD cost lot", p.Cost)
		}
		wantQty := decimalVal(t, "10")
		if p.Units.Number.Cmp(&wantQty) != 0 {
			t.Errorf("r.Final(Assets:Brokerage): remaining lot units = %s, want 10", p.Units.Number.Text('f'))
		}
	}

	// Assets:BrokerB must still hold its 5 STOCK (failed reduction, rolled back).
	brokerB := r.Final("Assets:BrokerB")
	if brokerB == nil {
		t.Fatalf("r.Final(Assets:BrokerB) is nil; buySTOCK should have seeded it")
	}
	if brokerB.Len() != 1 {
		t.Errorf("r.Final(Assets:BrokerB) has %d positions, want 1 (5 STOCK unaffected)", brokerB.Len())
	}

	// The EUR child's Source must point into the booked txn.Postings.
	if len(sellBooked) == 1 {
		bp := sellBooked[0]
		if bp.Source != &bookedSell.Postings[0] {
			t.Errorf("EUR child BookedPosting.Source does not point into booked txn.Postings[0]")
		}
	}
}

// TestReverseBooking_AugmentationInverse verifies that reversing an
// augmentation (bp.Reduction == nil) removes the added lot from the
// inventory.
func TestReverseBooking_AugmentationInverse(t *testing.T) {
	buyDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	lot := &Cost{
		Number:   decimalVal(t, "100"),
		Currency: "USD",
		Date:     buyDate,
	}

	// Cash augmentation inverse (nil Lot).
	t.Run("cash", func(t *testing.T) {
		inv := NewInventory()
		if err := inv.Add(Position{
			Units: ast.Amount{Number: decimalVal(t, "500"), Currency: "USD"},
		}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		bp := BookedPosting{
			Account: "Assets:Cash",
			Units:   ast.Amount{Number: decimalVal(t, "500"), Currency: "USD"},
			Lot:     nil,
		}
		if err := reverseBooking(inv, bp); err != nil {
			t.Fatalf("reverseBooking: %v", err)
		}
		if !inv.IsEmpty() {
			t.Errorf("inventory after cash augmentation reverse = non-empty, want empty")
		}
	})

	// Lot augmentation inverse (non-nil Lot).
	t.Run("lot", func(t *testing.T) {
		inv := NewInventory()
		if err := inv.Add(Position{
			Units: ast.Amount{Number: decimalVal(t, "10"), Currency: "AAPL"},
			Cost:  lot.Clone(),
		}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		bp := BookedPosting{
			Account: "Assets:Brokerage",
			Units:   ast.Amount{Number: decimalVal(t, "10"), Currency: "AAPL"},
			Lot:     lot.Clone(),
		}
		if err := reverseBooking(inv, bp); err != nil {
			t.Fatalf("reverseBooking: %v", err)
		}
		if !inv.IsEmpty() {
			t.Errorf("inventory after lot augmentation reverse = non-empty, want empty")
		}
	})
}

// TestReverseBooking_ReductionInverse verifies that reversing a
// reduction (bp.Reduction != nil) restores the consumed lot.
func TestReverseBooking_ReductionInverse(t *testing.T) {
	buyDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	lot := Cost{
		Number:   decimalVal(t, "100"),
		Currency: "USD",
		Date:     buyDate,
	}

	// Cash-sentinel reduction inverse: Lot is the zero value, so Cost
	// should be nil after reversal (Inventory.Add merges into cash slot).
	t.Run("cash_sentinel", func(t *testing.T) {
		inv := NewInventory() // empty; the reduction consumed the position entirely
		step := ReductionStep{
			Lot:   Cost{}, // zero value = cash sentinel
			Units: decimalVal(t, "200"),
		}
		bp := BookedPosting{
			Account:   "Assets:Cash",
			Units:     ast.Amount{Number: decimalVal(t, "-200"), Currency: "USD"},
			Reduction: &step,
		}
		if err := reverseBooking(inv, bp); err != nil {
			t.Fatalf("reverseBooking: %v", err)
		}
		if inv.Len() != 1 {
			t.Fatalf("inventory after cash sentinel reverse: %d positions, want 1", inv.Len())
		}
		want := decimalVal(t, "200")
		for p := range inv.All() {
			if p.Units.Number.Cmp(&want) != 0 {
				t.Errorf("restored position units = %s, want 200", p.Units.Number.Text('f'))
			}
			if p.Cost != nil {
				t.Errorf("restored position Cost = %v, want nil (cash sentinel)", p.Cost)
			}
		}
	})

	// Non-sentinel lot reduction inverse: the consumed lot is re-added with
	// its original cost. A completely consumed lot (zero remaining) is
	// re-created from scratch.
	t.Run("lot_full_consumption", func(t *testing.T) {
		inv := NewInventory() // the lot was fully consumed; inventory is empty
		step := ReductionStep{
			Lot:   lot,
			Units: decimalVal(t, "5"),
		}
		bp := BookedPosting{
			Account:   "Assets:Brokerage",
			Units:     ast.Amount{Number: decimalVal(t, "-5"), Currency: "AAPL"},
			Reduction: &step,
		}
		if err := reverseBooking(inv, bp); err != nil {
			t.Fatalf("reverseBooking: %v", err)
		}
		lotCopy := lot
		want := []Position{{
			Units: ast.Amount{Number: decimalVal(t, "5"), Currency: "AAPL"},
			Cost:  lotCopy.Clone(),
		}}
		var got []Position
		for p := range inv.All() {
			got = append(got, p)
		}
		if diff := cmp.Diff(want, got, astCmpOpts...); diff != "" {
			t.Errorf("inventory after lot reduction reverse (-want +got):\n%s", diff)
		}
	})
}
