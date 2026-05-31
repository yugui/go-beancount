package std_test

import (
	"context"
	"sync"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"

	_ "github.com/yugui/go-beancount/pkg/query/env/std"
)

// priceLedger holds one AAPL holding (2 units @ 10 USD cost) plus a small
// price history: AAPL→USD at 9 then 11, and EUR→USD at 1.2 (the inverse seam
// for USD→EUR lookups).
func priceLedger(t *testing.T) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Price{Date: date(2021, 1, 1), Commodity: "AAPL", Amount: ast.Amount{Number: dec(t, "9"), Currency: "USD"}},
		&ast.Price{Date: date(2021, 3, 15), Commodity: "AAPL", Amount: ast.Amount{Number: dec(t, "11"), Currency: "USD"}},
		&ast.Price{Date: date(2021, 3, 10), Commodity: "EUR", Amount: ast.Amount{Number: dec(t, "1.2"), Currency: "USD"}},
		&ast.Transaction{
			Date: date(2021, 3, 15),
			Flag: '*',
			Postings: []ast.Posting{
				{
					Account: "Assets:Brokerage:AAPL",
					Amount:  &ast.Amount{Number: dec(t, "2"), Currency: "AAPL"},
					Cost:    &ast.Cost{Number: dec(t, "10"), Currency: "USD", Date: date(2021, 1, 1)},
				},
				{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "-20"), Currency: "USD"}},
			},
		},
	})
	return l
}

func TestGetpriceLatest(t *testing.T) {
	l := priceLedger(t)
	res := mustQuery(t, l, "SELECT getprice('AAPL', 'USD') AS p FROM postings LIMIT 1")
	if got := res.Rows[0][0].Format(); got != "11" {
		t.Errorf("getprice latest = %s, want 11", got)
	}
}

func TestGetpriceOnOrBeforeDate(t *testing.T) {
	l := priceLedger(t)
	// On/before 2021-02-01 only the 2021-01-01 price (9) exists.
	res := mustQuery(t, l, "SELECT getprice('AAPL', 'USD', 2021-02-01) AS p FROM postings LIMIT 1")
	if got := res.Rows[0][0].Format(); got != "9" {
		t.Errorf("getprice on/before 2021-02-01 = %s, want 9", got)
	}
	// The boundary date itself selects that day's price (inclusive).
	res = mustQuery(t, l, "SELECT getprice('AAPL', 'USD', 2021-03-15) AS p FROM postings LIMIT 1")
	if got := res.Rows[0][0].Format(); got != "11" {
		t.Errorf("getprice on 2021-03-15 = %s, want 11", got)
	}
}

func TestGetpriceBeforeAllIsNull(t *testing.T) {
	l := priceLedger(t)
	res := mustQuery(t, l, "SELECT getprice('AAPL', 'USD', 2020-12-31) AS p FROM postings LIMIT 1")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("getprice before any price = %v, want NULL", res.Rows[0][0])
	}
}

func TestGetpriceMissingPairIsNull(t *testing.T) {
	l := priceLedger(t)
	res := mustQuery(t, l, "SELECT getprice('XXX', 'USD') AS p FROM postings LIMIT 1")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("getprice missing pair = %v, want NULL", res.Rows[0][0])
	}
}

func TestGetpriceInverse(t *testing.T) {
	l := priceLedger(t)
	// Only EUR→USD (1.2) is recorded; USD→EUR uses the one-hop inverse 1/1.2.
	res := mustQuery(t, l, "SELECT getprice('USD', 'EUR') AS p FROM postings LIMIT 1")
	d, ok := types.AsDecimal(res.Rows[0][0])
	if !ok {
		t.Fatalf("getprice inverse = %v, want a decimal", res.Rows[0][0])
	}
	// 1/1.2 = 0.8333… ; check the leading digits without overcommitting to
	// the full decimal128 expansion.
	if got := d.Text('f'); got[:8] != "0.833333" {
		t.Errorf("getprice('USD','EUR') = %s, want ~0.8333", got)
	}
}

func TestGetpriceCaseInsensitiveCurrency(t *testing.T) {
	l := priceLedger(t)
	res := mustQuery(t, l, "SELECT getprice('aapl', 'usd') AS p FROM postings LIMIT 1")
	if got := res.Rows[0][0].Format(); got != "11" {
		t.Errorf("getprice lowercased = %s, want 11", got)
	}
}

