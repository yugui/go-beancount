package std_test

import (
	"context"
	"sync"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query"
	"github.com/yugui/go-beancount/pkg/query/types"
)

// directiveLedger builds a ledger with Open/Close/Commodity directives
// carrying metadata, plus a few postings for the query tables.
func directiveLedger(t *testing.T) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		// Assets:Cash — open, never closed; no metadata
		&ast.Open{
			Date:    date(2020, 1, 1),
			Account: "Assets:Cash",
		},
		// Income:Salary — open+closed, with meta
		&ast.Open{
			Date:    date(2020, 1, 1),
			Account: "Income:Salary",
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"category": {Kind: ast.MetaString, String: "employment"},
			}},
		},
		&ast.Close{
			Date:    date(2023, 12, 31),
			Account: "Income:Salary",
		},
		// Commodity directive for USD
		&ast.Commodity{
			Date:     date(2020, 1, 1),
			Currency: "USD",
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"name": {Kind: ast.MetaString, String: "US Dollar"},
				"rate": {Kind: ast.MetaNumber, Number: dec(t, "1")},
			}},
		},
		// Transactions to give the tables rows to iterate over
		&ast.Transaction{
			Date: date(2022, 6, 15),
			Flag: '*',
			Postings: []ast.Posting{
				{
					Account: "Assets:Cash",
					Amount:  &ast.Amount{Number: dec(t, "1000"), Currency: "USD"},
				},
				{
					Account: "Income:Salary",
					Amount:  &ast.Amount{Number: dec(t, "-1000"), Currency: "USD"},
				},
			},
		},
	})
	return l
}

// --- open_date / close_date ---

func TestOpenDate(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT open_date('Assets:Cash') AS d FROM postings LIMIT 1")
	v := res.Rows[0][0]
	if v.Type() != types.Date {
		t.Errorf("open_date type = %s, want Date", v.Type())
	}
	if v.Format() != "2020-01-01" {
		t.Errorf("open_date = %v, want 2020-01-01", v)
	}
}

func TestOpenDateMissIsNull(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT open_date('Expenses:Unknown') AS d FROM postings LIMIT 1")
	v := res.Rows[0][0]
	if !v.IsNull() {
		t.Errorf("open_date(unknown) = %v, want NULL", v)
	}
	if v.Type() != types.Date {
		t.Errorf("open_date(unknown) type = %s, want Date", v.Type())
	}
}

func TestCloseDate(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT close_date('Income:Salary') AS d FROM postings LIMIT 1")
	v := res.Rows[0][0]
	if v.Type() != types.Date {
		t.Errorf("close_date type = %s, want Date", v.Type())
	}
	if v.Format() != "2023-12-31" {
		t.Errorf("close_date = %v, want 2023-12-31", v)
	}
}

func TestCloseDateNeverClosedIsNull(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT close_date('Assets:Cash') AS d FROM postings LIMIT 1")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("close_date(open-only) = %v, want NULL", res.Rows[0][0])
	}
}

// --- open_meta ---

func TestOpenMetaDict(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT open_meta('Income:Salary') AS m FROM postings LIMIT 1")
	v := res.Rows[0][0]
	if v.Type() != types.DictType || v.IsNull() {
		t.Fatalf("open_meta dict = %v (type %s), want a Dict", v, v.Type())
	}
	d, ok := types.AsDict(v)
	if !ok {
		t.Fatalf("AsDict failed for %v", v)
	}
	val, found := d.Get("category")
	if !found {
		t.Fatalf("open_meta dict missing key 'category'")
	}
	checkStr(t, val, "employment")
}

func TestOpenMetaDictMissIsNull(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT open_meta('Expenses:Unknown') AS m FROM postings LIMIT 1")
	v := res.Rows[0][0]
	if !v.IsNull() {
		t.Errorf("open_meta(unknown) dict = %v, want NULL", v)
	}
	if v.Type() != types.DictType {
		t.Errorf("open_meta(unknown) type = %s, want DictType", v.Type())
	}
}

func TestOpenMetaKey(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT open_meta('Income:Salary', 'category') AS v FROM postings LIMIT 1")
	checkStr(t, res.Rows[0][0], "employment")
}

