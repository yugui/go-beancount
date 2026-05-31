package directives_test

import (
	"sync"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/query/directives"
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func meta(pairs ...string) ast.Metadata {
	if len(pairs) == 0 {
		return ast.Metadata{}
	}
	props := make(map[string]ast.MetaValue, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		props[pairs[i]] = ast.MetaValue{Kind: ast.MetaString, String: pairs[i+1]}
	}
	return ast.Metadata{Props: props}
}

func newIndex(t *testing.T, dirs ...ast.Directive) *directives.Index {
	t.Helper()
	l := &ast.Ledger{}
	l.InsertAll(dirs)
	return directives.NewIndex(l, l.Options)
}

func parseOptions(t *testing.T, kv map[string]string) *ast.OptionValues {
	t.Helper()
	l := &ast.Ledger{}
	for k, v := range kv {
		l.Insert(&ast.Option{Key: k, Value: v})
	}
	opts, diags := ast.ParseOptions(l)
	for _, d := range diags {
		if d.Severity == ast.Error {
			t.Fatalf("ParseOptions: %s", d.Message)
		}
	}
	return opts
}

func TestOpenCloseHasAccount(t *testing.T) {
	idx := newIndex(t,
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash", Meta: meta("desc", "petty")},
		&ast.Close{Date: date(2022, 6, 30), Account: "Assets:Cash"},
		&ast.Open{Date: date(2020, 2, 1), Account: "Expenses:Food"},
	)

	if d, ok := idx.OpenDate("Assets:Cash"); !ok || !d.Equal(date(2020, 1, 1)) {
		t.Errorf("OpenDate(Assets:Cash) = %v, %v; want 2020-01-01, true", d, ok)
	}
	if d, ok := idx.CloseDate("Assets:Cash"); !ok || !d.Equal(date(2022, 6, 30)) {
		t.Errorf("CloseDate(Assets:Cash) = %v, %v; want 2022-06-30, true", d, ok)
	}
	if !idx.HasAccount("Assets:Cash") {
		t.Error("HasAccount(Assets:Cash) = false; want true")
	}

	// Open without Close: HasAccount true, CloseDate misses.
	if !idx.HasAccount("Expenses:Food") {
		t.Error("HasAccount(Expenses:Food) = false; want true")
	}
	if _, ok := idx.CloseDate("Expenses:Food"); ok {
		t.Error("CloseDate(Expenses:Food) found a close; want miss")
	}

	// Never-opened account misses everywhere.
	if _, ok := idx.OpenDate("Assets:Unknown"); ok {
		t.Error("OpenDate(Assets:Unknown) found an open; want miss")
	}
	if idx.HasAccount("Assets:Unknown") {
		t.Error("HasAccount(Assets:Unknown) = true; want false")
	}
}

func TestFirstOpenWins(t *testing.T) {
	idx := newIndex(t,
		&ast.Open{Date: date(2019, 5, 5), Account: "Assets:Cash", Meta: meta("which", "first")},
		&ast.Open{Date: date(2021, 9, 9), Account: "Assets:Cash", Meta: meta("which", "second")},
	)

	if d, ok := idx.OpenDate("Assets:Cash"); !ok || !d.Equal(date(2019, 5, 5)) {
		t.Errorf("OpenDate(Assets:Cash) = %v, %v; want 2019-05-05 (first open wins)", d, ok)
	}
	got, ok := idx.OpenMeta("Assets:Cash")
	if !ok {
		t.Fatal("OpenMeta(Assets:Cash) = _, false; want ok")
	}
	if v, _ := got.Get("which"); v.Format() != "first" {
		t.Errorf("OpenMeta(Assets:Cash)[which] = %q; want \"first\"", v.Format())
	}
}

func TestOpenMeta(t *testing.T) {
	idx := newIndex(t,
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash", Meta: meta("color", "green")},
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Bare"},
	)

	got, ok := idx.OpenMeta("Assets:Cash")
	if !ok {
		t.Fatal("OpenMeta(Assets:Cash) = _, false; want ok")
	}
	if v, has := got.Get("color"); !has || v.Format() != "green" {
		t.Errorf("OpenMeta(Assets:Cash)[color] = %q (has=%v); want \"green\"", v.Format(), has)
	}

	// Open exists but has no metadata: ok true, empty Dict.
	bare, ok := idx.OpenMeta("Assets:Bare")
	if !ok {
		t.Fatal("OpenMeta(Assets:Bare) = _, false; want ok")
	}
	if bare.Len() != 0 {
		t.Errorf("OpenMeta(Assets:Bare).Len() = %d; want 0", bare.Len())
	}

	// Miss: empty Dict and false.
	miss, ok := idx.OpenMeta("Assets:None")
	if ok {
		t.Error("OpenMeta(Assets:None) = _, true; want false")
	}
	if miss.Len() != 0 {
		t.Errorf("OpenMeta(Assets:None).Len() = %d; want 0", miss.Len())
	}
}

func TestCurrencyMeta(t *testing.T) {
	idx := newIndex(t,
		&ast.Commodity{Date: date(2020, 1, 1), Currency: "USD", Meta: meta("name", "US Dollar")},
	)

	got, ok := idx.CurrencyMeta("USD")
	if !ok {
		t.Fatal("CurrencyMeta(USD) = _, false; want ok")
	}
	if v, has := got.Get("name"); !has || v.Format() != "US Dollar" {
		t.Errorf("CurrencyMeta(USD)[name] = %q (has=%v); want \"US Dollar\"", v.Format(), has)
	}

	miss, ok := idx.CurrencyMeta("EUR")
	if ok {
		t.Error("CurrencyMeta(EUR) = _, true; want false")
	}
	if miss.Len() != 0 {
		t.Errorf("CurrencyMeta(EUR).Len() = %d; want 0", miss.Len())
	}
}

func TestSign(t *testing.T) {
	idx := directives.NewIndex(nil, nil)
	cases := []struct {
		acct ast.Account
		want int
	}{
		{"Assets:Cash", +1},
		{"Expenses:Food", +1},
		{"Liabilities:Card", -1},
		{"Equity:Opening", -1},
		{"Income:Salary", -1},
		{"Bogus:Root", 0},
	}
	for _, tc := range cases {
		if got := idx.Sign(tc.acct); got != tc.want {
			t.Errorf("Sign(%q) = %d; want %d", tc.acct, got, tc.want)
		}
	}
}

func TestSortKeyOrdering(t *testing.T) {
	idx := directives.NewIndex(nil, nil)

	// One representative per root in canonical order, plus an unknown root.
	accts := []ast.Account{
		"Assets:B",
		"Assets:A",
		"Liabilities:A",
		"Equity:A",
		"Income:A",
		"Expenses:A",
		"Zzz:A",
	}
	// Expected order: Assets:A, Assets:B, Liabilities, Equity, Income, Expenses, unknown last.
	want := []ast.Account{
		"Assets:A",
		"Assets:B",
		"Liabilities:A",
		"Equity:A",
		"Income:A",
		"Expenses:A",
		"Zzz:A",
	}

	keyOf := map[ast.Account]string{}
	for _, a := range accts {
		keyOf[a] = idx.SortKey(a)
	}

	for i := 0; i+1 < len(want); i++ {
		lo, hi := keyOf[want[i]], keyOf[want[i+1]]
		if !(lo < hi) {
			t.Errorf("SortKey ordering violated: %q (key %q) should sort before %q (key %q)",
				want[i], lo, want[i+1], hi)
		}
	}
}

func TestNilLedgerIndex(t *testing.T) {
	idx := directives.NewIndex(nil, nil)

	if _, ok := idx.OpenDate("Assets:Cash"); ok {
		t.Error("OpenDate on nil-ledger index found an open; want miss")
	}
	if idx.HasAccount("Assets:Cash") {
		t.Error("HasAccount on nil-ledger index = true; want false")
	}
	if _, ok := idx.OpenMeta("Assets:Cash"); ok {
		t.Error("OpenMeta on nil-ledger index = _, true; want false")
	}
	if _, ok := idx.CurrencyMeta("USD"); ok {
		t.Error("CurrencyMeta on nil-ledger index = _, true; want false")
	}

	// Account-type methods still work via option defaults.
	if got := idx.Sign("Assets:Cash"); got != +1 {
		t.Errorf("Sign(Assets:Cash) on nil-ledger index = %d; want +1", got)
	}
	if idx.SortKey("Assets:A") >= idx.SortKey("Liabilities:A") {
		t.Error("SortKey ordering broken on nil-ledger index")
	}
}

func TestCustomNameOptions(t *testing.T) {
	opts := parseOptions(t, map[string]string{
		"name_assets": "Aktiva",
		"name_income": "Ertrag",
	})
	idx := directives.NewIndex(&ast.Ledger{Options: opts}, opts)

	if got := idx.Sign("Aktiva:Cash"); got != +1 {
		t.Errorf("Sign(Aktiva:Cash) = %d; want +1 with custom name_assets", got)
	}
	if got := idx.Sign("Ertrag:Salary"); got != -1 {
		t.Errorf("Sign(Ertrag:Salary) = %d; want -1 with custom name_income", got)
	}
	// The default English root no longer classifies as Assets.
	if got := idx.Sign("Assets:Cash"); got != 0 {
		t.Errorf("Sign(Assets:Cash) = %d; want 0 (default root not in use)", got)
	}
	// Custom Assets sorts before custom Income.
	if idx.SortKey("Aktiva:A") >= idx.SortKey("Ertrag:A") {
		t.Error("SortKey: custom Assets should sort before custom Income")
	}
}

func TestConcurrentLazyBuild(t *testing.T) {
	// Concurrent first lookups must trigger the sync.Once build exactly once
	// and yield consistent results (Decision 6; see pkg/query/ARCHITECTURE.md
	// §4); run under -race to validate.
	idx := newIndex(t,
		&ast.Open{Date: date(2020, 1, 1), Account: "Assets:Cash"},
		&ast.Close{Date: date(2022, 1, 1), Account: "Assets:Cash"},
	)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !idx.HasAccount("Assets:Cash") {
				t.Error("HasAccount(Assets:Cash) = false; want true")
			}
			if _, ok := idx.CloseDate("Assets:Cash"); !ok {
				t.Error("CloseDate(Assets:Cash) missed; want hit")
			}
			if idx.Sign("Assets:Cash") != +1 {
				t.Error("Sign(Assets:Cash) != +1")
			}
		}()
	}
	wg.Wait()
}
