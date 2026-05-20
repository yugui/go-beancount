package csvimp

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
)

func extract(t *testing.T, imp *Importer, in importer.Input) importer.Output {
	t.Helper()
	out, err := imp.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return out
}

// txSummary captures the observable transaction fields we assert in happy-path tests.
type txSummary struct {
	Flag     byte
	Account  ast.Account
	Currency string
}

func summariseTx(t *testing.T, d ast.Directive, i int) txSummary {
	t.Helper()
	tx, ok := d.(*ast.Transaction)
	if !ok {
		t.Fatalf("directive %d: type %T, want *ast.Transaction", i, d)
	}
	if len(tx.Postings) != 1 {
		t.Fatalf("directive %d: %d postings, want 1", i, len(tx.Postings))
	}
	return txSummary{
		Flag:     tx.Flag,
		Account:  tx.Postings[0].Account,
		Currency: tx.Postings[0].Amount.Currency,
	}
}

func TestExtract_Happy_SingleSignedAmount(t *testing.T) {
	imp := newConfigured(t, simpleTOML)
	body := "Date,Amount\n2024-01-15,-4.50\n2024-01-17,2500.00\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))

	if len(out.Diagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 2 {
		t.Fatalf("got %d directives, want 2", len(out.Directives))
	}

	want := txSummary{Flag: '*', Account: "Assets:Checking", Currency: "USD"}
	for i, d := range out.Directives {
		got := summariseTx(t, d, i)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("directive %d mismatch (-want +got):\n%s", i, diff)
		}
		tx := d.(*ast.Transaction)
		if _, ok := tx.Meta.Props[rowhashKey]; !ok {
			t.Errorf("directive %d missing %q metadata", i, rowhashKey)
		}
	}
}

const debitCreditTOML = `
date_col         = "Date"
date_format      = "2006-01-02"
default_currency = "JPY"
account          = "Assets:Bank"
payee_col        = "Payee"
narration_cols      = ["Description", "Memo"]
narration_separator = " / "

[[amount]]
col    = "Withdrawal"
negate = true

[[amount]]
col    = "Deposit"
negate = false
`

func TestExtract_DebitCreditNegateSum(t *testing.T) {
	imp := newConfigured(t, debitCreditTOML)
	body := strings.Join([]string{
		"Date,Payee,Description,Memo,Withdrawal,Deposit",
		"2024-02-01,ATM,Cash out,,5000,",
		"2024-02-02,Employer,Salary,Feb,,300000",
		"2024-02-03,FX,Adj,,1000,500",
		"",
	}, "\n")

	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 3 {
		t.Fatalf("got %d directives, want 3", len(out.Directives))
	}
	want := []string{"-5000", "300000", "-500"}
	for i, d := range out.Directives {
		tx, ok := d.(*ast.Transaction)
		if !ok {
			t.Fatalf("directive %d: type %T, want *ast.Transaction", i, d)
		}
		got := tx.Postings[0].Amount.Number.Text('f')
		if got != want[i] {
			t.Errorf("directive %d amount = %q, want %q", i, got, want[i])
		}
	}
}

func TestExtract_NarrationConcatSkipsEmpty(t *testing.T) {
	imp := newConfigured(t, debitCreditTOML)
	body := strings.Join([]string{
		"Date,Payee,Description,Memo,Withdrawal,Deposit",
		"2024-02-01,X,Hello,World,,100",
		"2024-02-02,Y,,World,,100",
		"2024-02-03,Z,Hello,,,100",
		"2024-02-04,W,,,,100",
		"",
	}, "\n")
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("diags: %+v", out.Diagnostics)
	}
	want := []string{"Hello / World", "World", "Hello", ""}
	for i, d := range out.Directives {
		tx, ok := d.(*ast.Transaction)
		if !ok {
			t.Fatalf("directive %d: type %T, want *ast.Transaction", i, d)
		}
		got := tx.Narration
		if got != want[i] {
			t.Errorf("Extract row %d: narration = %q, want %q", i, got, want[i])
		}
	}
}

