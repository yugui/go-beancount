package formatopt

import "testing"

func TestStringWidth(t *testing.T) {
	tests := []struct {
		name             string
		s                string
		eaAmbiguousWidth int
		want             int
	}{
		{name: "empty", s: "", eaAmbiguousWidth: 1, want: 0},
		{name: "ascii", s: "hello", eaAmbiguousWidth: 1, want: 5},
		{name: "cjk", s: "漢字", eaAmbiguousWidth: 1, want: 4},
		{name: "mixed", s: "ab漢字cd", eaAmbiguousWidth: 1, want: 8},
		{name: "ambiguous_narrow", s: "○α", eaAmbiguousWidth: 1, want: 2},
		{name: "ambiguous_wide", s: "○α", eaAmbiguousWidth: 2, want: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StringWidth(tt.s, tt.eaAmbiguousWidth)
			if got != tt.want {
				t.Errorf("StringWidth(%q, %d) = %d, want %d", tt.s, tt.eaAmbiguousWidth, got, tt.want)
			}
		})
	}
}
