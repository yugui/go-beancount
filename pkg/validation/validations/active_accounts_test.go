package validations

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/internal/accountstate"
)

// mkState builds a per-account lifecycle map for tests. opens maps account
// names to their OpenDate; closes maps subset of them to a CloseDate.
func mkState(opens map[ast.Account]time.Time, closes map[ast.Account]time.Time) map[ast.Account]*accountstate.State {
	m := make(map[ast.Account]*accountstate.State)
	for a, d := range opens {
		m[a] = &accountstate.State{OpenDate: d}
	}
	for a, d := range closes {
		st, ok := m[a]
		if !ok {
			continue
		}
		st.Closed = true
		st.CloseDate = d
	}
	return m
}

func date(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

func TestActiveAccounts_Name(t *testing.T) {
	v := newActiveAccounts(nil)
	if got, want := v.Name(), "active_accounts"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestActiveAccounts_FinishIsNoOp(t *testing.T) {
	v := newActiveAccounts(mkState(map[ast.Account]time.Time{
		"Assets:Cash": date(2024, 1, 1),
	}, nil))
	if got := v.Finish(); got != nil {
		t.Errorf("Finish() = %v, want nil", got)
	}
}

func TestActiveAccounts_Transaction_UnopenedAccount(t *testing.T) {
	state := mkState(nil, nil)
	v := newActiveAccounts(state)

	txnSpan := ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 10, Offset: 100}}
	postingSpan := ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 11, Offset: 120}}
	txn := &ast.Transaction{
		Date: date(2024, 1, 15),
		Span: txnSpan,
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Span: postingSpan},
		},
	}

	errs := v.ProcessEntry(txn)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1; errs = %v", len(errs), errs)
	}
	e := errs[0]
	if e.Code != string(validation.CodeAccountNotOpen) {
		t.Errorf("Code = %q, want %q", e.Code, validation.CodeAccountNotOpen)
	}
	if e.Span != postingSpan {
		t.Errorf("Span = %#v, want posting span %#v", e.Span, postingSpan)
	}
	if want := `account "Assets:Cash" is not open`; e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}

func TestActiveAccounts_Transaction_PostingSpanFallsBackToTxnSpan(t *testing.T) {
	v := newActiveAccounts(mkState(nil, nil))

	txnSpan := ast.Span{Start: ast.Position{Line: 5, Offset: 50}}
	txn := &ast.Transaction{
		Date: date(2024, 1, 15),
		Span: txnSpan,
		Postings: []ast.Posting{
			{Account: "Assets:Cash"}, // zero Span
		},
	}
	errs := v.ProcessEntry(txn)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1; errs = %v", len(errs), errs)
	}
	if errs[0].Span != txnSpan {
		t.Errorf("Span = %#v, want txn span %#v (posting had zero span)", errs[0].Span, txnSpan)
	}
}

func TestActiveAccounts_Transaction_PostingBeforeOpen(t *testing.T) {
	state := mkState(map[ast.Account]time.Time{
		"Assets:Cash": date(2024, 2, 1),
	}, nil)
	v := newActiveAccounts(state)

	txn := &ast.Transaction{
		Date: date(2024, 1, 15),
		Span: ast.Span{Start: ast.Position{Line: 1}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Span: ast.Span{Start: ast.Position{Line: 2}}},
		},
	}
	errs := v.ProcessEntry(txn)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1; errs = %v", len(errs), errs)
	}
	e := errs[0]
	if e.Code != string(validation.CodeAccountNotYetOpen) {
		t.Errorf("Code = %q, want %q", e.Code, validation.CodeAccountNotYetOpen)
	}
	if want := `account "Assets:Cash" is not open on 2024-01-15`; e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}

