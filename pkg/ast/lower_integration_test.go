package ast_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/syntax"
)

func TestLower_Integration_RealisticLedger(t *testing.T) {
	src := `option "title" "Personal Finances"
option "operating_currency" "USD"
plugin "beancount.plugins.auto"
include "prices.beancount"

2024-01-01 open Assets:Bank:Checking USD
2024-01-01 open Assets:Bank:Savings USD
2024-01-01 open Expenses:Food
2024-01-01 open Expenses:Rent
2024-01-01 open Income:Salary USD
2024-01-01 open Equity:Opening-Balances

2024-01-01 pad Assets:Bank:Checking Equity:Opening-Balances
2024-01-02 balance Assets:Bank:Checking 5000 USD

2024-01-01 commodity USD
  name: "US Dollar"

2024-01-15 * "Employer" "January salary"
  Income:Salary  -3000 USD
  Assets:Bank:Checking  3000 USD

2024-01-20 * "Supermarket" "Groceries" #food
  Expenses:Food  150.50 USD
  Assets:Bank:Checking

2024-02-01 * "Landlord" "February rent"
  Expenses:Rent  1200 USD
  Assets:Bank:Checking

2024-02-01 balance Assets:Bank:Checking 6649.50 USD

2024-01-01 price AAPL 185.50 USD

2024-01-01 event "location" "New York"

2024-01-01 note Assets:Bank:Checking "Opened new checking account"

2024-03-01 close Assets:Bank:Savings
`
	cst := syntax.Parse(src)
	f := ast.Lower("ledger.beancount", cst)

	// Should have no diagnostics
	if len(f.Diagnostics) > 0 {
		for _, d := range f.Diagnostics {
			t.Errorf("unexpected diagnostic: %q", d.Message)
		}
		t.FailNow()
	}

	// Count directive types
	var (
		options, plugins, includes int
		opens, closes              int
		commodities, pads          int
		balances, transactions     int
		prices, events, notes      int
	)
	for _, d := range f.Directives {
		switch d.(type) {
		case *ast.Option:
			options++
		case *ast.Plugin:
			plugins++
		case *ast.Include:
			includes++
		case *ast.Open:
			opens++
		case *ast.Close:
			closes++
		case *ast.Commodity:
			commodities++
		case *ast.Pad:
			pads++
		case *ast.Balance:
			balances++
		case *ast.Transaction:
			transactions++
		case *ast.Price:
			prices++
		case *ast.Event:
			events++
		case *ast.Note:
			notes++
		}
	}
	// Verify counts
	assertEqual(t, "options", options, 2)
	assertEqual(t, "plugins", plugins, 1)
	assertEqual(t, "includes", includes, 1)
	assertEqual(t, "opens", opens, 6)
	assertEqual(t, "closes", closes, 1)
	assertEqual(t, "commodities", commodities, 1)
	assertEqual(t, "pads", pads, 1)
	assertEqual(t, "balances", balances, 2)
	assertEqual(t, "transactions", transactions, 3)
	assertEqual(t, "prices", prices, 1)
	assertEqual(t, "events", events, 1)
	assertEqual(t, "notes", notes, 1)
}

func assertEqual(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %d, want %d", name, got, want)
	}
}

func TestLower_Integration_ErrorRecovery(t *testing.T) {
	// Mix of valid and invalid directives
	src := `2024-01-01 open Assets:Bank USD
this is garbage
2024-01-15 * "Valid transaction"
  Expenses:Food  50 USD
  Assets:Bank
another bad line here
2024-02-01 close Assets:Bank
`
	cst := syntax.Parse(src)
	f := ast.Lower("mixed.beancount", cst)

	// Should have diagnostics for the bad lines
	if len(f.Diagnostics) == 0 {
		t.Error("expected diagnostics for syntax errors")
	}

	// Valid directives should still be lowered
	if len(f.Directives) < 3 {
		t.Fatalf("got %d directives, want at least 3 (open, transaction, close)", len(f.Directives))
	}

	// Verify the valid directives are correct types
	if _, ok := f.Directives[0].(*ast.Open); !ok {
		t.Errorf("Directives[0] is %T, want *ast.Open", f.Directives[0])
	}
	if _, ok := f.Directives[1].(*ast.Transaction); !ok {
		t.Errorf("Directives[1] is %T, want *ast.Transaction", f.Directives[1])
	}
	if _, ok := f.Directives[2].(*ast.Close); !ok {
		t.Errorf("Directives[2] is %T, want *ast.Close", f.Directives[2])
	}
}

