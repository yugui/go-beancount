package ast

import (
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
)

func TestPostingPerUnitCost(t *testing.T) {
	tests := []struct {
		name    string
		posting *Posting
		want    *Amount // nil means PerUnitCost should return (nil, nil)
		wantErr bool
	}{
		{
			name:    "nil cost",
			posting: &Posting{Amount: amount(t, "10", "HOOL")},
		},
		{
			name: "empty cost spec ({})",
			posting: &Posting{
				Amount: amount(t, "10", "HOOL"),
				Cost:   &CostSpec{},
			},
		},
		{
			name: "currency-only cost spec ({CUR})",
			posting: &Posting{
				Amount: amount(t, "10", "HOOL"),
				Cost:   &CostSpec{Currency: "USD"},
			},
		},
		{
			name: "PerUnit only ({X CUR})",
			posting: &Posting{
				Amount: amount(t, "10", "HOOL"),
				Cost: &CostSpec{
					PerUnit:  decPtr(t, "100"),
					Currency: "USD",
				},
			},
			want: amount(t, "100", "USD"),
		},
		{
			name: "PerUnit only, negative units (no sign change in per-unit)",
			posting: &Posting{
				Amount: amount(t, "-10", "HOOL"),
				Cost: &CostSpec{
					PerUnit:  decPtr(t, "100"),
					Currency: "USD",
				},
			},
			want: amount(t, "100", "USD"),
		},
		{
			name: "Total only ({{T CUR}})",
			posting: &Posting{
				Amount: amount(t, "10", "HOOL"),
				Cost: &CostSpec{
					Total:    decPtr(t, "5642"),
					Currency: "USD",
				},
			},
			want: amount(t, "564.2", "USD"),
		},
		{
			name: "Total only, negative units (uses |units|)",
			posting: &Posting{
				Amount: amount(t, "-10", "HOOL"),
				Cost: &CostSpec{
					Total:    decPtr(t, "200"),
					Currency: "USD",
				},
			},
			want: amount(t, "20", "USD"),
		},
		{
			name: "combined ({X # T CUR})",
			posting: &Posting{
				Amount: amount(t, "10", "HOOL"),
				Cost: &CostSpec{
					PerUnit:  decPtr(t, "1"),
					Total:    decPtr(t, "2"),
					Currency: "USD",
				},
			},
			want: amount(t, "1.2", "USD"),
		},
		{
			name: "combined, negative units uses |units| in residual",
			posting: &Posting{
				Amount: amount(t, "-10", "HOOL"),
				Cost: &CostSpec{
					PerUnit:  decPtr(t, "1"),
					Total:    decPtr(t, "2"),
					Currency: "USD",
				},
			},
			want: amount(t, "1.2", "USD"),
		},
		{
			// 34-digit precision shared with the booking layer
			name: "Total-form non-terminating per-unit yields rounded result",
			posting: &Posting{
				Amount: amount(t, "3", "STOCK"),
				Cost: &CostSpec{
					Total:    decPtr(t, "1"),
					Currency: "JPY",
				},
			},
			want: &Amount{
				Number:   dec(t, "0.3333333333333333333333333333333333"),
				Currency: "JPY",
			},
		},
		{
			name: "Total-form with zero units is an error",
			posting: &Posting{
				Amount: amount(t, "0", "HOOL"),
				Cost: &CostSpec{
					Total:    decPtr(t, "100"),
					Currency: "USD",
				},
			},
			wantErr: true,
		},
		{
			name: "Total-form with nil units is an error",
			posting: &Posting{
				// Amount nil — total-form needs units to interpret.
				Cost: &CostSpec{
					Total:    decPtr(t, "100"),
					Currency: "USD",
				},
			},
			wantErr: true,
		},
		{
			name: "PerUnit-only with nil units returns the literal",
			// PerUnit-only does not need units; an auto-posting carrying
			// a per-unit cost still has a defined per-unit price.
			posting: &Posting{
				Cost: &CostSpec{
					PerUnit:  decPtr(t, "100"),
					Currency: "USD",
				},
			},
			want: amount(t, "100", "USD"),
		},
		{
			name: "booked *Cost returns Number directly",
			posting: &Posting{
				Amount: amount(t, "10", "HOOL"),
				Cost: &Cost{
					Number:   dec(t, "564.20"),
					Currency: "USD",
				},
			},
			want: amount(t, "564.20", "USD"),
		},
		{
			name: "booked *Cost ignores presentation provenance (PerUnit/Total fields)",
			posting: &Posting{
				Amount: amount(t, "10", "HOOL"),
				Cost: &Cost{
					Number:   dec(t, "564.20"),
					Currency: "USD",
					Total:    amount(t, "5642", "USD"),
				},
			},
			want: amount(t, "564.20", "USD"),
		},
		{
			name: "booked *Cost with empty Currency yields no result",
			// The cash-position sentinel (zero Cost) reports no cost.
			posting: &Posting{
				Amount: amount(t, "10", "USD"),
				Cost:   &Cost{},
			},
		},
		{
			name: "booked *Cost from empty-cost reduction ({}) yields its canonical Number",
			// Regression: an empty {} cost matched against a lot during
			// booking installs a booked *Cost whose Number is the
			// matched lot's canonical per-unit but whose PerUnit / Total
			// provenance fields are nil. PerUnitCost must still surface
			// the canonical value — this is the case upstream's
			// implicit_prices emits for, and the case the old
			// CostHolder-only path was silently dropping.
			posting: &Posting{
				Amount: amount(t, "-3", "HOOL"),
				Cost: &Cost{
					Number:   dec(t, "100"),
					Currency: "USD",
				},
			},
			want: amount(t, "100", "USD"),
		},
		{
			name:    "nil posting returns (nil, nil)",
			posting: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.posting.PerUnitCost()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("PerUnitCost() = (%v, nil), want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("PerUnitCost() error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got, astCloneCmpOpts); diff != "" {
				t.Errorf("PerUnitCost() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestPerUnitCost_ExactDivisionExponent pins the *exponent* of an exact
// total→per-unit quotient, which the value-based cmp.Diff in
// TestPostingPerUnitCost cannot observe. apd's Quo pads exact quotients to the
// context precision (564.2000…0, 100.000…0); the canonical per-unit cost must
// instead carry the General Decimal Arithmetic ideal exponent, matching
// upstream beancount and keeping booked lots / weights / LSP rendering clean.
func TestPerUnitCost_ExactDivisionExponent(t *testing.T) {
	cases := []struct {
		name              string
		total, units, cur string
		want              string // Text('f')
	}{
		{"fractional-ideal", "5642", "10", "USD", "564.2"},
		{"integer-ideal", "500", "5", "USD", "100"},
		{"negative-units-magnitude", "500", "-5", "USD", "100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Posting{
				Amount: amount(t, tc.units, "HOOL"),
				Cost:   &CostSpec{Total: decPtr(t, tc.total), Currency: tc.cur},
			}
			got, err := p.PerUnitCost()
			if err != nil {
				t.Fatalf("PerUnitCost() error: %v", err)
			}
			if s := got.Number.Text('f'); s != tc.want {
				t.Errorf("PerUnitCost().Number = %s, want %s", s, tc.want)
			}
		})
	}
}

// TestPerUnitCost_InexactDivisionUnchanged confirms the exact-quotient
// normalization does not touch a non-terminating quotient: 1/3 must stay at
// full 34-digit precision, the inexact branch the booking layer relies on.
func TestPerUnitCost_InexactDivisionUnchanged(t *testing.T) {
	p := &Posting{
		Amount: amount(t, "3", "STOCK"),
		Cost:   &CostSpec{Total: decPtr(t, "1"), Currency: "JPY"},
	}
	got, err := p.PerUnitCost()
	if err != nil {
		t.Fatalf("PerUnitCost() error: %v", err)
	}
	if s := got.Number.Text('f'); s != "0.3333333333333333333333333333333333" {
		t.Errorf("PerUnitCost().Number = %s, want 34-digit 0.333…", s)
	}
}

func TestPerUnitCost_NilHolder(t *testing.T) {
	got, err := PerUnitCost(nil, amount(t, "10", "HOOL"))
	if err != nil {
		t.Fatalf("PerUnitCost(nil, _) error: %v", err)
	}
	if got != nil {
		t.Errorf("PerUnitCost(nil, _) = %v, want nil", got)
	}
}

func TestPerUnitCost_ResultIsFreshAmount(t *testing.T) {
	// Mutating the returned Amount must not propagate back into the
	// posting's CostSpec — PerUnitCost callers (e.g. the implicit-price
	// plugin) build downstream directives by repurposing the result and
	// must not corrupt the source.
	p := &Posting{
		Amount: amount(t, "1", "STOCK"),
		Cost:   &CostSpec{PerUnit: decPtr(t, "5"), Currency: "USD"},
	}
	got, err := p.PerUnitCost()
	if err != nil {
		t.Fatalf("PerUnitCost() error: %v", err)
	}
	if got == nil {
		t.Fatal("PerUnitCost() returned nil")
	}
	got.Number.Set(&apd.Decimal{})
	cs := p.Cost.(*CostSpec)
	if cs.PerUnit.String() != "5" {
		t.Errorf("PerUnitCost() mutating result corrupted CostSpec: PerUnit = %s", cs.PerUnit.String())
	}
}

func TestPerUnitCost_BookedCostResultIsFresh(t *testing.T) {
	// The booked-*Cost branch returns Number cloned from c.Number.
	// Verify the result is independent so plugin code can build a
	// freshly allocated price Amount without disturbing the booked
	// posting.
	c := &Cost{Number: dec(t, "100"), Currency: "USD"}
	orig := dec(t, "100")
	p := &Posting{Amount: amount(t, "1", "HOOL"), Cost: c}
	got, err := p.PerUnitCost()
	if err != nil {
		t.Fatalf("PerUnitCost() error: %v", err)
	}
	got.Number.Set(&apd.Decimal{})
	if c.Number.Cmp(&orig) != 0 {
		t.Errorf("PerUnitCost() leaked into booked Cost.Number: %s, want %s", c.Number.String(), orig.String())
	}
}
