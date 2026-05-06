package ast

import (
	"testing"

	"github.com/cockroachdb/apd/v3"
)

// dec parses s as an apd.Decimal and returns it. Fails the test
// fatally on parse error; only intended for test setup.
func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString(s); err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return d
}

func amount(t *testing.T, num, cur string) *Amount {
	t.Helper()
	return &Amount{Number: dec(t, num), Currency: cur}
}

func TestPostingTotalCost(t *testing.T) {
	tests := []struct {
		name    string
		posting *Posting
		wantNum string
		wantCur string
		wantNil bool
		wantErr bool
	}{
		{
			name:    "auto-posting (Amount nil)",
			posting: &Posting{},
			wantNil: true,
		},
		{
			name: "no cost spec",
			posting: &Posting{
				Amount: amount(t, "100", "USD"),
			},
			wantNil: true,
		},
		{
			name: "empty cost spec",
			posting: &Posting{
				Amount: amount(t, "100", "USD"),
				Cost:   &CostSpec{},
			},
			wantNil: true,
		},
		{
			name: "PerUnit only, positive units",
			posting: &Posting{
				Amount: amount(t, "10", "HOOL"),
				Cost: &CostSpec{
					PerUnit: amount(t, "100", "USD"),
				},
			},
			wantNum: "1000",
			wantCur: "USD",
		},
		{
			name: "PerUnit only, negative units (reduction)",
			posting: &Posting{
				Amount: amount(t, "-10", "HOOL"),
				Cost: &CostSpec{
					PerUnit: amount(t, "100", "USD"),
				},
			},
			wantNum: "-1000",
			wantCur: "USD",
		},
		{
			name: "Total only, negative units (reduction sign flip)",
			posting: &Posting{
				Amount: amount(t, "-3", "STOCK"),
				Cost: &CostSpec{
					Total: amount(t, "1", "JPY"),
				},
			},
			wantNum: "-1",
			wantCur: "JPY",
		},
		{
			name: "Total only, negative Total magnitude (still uses |Total|)",
			posting: &Posting{
				Amount: amount(t, "3", "STOCK"),
				Cost: &CostSpec{
					Total: amount(t, "-1", "JPY"),
				},
			},
			wantNum: "1",
			wantCur: "JPY",
		},
		{
			name: "combined PerUnit and Total, positive units",
			posting: &Posting{
				Amount: amount(t, "10", "HOOL"),
				Cost: &CostSpec{
					PerUnit: amount(t, "502.12", "USD"),
					Total:   amount(t, "9.95", "USD"),
				},
			},
			wantNum: "5031.15", // 10 * 502.12 + 9.95
			wantCur: "USD",
		},
		{
			name: "combined PerUnit and Total, negative units",
			posting: &Posting{
				Amount: amount(t, "-10", "HOOL"),
				Cost: &CostSpec{
					PerUnit: amount(t, "502.12", "USD"),
					Total:   amount(t, "9.95", "USD"),
				},
			},
			// -10 * 502.12 = -5021.2; sign(-) * |9.95| = -9.95; sum = -5031.15
			wantNum: "-5031.15",
			wantCur: "USD",
		},
		{
			name: "combined currencies disagree",
			posting: &Posting{
				Amount: amount(t, "10", "HOOL"),
				Cost: &CostSpec{
					PerUnit: amount(t, "100", "USD"),
					Total:   amount(t, "5", "EUR"),
				},
			},
			wantErr: true,
		},
		{
			name: "Total form preserves exactness for non-terminating per-unit",
			// 1 JPY total over 3 STOCK: per-unit would be 1/3 (non-terminating),
			// but TotalCost returns exactly 1 JPY without dividing.
			posting: &Posting{
				Amount: amount(t, "3", "STOCK"),
				Cost: &CostSpec{
					Total: amount(t, "1", "JPY"),
				},
			},
			wantNum: "1",
			wantCur: "JPY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.posting.TotalCost()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("TotalCost() = (%v, nil), want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("TotalCost() error: %v", err)
			}
			if tt.wantNil {
				if got != nil {
					t.Fatalf("TotalCost() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("TotalCost() = nil, want %s %s", tt.wantNum, tt.wantCur)
			}
			if gotStr := got.Number.String(); gotStr != tt.wantNum {
				t.Errorf("TotalCost() Number = %q, want %q", gotStr, tt.wantNum)
			}
			if got.Currency != tt.wantCur {
				t.Errorf("TotalCost() Currency = %q, want %q", got.Currency, tt.wantCur)
			}
		})
	}
}

func TestPostingTotalCost_ResultIsFreshAmount(t *testing.T) {
	// Mutating the returned Amount must not propagate back into the
	// posting's CostSpec.
	p := &Posting{
		Amount: amount(t, "1", "STOCK"),
		Cost:   &CostSpec{PerUnit: amount(t, "5", "USD")},
	}
	got, err := p.TotalCost()
	if err != nil {
		t.Fatalf("TotalCost() error: %v", err)
	}
	if got == nil {
		t.Fatal("TotalCost() returned nil")
	}
	got.Number.Set(&apd.Decimal{}) // overwrite to zero
	if p.Cost.PerUnit.Number.String() != "5" {
		t.Errorf("TotalCost() mutating result corrupted CostSpec: PerUnit.Number = %s", p.Cost.PerUnit.Number.String())
	}
}
