package price_test

import (
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/price"
)

func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("apd.NewFromString(%q): %v", s, err)
	}
	return *d
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func priced(t *testing.T) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Price{Date: date(2021, 1, 1), Commodity: "AAPL", Amount: ast.Amount{Number: dec(t, "9"), Currency: "USD"}},
		&ast.Price{Date: date(2021, 3, 15), Commodity: "AAPL", Amount: ast.Amount{Number: dec(t, "11"), Currency: "USD"}},
		&ast.Price{Date: date(2021, 3, 10), Commodity: "EUR", Amount: ast.Amount{Number: dec(t, "1.25"), Currency: "USD"}},
	})
	return l
}

func TestGetNearestOnOrBefore(t *testing.T) {
	m := price.NewMap(priced(t))

	r, ok := m.Get("AAPL", "USD", date(2021, 2, 1))
	if !ok || r.Text('f') != "9" {
		t.Errorf("Get on/before 2021-02-01 = %v (ok=%v), want 9", r.Text('f'), ok)
	}
	r, ok = m.Get("AAPL", "USD", date(2021, 3, 15))
	if !ok || r.Text('f') != "11" {
		t.Errorf("Get on 2021-03-15 = %v (ok=%v), want 11", r.Text('f'), ok)
	}
}

func TestGetBeforeAllMisses(t *testing.T) {
	m := price.NewMap(priced(t))
	if _, ok := m.Get("AAPL", "USD", date(2020, 12, 31)); ok {
		t.Error("Get before earliest price unexpectedly found a rate")
	}
}

func TestLatest(t *testing.T) {
	m := price.NewMap(priced(t))
	r, ok := m.Latest("AAPL", "USD")
	if !ok || r.Text('f') != "11" {
		t.Errorf("Latest = %v (ok=%v), want 11", r.Text('f'), ok)
	}
}

func TestInverseRate(t *testing.T) {
	m := price.NewMap(priced(t))
	// Only EUR→USD (1.25) recorded; USD→EUR is the one-hop inverse 1/1.25 = 0.8.
	r, ok := m.Latest("USD", "EUR")
	if !ok || r.Text('f') != "0.8" {
		t.Errorf("inverse Latest USD→EUR = %v (ok=%v), want 0.8", r.Text('f'), ok)
	}
	r, ok = m.Get("USD", "EUR", date(2021, 3, 12))
	if !ok || r.Text('f') != "0.8" {
		t.Errorf("inverse Get USD→EUR = %v (ok=%v), want 0.8", r.Text('f'), ok)
	}
}

func TestSameCurrencyIsOne(t *testing.T) {
	m := price.NewMap(priced(t))
	r, ok := m.Latest("USD", "USD")
	if !ok || r.Text('f') != "1" {
		t.Errorf("Latest USD→USD = %v (ok=%v), want 1", r.Text('f'), ok)
	}
}

func TestMissingPairMisses(t *testing.T) {
	m := price.NewMap(priced(t))
	if _, ok := m.Latest("AAPL", "JPY"); ok {
		t.Error("Latest for an unreachable pair unexpectedly found a rate")
	}
}

func TestNilLedgerMisses(t *testing.T) {
	m := price.NewMap(nil)
	if _, ok := m.Latest("AAPL", "USD"); ok {
		t.Error("Latest over a nil ledger unexpectedly found a rate")
	}
}
