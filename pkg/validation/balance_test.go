package validation

import (
	"strings"
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
)

// decimalFromString parses s into an apd.Decimal for test construction.
func decimalFromString(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("parse decimal %q: %v", s, err)
	}
	return *d
}

// amtStr constructs an Amount whose number is parsed from s. This preserves
// the decimal exponent, which matters for inferred tolerance.
func amtStr(t *testing.T, s, cur string) ast.Amount {
	t.Helper()
	return ast.Amount{Number: decimalFromString(t, s), Currency: cur}
}

func TestBalanceMatchingAssertion(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	td := parseDay(t, "2024-02-01")
	pos := amt(100, "USD")
	neg := amt(-100, "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	bal := &ast.Balance{
		Date:    parseDay(t, "2024-03-01"),
		Account: "Assets:Cash",
		Amount:  amt(100, "USD"),
	}
	errs := Check(ledgerOf(append(dirs, txn, bal)...))
	if len(errs) != 0 {
		t.Fatalf("matching balance: got %v, want no errors", errs)
	}
}

func TestBalanceWithinInferredTolerance(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	td := parseDay(t, "2024-02-01")
	// Post 100.004 to Cash, -100.004 to Food. Balance assertion "100.00 USD"
	// (exp -2) -> inferred tolerance 0.005. diff = 0.004 -> within.
	pos := amtStr(t, "100.004", "USD")
	neg := amtStr(t, "-100.004", "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	bal := &ast.Balance{
		Date:    parseDay(t, "2024-03-01"),
		Account: "Assets:Cash",
		Amount:  amtStr(t, "100.00", "USD"),
	}
	errs := Check(ledgerOf(append(dirs, txn, bal)...))
	if len(errs) != 0 {
		t.Fatalf("within-tolerance balance: got %v, want no errors", errs)
	}
}

func TestBalanceOutsideTolerance(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	td := parseDay(t, "2024-02-01")
	pos := amtStr(t, "100.00", "USD")
	neg := amtStr(t, "-100.00", "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	// Assert 101.00 USD against actual 100.00. diff=1, tolerance=0.005.
	balSpan := ast.Span{Start: ast.Position{Filename: "test.beancount", Line: 42, Column: 1}}
	bal := &ast.Balance{
		Span:    balSpan,
		Date:    parseDay(t, "2024-03-01"),
		Account: "Assets:Cash",
		Amount:  amtStr(t, "101.00", "USD"),
	}
	errs := Check(ledgerOf(append(dirs, txn, bal)...))
	wantCodes(t, errs, CodeBalanceMismatch)
	if errs[0].Span != balSpan {
		t.Errorf("span = %+v, want %+v", errs[0].Span, balSpan)
	}
	if !strings.Contains(errs[0].Message, "Assets:Cash") {
		t.Errorf("message = %q, want it to mention the account", errs[0].Message)
	}
}

func TestBalanceExplicitTolerance(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	td := parseDay(t, "2024-02-01")
	// Actual balance will be 100.0009.
	pos := amtStr(t, "100.0009", "USD")
	neg := amtStr(t, "-100.0009", "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	tol := decimalFromString(t, "0.001")
	bal := &ast.Balance{
		Date:      parseDay(t, "2024-03-01"),
		Account:   "Assets:Cash",
		Amount:    amtStr(t, "100.00", "USD"),
		Tolerance: &tol,
	}
	errs := Check(ledgerOf(append(dirs, txn, bal)...))
	if len(errs) != 0 {
		t.Fatalf("explicit tolerance: got %v, want no errors", errs)
	}
}

func TestBalanceExplicitToleranceTooTight(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	td := parseDay(t, "2024-02-01")
	pos := amtStr(t, "100.01", "USD")
	neg := amtStr(t, "-100.01", "USD")
	txn := &ast.Transaction{
		Date: td,
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	tol := decimalFromString(t, "0.001")
	bal := &ast.Balance{
		Date:      parseDay(t, "2024-03-01"),
		Account:   "Assets:Cash",
		Amount:    amtStr(t, "100.00", "USD"),
		Tolerance: &tol,
	}
	errs := Check(ledgerOf(append(dirs, txn, bal)...))
	wantCodes(t, errs, CodeBalanceMismatch)
}

func TestBalanceZeroOnUntouchedAccount(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash")
	bal := &ast.Balance{
		Date:    parseDay(t, "2024-02-01"),
		Account: "Assets:Cash",
		Amount:  amt(0, "USD"),
	}
	errs := Check(ledgerOf(append(dirs, bal)...))
	if len(errs) != 0 {
		t.Fatalf("zero balance on untouched account: got %v, want no errors", errs)
	}
}

func TestBalanceNonZeroOnUntouchedAccount(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash")
	bal := &ast.Balance{
		Date:    parseDay(t, "2024-02-01"),
		Account: "Assets:Cash",
		Amount:  amt(100, "USD"),
	}
	errs := Check(ledgerOf(append(dirs, bal)...))
	wantCodes(t, errs, CodeBalanceMismatch)
}

func TestRunningBalanceAccumulatesAcrossTransactions(t *testing.T) {
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Income:Salary")
	pos1 := amt(100, "USD")
	neg1 := amt(-100, "USD")
	txn1 := &ast.Transaction{
		Date: parseDay(t, "2024-02-01"),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos1},
			{Account: "Income:Salary", Amount: &neg1},
		},
	}
	pos2 := amt(50, "USD")
	neg2 := amt(-50, "USD")
	txn2 := &ast.Transaction{
		Date: parseDay(t, "2024-02-15"),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos2},
			{Account: "Income:Salary", Amount: &neg2},
		},
	}
	bal := &ast.Balance{
		Date:    parseDay(t, "2024-03-01"),
		Account: "Assets:Cash",
		Amount:  amt(150, "USD"),
	}
	errs := Check(ledgerOf(append(dirs, txn1, txn2, bal)...))
	if len(errs) != 0 {
		t.Fatalf("running balance accumulation: got %v, want no errors", errs)
	}
}

