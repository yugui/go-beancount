package predict_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/hook/std/predict"
)

func mustDate(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func post(acct, num, cur string) ast.Posting {
	return ast.Posting{Account: ast.Account(acct), Amount: amt(num, cur)}
}

func txn(date, payee string, ps ...ast.Posting) *ast.Transaction {
	return &ast.Transaction{Date: mustDate(date), Flag: '*', Payee: payee, Postings: ps}
}

func ledger(ds ...ast.Directive) *ast.Ledger {
	l := &ast.Ledger{}
	l.InsertAll(ds)
	return l
}

func labelsOf(exs []predict.Example) []ast.Account {
	out := make([]ast.Account, len(exs))
	for i, e := range exs {
		out[i] = e.Label
	}
	return out
}

func TestExtractExamplesSourceSide(t *testing.T) {
	l := ledger(txn("2024-01-02", "Cafe",
		post("Assets:Bank:Checking", "-5", "USD"),
		post("Expenses:Coffee", "5", "USD"),
	))
	exs := predict.ExtractExamples(l, splitTok{}, predict.DefaultFieldWeights())
	if got, want := len(exs), 1; got != want {
		t.Fatalf("examples = %d, want %d", got, want)
	}
	if exs[0].Label != "Expenses:Coffee" {
		t.Errorf("label = %q, want Expenses:Coffee", exs[0].Label)
	}
	if exs[0].Features.Sign != predict.SignCredit {
		t.Errorf("known sign = %v, want SignCredit (Assets posting is known)", exs[0].Features.Sign)
	}
	if !exs[0].Date.Equal(mustDate("2024-01-02")) {
		t.Errorf("date = %v, want 2024-01-02", exs[0].Date)
	}
}

func TestExtractExamplesLiabilitySource(t *testing.T) {
	l := ledger(txn("2024-01-02", "Grocer",
		post("Expenses:Food", "20", "USD"),
		post("Liabilities:CreditCard", "-20", "USD"),
	))
	exs := predict.ExtractExamples(l, splitTok{}, predict.DefaultFieldWeights())
	if got, want := len(exs), 1; got != want {
		t.Fatalf("examples = %d, want %d", got, want)
	}
	if exs[0].Label != "Expenses:Food" {
		t.Errorf("label = %q, want Expenses:Food", exs[0].Label)
	}
}

func TestExtractExamplesTransferBothOrientations(t *testing.T) {
	l := ledger(txn("2024-01-02", "Move",
		post("Assets:A", "-10", "USD"),
		post("Assets:B", "10", "USD"),
	))
	exs := predict.ExtractExamples(l, splitTok{}, predict.DefaultFieldWeights())
	want := []ast.Account{"Assets:B", "Assets:A"}
	if diff := cmp.Diff(want, labelsOf(exs)); diff != "" {
		t.Errorf("transfer labels (-want +got):\n%s", diff)
	}
}

func TestExtractExamplesNeitherSourceLike(t *testing.T) {
	l := ledger(txn("2024-01-02", "Payroll",
		post("Income:Salary", "-1000", "USD"),
		post("Expenses:Tax", "1000", "USD"),
	))
	exs := predict.ExtractExamples(l, splitTok{}, predict.DefaultFieldWeights())
	want := []ast.Account{"Expenses:Tax", "Income:Salary"}
	if diff := cmp.Diff(want, labelsOf(exs)); diff != "" {
		t.Errorf("non-source labels (-want +got):\n%s", diff)
	}
}

func TestExtractExamplesSkips(t *testing.T) {
	singleLeg := txn("2024-01-01", "single", post("Assets:Bank", "-5", "USD"))
	threeLeg := txn("2024-01-02", "split",
		post("Assets:Bank", "-5", "USD"),
		post("Expenses:A", "2", "USD"),
		post("Expenses:B", "3", "USD"),
	)
	missingAmount := &ast.Transaction{
		Date:  mustDate("2024-01-03"),
		Payee: "incomplete",
		Postings: []ast.Posting{
			post("Assets:Bank", "-5", "USD"),
			{Account: "Expenses:Auto"}, // nil amount
		},
	}
	l := ledger(singleLeg, threeLeg, missingAmount)
	exs := predict.ExtractExamples(l, splitTok{}, predict.DefaultFieldWeights())
	if len(exs) != 0 {
		t.Errorf("examples = %d, want 0 (all ineligible)", len(exs))
	}
}

func TestExtractExamplesOrder(t *testing.T) {
	l := ledger(
		txn("2024-02-01", "later",
			post("Assets:Bank", "-1", "USD"), post("Expenses:Late", "1", "USD")),
		txn("2024-01-01", "earlier",
			post("Assets:Bank", "-1", "USD"), post("Expenses:Early", "1", "USD")),
	)
	exs := predict.ExtractExamples(l, splitTok{}, predict.DefaultFieldWeights())
	want := []ast.Account{"Expenses:Early", "Expenses:Late"}
	if diff := cmp.Diff(want, labelsOf(exs)); diff != "" {
		t.Errorf("canonical order (-want +got):\n%s", diff)
	}
}

func TestOpenAccounts(t *testing.T) {
	l := ledger(
		&ast.Open{Date: mustDate("2024-01-01"), Account: "Assets:A"},
		&ast.Open{Date: mustDate("2024-01-01"), Account: "Assets:B"},
		&ast.Close{Date: mustDate("2024-06-01"), Account: "Assets:A"},
		&ast.Open{Date: mustDate("2024-07-01"), Account: "Assets:A"}, // reopen
	)
	got := predict.OpenAccounts(l)
	want := map[ast.Account]bool{"Assets:A": true, "Assets:B": true}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("OpenAccounts (-want +got):\n%s", diff)
	}
}

func TestOpenAccountsClosedExcluded(t *testing.T) {
	l := ledger(
		&ast.Open{Date: mustDate("2024-01-01"), Account: "Assets:A"},
		&ast.Close{Date: mustDate("2024-06-01"), Account: "Assets:A"},
	)
	if got := predict.OpenAccounts(l); len(got) != 0 {
		t.Errorf("OpenAccounts = %v, want empty after close", got)
	}
}

func TestOpenAccountsNilLedger(t *testing.T) {
	got := predict.OpenAccounts(nil)
	if got == nil {
		t.Errorf("OpenAccounts(nil) = nil, want empty non-nil map")
	}
	if len(got) != 0 {
		t.Errorf("OpenAccounts(nil) = %v, want empty", got)
	}
}