func TestActiveAccounts_Transaction_PostingAfterClose(t *testing.T) {
	state := mkState(
		map[ast.Account]time.Time{"Assets:Cash": date(2023, 1, 1)},
		map[ast.Account]time.Time{"Assets:Cash": date(2024, 1, 1)},
	)
	v := newActiveAccounts(state)

	txn := &ast.Transaction{
		Date: date(2024, 6, 1),
		Span: ast.Span{Start: ast.Position{Line: 1}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Span: ast.Span{Start: ast.Position{Line: 2}}},
		},
	}
	errs := v.ProcessEntry(txn)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1; errs = %v", len(errs), errs)
	}
	e := errs[0]
	if e.Code != string(validation.CodeAccountClosed) {
		t.Errorf("Code = %q, want %q", e.Code, validation.CodeAccountClosed)
	}
	if want := `account "Assets:Cash" is closed on 2024-06-01`; e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}

func TestActiveAccounts_Balance_UnopenedAccount(t *testing.T) {
	v := newActiveAccounts(mkState(nil, nil))
	span := ast.Span{Start: ast.Position{Line: 5, Offset: 55}}
	d := &ast.Balance{
		Date:    date(2024, 1, 15),
		Account: "Assets:Cash",
		Span:    span,
	}
	errs := v.ProcessEntry(d)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1; errs = %v", len(errs), errs)
	}
	if errs[0].Code != string(validation.CodeAccountNotOpen) {
		t.Errorf("Code = %q, want %q", errs[0].Code, validation.CodeAccountNotOpen)
	}
	if errs[0].Span != span {
		t.Errorf("Span = %#v, want %#v", errs[0].Span, span)
	}
	if want := `account "Assets:Cash" is not open`; errs[0].Message != want {
		t.Errorf("Message = %q, want %q", errs[0].Message, want)
	}
}

func TestActiveAccounts_Pad_UnopenedAccounts(t *testing.T) {
	// Neither account is open → two errors (one per referenced account),
	// matching the legacy visitPad that calls requireOpen for both.
	v := newActiveAccounts(mkState(nil, nil))
	span := ast.Span{Start: ast.Position{Line: 7, Offset: 70}}
	d := &ast.Pad{
		Date:       date(2024, 1, 15),
		Account:    "Assets:Target",
		PadAccount: "Equity:Opening",
		Span:       span,
	}
	errs := v.ProcessEntry(d)
	if len(errs) != 2 {
		t.Fatalf("got %d errors, want 2; errs = %v", len(errs), errs)
	}
	wantAccts := []string{"Assets:Target", "Equity:Opening"}
	for i, e := range errs {
		if e.Code != string(validation.CodeAccountNotOpen) {
			t.Errorf("errs[%d].Code = %q, want %q", i, e.Code, validation.CodeAccountNotOpen)
		}
		want := `account "` + wantAccts[i] + `" is not open`
		if e.Message != want {
			t.Errorf("errs[%d].Message = %q, want %q", i, e.Message, want)
		}
		if e.Span != span {
			t.Errorf("errs[%d].Span = %#v, want %#v", i, e.Span, span)
		}
	}
}

func TestActiveAccounts_Pad_OnlySourceMissing(t *testing.T) {
	// Target open, source not → exactly one error for the source account.
	v := newActiveAccounts(mkState(map[ast.Account]time.Time{
		"Assets:Target": date(2024, 1, 1),
	}, nil))
	d := &ast.Pad{
		Date:       date(2024, 1, 15),
		Account:    "Assets:Target",
		PadAccount: "Equity:Opening",
		Span:       ast.Span{Start: ast.Position{Line: 1}},
	}
	errs := v.ProcessEntry(d)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1; errs = %v", len(errs), errs)
	}
	if want := `account "Equity:Opening" is not open`; errs[0].Message != want {
		t.Errorf("Message = %q, want %q", errs[0].Message, want)
	}
}

func TestActiveAccounts_Note_UnopenedAccount(t *testing.T) {
	v := newActiveAccounts(mkState(nil, nil))
	d := &ast.Note{
		Date:    date(2024, 1, 15),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Line: 3}},
	}
	errs := v.ProcessEntry(d)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1; errs = %v", len(errs), errs)
	}
	if errs[0].Code != string(validation.CodeAccountNotOpen) {
		t.Errorf("Code = %q, want %q", errs[0].Code, validation.CodeAccountNotOpen)
	}
}

