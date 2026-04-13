package ast

import (
	"reflect"
	"testing"
)

func TestAccountRootConstantsAreValid(t *testing.T) {
	for _, a := range []Account{Assets, Liabilities, Equity, Income, Expenses} {
		if !a.IsValid() {
			t.Errorf("%q.IsValid() = false, want true", a)
		}
		if a.Root() != a {
			t.Errorf("%q.Root() = %q, want %q", a, a.Root(), a)
		}
		if a.Parent() != "" {
			t.Errorf("%q.Parent() = %q, want empty", a, a.Parent())
		}
	}
}

func TestAccountSub(t *testing.T) {
	got, err := Assets.Sub("Cash", "JPY")
	if err != nil {
		t.Fatalf("Assets.Sub(Cash, JPY): %v", err)
	}
	if want := Account("Assets:Cash:JPY"); got != want {
		t.Errorf("Sub returned %q, want %q", got, want)
	}

	same, err := Assets.Sub()
	if err != nil {
		t.Fatalf("Assets.Sub() with no args: %v", err)
	}
	if same != Assets {
		t.Errorf("Assets.Sub() = %q, want %q", same, Assets)
	}
}

func TestAccountSubRejectsInvalidComponents(t *testing.T) {
	cases := []struct {
		name       string
		components []string
	}{
		{"empty component", []string{""}},
		{"lowercase start", []string{"cash"}},
		{"illegal rune", []string{"Cash*"}},
		{"second component invalid", []string{"Cash", ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Assets.Sub(c.components...)
			if err == nil {
				t.Errorf("Sub(%v): want error, got %q", c.components, got)
			}
			if got != "" {
				t.Errorf("Sub(%v): want empty Account on error, got %q", c.components, got)
			}
		})
	}
}

func TestAccountSubAcceptsValidComponents(t *testing.T) {
	cases := []struct {
		name       string
		components []string
		want       Account
	}{
		{"digit start", []string{"1Cash"}, "Assets:1Cash"},
		{"hyphen continuation", []string{"Cash-Reserve"}, "Assets:Cash-Reserve"},
		{"unicode continuation", []string{"現金"}, "Assets:現金"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Assets.Sub(c.components...)
			if err != nil {
				t.Fatalf("Sub(%v): unexpected error: %v", c.components, err)
			}
			if got != c.want {
				t.Errorf("Sub(%v) = %q, want %q", c.components, got, c.want)
			}
		})
	}
}

func TestAccountSubDoesNotReValidateReceiver(t *testing.T) {
	// Per the Sub doc, the receiver is not re-validated. Calling Sub on an
	// already-invalid Account is allowed, and extends it mechanically.
	got, err := Account("bad").Sub("Cash")
	if err != nil {
		t.Fatalf("Sub on invalid receiver: unexpected error: %v", err)
	}
	if want := Account("bad:Cash"); got != want {
		t.Errorf("Sub on invalid receiver = %q, want %q", got, want)
	}
}

func TestAccountMustSubPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustSub did not panic on invalid input")
		}
	}()
	_ = Assets.MustSub("")
}

func TestAccountParent(t *testing.T) {
	cases := []struct {
		in   Account
		want Account
	}{
		{"", ""},
		{Assets, ""},
		{"Assets:Cash", Assets},
		{"Assets:Cash:JPY", "Assets:Cash"},
	}
	for _, c := range cases {
		if got := c.in.Parent(); got != c.want {
			t.Errorf("%q.Parent() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAccountRoot(t *testing.T) {
	cases := []struct {
		in   Account
		want Account
	}{
		{"", ""},
		{Assets, Assets},
		{"Assets:Cash", Assets},
		{"Expenses:Food:Restaurant", Expenses},
	}
	for _, c := range cases {
		if got := c.in.Root(); got != c.want {
			t.Errorf("%q.Root() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAccountParts(t *testing.T) {
	cases := []struct {
		in   Account
		want []string
	}{
		{"", nil},
		{Assets, []string{"Assets"}},
		{"Assets:Cash:JPY", []string{"Assets", "Cash", "JPY"}},
	}
	for _, c := range cases {
		if got := c.in.Parts(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q.Parts() = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestAccountIsValid(t *testing.T) {
	cases := []struct {
		in   Account
		want bool
	}{
		{"", false},
		{Assets, true},
		{"Assets:Cash", true},
		{"Assets:Cash-Reserve", true}, // hyphen in continuation is allowed
		{"Assets:1Cash", true},        // digit start is allowed
		{"assets:Cash", false},        // lowercase root
		{"Assets:cash", false},        // lowercase component start
		{"Assets:", false},            // empty trailing component
		{"Assets:Cash*", false},       // illegal rune
		{"資産:現金", false},              // root must be one of the five English constants
		{"Assets:現金", true},           // Unicode sub-component (Lo)
	}
	for _, c := range cases {
		if got := c.in.IsValid(); got != c.want {
			t.Errorf("%q.IsValid() = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestAccountSubChain(t *testing.T) {
	a := Assets.MustSub("Cash").MustSub("JPY")
	if want := Account("Assets:Cash:JPY"); a != want {
		t.Errorf("chained MustSub = %q, want %q", a, want)
	}
	if a.Parent() != Assets.MustSub("Cash") {
		t.Errorf("Parent chain: got %q", a.Parent())
	}
	if a.Root() != Assets {
		t.Errorf("Root chain: got %q", a.Root())
	}
}
