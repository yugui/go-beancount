package csvkit_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

func TestParseNumber(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		format    csvkit.NumberFormat
		want      string // apd.Decimal.String(); ignored when wantBlank/wantErr
		wantBlank bool
		wantErr   bool
	}{
		{name: "plain integer", in: "1234", want: "1234"},
		{name: "plain decimal", in: "-4.50", want: "-4.50"},
		{name: "trims whitespace", in: "  12 ", want: "12"},
		{name: "blank is no value", in: "   ", wantBlank: true},
		{name: "comma rejected by default", in: "1,234", wantErr: true},
		{
			name:   "comma stripped when configured",
			in:     "1,234,567",
			format: csvkit.NumberFormat{ThousandsSep: ","},
			want:   "1234567",
		},
		{
			name:      "placeholder is no value",
			in:        "-",
			format:    csvkit.NumberFormat{Placeholders: []string{"-"}},
			wantBlank: true,
		},
		{
			name:   "european decimal comma",
			in:     "1.234,56",
			format: csvkit.NumberFormat{ThousandsSep: ".", DecimalSep: ","},
			want:   "1234.56",
		},
		{
			name:    "malformed is error",
			in:      "12x3",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, blank, err := csvkit.ParseNumber(tc.in, tc.format)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseNumber(%q) err = nil, want error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseNumber(%q) unexpected err: %v", tc.in, err)
			}
			if blank != tc.wantBlank {
				t.Fatalf("ParseNumber(%q) blank = %v, want %v", tc.in, blank, tc.wantBlank)
			}
			if tc.wantBlank {
				return
			}
			if got := v.String(); got != tc.want {
				t.Errorf("ParseNumber(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAmountParserSum(t *testing.T) {
	cells := func(m map[string]string) func(string) string {
		return func(col string) string { return m[col] }
	}

	cases := []struct {
		name       string
		cols       []csvkit.AmountColumn
		format     csvkit.NumberFormat
		row        map[string]string
		want       string
		wantStatus csvkit.AmountStatus
		wantBadCol string
	}{
		{
			name:       "single signed",
			cols:       []csvkit.AmountColumn{{Col: "Amount"}},
			row:        map[string]string{"Amount": "-4.50"},
			want:       "-4.50",
			wantStatus: csvkit.AmountOK,
		},
		{
			name:       "debit credit split",
			cols:       []csvkit.AmountColumn{{Col: "Withdrawal", Negate: true}, {Col: "Deposit"}},
			row:        map[string]string{"Withdrawal": "5000", "Deposit": ""},
			want:       "-5000",
			wantStatus: csvkit.AmountOK,
		},
		{
			name:       "all blank",
			cols:       []csvkit.AmountColumn{{Col: "Withdrawal", Negate: true}, {Col: "Deposit"}},
			row:        map[string]string{},
			wantStatus: csvkit.AmountAllBlank,
		},
		{
			name:       "bad column reported",
			cols:       []csvkit.AmountColumn{{Col: "Amount"}},
			row:        map[string]string{"Amount": "bogus"},
			wantStatus: csvkit.AmountBad,
			wantBadCol: "Amount",
		},
		{
			name:       "placeholder treated as blank",
			cols:       []csvkit.AmountColumn{{Col: "Amount"}},
			format:     csvkit.NumberFormat{Placeholders: []string{"-"}},
			row:        map[string]string{"Amount": "-"},
			wantStatus: csvkit.AmountAllBlank,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := csvkit.AmountParser{Format: tc.format}
			sum, status, badCol := p.Sum(tc.cols, cells(tc.row))
			if status != tc.wantStatus {
				t.Fatalf("Sum() status = %v, want %v", status, tc.wantStatus)
			}
			if badCol != tc.wantBadCol {
				t.Errorf("Sum() badCol = %q, want %q", badCol, tc.wantBadCol)
			}
			if tc.wantStatus == csvkit.AmountOK {
				if got := sum.String(); got != tc.want {
					t.Errorf("Sum() = %q, want %q", got, tc.want)
				}
			}
		})
	}
}