func TestActiveAccounts_Document_UnopenedAccount(t *testing.T) {
	v := newActiveAccounts(mkState(nil, nil))
	d := &ast.Document{
		Date:    date(2024, 1, 15),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Line: 4}},
	}
	errs := v.ProcessEntry(d)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1; errs = %v", len(errs), errs)
	}
	if errs[0].Code != string(validation.CodeAccountNotOpen) {
		t.Errorf("Code = %q, want %q", errs[0].Code, validation.CodeAccountNotOpen)
	}
}

func TestActiveAccounts_HappyPath_WithinOpenWindow(t *testing.T) {
	state := mkState(
		map[ast.Account]time.Time{"Assets:Cash": date(2024, 1, 1)},
		map[ast.Account]time.Time{"Assets:Cash": date(2024, 12, 31)},
	)
	v := newActiveAccounts(state)

	cases := []ast.Directive{
		&ast.Transaction{
			Date: date(2024, 6, 15),
			Span: ast.Span{Start: ast.Position{Line: 1}},
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Span: ast.Span{Start: ast.Position{Line: 2}}},
			},
		},
		&ast.Balance{
			Date: date(2024, 6, 15), Account: "Assets:Cash",
			Span: ast.Span{Start: ast.Position{Line: 3}},
		},
		&ast.Note{
			Date: date(2024, 6, 15), Account: "Assets:Cash",
			Span: ast.Span{Start: ast.Position{Line: 4}},
		},
		&ast.Document{
			Date: date(2024, 6, 15), Account: "Assets:Cash",
			Span: ast.Span{Start: ast.Position{Line: 5}},
		},
	}
	for _, d := range cases {
		if errs := v.ProcessEntry(d); len(errs) != 0 {
			t.Errorf("ProcessEntry(%T) = %v, want no errors", d, errs)
		}
	}
}

func TestActiveAccounts_HappyPath_ExactlyOnOpenDateIsAllowed(t *testing.T) {
	// Legacy requireOpen uses at.Before(OpenDate) so a date equal to OpenDate
	// is valid.
	state := mkState(map[ast.Account]time.Time{
		"Assets:Cash": date(2024, 1, 1),
	}, nil)
	v := newActiveAccounts(state)

	d := &ast.Balance{
		Date: date(2024, 1, 1), Account: "Assets:Cash",
		Span: ast.Span{Start: ast.Position{Line: 1}},
	}
	if errs := v.ProcessEntry(d); len(errs) != 0 {
		t.Errorf("ProcessEntry(Balance on OpenDate) = %v, want no errors", errs)
	}
}

func TestActiveAccounts_HappyPath_ExactlyOnCloseDateIsAllowed(t *testing.T) {
	// Legacy requireOpen uses at.After(CloseDate) so a date equal to
	// CloseDate is valid (the account is "closed on" but the reference on
	// the same day is accepted).
	state := mkState(
		map[ast.Account]time.Time{"Assets:Cash": date(2024, 1, 1)},
		map[ast.Account]time.Time{"Assets:Cash": date(2024, 6, 30)},
	)
	v := newActiveAccounts(state)
	d := &ast.Balance{
		Date: date(2024, 6, 30), Account: "Assets:Cash",
		Span: ast.Span{Start: ast.Position{Line: 1}},
	}
	if errs := v.ProcessEntry(d); len(errs) != 0 {
		t.Errorf("ProcessEntry(Balance on CloseDate) = %v, want no errors", errs)
	}
}

func TestActiveAccounts_IgnoresUnrelatedDirectives(t *testing.T) {
	// The validator must not emit for directive types it does not dispatch on.
	v := newActiveAccounts(mkState(nil, nil))
	for _, d := range []ast.Directive{
		&ast.Open{Date: date(2024, 1, 1), Account: "Assets:Cash"},
		&ast.Close{Date: date(2024, 12, 31), Account: "Assets:Cash"},
		&ast.Event{Date: date(2024, 1, 1)},
		&ast.Commodity{Date: date(2024, 1, 1)},
		&ast.Price{Date: date(2024, 1, 1)},
	} {
		if errs := v.ProcessEntry(d); len(errs) != 0 {
			t.Errorf("ProcessEntry(%T) = %v, want no errors (directive type not covered)", d, errs)
		}
	}
}
