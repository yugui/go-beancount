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
	Postings int
}

func summariseTx(t *testing.T, d ast.Directive, i int) txSummary {
	t.Helper()
	tx, ok := d.(*ast.Transaction)
	if !ok {
		t.Fatalf("directive %d: type %T, want *ast.Transaction", i, d)
	}
	if len(tx.Postings) < 1 {
		t.Fatalf("directive %d: %d postings, want >= 1", i, len(tx.Postings))
	}
	return txSummary{
		Flag:     tx.Flag,
		Account:  tx.Postings[0].Account,
		Currency: tx.Postings[0].Amount.Currency,
		Postings: len(tx.Postings),
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

	want := txSummary{Flag: '*', Account: "Assets:Checking", Currency: "USD", Postings: 1}
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
[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:Bank"

[payee]
col = "Payee"

[currency]
default = "JPY"

[narration]
col       = ["Description", "Memo"]
separator = " / "

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
[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:Bank"

[currency]
col     = "Cur"
default = "USD"

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

// blankAccountCellTOML configures [account].col + [account.map] but no
// default. A row whose Acct cell is blank cannot resolve to anything and
// therefore emits DiagMissingAccount.
const blankAccountCellTOML = `
[date]
col    = "Date"
format = "2006-01-02"

[account]
col = "Acct"

[account.map]
"X" = "Assets:X"

[currency]
default = "USD"

[[amount]]
col = "Amount"
`

func TestExtract_DiagMissingAccount(t *testing.T) {
	imp := newConfigured(t, blankAccountCellTOML)
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", "Date,Acct,Amount\n2024-01-01,,1\n"))
	if len(out.Directives) != 0 {
		t.Errorf("got %d directives, want 0", len(out.Directives))
	}
	mustOneDiag(t, out, DiagMissingAccount)
}

// blankCurrencyCellTOML configures [currency].col but no default. A row
// whose Cur cell is blank emits DiagMissingCurrency.
const blankCurrencyCellTOML = `
[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:X"

[currency]
col = "Cur"

[[amount]]
col = "Amount"
`

func TestExtract_DiagMissingCurrency(t *testing.T) {
	imp := newConfigured(t, blankCurrencyCellTOML)
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", "Date,Cur,Amount\n2024-01-01,,1\n"))
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
skip_lines = 2

[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:X"

[currency]
default = "USD"

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

// The resolver tests below exercise unexported helpers directly. Per the
// CLAUDE.md exception clause, these helpers are package-internal building
// blocks with independent contracts (one per field); covering every
// branch end-to-end through Extract would require ~20+ CSV fixtures,
// most of which would test the same map-hit / map-miss / pass-through
// branches with extra ceremony. The integration paths are still covered
// by the multiaccount / translations testdata fixtures and by
// TestExtract_DiagUnmappedAccount.

func TestResolveAccount(t *testing.T) {
	cases := []struct {
		name string

		accountCol     string
		accountDefault string
		accountMap     map[string]string

		row      []string
		hints    map[string]string
		want     string
		wantDiag string
	}{
		{
			name:           "hints override beats every shape source",
			accountCol:     "Acct",
			accountDefault: "Assets:Default",
			accountMap:     map[string]string{"x": "Assets:Mapped"},
			row:            []string{"x"},
			hints:          map[string]string{"account": "Assets:Hinted"},
			want:           "Assets:Hinted",
		},
		{
			name:       "col + map: hit returns mapped value",
			accountCol: "Acct",
			accountMap: map[string]string{"x": "Assets:X"},
			row:        []string{"x"},
			want:       "Assets:X",
		},
		{
			name:       "col + map: miss returns DiagUnmappedAccount",
			accountCol: "Acct",
			accountMap: map[string]string{"x": "Assets:X"},
			row:        []string{"y"},
			wantDiag:   DiagUnmappedAccount,
		},
		{
			name:           "col + map: blank cell falls back to default",
			accountCol:     "Acct",
			accountDefault: "Assets:Fallback",
			accountMap:     map[string]string{"x": "Assets:X"},
			row:            []string{""},
			want:           "Assets:Fallback",
		},
		{
			name:       "col + map: blank cell, no default, no hint -> DiagMissingAccount",
			accountCol: "Acct",
			accountMap: map[string]string{"x": "Assets:X"},
			row:        []string{""},
			wantDiag:   DiagMissingAccount,
		},
		{
			name:       "col without map: cell value used verbatim",
			accountCol: "Acct",
			row:        []string{"Assets:Verbatim"},
			want:       "Assets:Verbatim",
		},
		{
			name:           "default only (no col, no hint)",
			accountDefault: "Assets:Only",
			row:            nil,
			want:           "Assets:Only",
		},
		{
			name:       "col + map: trims cell before lookup",
			accountCol: "Acct",
			accountMap: map[string]string{"x": "Assets:X"},
			row:        []string{"  x  "},
			want:       "Assets:X",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &shape{
				accountDefault: tc.accountDefault,
				accountMap:     tc.accountMap,
			}
			idx := map[string]int{}
			if tc.accountCol != "" {
				s.accountCols = []string{tc.accountCol}
				idx[tc.accountCol] = 0
			}
			got, diag := resolveAccount(s, idx, tc.row, tc.hints, "/tmp/x.csv", 1)
			if tc.wantDiag != "" {
				if diag == nil {
					t.Fatalf("resolveAccount() diag = nil, want %q", tc.wantDiag)
				}
				if diag.Code != tc.wantDiag {
					t.Errorf("resolveAccount() diag.Code = %q, want %q", diag.Code, tc.wantDiag)
				}
				if got != "" {
					t.Errorf("resolveAccount() account = %q on diag path, want \"\"", got)
				}
				return
			}
			if diag != nil {
				t.Fatalf("resolveAccount() unexpected diag: %+v", *diag)
			}
			if got != tc.want {
				t.Errorf("resolveAccount() account = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveCurrency(t *testing.T) {
	cases := []struct {
		name string

		currencyCol     string
		currencyDefault string
		currencyMap     map[string]string

		row  []string
		want string
	}{
		{
			name:        "col + map: hit returns mapped",
			currencyCol: "Cur",
			currencyMap: map[string]string{"¥": "JPY"},
			row:         []string{"¥"},
			want:        "JPY",
		},
		{
			name:        "col + map: miss passes through",
			currencyCol: "Cur",
			currencyMap: map[string]string{"¥": "JPY"},
			row:         []string{"EUR"},
			want:        "EUR",
		},
		{
			name:        "col without map: cell verbatim",
			currencyCol: "Cur",
			row:         []string{"EUR"},
			want:        "EUR",
		},
		{
			name:            "blank cell falls back to default",
			currencyCol:     "Cur",
			currencyDefault: "USD",
			currencyMap:     map[string]string{"¥": "JPY"},
			row:             []string{""},
			want:            "USD",
		},
		{
			name:            "no col: returns default",
			currencyDefault: "USD",
			row:             nil,
			want:            "USD",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &shape{
				currencyCol:     tc.currencyCol,
				currencyDefault: tc.currencyDefault,
				currencyMap:     tc.currencyMap,
			}
			idx := map[string]int{}
			if tc.currencyCol != "" {
				idx[tc.currencyCol] = 0
			}
			if got := resolveCurrency(s, idx, tc.row); got != tc.want {
				t.Errorf("resolveCurrency() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolvePayee(t *testing.T) {
	cases := []struct {
		name string

		payeeCol string
		payeeMap map[string]string

		row  []string
		want string
	}{
		{
			name:     "col + map: hit",
			payeeCol: "Payee",
			payeeMap: map[string]string{"AMZN": "Amazon"},
			row:      []string{"AMZN"},
			want:     "Amazon",
		},
		{
			name:     "col + map: miss passes through",
			payeeCol: "Payee",
			payeeMap: map[string]string{"AMZN": "Amazon"},
			row:      []string{"Whole Foods"},
			want:     "Whole Foods",
		},
		{
			name:     "no col: empty",
			payeeCol: "",
			row:      nil,
			want:     "",
		},
		{
			name:     "blank cell: empty",
			payeeCol: "Payee",
			payeeMap: map[string]string{"AMZN": "Amazon"},
			row:      []string{"  "},
			want:     "",
		},
		{
			name:     "trims cell before map lookup",
			payeeCol: "Payee",
			payeeMap: map[string]string{"AMZN": "Amazon"},
			row:      []string{"  AMZN  "},
			want:     "Amazon",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &shape{payeeMap: tc.payeeMap}
			idx := map[string]int{}
			if tc.payeeCol != "" {
				s.payeeCols = []string{tc.payeeCol}
				idx[tc.payeeCol] = 0
			}
			if got := resolvePayee(s, idx, tc.row); got != tc.want {
				t.Errorf("resolvePayee() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildNarration(t *testing.T) {
	cases := []struct {
		name string

		cols []string
		sep  string
		nMap map[string]string

		row  []string
		want string
	}{
		{
			name: "no cols configured",
			cols: nil,
			row:  []string{"x"},
			want: "",
		},
		{
			name: "concat with separator, skip blanks",
			cols: []string{"A", "B", "C"},
			sep:  " / ",
			row:  []string{"hello", "", "world"},
			want: "hello / world",
		},
		{
			name: "map hit replaces value per cell, before join",
			cols: []string{"A", "B"},
			sep:  " / ",
			nMap: map[string]string{"ATM": "ATM withdrawal"},
			row:  []string{"ATM", "Branch 7"},
			want: "ATM withdrawal / Branch 7",
		},
		{
			name: "map miss passes through",
			cols: []string{"A"},
			nMap: map[string]string{"ATM": "ATM withdrawal"},
			row:  []string{"Coffee"},
			want: "Coffee",
		},
		{
			name: "mapped value of empty string drops the cell",
			cols: []string{"A", "B"},
			sep:  " / ",
			nMap: map[string]string{"NOISE": ""},
			row:  []string{"NOISE", "kept"},
			want: "kept",
		},
		{
			name: "map nil: behaves like no map",
			cols: []string{"A"},
			row:  []string{"x"},
			want: "x",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &shape{
				narrationCols: tc.cols,
				narrationSep:  tc.sep,
				narrationMap:  tc.nMap,
			}
			idx := map[string]int{}
			for i, c := range tc.cols {
				idx[c] = i
			}
			if got := buildNarration(s, idx, tc.row); got != tc.want {
				t.Errorf("buildNarration() = %q, want %q", got, tc.want)
			}
		})
	}
}

const unmappedAccountTOML = `
[date]
col    = "Date"
format = "2006-01-02"

[account]
col = "Acct"

[account.map]
"chk-1" = "Assets:Checking"

[currency]
default = "USD"

[[amount]]
col = "Amount"
`

func TestExtract_DiagUnmappedAccount(t *testing.T) {
	imp := newConfigured(t, unmappedAccountTOML)
	body := "Date,Acct,Amount\n2024-05-01,unknown-99,10\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Directives) != 0 {
		t.Errorf("got %d directives, want 0", len(out.Directives))
	}
	mustOneDiag(t, out, DiagUnmappedAccount)
}

// accountColVerbatimTOML configures [account].col without [account.map].
// Cell values are used verbatim as the posting account.
const accountColVerbatimTOML = `
[date]
col    = "Date"
format = "2006-01-02"

[account]
col     = "Acct"
default = "Assets:Default"

[currency]
default = "USD"

[[amount]]
col = "Amount"
`

func TestExtract_AccountColVerbatim(t *testing.T) {
	imp := newConfigured(t, accountColVerbatimTOML)
	body := "Date,Acct,Amount\n2024-06-01,Assets:Verbatim,5\n2024-06-02,,7\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 2 {
		t.Fatalf("got %d directives, want 2", len(out.Directives))
	}
	want := []ast.Account{"Assets:Verbatim", "Assets:Default"}
	for i, d := range out.Directives {
		tx, ok := d.(*ast.Transaction)
		if !ok {
			t.Errorf("directive %d: type %T, want *ast.Transaction", i, d)
			continue
		}
		if got := tx.Postings[0].Account; got != want[i] {
			t.Errorf("row %d account = %q, want %q", i, got, want[i])
		}
	}
}

// emptyAccountMapTOML writes an empty [account.map] table. Empty maps
// must NOT activate strict mode (DiagUnmappedAccount); they normalise
// to nil so cell values are used verbatim. This pins the contract that
// guards against accidentally-empty maps left over during editing.
const emptyAccountMapTOML = `
[date]
col    = "Date"
format = "2006-01-02"

[account]
col     = "Acct"
default = "Assets:Default"

[account.map]

[currency]
default = "USD"

[[amount]]
col = "Amount"
`

func TestExtract_EmptyAccountMapIsNotStrict(t *testing.T) {
	imp := newConfigured(t, emptyAccountMapTOML)
	body := "Date,Acct,Amount\n2024-06-01,Assets:Verbatim,5\n2024-06-02,,7\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 2 {
		t.Fatalf("got %d directives, want 2", len(out.Directives))
	}
	want := []ast.Account{"Assets:Verbatim", "Assets:Default"}
	for i, d := range out.Directives {
		tx, ok := d.(*ast.Transaction)
		if !ok {
			t.Errorf("Extract row %d: directive type %T, want *ast.Transaction", i, d)
			continue
		}
		if got := tx.Postings[0].Account; got != want[i] {
			t.Errorf("Extract row %d: account = %q, want %q", i, got, want[i])
		}
	}
}

// multiNarrationMapTOML exercises [narration.map] across multiple cols:
// one hits, one misses. The per-cell-then-join contract requires the
// hit to be replaced and the miss to pass through, in order.
const multiNarrationMapTOML = `
[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:X"

[currency]
default = "USD"

[narration]
col       = ["A", "B"]
separator = " / "

[narration.map]
"ATM" = "ATM withdrawal"

[[amount]]
col = "Amount"
`

// counterAccountTOML configures [counter_account] with a single col +
// map + default. Used to exercise both the map-hit path and the
// blank-cell-with-default soft fallback.
const counterAccountTOML = `
[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:Checking"

[currency]
default = "USD"

[counter_account]
col     = "Category"
default = "Expenses:Misc"

[counter_account.map]
"Food" = "Expenses:Food"

[[amount]]
col = "Amount"
`

func TestExtract_CounterAccountEmitsSecondPosting(t *testing.T) {
	imp := newConfigured(t, counterAccountTOML)
	body := "Date,Category,Amount\n2024-03-01,Food,-12.50\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx := out.Directives[0].(*ast.Transaction)
	if len(tx.Postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(tx.Postings))
	}
	if got, want := tx.Postings[0].Account, ast.Account("Assets:Checking"); got != want {
		t.Errorf("primary account = %q, want %q", got, want)
	}
	if got, want := tx.Postings[1].Account, ast.Account("Expenses:Food"); got != want {
		t.Errorf("counter account = %q, want %q", got, want)
	}
	if got, want := tx.Postings[0].Amount.Number.Text('f'), "-12.50"; got != want {
		t.Errorf("primary amount = %q, want %q", got, want)
	}
	if got, want := tx.Postings[1].Amount.Number.Text('f'), "12.50"; got != want {
		t.Errorf("counter amount = %q, want %q (negated)", got, want)
	}
	if got, want := tx.Postings[1].Amount.Currency, "USD"; got != want {
		t.Errorf("counter currency = %q, want %q", got, want)
	}
}

func TestExtract_CounterAccountBlankCellUsesDefault(t *testing.T) {
	imp := newConfigured(t, counterAccountTOML)
	body := "Date,Category,Amount\n2024-03-02,,-5.00\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	tx := out.Directives[0].(*ast.Transaction)
	if len(tx.Postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(tx.Postings))
	}
	if got, want := tx.Postings[1].Account, ast.Account("Expenses:Misc"); got != want {
		t.Errorf("counter account = %q, want %q (fallback to default)", got, want)
	}
}

// counterAccountNoDefaultTOML omits [counter_account].default. A blank
// Category cell must fall back to a single posting (soft fallback), not
// emit a diagnostic.
const counterAccountNoDefaultTOML = `
[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:Checking"

[currency]
default = "USD"

[counter_account]
col = "Category"

[counter_account.map]
"Food" = "Expenses:Food"

[[amount]]
col = "Amount"
`

func TestExtract_CounterAccountBlankCellNoDefaultEmitsSinglePosting(t *testing.T) {
	imp := newConfigured(t, counterAccountNoDefaultTOML)
	body := "Date,Category,Amount\n2024-03-02,,-5.00\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx := out.Directives[0].(*ast.Transaction)
	if len(tx.Postings) != 1 {
		t.Errorf("got %d postings, want 1 (soft fallback)", len(tx.Postings))
	}
}

// TestExtract_DiagUnmappedCounterAccountIsWarning pins the contract
// that an unmapped strict-mode counter_account key does NOT drop the
// row: a single-posting transaction is still emitted, and a warning
// diagnostic surfaces the configuration gap.
func TestExtract_DiagUnmappedCounterAccountIsWarning(t *testing.T) {
	imp := newConfigured(t, counterAccountNoDefaultTOML)
	body := "Date,Category,Amount\n2024-03-02,Travel,-5.00\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1 (row kept on counter warning)", len(out.Directives))
	}
	tx := out.Directives[0].(*ast.Transaction)
	if len(tx.Postings) != 1 {
		t.Errorf("got %d postings, want 1 (counter posting suppressed)", len(tx.Postings))
	}
	if len(out.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(out.Diagnostics), out.Diagnostics)
	}
	d := out.Diagnostics[0]
	if d.Code != DiagUnmappedCounterAccount {
		t.Errorf("diag code = %q, want %q", d.Code, DiagUnmappedCounterAccount)
	}
	if d.Severity != ast.Warning {
		t.Errorf("diag severity = %v, want Warning", d.Severity)
	}
}

// counterAccountMultiColTOML joins two columns with separator before
// map lookup.
const counterAccountMultiColTOML = `
[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:Checking"

[currency]
default = "USD"

[counter_account]
col       = ["Category", "Subcategory"]
separator = ":"

[counter_account.map]
"Food:Restaurants" = "Expenses:Food:Restaurants"

[[amount]]
col = "Amount"
`

func TestExtract_CounterAccountMultiColJoinedKey(t *testing.T) {
	imp := newConfigured(t, counterAccountMultiColTOML)
	body := "Date,Category,Subcategory,Amount\n2024-04-01,Food,Restaurants,-25.00\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	tx := out.Directives[0].(*ast.Transaction)
	if len(tx.Postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(tx.Postings))
	}
	if got, want := tx.Postings[1].Account, ast.Account("Expenses:Food:Restaurants"); got != want {
		t.Errorf("counter account = %q, want %q", got, want)
	}
}

// TestExtract_HintsDoesNotOverrideCounterAccount pins the contract that
// Hints["account"] affects only the primary account, never the counter
// account. The counter account is still resolved from its own col/map.
func TestExtract_HintsDoesNotOverrideCounterAccount(t *testing.T) {
	imp := newConfigured(t, counterAccountTOML)
	in := inputFromString("/tmp/x.csv", "", "Date,Category,Amount\n2024-03-01,Food,-12.50\n")
	in.Hints = map[string]string{"account": "Assets:Hinted"}
	out := extract(t, imp, in)
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx := out.Directives[0].(*ast.Transaction)
	if len(tx.Postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(tx.Postings))
	}
	if got, want := tx.Postings[0].Account, ast.Account("Assets:Hinted"); got != want {
		t.Errorf("primary account = %q, want %q (Hints wins)", got, want)
	}
	if got, want := tx.Postings[1].Account, ast.Account("Expenses:Food"); got != want {
		t.Errorf("counter account = %q, want %q (Hints does not apply)", got, want)
	}
}

// accountMultiColTOML configures primary [account].col as a two-column
// list joined by separator before [account.map] lookup.
const accountMultiColTOML = `
[date]
col    = "Date"
format = "2006-01-02"

[account]
col       = ["AcctType", "AcctID"]
separator = "-"

[account.map]
"chk-001" = "Assets:Checking"
"sav-002" = "Assets:Savings"

[currency]
default = "USD"

[[amount]]
col = "Amount"
`

func TestExtract_PrimaryAccountMultiCol(t *testing.T) {
	imp := newConfigured(t, accountMultiColTOML)
	body := "Date,AcctType,AcctID,Amount\n2024-05-01,chk,001,100\n2024-05-02,sav,002,200\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 2 {
		t.Fatalf("got %d directives, want 2", len(out.Directives))
	}
	want := []ast.Account{"Assets:Checking", "Assets:Savings"}
	for i, d := range out.Directives {
		tx := d.(*ast.Transaction)
		if got := tx.Postings[0].Account; got != want[i] {
			t.Errorf("row %d account = %q, want %q", i, got, want[i])
		}
	}
}

// payeeMultiColTOML demonstrates payee resolution from joined columns.
const payeeMultiColTOML = `
[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:X"

[currency]
default = "USD"

[payee]
col       = ["PayeeName", "PayeeBranch"]
separator = " - "

[payee.map]
"Acme - HQ" = "Acme Headquarters"

[[amount]]
col = "Amount"
`

func TestExtract_PayeeMultiCol(t *testing.T) {
	imp := newConfigured(t, payeeMultiColTOML)
	body := "Date,PayeeName,PayeeBranch,Amount\n2024-06-01,Acme,HQ,1\n2024-06-02,Other,Branch,1\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	want := []string{"Acme Headquarters", "Other - Branch"}
	for i, d := range out.Directives {
		tx := d.(*ast.Transaction)
		if tx.Payee != want[i] {
			t.Errorf("row %d payee = %q, want %q", i, tx.Payee, want[i])
		}
	}
}

func TestExtract_MultiColNarrationMap(t *testing.T) {
	imp := newConfigured(t, multiNarrationMapTOML)
	body := "Date,A,B,Amount\n2024-07-01,ATM,Branch 7,1\n"
	out := extract(t, imp, inputFromString("/tmp/x.csv", "", body))
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx, ok := out.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("Extract row 0: directive type %T, want *ast.Transaction", out.Directives[0])
	}
	want := "ATM withdrawal / Branch 7"
	if got := tx.Narration; got != want {
		t.Errorf("Extract row 0: narration = %q, want %q", got, want)
	}
}