func TestConvertPosition(t *testing.T) {
	l := priceLedger(t)
	// 2 AAPL at the latest price (11) = 22 USD.
	res := mustQuery(t, l,
		"SELECT convert(position, 'USD') AS v FROM postings WHERE account = 'Assets:Brokerage:AAPL'")
	v := res.Rows[0][0]
	if v.Type() != types.Amount || v.Format() != "22 USD" {
		t.Errorf("convert(position,'USD') = %v, want 22 USD", v)
	}
}

func TestConvertAmountIdentity(t *testing.T) {
	l := priceLedger(t)
	// weight on the cash posting is already USD: conversion is the identity.
	res := mustQuery(t, l,
		"SELECT convert(weight, 'USD') AS v FROM postings WHERE account = 'Assets:Cash'")
	if got := res.Rows[0][0].Format(); got != "-20 USD" {
		t.Errorf("convert(weight,'USD') identity = %s, want -20 USD", got)
	}
}

func TestConvertAmountMissingIsNull(t *testing.T) {
	l := priceLedger(t)
	// units is 2 AAPL; converting to a currency with no path yields NULL.
	res := mustQuery(t, l,
		"SELECT convert(units(position), 'JPY') AS v FROM postings WHERE account = 'Assets:Brokerage:AAPL'")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("convert to unreachable currency = %v, want NULL", res.Rows[0][0])
	}
}

func TestConvertBalanceInventory(t *testing.T) {
	l := priceLedger(t)
	// The running balance over the AAPL posting alone is 2 AAPL; converting the
	// inventory to USD yields a one-position inventory of 22 USD.
	res := mustQuery(t, l,
		"SELECT convert(balance, 'USD') AS v FROM postings WHERE account = 'Assets:Brokerage:AAPL'")
	v := res.Rows[0][0]
	if v.Type() != types.Inventory || v.Format() != "(22 USD)" {
		t.Errorf("convert(balance,'USD') = %v, want (22 USD)", v)
	}
}

func TestValuePositionToCostCurrency(t *testing.T) {
	l := priceLedger(t)
	// value() converts 2 AAPL to its cost currency (USD) at the market price 11.
	res := mustQuery(t, l,
		"SELECT value(position) AS v FROM postings WHERE account = 'Assets:Brokerage:AAPL'")
	v := res.Rows[0][0]
	if v.Type() != types.Amount || v.Format() != "22 USD" {
		t.Errorf("value(position) = %v, want 22 USD", v)
	}
}

func TestValuePositionNoCostPassesThrough(t *testing.T) {
	l := priceLedger(t)
	// The cash position has no cost lot: value() returns its units unchanged.
	res := mustQuery(t, l,
		"SELECT value(position) AS v FROM postings WHERE account = 'Assets:Cash'")
	if got := res.Rows[0][0].Format(); got != "-20 USD" {
		t.Errorf("value(no-cost position) = %s, want -20 USD", got)
	}
}

func TestValueWithDateUsesEarlierPrice(t *testing.T) {
	l := priceLedger(t)
	// As of 2021-02-01 the AAPL market price is 9, so 2 AAPL value 18 USD.
	res := mustQuery(t, l,
		"SELECT value(position, 2021-02-01) AS v FROM postings WHERE account = 'Assets:Brokerage:AAPL'")
	if got := res.Rows[0][0].Format(); got != "18 USD" {
		t.Errorf("value(position, 2021-02-01) = %s, want 18 USD", got)
	}
}

// TestConcurrentPriceContext proves Decision 6 for price functions: one
// *Compiled carrying one shared, lazily-built price context, run from many
// goroutines, reads the map race-free and yields identical results. Run under
// `go test -race`.
func TestConcurrentPriceContext(t *testing.T) {
	l := priceLedger(t)
	c, err := query.Compile(
		"SELECT convert(position, 'USD') AS v FROM postings WHERE account = 'Assets:Brokerage:AAPL'", l)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	const goroutines = 32
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.Run(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if got := res.Rows[0][0].Format(); got != "22 USD" {
				t.Errorf("convert(position,'USD') = %s, want 22 USD", got)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Run: %v", err)
	}
}
