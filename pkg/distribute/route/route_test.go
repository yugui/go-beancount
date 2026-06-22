package route

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/yugui/go-beancount/internal/formatopt"
	"github.com/yugui/go-beancount/pkg/ast"
)

// decisionCmp ignores the opaque Format closures (which cmp cannot
// equate) and instead compares the resolved spacing fields and
// Path/Order/DateWindowDays directly.
var decisionCmp = cmpopts.IgnoreFields(Decision{}, "Format")

// ptrInt returns a pointer to n. It mirrors the *int shape used by
// DateWindowDays to distinguish "absent" from an explicit value.
func ptrInt(n int) *int { return &n }

// defaultDecision returns a Decision with the resolved spacing fields
// set to formatopt.Default()'s values, the same defaults Decide returns
// when no override and no [routes.format] section apply.
func defaultDecision(path string) Decision {
	d := formatopt.Default()
	return Decision{
		Path:                              path,
		Order:                             OrderAscending,
		BlankLinesBetweenDirectives:       d.BlankLinesBetweenDirectives,
		InsertBlankLinesBetweenDirectives: d.InsertBlankLinesBetweenDirectives,
	}
}

// jan15 is the canonical date used across these tests. The chosen
// month-day combination exercises the zero-padding in the YYYYmm
// formatter (single-digit month).
var jan15 = time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC)

