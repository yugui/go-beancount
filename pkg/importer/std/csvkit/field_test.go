package csvkit_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

func TestJoin(t *testing.T) {
	row := map[string]string{"A": " x ", "B": "y", "C": "  ", "D": "z"}
	get := func(c string) string { return row[c] }

	cases := []struct {
		name string
		cols []string
		sep  string
		want string
	}{
		{name: "no cols", cols: nil, want: ""},
		{name: "single trimmed", cols: []string{"A"}, sep: "-", want: "x"},
		{name: "drops blank cell", cols: []string{"A", "C", "D"}, sep: "-", want: "x-z"},
		{name: "all blank", cols: []string{"C"}, sep: "-", want: ""},
		{name: "joins with separator", cols: []string{"A", "B"}, sep: " / ", want: "x / y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := csvkit.Join(tc.cols, tc.sep, get); got != tc.want {
				t.Errorf("Join(%v) = %q, want %q", tc.cols, got, tc.want)
			}
		})
	}
}

func TestResolveThroughMap(t *testing.T) {
	m := map[string]string{"AMZN": "Amazon", "noise": ""}

	cases := []struct {
		name   string
		key    string
		m      map[string]string
		mode   csvkit.MapMode
		want   string
		wantOK bool
	}{
		{name: "hit", key: "AMZN", m: m, mode: csvkit.Verbatim, want: "Amazon", wantOK: true},
		{name: "verbatim miss passes through", key: "EUR", m: m, mode: csvkit.Verbatim, want: "EUR", wantOK: true},
		{name: "strict miss unresolved", key: "EUR", m: m, mode: csvkit.Strict, want: "", wantOK: false},
		{name: "hit to empty string", key: "noise", m: m, mode: csvkit.Verbatim, want: "", wantOK: true},
		{name: "nil map verbatim", key: "x", m: nil, mode: csvkit.Verbatim, want: "x", wantOK: true},
		{name: "nil map strict", key: "x", m: nil, mode: csvkit.Strict, want: "", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := csvkit.ResolveThroughMap(tc.key, tc.m, tc.mode)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("ResolveThroughMap(%q) = (%q, %v), want (%q, %v)", tc.key, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
