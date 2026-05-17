package formatopt

import "testing"

func TestInsertCommas(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"1234", "1,234"},
		{"1234567", "1,234,567"},
		{"1234.56", "1,234.56"},
		{"123", "123"},
		{"1234567.89", "1,234,567.89"},
		{"-1234", "-1,234"},
		{"1,234", "1,234"}, // already has commas
	}
	for _, tt := range tests {
		got := InsertCommas(tt.in)
		if got != tt.want {
			t.Errorf("InsertCommas(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestStripCommas(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"1,234", "1234"},
		{"1,234,567.89", "1234567.89"},
		{"1234", "1234"},
	}
	for _, tt := range tests {
		got := StripCommas(tt.in)
		if got != tt.want {
			t.Errorf("StripCommas(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestQuantize(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		digits int
		want   string
	}{
		{"pad zeros", "50", 2, "50.00"},
		{"pad zeros with one dp", "50.0", 2, "50.00"},
		{"half-even rounds down", "1.125", 2, "1.12"},
		{"half-even rounds up", "1.135", 2, "1.14"},
		{"negative", "-1.125", 2, "-1.12"},
		{"zero dp no trailing dot", "1000", 0, "1000"},
		{"zero dp rounds", "1000.7", 0, "1001"},
		{"strip commas", "1,234.5", 2, "1234.50"},
		{"negative digits pass-through", "1.5", -1, "1.5"},
		{"parse failure pass-through", "not-a-number", 2, "not-a-number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Quantize(tt.in, tt.digits)
			if got != tt.want {
				t.Errorf("Quantize(%q, %d) = %q, want %q", tt.in, tt.digits, got, tt.want)
			}
		})
	}
}
