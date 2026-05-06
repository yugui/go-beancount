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
				PerUnit: mkAmountPtr(t, "100", "JPY"),
				Date:    &buyDate,
				Label:   "label",
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
			if len(bp.Reductions) == 0 {
				t.Fatalf("Walk Assets:B: Reductions is empty, want a step")
			}
			step := bp.Reductions[0]
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
				PerUnit: mkAmountPtr(t, "100", "JPY"),
				Date:    &buyDate,
				Label:   "label",
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
				PerUnit: mkAmountPtr(t, "100", "JPY"),
				Date:    &buyDate,
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
				PerUnit: mkAmountPtr(t, "100", "JPY"),
				Date:    &buyDate,
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
				PerUnit: mkAmountPtr(t, "100", "JPY"),
				Date:    &buyDate,
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
				PerUnit: mkAmountPtr(t, "100", "JPY"),
				Date:    &buyDate,
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

// TestReducerWalk_DoesNotMutateInput exercises every interpolation
// path the reducer can take — auto-posting amount fill, deferred
// per-unit cost fill, and multi-lot reduction Total synthesis from a
// bare cost spec — in a single Walk and asserts that the caller's
// directives are byte-for-byte identical afterwards.
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
				PerUnit: mkAmountPtr(t, "100", "JPY"),
				Date:    &buyDate,
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
				PerUnit: mkAmountPtr(t, "110", "JPY"),
				Date:    &buy2Date,
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
	// resolves the lots from inventory; the reducer historically
	// wrote a synthesized Cost.Total back to the AST.
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
