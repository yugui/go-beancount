package inventory

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

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

	r := NewReducer(ledger)
	var visited int
	errs := r.Walk(func(got *ast.Transaction, before, after map[ast.Account]*Inventory, booked []BookedPosting) bool {
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

	r := NewReducer(ledger)
	var inferredSeen bool
	errs := r.Walk(func(_ *ast.Transaction, _, _ map[ast.Account]*Inventory, booked []BookedPosting) bool {
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

	// The auto-posting should have been mutated to carry the residual.
	autoPosting := &txn.Postings[1]
	if autoPosting.Amount == nil {
		t.Fatalf("auto-posting Amount is still nil after Walk")
	}
	if got := autoPosting.Amount.Currency; got != "USD" {
		t.Errorf("auto-posting currency = %q, want USD", got)
	}
	want := decimalVal(t, "-42.50")
	if got := autoPosting.Amount.Number; got.Cmp(&want) != 0 {
		t.Errorf("auto-posting number = %s, want %s", got.Text('f'), want.Text('f'))
	}
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

	r := NewReducer(ledger)
	visited := 0
	errs := r.Walk(func(*ast.Transaction, map[ast.Account]*Inventory, map[ast.Account]*Inventory, []BookedPosting) bool {
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

	r := NewReducer(ledger)
	visited := 0
	errs := r.Walk(func(*ast.Transaction, map[ast.Account]*Inventory, map[ast.Account]*Inventory, []BookedPosting) bool {
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
// to zero) is rejected with CodeUnresolvableAutoPosting. The explicit
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

	r := NewReducer(ledger)
	var gotBooked []BookedPosting
	errs := r.Walk(func(_ *ast.Transaction, _, _ map[ast.Account]*Inventory, booked []BookedPosting) bool {
		gotBooked = append(gotBooked, booked...)
		return true
	})
	if len(errs) != 1 || errs[0].Code != CodeUnresolvableAutoPosting {
		t.Fatalf("Walk errs = %v, want [CodeUnresolvableAutoPosting]", errs)
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

	r := NewReducer(ledger)
	var gotBooked []BookedPosting
	errs := r.Walk(func(_ *ast.Transaction, _, _ map[ast.Account]*Inventory, booked []BookedPosting) bool {
		gotBooked = append(gotBooked, booked...)
		return true
	})
	if len(errs) != 1 || errs[0].Code != CodeUnresolvableAutoPosting {
		t.Fatalf("Walk errs = %v, want [CodeUnresolvableAutoPosting]", errs)
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

	r := NewReducer(ledger)
	errs := r.Walk(nil)
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

	r := NewReducer(ledger)
	var afterT1 *Inventory
	call := 0
	errs := r.Walk(func(txn *ast.Transaction, before, after map[ast.Account]*Inventory, _ []BookedPosting) bool {
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

	r := NewReducer(ledger)
	calls := 0
	errs := r.Walk(func(*ast.Transaction, map[ast.Account]*Inventory, map[ast.Account]*Inventory, []BookedPosting) bool {
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

	r := NewReducer(ledger)
	errs := r.Walk(nil)
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

	r := NewReducer(ledger)
	errs := r.Walk(func(_ *ast.Transaction, before, _ map[ast.Account]*Inventory, _ []BookedPosting) bool {
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
		errs := r.Walk(func(_ *ast.Transaction, _, after map[ast.Account]*Inventory, _ []BookedPosting) bool {
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

	r := NewReducer(ledger)
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
	refR := NewReducer(ledger)
	var refFinal map[ast.Account]*Inventory
	refErrs := refR.Walk(func(_ *ast.Transaction, _, after map[ast.Account]*Inventory, _ []BookedPosting) bool {
		refFinal = after
		return true
	})
	if len(refErrs) != 0 {
		t.Fatalf("reference Walk errs = %v", refErrs)
	}

	r := NewReducer(ledger)
	errs := r.Run()
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

	r := NewReducer(ledger)
	errs := r.Run()
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

	r := NewReducer(ledger)
	if errs := r.Run(); len(errs) != 0 {
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

	r := NewReducer(ledger)
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
	refR := NewReducer(ledger)
	var afterT1, afterT2 map[ast.Account]*Inventory
	refErrs := refR.Walk(func(got *ast.Transaction, _, after map[ast.Account]*Inventory, _ []BookedPosting) bool {
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

	r := NewReducer(ledger)
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

	r := NewReducer(ledger)
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

	r := NewReducer(ledger)
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
	if errs := r.Walk(func(*ast.Transaction, map[ast.Account]*Inventory, map[ast.Account]*Inventory, []BookedPosting) bool {
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