func TestOpenMetaKeyMissingKeyIsNull(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT open_meta('Income:Salary', 'nonexistent') AS v FROM postings LIMIT 1")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("open_meta(key=missing) = %v, want NULL", res.Rows[0][0])
	}
}

func TestOpenMetaKeyMissingAccountIsNull(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT open_meta('Expenses:Unknown', 'category') AS v FROM postings LIMIT 1")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("open_meta(unknown acct, key) = %v, want NULL", res.Rows[0][0])
	}
}

// --- currency_meta / commodity_meta ---

func TestCurrencyMetaDict(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT currency_meta('USD') AS m FROM postings LIMIT 1")
	v := res.Rows[0][0]
	if v.Type() != types.DictType || v.IsNull() {
		t.Fatalf("currency_meta dict = %v, want Dict", v)
	}
	d, ok := types.AsDict(v)
	if !ok {
		t.Fatalf("AsDict failed for %v", v)
	}
	val, found := d.Get("name")
	if !found {
		t.Fatalf("currency_meta dict missing key 'name'")
	}
	checkStr(t, val, "US Dollar")
}

func TestCurrencyMetaKey(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT currency_meta('USD', 'name') AS v FROM postings LIMIT 1")
	checkStr(t, res.Rows[0][0], "US Dollar")
}

func TestCurrencyMetaDictMissIsNull(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT currency_meta('JPY') AS m FROM postings LIMIT 1")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("currency_meta(unknown) = %v, want NULL", res.Rows[0][0])
	}
}

func TestCurrencyMetaKeyMissingKey(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT currency_meta('USD', 'nonexistent') AS v FROM postings LIMIT 1")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("currency_meta(USD, nonexistent) = %v, want NULL", res.Rows[0][0])
	}
}

func TestCurrencyMetaKeyMissingCurrency(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT currency_meta('JPY', 'name') AS v FROM postings LIMIT 1")
	if !res.Rows[0][0].IsNull() {
		t.Errorf("currency_meta(JPY, name) = %v, want NULL", res.Rows[0][0])
	}
}

// commodity_meta is an alias of currency_meta: same overloads, same results.

func TestCommodityMetaParityWithCurrencyMeta(t *testing.T) {
	l := directiveLedger(t)
	r1 := mustQuery(t, l, "SELECT currency_meta('USD') AS m FROM postings LIMIT 1")
	r2 := mustQuery(t, l, "SELECT commodity_meta('USD') AS m FROM postings LIMIT 1")
	if r1.Rows[0][0].Format() != r2.Rows[0][0].Format() {
		t.Errorf("commodity_meta != currency_meta: %v vs %v",
			r2.Rows[0][0], r1.Rows[0][0])
	}
}

func TestCommodityMetaKey(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT commodity_meta('USD', 'name') AS v FROM postings LIMIT 1")
	checkStr(t, res.Rows[0][0], "US Dollar")
}

// --- account_sortkey ---

func TestAccountSortkeyOrdering(t *testing.T) {
	l := directiveLedger(t)
	// Assets sorts before Income; ORDER BY account_sortkey should put
	// the Assets:Cash posting first regardless of insertion order.
	res := mustQuery(t, l,
		"SELECT account, account_sortkey(account) AS sk FROM postings "+
			"ORDER BY account_sortkey(account)")
	if len(res.Rows) < 2 {
		t.Fatalf("expected at least 2 rows, got %d", len(res.Rows))
	}
	acctCol := column(t, res, "account")
	first := res.Rows[0][acctCol].Format()
	if first != "Assets:Cash" {
		t.Errorf("first row by sortkey = %q, want Assets:Cash (Assets < Income)", first)
	}
}

func TestAccountSortkeyUnknownAccountNonNull(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT account_sortkey('Weird:Unknown') AS sk FROM postings LIMIT 1")
	// Unknown root still returns a non-null string (sorts after known roots).
	v := res.Rows[0][0]
	if v.IsNull() {
		t.Errorf("account_sortkey(unknown root) = NULL, want non-null string")
	}
}

// --- has_account ---