func TestLower_Integration_FullTransaction(t *testing.T) {
	src := `pushtag #trip-2024

2024-06-15 ! "Airline" "Flight to Tokyo" #flight ^invoice-456
  trip-type: "international"
  Expenses:Travel:Flights  1500.00 USD
    class: "economy"
  Assets:CreditCard  -1500.00 USD

poptag #trip-2024
`
	cst := syntax.Parse(src)
	f := ast.Lower("trips.beancount", cst)

	if len(f.Diagnostics) > 0 {
		for _, d := range f.Diagnostics {
			t.Errorf("unexpected diagnostic: %q", d.Message)
		}
		t.FailNow()
	}

	if len(f.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(f.Directives))
	}

	txn, ok := f.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("directive is %T, want *ast.Transaction", f.Directives[0])
	}

	// Flag should be '!'
	if txn.Flag != '!' {
		t.Errorf("Flag = %c, want !", txn.Flag)
	}

	// Payee and narration
	if txn.Payee != "Airline" {
		t.Errorf("Payee = %q, want %q", txn.Payee, "Airline")
	}
	if txn.Narration != "Flight to Tokyo" {
		t.Errorf("Narration = %q, want %q", txn.Narration, "Flight to Tokyo")
	}

	// Tags should include both #flight (explicit) and #trip-2024 (from pushtag)
	hasTag := func(tags []string, want string) bool {
		for _, tag := range tags {
			if tag == want {
				return true
			}
		}
		return false
	}
	if !hasTag(txn.Tags, "flight") {
		t.Errorf("Tags %v missing \"flight\"", txn.Tags)
	}
	if !hasTag(txn.Tags, "trip-2024") {
		t.Errorf("Tags %v missing \"trip-2024\" (from pushtag)", txn.Tags)
	}

	// Links
	if len(txn.Links) != 1 || txn.Links[0] != "invoice-456" {
		t.Errorf("Links = %v, want [invoice-456]", txn.Links)
	}

	// Transaction-level metadata
	if txn.Meta.Props == nil {
		t.Fatal("expected transaction metadata")
	}
	if v, ok := txn.Meta.Props["trip-type"]; !ok || v.String != "international" {
		t.Errorf("Meta[trip-type] = %v, want MetaString(\"international\")", v)
	}

	// Postings
	if len(txn.Postings) != 2 {
		t.Fatalf("Postings count = %d, want 2", len(txn.Postings))
	}

	// First posting with metadata
	p0 := txn.Postings[0]
	if p0.Account != "Expenses:Travel:Flights" {
		t.Errorf("Posting[0].Account = %q, want %q", p0.Account, "Expenses:Travel:Flights")
	}
	if p0.Amount == nil || p0.Amount.Number.String() != "1500.00" {
		t.Errorf("Posting[0].Amount = %v", p0.Amount)
	}
	if p0.Meta.Props == nil {
		t.Error("expected posting[0] metadata")
	} else if v, ok := p0.Meta.Props["class"]; !ok || v.String != "economy" {
		t.Errorf("Posting[0].Meta[class] = %v, want MetaString(\"economy\")", v)
	}
}

func TestLower_Integration_CostAndPrice(t *testing.T) {
	src := `2024-01-15 * "Buy stocks"
  Assets:Brokerage  10 AAPL {185.50 USD, 2024-01-15, "lot1"} @ 186.00 USD
  Assets:Bank:Checking  -1855.00 USD

2024-06-15 * "Sell stocks"
  Assets:Brokerage  -5 AAPL {185.50 USD, 2024-01-15} @ 200.00 USD
  Assets:Bank:Checking  1000.00 USD
  Income:CapitalGains
`
	cst := syntax.Parse(src)
	f := ast.Lower("stocks.beancount", cst)

	if len(f.Diagnostics) > 0 {
		for _, d := range f.Diagnostics {
			t.Errorf("unexpected diagnostic: %q", d.Message)
		}
		t.FailNow()
	}

	if len(f.Directives) != 2 {
		t.Fatalf("got %d directives, want 2", len(f.Directives))
	}

	// First transaction: buy
	buy, ok := f.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("Directives[0] is %T, want *ast.Transaction", f.Directives[0])
	}
	p0 := buy.Postings[0]
	if p0.Cost == nil {
		t.Fatal("buy posting Cost is nil")
	}
	if p0.Cost.PerUnit == nil || p0.Cost.PerUnit.Number.String() != "185.50" {
		t.Errorf("Cost.PerUnit = %v, want 185.50 USD", p0.Cost.PerUnit)
	}
	if p0.Cost.Label != "lot1" {
		t.Errorf("Cost.Label = %q, want %q", p0.Cost.Label, "lot1")
	}
	if p0.Cost.Date == nil {
		t.Error("Cost.Date is nil")
	}
	if p0.Price == nil {
		t.Fatal("buy posting Price is nil")
	}
	if p0.Price.Amount.Number.String() != "186.00" {
		t.Errorf("Price.Amount = %v, want 186.00 USD", p0.Price.Amount)
	}
	if p0.Price.IsTotal {
		t.Error("Price.IsTotal = true, want false")
	}
}

func TestLower_Integration_CustomAndQuery(t *testing.T) {
	src := `2024-01-01 custom "budget" Expenses:Food "monthly" 500 USD
2024-01-01 query "food-expenses" "SELECT account, sum(position) WHERE account ~ 'Food'"
`
	cst := syntax.Parse(src)
	f := ast.Lower("custom.beancount", cst)

	if len(f.Diagnostics) > 0 {
		t.Fatalf("unexpected diagnostics: %v", f.Diagnostics)
	}

	if len(f.Directives) != 2 {
		t.Fatalf("got %d directives, want 2", len(f.Directives))
	}

	c, ok := f.Directives[0].(*ast.Custom)
	if !ok {
		t.Fatalf("Directives[0] is %T, want *ast.Custom", f.Directives[0])
	}
	if c.TypeName != "budget" {
		t.Errorf("Custom.TypeName = %q, want %q", c.TypeName, "budget")
	}

	q, ok := f.Directives[1].(*ast.Query)
	if !ok {
		t.Fatalf("Directives[1] is %T, want *ast.Query", f.Directives[1])
	}
	if q.Name != "food-expenses" {
		t.Errorf("Query.Name = %q, want %q", q.Name, "food-expenses")
	}
}
