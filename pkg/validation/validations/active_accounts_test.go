package validations

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
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

	want := []ast.Diagnostic{{
		Code:     string(validation.CodeAccountNotOpen),
		Span:     postingSpan,
		Message:  `account "Assets:Cash" is not open`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(txn)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
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
	want := []ast.Diagnostic{{
		Code:     string(validation.CodeAccountNotOpen),
		Span:     txnSpan,
		Message:  `account "Assets:Cash" is not open`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(txn)); diff != "" {
		t.Errorf("ProcessEntry: posting had zero span; want fallback to txn span (-want +got):\n%s", diff)
	}
}

func TestActiveAccounts_Transaction_PostingBeforeOpen(t *testing.T) {
	state := mkState(map[ast.Account]time.Time{
		"Assets:Cash": date(2024, 2, 1),
	}, nil)
	v := newActiveAccounts(state)

	postingSpan := ast.Span{Start: ast.Position{Line: 2}}
	txn := &ast.Transaction{
		Date: date(2024, 1, 15),
		Span: ast.Span{Start: ast.Position{Line: 1}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Span: postingSpan},
		},
	}
	want := []ast.Diagnostic{{
		Code:     string(validation.CodeAccountNotYetOpen),
		Span:     postingSpan,
		Message:  `account "Assets:Cash" is not open on 2024-01-15`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(txn)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
	}
}

func TestActiveAccounts_Transaction_PostingAfterClose(t *testing.T) {
	state := mkState(
		map[ast.Account]time.Time{"Assets:Cash": date(2023, 1, 1)},
		map[ast.Account]time.Time{"Assets:Cash": date(2024, 1, 1)},
	)
	v := newActiveAccounts(state)

	postingSpan := ast.Span{Start: ast.Position{Line: 2}}
	txn := &ast.Transaction{
		Date: date(2024, 6, 1),
		Span: ast.Span{Start: ast.Position{Line: 1}},
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Span: postingSpan},
		},
	}
	want := []ast.Diagnostic{{
		Code:     string(validation.CodeAccountClosed),
		Span:     postingSpan,
		Message:  `account "Assets:Cash" is closed on 2024-06-01`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(txn)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
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
	want := []ast.Diagnostic{{
		Code:     string(validation.CodeAccountNotOpen),
		Span:     span,
		Message:  `account "Assets:Cash" is not open`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(d)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
	}
}

func TestActiveAccounts_Pad_UnopenedAccounts(t *testing.T) {
	// Neither account is open → two errors (one per referenced account),
	// matching upstream beancount's pad visitor which calls
	// require-open on both.
	v := newActiveAccounts(mkState(nil, nil))
	span := ast.Span{Start: ast.Position{Line: 7, Offset: 70}}
	d := &ast.Pad{
		Date:       date(2024, 1, 15),
		Account:    "Assets:Target",
		PadAccount: "Equity:Opening",
		Span:       span,
	}
	want := []ast.Diagnostic{
		{
			Code:     string(validation.CodeAccountNotOpen),
			Span:     span,
			Message:  `account "Assets:Target" is not open`,
			Severity: ast.Error,
		},
		{
			Code:     string(validation.CodeAccountNotOpen),
			Span:     span,
			Message:  `account "Equity:Opening" is not open`,
			Severity: ast.Error,
		},
	}
	if diff := cmp.Diff(want, v.ProcessEntry(d)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
	}
}

func TestActiveAccounts_Pad_OnlySourceMissing(t *testing.T) {
	// Target open, source not → exactly one error for the source account.
	v := newActiveAccounts(mkState(map[ast.Account]time.Time{
		"Assets:Target": date(2024, 1, 1),
	}, nil))
	span := ast.Span{Start: ast.Position{Line: 1}}
	d := &ast.Pad{
		Date:       date(2024, 1, 15),
		Account:    "Assets:Target",
		PadAccount: "Equity:Opening",
		Span:       span,
	}
	want := []ast.Diagnostic{{
		Code:     string(validation.CodeAccountNotOpen),
		Span:     span,
		Message:  `account "Equity:Opening" is not open`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(d)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
	}
}

func TestActiveAccounts_Note_UnopenedAccount(t *testing.T) {
	v := newActiveAccounts(mkState(nil, nil))
	span := ast.Span{Start: ast.Position{Line: 3}}
	d := &ast.Note{
		Date:    date(2024, 1, 15),
		Account: "Assets:Cash",
		Span:    span,
	}
	want := []ast.Diagnostic{{
		Code:     string(validation.CodeAccountNotOpen),
		Span:     span,
		Message:  `account "Assets:Cash" is not open`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(d)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
	}
}

func TestActiveAccounts_Document_UnopenedAccount(t *testing.T) {
	v := newActiveAccounts(mkState(nil, nil))
	span := ast.Span{Start: ast.Position{Line: 4}}
	d := &ast.Document{
		Date:    date(2024, 1, 15),
		Account: "Assets:Cash",
		Span:    span,
	}
	want := []ast.Diagnostic{{
		Code:     string(validation.CodeAccountNotOpen),
		Span:     span,
		Message:  `account "Assets:Cash" is not open`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(d)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
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
	// The validator uses at.Before(OpenDate) so a date equal to OpenDate
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
	// The validator uses at.After(CloseDate) so a date equal to
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

// TestActiveAccounts_BalanceAfterClose_Accepted asserts that a balance
// directive dated after the account's close date emits no diagnostic,
// matching upstream beancount which permits retrospective balance
// assertions on closed accounts.
func TestActiveAccounts_BalanceAfterClose_Accepted(t *testing.T) {
	state := mkState(
		map[ast.Account]time.Time{"Assets:Cash": date(2023, 1, 1)},
		map[ast.Account]time.Time{"Assets:Cash": date(2024, 1, 1)},
	)
	v := newActiveAccounts(state)
	d := &ast.Balance{
		Date:    date(2024, 6, 1),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Line: 1}},
	}
	if errs := v.ProcessEntry(d); len(errs) != 0 {
		t.Errorf("ProcessEntry(Balance after close) = %v, want no errors", errs)
	}
}

// TestActiveAccounts_NoteAfterClose_Accepted asserts that a note
// directive dated after the account's close date emits no diagnostic,
// matching upstream beancount.
func TestActiveAccounts_NoteAfterClose_Accepted(t *testing.T) {
	state := mkState(
		map[ast.Account]time.Time{"Assets:Cash": date(2023, 1, 1)},
		map[ast.Account]time.Time{"Assets:Cash": date(2024, 1, 1)},
	)
	v := newActiveAccounts(state)
	d := &ast.Note{
		Date:    date(2024, 6, 1),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Line: 1}},
	}
	if errs := v.ProcessEntry(d); len(errs) != 0 {
		t.Errorf("ProcessEntry(Note after close) = %v, want no errors", errs)
	}
}

// TestActiveAccounts_DocumentAfterClose_Accepted asserts that a
// document directive dated after the account's close date emits no
// diagnostic, matching upstream beancount.
func TestActiveAccounts_DocumentAfterClose_Accepted(t *testing.T) {
	state := mkState(
		map[ast.Account]time.Time{"Assets:Cash": date(2023, 1, 1)},
		map[ast.Account]time.Time{"Assets:Cash": date(2024, 1, 1)},
	)
	v := newActiveAccounts(state)
	d := &ast.Document{
		Date:    date(2024, 6, 1),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Line: 1}},
	}
	if errs := v.ProcessEntry(d); len(errs) != 0 {
		t.Errorf("ProcessEntry(Document after close) = %v, want no errors", errs)
	}
}

// TestActiveAccounts_PadAfterClose_StillRejected asserts that a pad
// directive dated after the account's close date is still rejected for
// both the destination and source account slots, matching upstream
// beancount's "Invalid reference to inactive account" behavior.
func TestActiveAccounts_PadAfterClose_StillRejected(t *testing.T) {
	state := mkState(
		map[ast.Account]time.Time{
			"Assets:Target":  date(2023, 1, 1),
			"Equity:Opening": date(2023, 1, 1),
		},
		map[ast.Account]time.Time{
			"Assets:Target":  date(2024, 1, 1),
			"Equity:Opening": date(2024, 1, 1),
		},
	)
	v := newActiveAccounts(state)
	span := ast.Span{Start: ast.Position{Line: 1}}
	d := &ast.Pad{
		Date:       date(2024, 6, 1),
		Account:    "Assets:Target",
		PadAccount: "Equity:Opening",
		Span:       span,
	}
	// Both slots must surface the same close-date diagnostic regardless
	// of which is checked first; sort by Message so the assertion does
	// not pin ProcessEntry's current Account-before-PadAccount visit
	// order.
	want := []ast.Diagnostic{
		{
			Code:     string(validation.CodeAccountClosed),
			Span:     span,
			Message:  `account "Assets:Target" is closed on 2024-06-01`,
			Severity: ast.Error,
		},
		{
			Code:     string(validation.CodeAccountClosed),
			Span:     span,
			Message:  `account "Equity:Opening" is closed on 2024-06-01`,
			Severity: ast.Error,
		},
	}
	sortDiags := cmpopts.SortSlices(func(a, b ast.Diagnostic) bool { return a.Message < b.Message })
	if diff := cmp.Diff(want, v.ProcessEntry(d), sortDiags); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
	}
}

// TestActiveAccounts_NoteBeforeOpen_NotYetOpen verifies that the
// "before open" diagnostic still fires for note directives even though
// the close-date check is now skipped for notes. Open / not-yet-open
// semantics are unchanged by the dispatch refactor.
func TestActiveAccounts_NoteBeforeOpen_NotYetOpen(t *testing.T) {
	state := mkState(map[ast.Account]time.Time{
		"Assets:Cash": date(2024, 2, 1),
	}, nil)
	v := newActiveAccounts(state)
	span := ast.Span{Start: ast.Position{Line: 1}}
	d := &ast.Note{
		Date:    date(2024, 1, 15),
		Account: "Assets:Cash",
		Span:    span,
	}
	want := []ast.Diagnostic{{
		Code:     string(validation.CodeAccountNotYetOpen),
		Span:     span,
		Message:  `account "Assets:Cash" is not open on 2024-01-15`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(d)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
	}
}

// TestActiveAccounts_DocumentBeforeOpen_NotYetOpen verifies that the
// "before open" diagnostic still fires for document directives even
// though the close-date check is now skipped for documents.
func TestActiveAccounts_DocumentBeforeOpen_NotYetOpen(t *testing.T) {
	state := mkState(map[ast.Account]time.Time{
		"Assets:Cash": date(2024, 2, 1),
	}, nil)
	v := newActiveAccounts(state)
	span := ast.Span{Start: ast.Position{Line: 1}}
	d := &ast.Document{
		Date:    date(2024, 1, 15),
		Account: "Assets:Cash",
		Span:    span,
	}
	want := []ast.Diagnostic{{
		Code:     string(validation.CodeAccountNotYetOpen),
		Span:     span,
		Message:  `account "Assets:Cash" is not open on 2024-01-15`,
		Severity: ast.Error,
	}}
	if diff := cmp.Diff(want, v.ProcessEntry(d)); diff != "" {
		t.Errorf("ProcessEntry mismatch (-want +got):\n%s", diff)
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
