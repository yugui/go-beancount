package format_test

import (
	"testing"

	"github.com/yugui/go-beancount/internal/formatopt"
	"github.com/yugui/go-beancount/pkg/format"
)

func TestWithCommaGrouping(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithCommaGrouping(true)})
	if !got.CommaGrouping {
		t.Errorf("WithCommaGrouping(true): CommaGrouping = false, want true")
	}
}

func TestWithAlignAmounts(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithAlignAmounts(false)})
	if got.AlignAmounts {
		t.Errorf("WithAlignAmounts(false): AlignAmounts = true, want false")
	}
}

func TestWithAmountColumn(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithAmountColumn(80)})
	if got.AmountColumn != 80 {
		t.Errorf("WithAmountColumn(80): AmountColumn = %d, want 80", got.AmountColumn)
	}
}

func TestWithEastAsianAmbiguousWidth(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithEastAsianAmbiguousWidth(1)})
	if got.EastAsianAmbiguousWidth != 1 {
		t.Errorf("WithEastAsianAmbiguousWidth(1): EastAsianAmbiguousWidth = %d, want 1", got.EastAsianAmbiguousWidth)
	}
}

func TestWithIndentWidth(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithIndentWidth(4)})
	if got.IndentWidth != 4 {
		t.Errorf("WithIndentWidth(4): IndentWidth = %d, want 4", got.IndentWidth)
	}
}

func TestWithBlankLinesBetweenDirectives(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithBlankLinesBetweenDirectives(2)})
	if got.BlankLinesBetweenDirectives != 2 {
		t.Errorf("WithBlankLinesBetweenDirectives(2): BlankLinesBetweenDirectives = %d, want 2", got.BlankLinesBetweenDirectives)
	}
}

func TestWithInsertBlankLinesBetweenDirectives(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithInsertBlankLinesBetweenDirectives(true)})
	if !got.InsertBlankLinesBetweenDirectives {
		t.Errorf("WithInsertBlankLinesBetweenDirectives(true): InsertBlankLinesBetweenDirectives = false, want true")
	}
}

func TestComposedOptions(t *testing.T) {
	got := formatopt.Resolve([]format.Option{
		format.WithCommaGrouping(true),
		format.WithAlignAmounts(false),
		format.WithAmountColumn(60),
		format.WithIndentWidth(4),
	})
	if !got.CommaGrouping {
		t.Errorf("ComposedOptions: CommaGrouping = false, want true")
	}
	if got.AlignAmounts {
		t.Errorf("ComposedOptions: AlignAmounts = true, want false")
	}
	if got.AmountColumn != 60 {
		t.Errorf("ComposedOptions: AmountColumn = %d, want 60", got.AmountColumn)
	}
	if got.IndentWidth != 4 {
		t.Errorf("ComposedOptions: IndentWidth = %d, want 4", got.IndentWidth)
	}
	// Unchanged fields should keep defaults.
	if got.EastAsianAmbiguousWidth != 2 {
		t.Errorf("ComposedOptions: EastAsianAmbiguousWidth (default) = %d, want 2", got.EastAsianAmbiguousWidth)
	}
	if got.BlankLinesBetweenDirectives != 1 {
		t.Errorf("ComposedOptions: BlankLinesBetweenDirectives (default) = %d, want 1", got.BlankLinesBetweenDirectives)
	}
}
