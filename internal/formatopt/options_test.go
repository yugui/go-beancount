package formatopt

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestDefault(t *testing.T) {
	want := Options{
		AlignAmounts:                true,
		AmountColumn:                52,
		EastAsianAmbiguousWidth:     2,
		IndentWidth:                 2,
		BlankLinesBetweenDirectives: 1,
	}
	if diff := cmp.Diff(want, Default()); diff != "" {
		t.Errorf("Default() mismatch (-want +got):\n%s", diff)
	}
}

func TestResolveNoOptions(t *testing.T) {
	got := Resolve(nil)
	want := Default()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Resolve(nil) mismatch (-want +got):\n%s", diff)
	}
}

func TestResolveWithOverrides(t *testing.T) {
	got := Resolve([]func(*Options){
		func(o *Options) { o.CommaGrouping = true },
		func(o *Options) { o.AmountColumn = 80 },
		func(o *Options) { o.EastAsianAmbiguousWidth = 1 },
	})
	want := Options{
		CommaGrouping:               true,
		AlignAmounts:                true,
		AmountColumn:                80,
		EastAsianAmbiguousWidth:     1,
		IndentWidth:                 2,
		BlankLinesBetweenDirectives: 1,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Resolve() mismatch (-want +got):\n%s", diff)
	}
}