const currencyColTOML = `
date_col         = "Date"
date_format      = "2006-01-02"
currency_col     = "Cur"
default_currency = "USD"
account          = "Assets:Bank"

[[amount]]
col = "Amount"
`

func TestExtract_CurrencyColumnTakesPrecedence(t *testing.T) {
	imp := newConfigured(t, currencyColTOML)
	body := "Date,Cur,Amount\n2024-01-01,EUR,1\n2024-01-02,,2\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("diags: %+v", out.Diagnostics)
	}
	if len(out.Directives) < 2 {
		t.Fatalf("got %d directives, want 2", len(out.Directives))
	}
	tx0, ok := out.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("directive 0: type %T, want *ast.Transaction", out.Directives[0])
	}
	if got := tx0.Postings[0].Amount.Currency; got != "EUR" {
		t.Errorf("row 0 currency = %q, want EUR", got)
	}
	tx1, ok := out.Directives[1].(*ast.Transaction)
	if !ok {
		t.Fatalf("directive 1: type %T, want *ast.Transaction", out.Directives[1])
	}
	if got := tx1.Postings[0].Amount.Currency; got != "USD" {
		t.Errorf("row 1 currency = %q, want USD (fell back to default)", got)
	}
}

func TestExtract_HintsAccountOverridesShape(t *testing.T) {
	imp := newConfigured(t, simpleTOML)
	in := inputFromString("/tmp/x.csv", "", "Date,Amount\n2024-01-01,1\n")
	in.Hints = map[string]string{"account": "Assets:Hinted"}
	out := extract(t, imp, in)
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx, ok := out.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("directive 0: type %T, want *ast.Transaction", out.Directives[0])
	}
	if got := tx.Postings[0].Account; got != "Assets:Hinted" {
		t.Errorf("account = %q, want Assets:Hinted (Hints wins)", got)
	}
}

// shape has no account; Hints empty → DiagMissingAccount.
const noAccountTOML = `
date_col         = "Date"
date_format      = "2006-01-02"
default_currency = "USD"

[[amount]]
col = "Amount"
`

func TestExtract_DiagMissingAccount(t *testing.T) {
	imp := newConfigured(t, noAccountTOML)
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", "Date,Amount\n2024-01-01,1\n"))
	if len(out.Directives) != 0 {
		t.Errorf("got %d directives, want 0", len(out.Directives))
	}
	mustOneDiag(t, out, DiagMissingAccount)
}

// shape has no default_currency and no currency_col → DiagMissingCurrency.
const noCurrencyTOML = `
date_col    = "Date"
date_format = "2006-01-02"
account     = "Assets:X"

[[amount]]
col = "Amount"
`

func TestExtract_DiagMissingCurrency(t *testing.T) {
	imp := newConfigured(t, noCurrencyTOML)
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", "Date,Amount\n2024-01-01,1\n"))
	if len(out.Directives) != 0 {
		t.Errorf("got %d directives, want 0", len(out.Directives))
	}
	mustOneDiag(t, out, DiagMissingCurrency)
}

func TestExtract_DiagBadDate(t *testing.T) {
	imp := newConfigured(t, simpleTOML)
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", "Date,Amount\nnotadate,1\n"))
	if len(out.Directives) != 0 {
		t.Errorf("got %d directives, want 0", len(out.Directives))
	}
	mustOneDiag(t, out, DiagBadDate)
}

func TestExtract_DiagBadAmount(t *testing.T) {
	imp := newConfigured(t, simpleTOML)
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", "Date,Amount\n2024-01-01,notnum\n"))
	if len(out.Directives) != 0 {
		t.Errorf("got %d directives, want 0", len(out.Directives))
	}
	mustOneDiag(t, out, DiagBadAmount)
}

