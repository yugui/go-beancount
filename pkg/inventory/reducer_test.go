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

// mkOpen constructs an Open directive. If booking is empty, no booking
// keyword is recorded, which resolves to ast.BookingDefault.
func mkOpen(date time.Time, account, booking string) *ast.Open {
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
		mkOpen(openDate, "Assets:Cash", ""),
		mkOpen(openDate, "Expenses:Food", ""),
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
		mkOpen(openDate, "Assets:Cash", ""),
		mkOpen(openDate, "Expenses:Food", ""),
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
		mkOpen(openDate, "Assets:Cash", ""),
		mkOpen(openDate, "Expenses:Food", ""),
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
		mkOpen(openDate, "Assets:Cash", ""),
		mkOpen(openDate, "Expenses:Food", ""),
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
		mkOpen(openDate, "Assets:Cash", ""),
		mkOpen(openDate, "Expenses:Food", ""),
		mkOpen(openDate, "Equity:Plug", ""),
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
		mkOpen(openDate, "Assets:USD", ""),
		mkOpen(openDate, "Assets:EUR", ""),
		mkOpen(openDate, "Equity:Plug", ""),
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
		mkOpen(openDate, "Assets:Cash", ""),
		mkOpen(openDate, "Expenses:Food", ""),
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
		mkOpen(openDate, "Assets:Cash", ""),
		mkOpen(openDate, "Expenses:Food", ""),
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
		mkOpen(openDate, "Assets:Cash", ""),
		mkOpen(openDate, "Expenses:Food", ""),
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
		mkOpen(openDate, "Assets:Stock", "FIFO"),
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

// TestReducerWalk_UnknownBookingMethodContinues verifies that an
// unparseable booking keyword records an error but does not abort the
// walk: subsequent transactions still run and the account falls back
// to BookingDefault.
func TestReducerWalk_UnknownBookingMethodContinues(t *testing.T) {
	openDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	txnDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

	pos := mkAmountPtr(t, "100.00", "USD")
	neg := mkAmountPtr(t, "-100.00", "USD")
	ledger := mkLedger(
		mkOpen(openDate, "Assets:Cash", "UNKNOWN"),
		mkOpen(openDate, "Expenses:Food", ""),
		mkTxn(txnDate,
			&ast.Posting{Account: "Assets:Cash", Amount: pos},
			&ast.Posting{Account: "Expenses:Food", Amount: neg},
		),
	)

	r := NewReducer(ledger)
	visited := 0
	errs := r.Walk(func(*ast.Transaction, map[ast.Account]*Inventory, map[ast.Account]*Inventory, []BookedPosting) bool {
		visited++
		return true
	})
	if visited != 1 {
		t.Errorf("visitor called %d times, want 1", visited)
	}
	if len(errs) != 1 || errs[0].Code != CodeInvalidBookingMethod {
		t.Fatalf("Walk errs = %v, want [CodeInvalidBookingMethod]", errs)
	}
	if got, want := r.booking["Assets:Cash"], ast.BookingDefault; got != want {
		t.Errorf("booking[Assets:Cash] = %v, want %v (fallback)", got, want)
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
		mkOpen(openDate, "Assets:Cash", ""),
		mkOpen(openDate, "Expenses:Food", ""),
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
		mkOpen(openDate, "Assets:Cash", ""),
		mkOpen(openDate, "Expenses:Food", ""),
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
