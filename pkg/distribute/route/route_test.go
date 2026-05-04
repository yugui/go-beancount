package route

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/yugui/go-beancount/pkg/ast"
)

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
			want := Decision{Path: wantPath, Order: OrderAscending}
			if diff := cmp.Diff(want, got); diff != "" {
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
		want := Decision{
			Path:  "transactions/Expenses/Food/202401.beancount",
			Order: OrderAscending,
		}
		if diff := cmp.Diff(want, got); diff != "" {
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
	want := Decision{
		Path:  "quotes/JPY/202401.beancount",
		Order: OrderAscending,
	}
	if diff := cmp.Diff(want, got); diff != "" {
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
			if diff := cmp.Diff(want, got); diff != "" {
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
	// nil Config and a zero-value *Config must yield identical results.
	d := &ast.Open{Date: jan15, Account: ast.Assets}
	gotNil, err := Decide(d, nil)
	if err != nil {
		t.Fatalf("Decide(nil cfg) returned error: %v", err)
	}
	gotZero, err := Decide(d, &Config{})
	if err != nil {
		t.Fatalf("Decide(&Config{}) returned error: %v", err)
	}
	if !cmp.Equal(gotNil, gotZero) {
		t.Errorf("Decide with nil Config differs from zero Config:\n%s",
			cmp.Diff(gotNil, gotZero))
	}
}
