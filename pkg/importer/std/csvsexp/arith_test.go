package csvsexp

import "testing"

// emitWith wraps body as the :amount expression of a one-line emit-transaction
// and returns the primary posting's amount as a string, asserting no
// diagnostics.
func scaledAmount(t *testing.T, amountExpr, csv string) string {
	t.Helper()
	prog := `(csv-import (emit-transaction
		:date (parse-date (column "D") "2006-01-02")
		:amount ` + amountExpr + `
		:currency (const "USD") :account (const "Assets:X")))`
	out := extractProgram(t, prog, csv)
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	tx := firstTxn(t, out)
	if tx.Postings[0].Amount == nil {
		t.Fatal("primary posting has no amount")
	}
	return tx.Postings[0].Amount.Number.String()
}

func TestDateOffset(t *testing.T) {
	cases := []struct {
		name   string
		offset string
		want   string
	}{
		{"forward", "2", "2024-01-03"},
		{"backward", "-1", "2023-12-31"},
		{"month boundary", "31", "2024-02-01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := `(csv-import (emit-transaction
				:date (date-offset (parse-date (column "D") "2006-01-02") ` + tc.offset + `)
				:amount (parse-amount (column "A"))
				:currency (const "USD") :account (const "Assets:X")))`
			out := extractProgram(t, prog, "D,A\n2024-01-01,100\n")
			if len(out.Diagnostics) != 0 {
				t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
			}
			got := firstTxn(t, out).Date.Format("2006-01-02")
			if got != tc.want {
				t.Errorf("offset %s: got %s, want %s", tc.offset, got, tc.want)
			}
		})
	}
}

// TestDateOffsetPropagatesSoftFail confirms an unparsable date short-circuits
// the offset: the row is dropped with the parse-date diagnostic rather than
// offsetting a zero time.
func TestDateOffsetPropagatesSoftFail(t *testing.T) {
	prog := `(csv-import (emit-transaction
		:date (date-offset (parse-date (column "D") "2006-01-02") 2)
		:amount (parse-amount (column "A"))
		:currency (const "USD") :account (const "Assets:X")))`
	out := extractProgram(t, prog, "D,A\nnot-a-date,100\n")
	if len(out.Directives) != 0 {
		t.Fatalf("expected no directives, got %d", len(out.Directives))
	}
	if len(out.Diagnostics) == 0 {
		t.Fatal("expected a diagnostic for the bad date")
	}
}

func TestScaleAmount(t *testing.T) {
	cases := []struct {
		name   string
		factor string
		want   string
	}{
		{"integer", "2", "200"},
		{"decimal", `"1.5"`, "150.0"},
		{"fraction", `"0.1"`, "10.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scaledAmount(t,
				`(scale-amount (parse-amount (column "A")) `+tc.factor+`)`,
				"D,A\n2024-01-01,100\n")
			if got != tc.want {
				t.Errorf("scale by %s: got %s, want %s", tc.factor, got, tc.want)
			}
		})
	}
}

// TestScaleAmountPreservesCurrencyHint confirms the parsed currency suffix
// survives scaling and reaches the posting.
func TestScaleAmountPreservesCurrencyHint(t *testing.T) {
	prog := `(csv-import (emit-transaction
		:date (parse-date (column "D") "2006-01-02")
		:amount (scale-amount (parse-amount (column "A") :split-currency #t) 2)
		:account (const "Assets:X")))`
	out := extractProgram(t, prog, "D,A\n2024-01-01,100 EUR\n")
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	p := firstTxn(t, out).Postings[0]
	if p.Amount.Number.String() != "200" || p.Amount.Currency != "EUR" {
		t.Errorf("got %s %s, want 200 EUR", p.Amount.Number.String(), p.Amount.Currency)
	}
}

// TestScaleAmountNilPassthrough confirms a blank amount cell scales to nil,
// yielding an auto-balanced posting rather than a diagnostic.
func TestScaleAmountNilPassthrough(t *testing.T) {
	prog := `(csv-import
		(let* ((d (parse-date (column "D") "2006-01-02")))
		  (emit (transaction :date d :postings (postings
		    (posting :account (const "Assets:X")
		             :amount (amount (parse-amount (column "A")) :currency (const "USD")))
		    (posting :account (const "Income:Y")
		             :amount (amount (scale-amount (parse-amount (column "B")) 2)
		                             :currency (const "USD"))))))))`
	// B is blank, so the scaled amount is nil and the leg auto-balances.
	out := extractProgram(t, prog, "D,A,B\n2024-01-01,100,\n")
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	tx := firstTxn(t, out)
	if len(tx.Postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(tx.Postings))
	}
	if tx.Postings[1].Amount != nil {
		t.Errorf("posting 1 amount = %v, want nil (auto)", tx.Postings[1].Amount)
	}
}

func TestDivideAmount(t *testing.T) {
	cases := []struct {
		name string
		expr string
		csv  string
		want string
	}{
		{"exact", `(divide-amount (parse-amount (column "A")) 4)`, "D,A\n2024-01-01,100\n", "25"},
		{"scaled half-even", `(divide-amount (parse-amount (column "A")) 3 :scale 2)`, "D,A\n2024-01-01,10\n", "3.33"},
		{"decimal divisor", `(divide-amount (parse-amount (column "A")) "2.5")`, "D,A\n2024-01-01,10\n", "4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scaledAmount(t, tc.expr, tc.csv); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRoundFloorCeilAmount(t *testing.T) {
	cases := []struct {
		form string
		want string
	}{
		{"round-amount", "2.34"},
		{"floor-amount", "2.34"},
		{"ceil-amount", "2.35"},
	}
	for _, tc := range cases {
		t.Run(tc.form, func(t *testing.T) {
			expr := `(` + tc.form + ` (parse-amount (column "A")) 2)`
			if got := scaledAmount(t, expr, "D,A\n2024-01-01,2.345\n"); got != tc.want {
				t.Errorf("%s 2.345 -> got %s, want %s", tc.form, got, tc.want)
			}
		})
	}
}

// TestFloorCeilNegative confirms floor/ceil follow mathematical direction
// (toward -inf / +inf) rather than truncating toward zero.
func TestFloorCeilNegative(t *testing.T) {
	if got := scaledAmount(t, `(floor-amount (parse-amount (column "A")) 1)`, "D,A\n2024-01-01,-2.34\n"); got != "-2.4" {
		t.Errorf("floor -2.34 -> got %s, want -2.4", got)
	}
	if got := scaledAmount(t, `(ceil-amount (parse-amount (column "A")) 1)`, "D,A\n2024-01-01,-2.34\n"); got != "-2.3" {
		t.Errorf("ceil -2.34 -> got %s, want -2.3", got)
	}
}
