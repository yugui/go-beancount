package formatopt

import "testing"

func TestDefault(t *testing.T) {
	d := Default()
	if d.CommaGrouping {
		t.Error("CommaGrouping: got true, want false")
	}
	if !d.AlignAmounts {
		t.Error("AlignAmounts: got false, want true")
	}
	if d.AmountColumn != 52 {
		t.Errorf("AmountColumn: got %d, want 52", d.AmountColumn)
	}
	if d.EastAsianAmbiguousWidth != 2 {
		t.Errorf("EastAsianAmbiguousWidth: got %d, want 2", d.EastAsianAmbiguousWidth)
	}
	if d.IndentWidth != 2 {
		t.Errorf("IndentWidth: got %d, want 2", d.IndentWidth)
	}
	if d.BlankLinesBetweenDirectives != 1 {
		t.Errorf("BlankLinesBetweenDirectives: got %d, want 1", d.BlankLinesBetweenDirectives)
	}
}

func TestResolveNoOptions(t *testing.T) {
	got := Resolve(nil)
	want := Default()
	if got != want {
		t.Errorf("Resolve(nil) = %+v, want %+v", got, want)
	}
}

func TestResolveWithOverrides(t *testing.T) {
	got := Resolve([]func(*Options){
		func(o *Options) { o.CommaGrouping = true },
		func(o *Options) { o.AmountColumn = 80 },
		func(o *Options) { o.EastAsianAmbiguousWidth = 1 },
	})
	if !got.CommaGrouping {
		t.Error("CommaGrouping: got false, want true")
	}
	if got.AmountColumn != 80 {
		t.Errorf("AmountColumn: got %d, want 80", got.AmountColumn)
	}
	if got.EastAsianAmbiguousWidth != 1 {
		t.Errorf("EastAsianAmbiguousWidth: got %d, want 1", got.EastAsianAmbiguousWidth)
	}
	// Unchanged fields should remain at defaults.
	if !got.AlignAmounts {
		t.Error("AlignAmounts: got false, want true")
	}
	if got.IndentWidth != 2 {
		t.Errorf("IndentWidth: got %d, want 2", got.IndentWidth)
	}
}