func TestHasAccountTrue(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT has_account('Assets:Cash') AS h FROM postings LIMIT 1")
	b, ok := types.AsBool(res.Rows[0][0])
	if !ok || !b {
		t.Errorf("has_account(existing) = %v, want true", res.Rows[0][0])
	}
}

func TestHasAccountFalse(t *testing.T) {
	l := directiveLedger(t)
	res := mustQuery(t, l,
		"SELECT has_account('Expenses:Unknown') AS h FROM postings LIMIT 1")
	b, ok := types.AsBool(res.Rows[0][0])
	if !ok || b {
		t.Errorf("has_account(nonexistent) = %v, want false (not NULL)", res.Rows[0][0])
	}
}

// --- possign ---

// possignLedger is like directiveLedger but ensures both an Assets and an
// Income account appear in the same query, with non-trivial amounts.
func possignLedger(t *testing.T) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Savings"},
		&ast.Open{Date: date(2020, 1, 1), Account: "Income:Wages"},
		&ast.Commodity{
			Date:     date(2020, 1, 1),
			Currency: "USD",
		},
		&ast.Transaction{
			Date: date(2022, 1, 1),
			Flag: '*',
			Postings: []ast.Posting{
				{
					Account: "Assets:Savings",
					Amount:  &ast.Amount{Number: dec(t, "500"), Currency: "USD"},
				},
				{
					Account: "Income:Wages",
					Amount:  &ast.Amount{Number: dec(t, "-500"), Currency: "USD"},
				},
			},
		},
	})
	return l
}

func TestPossignDecimalAssetsUnchanged(t *testing.T) {
	l := possignLedger(t)
	// Assets has sign +1: possign leaves the decimal unchanged.
	res := mustQuery(t, l,
		"SELECT possign(number(weight), account) AS v FROM postings "+
			"WHERE account = 'Assets:Savings'")
	if got := res.Rows[0][0].Format(); got != "500" {
		t.Errorf("possign(500 Assets) = %s, want 500 (unchanged)", got)
	}
}

func TestPossignDecimalIncomeNegated(t *testing.T) {
	l := possignLedger(t)
	// Income has sign -1: possign negates the number, making it positive.
	res := mustQuery(t, l,
		"SELECT possign(number(weight), account) AS v FROM postings "+
			"WHERE account = 'Income:Wages'")
	if got := res.Rows[0][0].Format(); got != "500" {
		t.Errorf("possign(-500 Income) = %s, want 500 (negated)", got)
	}
}

func TestPossignAmountIncomeNegated(t *testing.T) {
	l := possignLedger(t)
	res := mustQuery(t, l,
		"SELECT possign(weight, account) AS v FROM postings "+
			"WHERE account = 'Income:Wages'")
	if got := res.Rows[0][0].Format(); got != "500 USD" {
		t.Errorf("possign(weight Income) = %s, want 500 USD", got)
	}
}

func TestPossignAmountAssetsUnchanged(t *testing.T) {
	l := possignLedger(t)
	res := mustQuery(t, l,
		"SELECT possign(weight, account) AS v FROM postings "+
			"WHERE account = 'Assets:Savings'")
	if got := res.Rows[0][0].Format(); got != "500 USD" {
		t.Errorf("possign(weight Assets) = %s, want 500 USD unchanged", got)
	}
}

func TestPossignInventoryIncomeNegated(t *testing.T) {
	l := possignLedger(t)
	// Single-posting account: balance == weight == -500 USD for Income:Wages.
	res := mustQuery(t, l,
		"SELECT possign(balance, account) AS v FROM postings "+
			"WHERE account = 'Income:Wages'")
	if got := res.Rows[0][0].Format(); got != "(500 USD)" {
		t.Errorf("possign(balance Income) = %s, want (500 USD)", got)
	}
}

func TestPossignInventoryAssetsPassThrough(t *testing.T) {
	l := possignLedger(t)
	// Assets has sign +1: possign leaves the inventory unchanged.
	res := mustQuery(t, l,
		"SELECT possign(balance, account) AS v FROM postings "+
			"WHERE account = 'Assets:Savings'")
	if got := res.Rows[0][0].Format(); got != "(500 USD)" {
		t.Errorf("possign(balance Assets) = %s, want (500 USD)", got)
	}
}