func TestDecide_AccountKeyedDirectives(t *testing.T) {
	acct := ast.Assets.MustSub("Bank", "Checking")
	const wantPath = "transactions/Assets/Bank/Checking/202401.beancount"

	cases := []struct {
		name string
		d    ast.Directive
	}{
		{"Open", &ast.Open{Date: jan15, Account: acct}},
		{"Close", &ast.Close{Date: jan15, Account: acct}},
		{"Balance", &ast.Balance{Date: jan15, Account: acct, Amount: ast.Amount{}}},
		{"Note", &ast.Note{Date: jan15, Account: acct, Comment: "x"}},
		{"Document", &ast.Document{Date: jan15, Account: acct, Path: "/x"}},
		{"Pad", &ast.Pad{Date: jan15, Account: acct, PadAccount: ast.Equity.MustSub("Opening")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decide(tc.d, nil)
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			want := defaultDecision(wantPath)
			if diff := cmp.Diff(want, got, decisionCmp); diff != "" {
				t.Errorf("Decision mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDecide_Transaction(t *testing.T) {
	t.Run("UsesFirstPostingAccount", func(t *testing.T) {
		txn := &ast.Transaction{
			Date: jan15,
			Flag: '*',
			Postings: []ast.Posting{
				{Account: ast.Expenses.MustSub("Food")},
				{Account: ast.Assets.MustSub("Cash")},
			},
		}
		got, err := Decide(txn, nil)
		if err != nil {
			t.Fatalf("Decide returned error: %v", err)
		}
		want := defaultDecision("transactions/Expenses/Food/202401.beancount")
		want.StripMetaKeys = []string{defaultOverrideMetaKey}
		if diff := cmp.Diff(want, got, decisionCmp); diff != "" {
			t.Errorf("Decision mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("EmptyPostingsErrors", func(t *testing.T) {
		txn := &ast.Transaction{Date: jan15, Flag: '*'}
		if _, err := Decide(txn, nil); err == nil {
			t.Fatal("Decide on transaction with no postings: got nil error, want error")
		}
	})
}

// makeAmount returns a pointer to an Amount with the given sign
// (positive n → debit, negative n → credit; n==0 → zero).
func makeAmount(n int, cur string) *ast.Amount {
	var d apd.Decimal
	if n > 0 {
		d.SetInt64(int64(n))
	} else if n < 0 {
		d.SetInt64(int64(-n))
		d.Negative = true
	}
	return &ast.Amount{Number: d, Currency: cur}
}

// warnSink captures warnings emitted by cfg.Warn.
type warnSink struct {
	msgs []string
}

func (w *warnSink) fn(format string, args ...any) {
	w.msgs = append(w.msgs, fmt.Sprintf(format, args...))
}

func TestDecide_Transaction_Override(t *testing.T) {
	const key = defaultOverrideMetaKey

	food := ast.Expenses.MustSub("Food")
	cash := ast.Assets.MustSub("Cash")
	income := ast.Income.MustSub("Salary")

	// strMeta returns a Metadata with a single MetaString entry.
	strMeta := func(k, v string) ast.Metadata {
		return ast.Metadata{Props: map[string]ast.MetaValue{
			k: {Kind: ast.MetaString, String: v},
		}}
	}
	// boolMeta returns a Metadata with a single MetaBool entry.
	boolMeta := func(k string, v bool) ast.Metadata {
		return ast.Metadata{Props: map[string]ast.MetaValue{
			k: {Kind: ast.MetaBool, Bool: v},
		}}
	}
	// numMeta returns a Metadata with a MetaNumber entry (wrong kind for route-account).
	numMeta := func(k string) ast.Metadata {
		return ast.Metadata{Props: map[string]ast.MetaValue{
			k: {Kind: ast.MetaNumber},
		}}
	}

	cases := []struct {
		name         string
		txn          *ast.Transaction
		cfg          *Config
		wantAccount  ast.Account
		warnContains []string // substrings each warning must contain
	}{
		{
			name: "TxnLevelStringWins",
			txn: &ast.Transaction{
				Date:     jan15,
				Flag:     '*',
				Meta:     strMeta(key, "Assets:Cash"),
				Postings: []ast.Posting{{Account: food}},
			},
			wantAccount: cash,
		},
		{
			name: "PostingTRUEWins",
			txn: &ast.Transaction{
				Date: jan15,
				Flag: '*',
				Postings: []ast.Posting{
					{Account: food},
					{Account: cash, Meta: boolMeta(key, true)},
				},
			},
			wantAccount: cash,
		},
		{
			name: "PostingFALSEIgnored",
			// FALSE on posting[0] does not select it; falls through to fallback.
			txn: &ast.Transaction{
				Date: jan15,
				Flag: '*',
				Postings: []ast.Posting{
					{Account: food, Meta: boolMeta(key, false)},
					{Account: cash},
				},
			},
			wantAccount: food, // rule 4: Postings[0]
		},
		{
			name: "MultipleTRUEFirstWins",
			txn: &ast.Transaction{
				Date: jan15,
				Flag: '*',
				Postings: []ast.Posting{
					{Account: food, Meta: boolMeta(key, true)},
					{Account: cash, Meta: boolMeta(key, true)},
				},
			},
			wantAccount: food,
		},
		{
			name: "Strategy_FirstPosting",
			txn: &ast.Transaction{
				Date:     jan15,
				Flag:     '*',
				Postings: []ast.Posting{{Account: food}, {Account: cash}},
			},
			cfg: &Config{Routes: Routes{Transaction: TransactionSection{
				DefaultStrategy: "first-posting",
			}}},
			wantAccount: food,
		},
		{
			name: "Strategy_LastPosting",
			txn: &ast.Transaction{
				Date:     jan15,
				Flag:     '*',
				Postings: []ast.Posting{{Account: food}, {Account: cash}},
			},
			cfg: &Config{Routes: Routes{Transaction: TransactionSection{
				DefaultStrategy: "last-posting",
			}}},
			wantAccount: cash,
		},
		{
			name: "Strategy_FirstDebit",
			txn: &ast.Transaction{
				Date: jan15,
				Flag: '*',
				Postings: []ast.Posting{
					{Account: income, Amount: makeAmount(-100, "USD")},
					{Account: food, Amount: makeAmount(100, "USD")},
				},
			},
			cfg: &Config{Routes: Routes{Transaction: TransactionSection{
				DefaultStrategy: "first-debit",
			}}},
			wantAccount: food,
		},
		{
			name: "Strategy_FirstCredit",
			txn: &ast.Transaction{
				Date: jan15,
				Flag: '*',
				Postings: []ast.Posting{
					{Account: food, Amount: makeAmount(100, "USD")},
					{Account: income, Amount: makeAmount(-100, "USD")},
				},
			},
			cfg: &Config{Routes: Routes{Transaction: TransactionSection{
				DefaultStrategy: "first-credit",
			}}},
			wantAccount: income,
		},
		{
			name: "Strategy_FirstDebit_SkipsAutoPosting",
			// Auto-posting (nil Amount) must be skipped; the second posting is the first debit.
			txn: &ast.Transaction{
				Date: jan15,
				Flag: '*',
				Postings: []ast.Posting{
					{Account: income, Amount: nil},                  // auto-posting, skipped
					{Account: food, Amount: makeAmount(100, "USD")}, // debit
				},
			},
			cfg: &Config{Routes: Routes{Transaction: TransactionSection{
				DefaultStrategy: "first-debit",
			}}},
			wantAccount: food,
		},
		{
			name: "Strategy_FirstCredit_SkipsAutoPosting",
			// Auto-posting (nil Amount) must be skipped; the second posting is the first credit.
			txn: &ast.Transaction{
				Date: jan15,
				Flag: '*',
				Postings: []ast.Posting{
					{Account: food, Amount: nil},                       // auto-posting, skipped
					{Account: income, Amount: makeAmount(-100, "USD")}, // credit
				},
			},
			cfg: &Config{Routes: Routes{Transaction: TransactionSection{
				DefaultStrategy: "first-credit",
			}}},
			wantAccount: income,
		},
		{
			name: "Strategy_FirstDebit_NoMatch_FallsThrough",
			// All postings are credits (negative); first-debit finds nothing → fallback.
			txn: &ast.Transaction{
				Date: jan15,
				Flag: '*',
				Postings: []ast.Posting{
					{Account: income, Amount: makeAmount(-100, "USD")},
					{Account: food, Amount: makeAmount(-50, "USD")},
				},
			},
			cfg: &Config{Routes: Routes{Transaction: TransactionSection{
				DefaultStrategy: "first-debit",
			}}},
			wantAccount: income, // rule 4: Postings[0]
		},
		{
			name: "MalformedTxnValue_NotString",
			// txn meta key is MetaNumber instead of MetaString → warn, fall through.
			txn: &ast.Transaction{
				Date:     jan15,
				Flag:     '*',
				Meta:     numMeta(key),
				Postings: []ast.Posting{{Account: cash}},
			},
			wantAccount:  cash,
			warnContains: []string{key},
		},
		{
			name: "MalformedTxnValue_InvalidAccount",
			// txn meta key is MetaString but value fails IsValid.
			txn: &ast.Transaction{
				Date:     jan15,
				Flag:     '*',
				Meta:     strMeta(key, "not-an-account"),
				Postings: []ast.Posting{{Account: cash}},
			},
			wantAccount:  cash,
			warnContains: []string{key, "not-an-account"},
		},
		{
			name: "MalformedTxnValue_EmptyString",
			// txn meta key is MetaString with empty value.
			txn: &ast.Transaction{
				Date:     jan15,
				Flag:     '*',
				Meta:     strMeta(key, ""),
				Postings: []ast.Posting{{Account: cash}},
			},
			wantAccount:  cash,
			warnContains: []string{key},
		},
		{
			name: "MalformedPostingValue_WrongKind",
			// posting meta key is MetaNumber instead of MetaBool → warn, fall through to rule 4.
			txn: &ast.Transaction{
				Date: jan15,
				Flag: '*',
				Postings: []ast.Posting{
					{Account: food, Meta: numMeta(key)},
					{Account: cash},
				},
			},
			wantAccount:  food, // rule 4: Postings[0]
			warnContains: []string{key},
		},
		{
			name: "StripMetaKeysAlwaysSet",
			// Custom OverrideMetaKey should appear in StripMetaKeys.
			txn: &ast.Transaction{
				Date:     jan15,
				Flag:     '*',
				Postings: []ast.Posting{{Account: food}},
			},
			cfg: &Config{Routes: Routes{Transaction: TransactionSection{
				OverrideMetaKey: "my-route",
			}}},
			wantAccount: food,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ws warnSink
			// Shallow-copy the Config so that assigning Warn does not mutate
			// the shared tc.cfg pointer, which would bleed across table iterations.
			var cfgCopy Config
			if tc.cfg != nil {
				cfgCopy = *tc.cfg
			}
			cfg := &cfgCopy
			cfg.Warn = ws.fn

			got, err := Decide(tc.txn, cfg)
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}

			// Determine the expected override key.
			overrideKey := cfg.Routes.Transaction.OverrideMetaKey
			if overrideKey == "" {
				overrideKey = defaultOverrideMetaKey
			}

			// StripMetaKeys must always be set to the override key.
			if diff := cmp.Diff([]string{overrideKey}, got.StripMetaKeys); diff != "" {
				t.Errorf("StripMetaKeys mismatch (-want +got):\n%s", diff)
			}

			// Path must correspond to tc.wantAccount.
			wantPath := "transactions/" + strings.Join(tc.wantAccount.Parts(), "/") + "/202401.beancount"
			if got.Path != wantPath {
				t.Errorf("Path = %q, want %q (account %v)", got.Path, wantPath, tc.wantAccount)
			}

			// Validate warnings.
			for _, sub := range tc.warnContains {
				found := false
				for _, msg := range ws.msgs {
					if strings.Contains(msg, sub) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("want warning containing %q; warnings: %v", sub, ws.msgs)
				}
			}
			if len(tc.warnContains) == 0 && len(ws.msgs) > 0 {
				t.Errorf("unexpected warnings: %v", ws.msgs)
			}
		})
	}
}

func TestDecide_Price(t *testing.T) {
	d := &ast.Price{Date: jan15, Commodity: "JPY"}
	got, err := Decide(d, nil)
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	want := defaultDecision("quotes/JPY/202401.beancount")
	if diff := cmp.Diff(want, got, decisionCmp); diff != "" {
		t.Errorf("Decision mismatch (-want +got):\n%s", diff)
	}
}

func TestDecide_PassThrough(t *testing.T) {
	cases := []struct {
		name string
		d    ast.Directive
	}{
		{"Option", &ast.Option{Key: "title", Value: "x"}},
		{"Plugin", &ast.Plugin{Name: "p"}},
		{"Include", &ast.Include{Path: "x.beancount"}},
		{"Event", &ast.Event{Date: jan15, Name: "loc", Value: "Tokyo"}},
		{"Query", &ast.Query{Date: jan15, Name: "q", BQL: "SELECT 1"}},
		{"Custom", &ast.Custom{Date: jan15, TypeName: "x"}},
		{"Commodity", &ast.Commodity{Date: jan15, Currency: "USD"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decide(tc.d, nil)
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			want := Decision{PassThrough: true}
			if diff := cmp.Diff(want, got, decisionCmp); diff != "" {
				t.Errorf("Decision mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDecide_HierarchicalAccountFlattens(t *testing.T) {
	acct := ast.Assets.MustSub("Foo", "Bar", "Baz")
	d := &ast.Open{Date: jan15, Account: acct}
	got, err := Decide(d, nil)
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	const want = "transactions/Assets/Foo/Bar/Baz/202401.beancount"
	if got.Path != want {
		t.Errorf("Decide(Open %v).Path = %q, want %q", acct, got.Path, want)
	}
}

func TestDecide_DateFormats(t *testing.T) {
	// Each row exercises both the account/Open and price/Price paths with
	// FilePattern set on the relevant section.
	cases := []struct {
		name    string
		pattern string
		date    time.Time
		want    string // expected date suffix in the path
	}{
		// YYYY: year only
		{"YYYY/January", "YYYY", time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC), "2024"},
		{"YYYY/December", "YYYY", time.Date(2024, time.December, 31, 0, 0, 0, 0, time.UTC), "2024"},
		// 2024-01-01 00:00:00 JST is 2023-12-31 15:00:00 UTC; Year() must
		// read the local calendar field, not UTC.
		{"YYYY/OtherTimezone", "YYYY", time.Date(2024, time.January, 1, 0, 0, 0, 0, time.FixedZone("JST", 9*3600)), "2024"},

		// YYYYmm: year + month (explicit)
		{"YYYYmm/January", "YYYYmm", time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC), "202401"},
		{"YYYYmm/December", "YYYYmm", time.Date(2024, time.December, 31, 0, 0, 0, 0, time.UTC), "202412"},
		{"YYYYmm/OtherTimezone", "YYYYmm", time.Date(2024, time.January, 1, 0, 0, 0, 0, time.FixedZone("JST", 9*3600)), "202401"},

		// YYYYmm: empty string defaults to YYYYmm
		{"default/January", "", time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC), "202401"},
		{"default/December", "", time.Date(2024, time.December, 31, 0, 0, 0, 0, time.UTC), "202412"},

		// YYYYmmdd: year + month + day
		{"YYYYmmdd/January", "YYYYmmdd", time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC), "20240115"},
		{"YYYYmmdd/December", "YYYYmmdd", time.Date(2024, time.December, 31, 0, 0, 0, 0, time.UTC), "20241231"},
		{"YYYYmmdd/OtherTimezone", "YYYYmmdd", time.Date(2024, time.January, 1, 0, 0, 0, 0, time.FixedZone("JST", 9*3600)), "20240101"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/account", func(t *testing.T) {
			cfg := &Config{
				Routes: Routes{
					Account: AccountSection{FilePattern: tc.pattern},
				},
			}
			d := &ast.Open{Date: tc.date, Account: ast.Assets}
			got, err := Decide(d, cfg)
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			want := "transactions/Assets/" + tc.want + ".beancount"
			if got.Path != want {
				t.Errorf("pattern=%q date=%v: Path = %q, want %q", tc.pattern, tc.date, got.Path, want)
			}
		})
		t.Run(tc.name+"/price", func(t *testing.T) {
			cfg := &Config{
				Routes: Routes{
					Price: PriceSection{FilePattern: tc.pattern},
				},
			}
			d := &ast.Price{Date: tc.date, Commodity: "USD"}
			got, err := Decide(d, cfg)
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			want := "quotes/USD/" + tc.want + ".beancount"
			if got.Path != want {
				t.Errorf("pattern=%q date=%v: Path = %q, want %q", tc.pattern, tc.date, got.Path, want)
			}
		})
	}
}

func TestDecide_NilConfig(t *testing.T) {
	d := &ast.Open{Date: jan15, Account: ast.Assets}
	gotNil, err := Decide(d, nil)
	if err != nil {
		t.Fatalf("Decide(nil cfg) returned error: %v", err)
	}
	gotZero, err := Decide(d, &Config{})
	if err != nil {
		t.Fatalf("Decide(&Config{}) returned error: %v", err)
	}
	if diff := cmp.Diff(gotNil, gotZero, decisionCmp); diff != "" {
		t.Errorf("Decide with nil Config differs from zero Config (-nil +zero):\n%s", diff)
	}
}

func TestDecide_AccountOverrideLongestPrefixWins(t *testing.T) {
	cfg := &Config{
		Routes: Routes{
			Account: AccountSection{
				Overrides: []AccountOverride{
					{Prefix: "Assets", Template: "broad/{account}/{date}.beancount"},
					{Prefix: "Assets:JP", Template: "japan/{account}/{date}.beancount"},
				},
			},
		},
	}
	d := &ast.Open{Date: jan15, Account: ast.Assets.MustSub("JP", "Cash")}
	got, err := Decide(d, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	const wantPath = "japan/Assets/JP/Cash/202401.beancount"
	if got.Path != wantPath {
		t.Errorf("Path = %q, want %q", got.Path, wantPath)
	}
}

func TestDecide_AccountOverrideExactMatch(t *testing.T) {
	// An override prefix exactly equal to the directive's account must
	// match: this is the "all segments equal" boundary case, distinct
	// from the strict-subaccount and non-segment-prefix scenarios.
	cfg := &Config{
		Routes: Routes{
			Account: AccountSection{
				Overrides: []AccountOverride{
					{Prefix: "Assets:JP:Cash", Template: "exact/{account}/{date}.beancount"},
				},
			},
		},
	}
	d := &ast.Open{Date: jan15, Account: ast.Assets.MustSub("JP", "Cash")}
	got, err := Decide(d, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.Path != "exact/Assets/JP/Cash/202401.beancount" {
		t.Errorf("Path = %q, want exact-match override path", got.Path)
	}
}

func TestDecide_AccountOverrideSegmentBoundary(t *testing.T) {
	// "Assets:JP" must NOT match "Assets:JPN".
	cfg := &Config{
		Routes: Routes{
			Account: AccountSection{
				Overrides: []AccountOverride{
					{Prefix: "Assets:JP", Template: "japan/{account}/{date}.beancount"},
				},
			},
		},
	}
	d := &ast.Open{Date: jan15, Account: ast.Assets.MustSub("JPN")}
	got, err := Decide(d, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.Path != "transactions/Assets/JPN/202401.beancount" {
		t.Errorf("Path = %q, want default template", got.Path)
	}
}

func TestDecide_AccountOverrideTOMLOrderTie(t *testing.T) {
	// Two overrides at the same depth match; first declared wins.
	cfg := &Config{
		Routes: Routes{
			Account: AccountSection{
				Overrides: []AccountOverride{
					{Prefix: "Assets:JP", Template: "first/{account}/{date}.beancount"},
					{Prefix: "Assets:JP", Template: "second/{account}/{date}.beancount"},
				},
			},
		},
	}
	d := &ast.Open{Date: jan15, Account: ast.Assets.MustSub("JP", "Cash")}
	got, err := Decide(d, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.Path != "first/Assets/JP/Cash/202401.beancount" {
		t.Errorf("Path = %q, want first override", got.Path)
	}
}

func TestDecide_CommodityOverride(t *testing.T) {
	cfg := &Config{
		Routes: Routes{
			Price: PriceSection{
				Overrides: []CommodityOverride{
					{Commodity: "JPY", Template: "yen/{commodity}/{date}.beancount"},
				},
			},
		},
	}
	d := &ast.Price{Date: jan15, Commodity: "JPY"}
	got, err := Decide(d, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.Path != "yen/JPY/202401.beancount" {
		t.Errorf("Path = %q, want override", got.Path)
	}
	// A non-matching commodity falls back to the default template.
	other, err := Decide(&ast.Price{Date: jan15, Commodity: "USD"}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if other.Path != "quotes/USD/202401.beancount" {
		t.Errorf("non-matching commodity Path = %q, want default", other.Path)
	}
}

func TestDecide_DateWindowFromSection(t *testing.T) {
	cfg := &Config{
		Routes: Routes{
			Account: AccountSection{DateWindowDays: ptrInt(3)},
		},
	}
	got, err := Decide(&ast.Open{Date: jan15, Account: ast.Assets}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.DateWindowDays != 3 {
		t.Errorf("DateWindowDays = %d, want 3", got.DateWindowDays)
	}
}

func TestDecide_DateWindowOverrideReplaces(t *testing.T) {
	cfg := &Config{
		Routes: Routes{
			Account: AccountSection{
				DateWindowDays: ptrInt(3),
				Overrides: []AccountOverride{{
					Prefix:         "Assets:JP",
					DateWindowDays: ptrInt(7),
				}},
			},
		},
	}
	got, err := Decide(&ast.Open{Date: jan15, Account: ast.Assets.MustSub("JP", "Cash")}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.DateWindowDays != 7 {
		t.Errorf("DateWindowDays = %d, want 7 (override replaces)", got.DateWindowDays)
	}
}

func TestDecide_DateWindowOverrideSilences(t *testing.T) {
	// An explicit 0 in an override disables the rule for matching
	// accounts even though the section enables it.
	cfg := &Config{
		Routes: Routes{
			Account: AccountSection{
				DateWindowDays: ptrInt(3),
				Overrides: []AccountOverride{{
					Prefix:         "Assets:JP",
					DateWindowDays: ptrInt(0),
				}},
			},
		},
	}
	got, err := Decide(&ast.Open{Date: jan15, Account: ast.Assets.MustSub("JP")}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.DateWindowDays != 0 {
		t.Errorf("DateWindowDays = %d, want 0 (override silences)", got.DateWindowDays)
	}
}

func TestDecide_FormatPrecedenceFieldWise(t *testing.T) {
	// Global sets amount_column=40; section overrides indent_width=4;
	// override sets east_asian_ambiguous_width=1. The Decision must
	// reflect each layer in its own field, with un-set fields falling
	// back to formatopt.Default().
	bGlobal := 40
	iSection := 4
	wOverride := 1
	cfg := &Config{
		Routes: Routes{
			Format: FormatSection{AmountColumn: &bGlobal},
			Account: AccountSection{
				Format: FormatSection{IndentWidth: &iSection},
				Overrides: []AccountOverride{{
					Prefix: "Assets:JP",
					Format: FormatSection{EastAsianAmbiguousWidth: &wOverride},
				}},
			},
		},
	}
	got, err := Decide(&ast.Open{Date: jan15, Account: ast.Assets.MustSub("JP", "Cash")}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	// We can't introspect format.Option closures directly. Apply the
	// closures to a fresh formatopt.Options to inspect the body fields.
	resolved := formatopt.Resolve(got.Format)
	if resolved.AmountColumn != 40 {
		t.Errorf("AmountColumn = %d, want 40 (global)", resolved.AmountColumn)
	}
	if resolved.IndentWidth != 4 {
		t.Errorf("IndentWidth = %d, want 4 (section)", resolved.IndentWidth)
	}
	if resolved.EastAsianAmbiguousWidth != 1 {
		t.Errorf("EastAsianAmbiguousWidth = %d, want 1 (override)", resolved.EastAsianAmbiguousWidth)
	}
	// AlignAmounts was not set anywhere; falls back to default true.
	if !resolved.AlignAmounts {
		t.Error("AlignAmounts: got false, want true (default)")
	}
}

func TestDecide_FormatSpacingFieldsExposed(t *testing.T) {
	n := 3
	insert := true
	cfg := &Config{
		Routes: Routes{
			Format: FormatSection{
				BlankLinesBetweenDirectives:       &n,
				InsertBlankLinesBetweenDirectives: &insert,
			},
		},
	}
	got, err := Decide(&ast.Open{Date: jan15, Account: ast.Assets}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.BlankLinesBetweenDirectives != 3 {
		t.Errorf("BlankLinesBetweenDirectives = %d, want 3", got.BlankLinesBetweenDirectives)
	}
	if !got.InsertBlankLinesBetweenDirectives {
		t.Error("InsertBlankLinesBetweenDirectives = false, want true")
	}
}

func TestDecide_AccountTemplateInherits(t *testing.T) {
	cfg := &Config{
		Routes: Routes{
			Account: AccountSection{Template: "section/{account}/{date}.beancount"},
		},
	}
	got, err := Decide(&ast.Open{Date: jan15, Account: ast.Assets.MustSub("Cash")}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.Path != "section/Assets/Cash/202401.beancount" {
		t.Errorf("Path = %q, want section template", got.Path)
	}
}

func TestDecide_OrderInheritsToOrderKind(t *testing.T) {
	cfg := &Config{
		Routes: Routes{
			Account: AccountSection{Order: "ascending"},
		},
	}
	got, err := Decide(&ast.Open{Date: jan15, Account: ast.Assets}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.Order != OrderAscending {
		t.Errorf("Order = %v, want OrderAscending", got.Order)
	}
}