func TestExtract_DiagAllBlankAmount(t *testing.T) {
	imp := newConfigured(t, debitCreditTOML)
	body := "Date,Payee,Description,Memo,Withdrawal,Deposit\n2024-01-01,X,Y,Z,,\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Directives) != 0 {
		t.Errorf("got %d directives, want 0", len(out.Directives))
	}
	mustOneDiag(t, out, DiagAllBlankAmount)
}

func mustOneDiag(t *testing.T, out importer.Output, wantCode string) {
	t.Helper()
	if len(out.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(out.Diagnostics), out.Diagnostics)
	}
	d := out.Diagnostics[0]
	if d.Code != wantCode {
		t.Errorf("diag code = %q, want %q", d.Code, wantCode)
	}
	if d.Severity != ast.Error {
		t.Errorf("diag severity = %v, want Error", d.Severity)
	}
}

func TestExtract_BlankRowsSkipped(t *testing.T) {
	imp := newConfigured(t, simpleTOML)
	body := "Date,Amount\n2024-01-01,1\n\n2024-01-02,2\n   \n2024-01-03,3\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("diags: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 3 {
		t.Errorf("got %d directives, want 3", len(out.Directives))
	}
}

func TestExtract_ContextCancellation(t *testing.T) {
	imp := newConfigured(t, simpleTOML)
	in := inputFromString("/tmp/x.csv", "", "Date,Amount\n2024-01-01,1\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := imp.Extract(ctx, in)
	if err == nil {
		t.Fatal("Extract with cancelled context: nil error")
	}
}

// TestExtract_DiagMissingColumn_StatefulOpener verifies that DiagMissingColumn
// is emitted when a required column present at Identify time is absent when
// Extract re-opens the file. The stateful Opener returns a complete header on
// the first call (Identify succeeds) and a header missing "Amount" on the
// second call (Extract re-opens and finds the column absent).
func TestExtract_DiagMissingColumn_StatefulOpener(t *testing.T) {
	imp := newConfigured(t, simpleTOML)

	var calls atomic.Int32
	bodies := []string{
		"Date,Amount\n2024-01-01,1\n", // call 0: Identify
		"Date,Other\n2024-01-01,1\n",  // call 1: Extract — Amount missing
	}
	in := importer.Input{
		Path: "/tmp/x.csv",
		Opener: func() (io.ReadCloser, error) {
			i := int(calls.Add(1)) - 1
			if i >= len(bodies) {
				i = len(bodies) - 1
			}
			return io.NopCloser(strings.NewReader(bodies[i])), nil
		},
	}

	if !imp.Identify(context.Background(), in) {
		t.Fatal("Identify returned false; want true (complete header on first open)")
	}
	out, err := imp.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 0 {
		t.Errorf("got %d directives, want 0", len(out.Directives))
	}
	mustOneDiag(t, out, DiagMissingColumn)
}

func TestExtract_DiagLineNumberAccountsForSkipLines(t *testing.T) {
	// skip_lines = 2 means the header is physical line 3, body starts at line 4.
	// A bad-date row in the first body line should report line 4, not line 2.
	const src = `
skip_lines       = 2
date_col         = "Date"
date_format      = "2006-01-02"
default_currency = "USD"
account          = "Assets:X"

[[amount]]
col = "Amount"
`
	imp := newConfigured(t, src)
	// two banner lines, then header, then one bad row
	body := "Banner\nGenerated\nDate,Amount\nnotadate,1\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(out.Diagnostics), out.Diagnostics)
	}
	d := out.Diagnostics[0]
	// csv.Reader lines are 1-based relative to the reader start; the reader starts
	// after skip_lines, so the first body line is csv line 2 (header=1, body=2),
	// plus skip_lines offset of 2 = physical line 4.
	if d.Span.Start.Line != 4 {
		t.Errorf("diag line = %d, want 4 (skip_lines offset applied)", d.Span.Start.Line)
	}
}
