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
