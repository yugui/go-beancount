package route

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/yugui/go-beancount/internal/formatopt"
	"github.com/yugui/go-beancount/pkg/ast"
)

// decisionCmp ignores the opaque Format closures (which cmp cannot
// equate) and instead compares the resolved spacing fields and
// Path/Order/EqMetaKeys directly.
var decisionCmp = cmpopts.IgnoreFields(Decision{}, "Format")

// defaultDecision returns a Decision with the resolved spacing fields
// set to formatopt.Default()'s values, the same defaults Decide returns
// when no override and no [routes.format] section apply.
func defaultDecision(path string) Decision {
	d := formatopt.Default()
	return Decision{
		Path:                                path,
		Order:                               OrderAscending,
		ResolvedBlankLinesBetweenDirectives: d.BlankLinesBetweenDirectives,
		ResolvedInsertBlankLinesBetweenDirectives: d.InsertBlankLinesBetweenDirectives,
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

func TestDecide_DateFormatYYYYmm(t *testing.T) {
	cases := []struct {
		name string
		date time.Time
		want string // YYYYmm portion
	}{
		{"January", time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC), "202401"},
		{"December", time.Date(2024, time.December, 31, 0, 0, 0, 0, time.UTC), "202412"},
		// 2024-01-01 00:00:00 JST is 2023-12-31 15:00:00 UTC. The expected
		// "202401" verifies that Year/Month are read directly from the
		// time.Time in its original location, with no implicit UTC
		// conversion — a regression that called .UTC() would produce
		// "202312" and fail this case.
		{"OtherTimezone", time.Date(2024, time.January, 1, 0, 0, 0, 0, time.FixedZone("JST", 9*3600)), "202401"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &ast.Open{Date: tc.date, Account: ast.Assets}
			got, err := Decide(d, nil)
			if err != nil {
				t.Fatalf("Decide returned error: %v", err)
			}
			want := "transactions/Assets/" + tc.want + ".beancount"
			if got.Path != want {
				t.Errorf("Decide on date %v: Path = %q, want %q", tc.date, got.Path, want)
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
		AccountOverrides: []AccountOverride{
			{Prefix: "Assets", Template: "broad/{account}/{date}.beancount"},
			{Prefix: "Assets:JP", Template: "japan/{account}/{date}.beancount"},
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
		AccountOverrides: []AccountOverride{
			{Prefix: "Assets:JP:Cash", Template: "exact/{account}/{date}.beancount"},
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
		AccountOverrides: []AccountOverride{
			{Prefix: "Assets:JP", Template: "japan/{account}/{date}.beancount"},
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
		AccountOverrides: []AccountOverride{
			{Prefix: "Assets:JP", Template: "first/{account}/{date}.beancount"},
			{Prefix: "Assets:JP", Template: "second/{account}/{date}.beancount"},
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
		CommodityOverrides: []CommodityOverride{
			{Commodity: "JPY", Template: "yen/{commodity}/{date}.beancount"},
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

func TestDecide_EquivalenceMetaKeysFromSection(t *testing.T) {
	cfg := &Config{
		Account: AccountSection{EquivalenceMetaKeys: []string{"import-id"}},
	}
	got, err := Decide(&ast.Open{Date: jan15, Account: ast.Assets}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if diff := cmp.Diff([]string{"import-id"}, got.EqMetaKeys); diff != "" {
		t.Errorf("EqMetaKeys mismatch (-want +got):\n%s", diff)
	}
}

func TestDecide_EquivalenceMetaKeysOverrideReplaces(t *testing.T) {
	cfg := &Config{
		Account: AccountSection{EquivalenceMetaKeys: []string{"import-id"}},
		AccountOverrides: []AccountOverride{{
			Prefix:              "Assets:JP",
			EquivalenceMetaKeys: []string{"receipt-id"},
			HasEqMetaKeys:       true,
		}},
	}
	got, err := Decide(&ast.Open{Date: jan15, Account: ast.Assets.MustSub("JP", "Cash")}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if diff := cmp.Diff([]string{"receipt-id"}, got.EqMetaKeys); diff != "" {
		t.Errorf("EqMetaKeys mismatch (-want +got):\n%s", diff)
	}
}

func TestDecide_EquivalenceMetaKeysOverrideSilences(t *testing.T) {
	// HasEqMetaKeys=true with empty slice means "silence inherited keys".
	cfg := &Config{
		Account: AccountSection{EquivalenceMetaKeys: []string{"import-id"}},
		AccountOverrides: []AccountOverride{{
			Prefix:              "Assets:JP",
			EquivalenceMetaKeys: []string{},
			HasEqMetaKeys:       true,
		}},
	}
	got, err := Decide(&ast.Open{Date: jan15, Account: ast.Assets.MustSub("JP")}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(got.EqMetaKeys) != 0 {
		t.Errorf("EqMetaKeys = %v, want empty", got.EqMetaKeys)
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
		Format: FormatSection{AmountColumn: &bGlobal},
		Account: AccountSection{
			Format: FormatSection{IndentWidth: &iSection},
		},
		AccountOverrides: []AccountOverride{{
			Prefix: "Assets:JP",
			Format: FormatSection{EastAsianAmbiguousWidth: &wOverride},
		}},
	}
	got, err := Decide(&ast.Open{Date: jan15, Account: ast.Assets.MustSub("JP", "Cash")}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	// We can't introspect format.Option closures directly, but Resolved*
	// spacing fields confirm the merging logic touched the resolved
	// struct. Apply the closures to a fresh formatopt.Options to inspect
	// the body fields.
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
		Format: FormatSection{
			BlankLinesBetweenDirectives:       &n,
			InsertBlankLinesBetweenDirectives: &insert,
		},
	}
	got, err := Decide(&ast.Open{Date: jan15, Account: ast.Assets}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.ResolvedBlankLinesBetweenDirectives != 3 {
		t.Errorf("ResolvedBlankLinesBetweenDirectives = %d, want 3", got.ResolvedBlankLinesBetweenDirectives)
	}
	if !got.ResolvedInsertBlankLinesBetweenDirectives {
		t.Error("ResolvedInsertBlankLinesBetweenDirectives = false, want true")
	}
}

func TestDecide_AccountTemplateInherits(t *testing.T) {
	cfg := &Config{
		Account: AccountSection{Template: "section/{account}/{date}.beancount"},
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
		Account: AccountSection{Order: "ascending"},
	}
	got, err := Decide(&ast.Open{Date: jan15, Account: ast.Assets}, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got.Order != OrderAscending {
		t.Errorf("Order = %v, want OrderAscending", got.Order)
	}
}