// TestPossignPositionIncomeNegated verifies the Position overload when the
// account is Income-typed (sign -1): the units number is negated, cost preserved.
func TestPossignPositionIncomeNegated(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Brokerage"},
		&ast.Open{Date: date(2020, 1, 1), Account: "Income:Gain"},
		&ast.Transaction{
			Date: date(2022, 1, 1),
			Flag: '*',
			Postings: []ast.Posting{
				{
					Account: "Assets:Brokerage",
					Amount:  &ast.Amount{Number: dec(t, "2"), Currency: "AAPL"},
					Cost:    &ast.Cost{Number: dec(t, "10"), Currency: "USD"},
				},
				{
					Account: "Income:Gain",
					Amount:  &ast.Amount{Number: dec(t, "-20"), Currency: "USD"},
				},
			},
		},
	})
	res := mustQuery(t, l,
		"SELECT possign(position, account) AS v FROM postings "+
			"WHERE account = 'Income:Gain'")
	if got := res.Rows[0][0].Format(); got != "20 USD" {
		t.Errorf("possign(position Income) = %s, want 20 USD", got)
	}
}

// TestPossignPositionAssetsPassThrough verifies the Position overload when the
// account is Assets-typed (sign +1): the position is returned unchanged.
func TestPossignPositionAssetsPassThrough(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Brokerage"},
		&ast.Open{Date: date(2020, 1, 1), Account: "Income:Gain"},
		&ast.Transaction{
			Date: date(2022, 1, 1),
			Flag: '*',
			Postings: []ast.Posting{
				{
					Account: "Assets:Brokerage",
					Amount:  &ast.Amount{Number: dec(t, "2"), Currency: "AAPL"},
					Cost:    &ast.Cost{Number: dec(t, "10"), Currency: "USD"},
				},
				{
					Account: "Income:Gain",
					Amount:  &ast.Amount{Number: dec(t, "-20"), Currency: "USD"},
				},
			},
		},
	})
	res := mustQuery(t, l,
		"SELECT possign(position, account) AS v FROM postings "+
			"WHERE account = 'Assets:Brokerage'")
	if got := res.Rows[0][0].Format(); got != "2 AAPL {10 USD}" {
		t.Errorf("possign(position Assets) = %s, want 2 AAPL {10 USD} (unchanged)", got)
	}
}

// TestPossignUnknownRootPassesThrough verifies sign==0 (unknown root) returns
// the value unchanged (not NULL).
func TestPossignUnknownRootPassesThrough(t *testing.T) {
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash"},
		&ast.Transaction{
			Date: date(2022, 1, 1),
			Flag: '*',
			Postings: []ast.Posting{
				{Account: "Assets:Cash", Amount: &ast.Amount{Number: dec(t, "100"), Currency: "USD"}},
			},
		},
	})
	// "Unusual:Account" has no Open, so Sign returns 0 (unknown root).
	// possign should pass the value through unchanged.
	res := mustQuery(t, l,
		"SELECT possign(number(weight), 'Unusual:Account') AS v FROM postings LIMIT 1")
	if got := res.Rows[0][0].Format(); got != "100" {
		t.Errorf("possign(sign=0) = %s, want 100 (pass-through)", got)
	}
}

// TestConcurrentDirectiveContext proves Decision 6 for directive-context
// functions: a shared *Compiled carrying one directive Index, run from
// many goroutines, is race-free and yields identical results.
func TestConcurrentDirectiveContext(t *testing.T) {
	l := directiveLedger(t)
	c, err := query.Compile(
		"SELECT open_date('Assets:Cash') AS d, "+
			"has_account('Income:Salary') AS h, "+
			"possign(number(weight), account) AS v "+
			"FROM postings WHERE account = 'Assets:Cash'", l)
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
			if got := res.Rows[0][0].Format(); got != "2020-01-01" {
				t.Errorf("concurrent open_date = %s, want 2020-01-01", got)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Run: %v", err)
	}
}
