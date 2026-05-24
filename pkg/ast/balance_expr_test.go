package ast_test

import (
	"sort"
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/yugui/go-beancount/pkg/ast"
)

func decimal(t *testing.T, s string) apd.Decimal {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString(s); err != nil {
		t.Fatalf("bad test setup: cannot parse %q: %v", s, err)
	}
	return d
}

func codes(diags []ast.Diagnostic) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.Code
	}
	sort.Strings(out)
	return out
}

func TestParseBalanceAmount_Table(t *testing.T) {
	type want struct {
		amount     string
		currency   string
		tolerance  string // empty => nil
		diagCodes  []string
		expectZero bool // amount expected to be the zero value
	}
	cases := []struct {
		name string
		in   string
		want want
	}{
		{"plain", "100 USD", want{amount: "100", currency: "USD"}},
		{"with-commas", "1,000 + 500 USD", want{amount: "1500", currency: "USD"}},
		{"parens", "(100+200)*1.05 USD", want{amount: "315.00", currency: "USD"}},
		{"tolerance", "319.020 ~ 0.002 USD", want{amount: "319.020", currency: "USD", tolerance: "0.002"}},
		{"negative", "-100 USD", want{amount: "-100", currency: "USD"}},
		{"div-zero", "100 / 0 USD", want{expectZero: true, diagCodes: []string{"amount-expr-eval"}}},
		{"missing-cur", "100", want{expectZero: true, diagCodes: []string{"amount-missing-currency"}}},
		{"trailing", "100 USD trailing", want{expectZero: true, diagCodes: []string{"amount-trailing-input"}}},
		{"two-currencies", "100 USD EUR", want{expectZero: true, diagCodes: []string{"amount-trailing-input"}}},
		{"plus-no-rhs", "100 + USD", want{expectZero: true, diagCodes: []string{"amount-expr-parse"}}},
		{"empty", "", want{expectZero: true, diagCodes: []string{"amount-expr-parse"}}},
		{"tolerance-div-zero", "100 ~ 0/0 USD", want{expectZero: true, diagCodes: []string{"balance-tolerance-eval"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			amt, tol, diags := ast.ParseBalanceAmount(tc.in)

			gotCodes := codes(diags)
			wantCodes := append([]string(nil), tc.want.diagCodes...)
			sort.Strings(wantCodes)
			if diff := cmp.Diff(wantCodes, gotCodes, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("codes mismatch (-want +got):\n%s\nfull diags: %v", diff, diags)
			}
			for _, d := range diags {
				if d.Severity != ast.Error {
					t.Errorf("diag %q has Severity=%v, want Error", d.Code, d.Severity)
				}
				if d.Span.Start.Offset < 0 || d.Span.Start.Offset > len(tc.in) {
					t.Errorf("diag %q Span.Start.Offset = %d, out of [0,%d]", d.Code, d.Span.Start.Offset, len(tc.in))
				}
			}

			if tc.want.expectZero {
				zero := ast.Amount{}
				if amt.Currency != zero.Currency || amt.Number.Cmp(&zero.Number) != 0 {
					t.Errorf("amount = %+v, want zero value", amt)
				}
				if tol != nil {
					t.Errorf("tolerance = %v, want nil", tol)
				}
				return
			}

			if amt.Currency != tc.want.currency {
				t.Errorf("Currency = %q, want %q", amt.Currency, tc.want.currency)
			}
			wantNum := decimal(t, tc.want.amount)
			if amt.Number.Cmp(&wantNum) != 0 {
				t.Errorf("Number = %s, want %s", amt.Number.String(), tc.want.amount)
			}
			if tc.want.tolerance == "" {
				if tol != nil {
					t.Errorf("tolerance = %v, want nil", tol)
				}
			} else {
				if tol == nil {
					t.Fatal("tolerance nil, want non-nil")
				}
				wantTol := decimal(t, tc.want.tolerance)
				if tol.Cmp(&wantTol) != 0 {
					t.Errorf("tolerance = %s, want %s", tol.String(), tc.want.tolerance)
				}
			}
		})
	}
}

func TestParseAmountExpression_Table(t *testing.T) {
	type want struct {
		amount     string
		currency   string
		diagCodes  []string
		expectZero bool
	}
	cases := []struct {
		name string
		in   string
		want want
	}{
		{"plain", "100 USD", want{amount: "100", currency: "USD"}},
		{"decimal", "1,234.56 EUR", want{amount: "1234.56", currency: "EUR"}},
		// ParseAmountExpression deliberately rejects the tolerance form
		// (the `~` token lives in the balance grammar, not the amount
		// grammar) — the residual `~ 1 USD` becomes trailing input.
		{"tilde-trailing", "100 ~ 1 USD", want{expectZero: true, diagCodes: []string{"amount-trailing-input"}}},
		{"missing-cur", "100", want{expectZero: true, diagCodes: []string{"amount-missing-currency"}}},
		{"bad-syntax", "abc USD", want{expectZero: true, diagCodes: []string{"amount-expr-parse"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			amt, diags := ast.ParseAmountExpression(tc.in)

			gotCodes := codes(diags)
			wantCodes := append([]string(nil), tc.want.diagCodes...)
			sort.Strings(wantCodes)
			if diff := cmp.Diff(wantCodes, gotCodes, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("codes mismatch (-want +got):\n%s\nfull diags: %v", diff, diags)
			}
			for _, d := range diags {
				if d.Severity != ast.Error {
					t.Errorf("diag %q has Severity=%v, want Error", d.Code, d.Severity)
				}
			}

			if tc.want.expectZero {
				zero := ast.Amount{}
				if amt.Currency != zero.Currency || amt.Number.Cmp(&zero.Number) != 0 {
					t.Errorf("amount = %+v, want zero value", amt)
				}
				return
			}
			if amt.Currency != tc.want.currency {
				t.Errorf("Currency = %q, want %q", amt.Currency, tc.want.currency)
			}
			wantNum := decimal(t, tc.want.amount)
			if amt.Number.Cmp(&wantNum) != 0 {
				t.Errorf("Number = %s, want %s", amt.Number.String(), tc.want.amount)
			}
		})
	}
}

func TestParseBalanceAmount_DiagnosticSpanInRange(t *testing.T) {
	_, _, diags := ast.ParseBalanceAmount("100 USD trailing")
	if len(diags) == 0 {
		t.Fatal("expected at least one diagnostic")
	}
	d := diags[0]
	if d.Code != "amount-trailing-input" {
		t.Errorf("Code = %q, want amount-trailing-input", d.Code)
	}
	if d.Span.Start.Filename != "" || d.Span.Start.Line != 0 || d.Span.Start.Column != 0 {
		t.Errorf("Span.Start has filename/line/column set (%+v); should be empty for caller to rebase", d.Span.Start)
	}
	if d.Span.Start.Offset == 0 {
		t.Errorf("Span.Start.Offset = 0, want positive offset for trailing-input")
	}
}