func TestInferToleranceExponents(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"100.00", "0.005"},
		{"100", "0.5"},
		{"100.001", "0.0005"},
	}
	c := &checker{options: newOptionValues(defaultOptionRegistry)}
	for _, tc := range cases {
		a := amtStr(t, tc.in, "USD")
		got := c.inferTolerance(a)
		if got.Text('f') != tc.want {
			t.Errorf("inferTolerance(%q) = %s, want %s", tc.in, got.Text('f'), tc.want)
		}
	}
}

// buildMultiplierLedger constructs a ledger where a transaction posts
// 100.00 USD into Assets:Cash and a subsequent balance assertion claims
// 100.01 USD. The default multiplier 0.5 yields tolerance 0.005 (fail);
// a multiplier of 1 yields tolerance 0.01 (pass).
func buildMultiplierLedger(t *testing.T, withOption bool) *ast.Ledger {
	t.Helper()
	dirs := openAccounts(t, "2024-01-01", "Assets:Cash", "Expenses:Food")
	pos := amtStr(t, "100.00", "USD")
	neg := amtStr(t, "-100.00", "USD")
	txn := &ast.Transaction{
		Date: parseDay(t, "2024-02-01"),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: "Assets:Cash", Amount: &pos},
			{Account: "Expenses:Food", Amount: &neg},
		},
	}
	bal := &ast.Balance{
		Date:    parseDay(t, "2024-03-01"),
		Account: "Assets:Cash",
		Amount:  amtStr(t, "100.01", "USD"),
	}
	all := append([]ast.Directive{}, dirs...)
	if withOption {
		all = append(all, &ast.Option{Key: "inferred_tolerance_multiplier", Value: "1"})
	}
	all = append(all, txn, bal)
	return ledgerOf(all...)
}

func TestInferToleranceMultiplierOverride(t *testing.T) {
	// Multiplier = 1 → tolerance 0.01 → diff of 0.01 passes.
	errs := Check(buildMultiplierLedger(t, true))
	if len(errs) != 0 {
		t.Fatalf("multiplier=1: got %v, want no errors", errs)
	}

	// Default multiplier 0.5 → tolerance 0.005 → same diff fails.
	errs = Check(buildMultiplierLedger(t, false))
	wantCodes(t, errs, CodeBalanceMismatch)
}

func TestInferredToleranceMultiplierInvalid(t *testing.T) {
	ledger := &ast.Ledger{}
	ledger.InsertAll([]ast.Directive{
		&ast.Option{
			Span:  ast.Span{Start: ast.Position{Filename: "t.beancount", Line: 1, Column: 1}},
			Key:   "inferred_tolerance_multiplier",
			Value: "abc",
		},
	})
	errs := Check(ledger)
	if len(errs) != 1 {
		t.Fatalf("invalid multiplier: got %d errors, want 1: %v", len(errs), errs)
	}
	if errs[0].Code != CodeInvalidOption {
		t.Errorf("TestInferredToleranceMultiplierInvalid: errs[0].Code = %v, want %v", errs[0].Code, CodeInvalidOption)
	}
}
