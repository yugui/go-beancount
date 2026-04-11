package format_test

import (
	"testing"

	"github.com/yugui/go-beancount/internal/formatopt"
	"github.com/yugui/go-beancount/pkg/format"
)

func TestWithCommaGrouping(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithCommaGrouping(true)})
	if !got.CommaGrouping {
		t.Error("expected CommaGrouping to be true")
	}
}

func TestWithAlignAmounts(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithAlignAmounts(false)})
	if got.AlignAmounts {
		t.Error("expected AlignAmounts to be false")
	}
}

func TestWithAmountColumn(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithAmountColumn(80)})
	if got.AmountColumn != 80 {
		t.Errorf("expected AmountColumn=80, got %d", got.AmountColumn)
	}
}

func TestWithEastAsianAmbiguousWidth(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithEastAsianAmbiguousWidth(1)})
	if got.EastAsianAmbiguousWidth != 1 {
		t.Errorf("expected EastAsianAmbiguousWidth=1, got %d", got.EastAsianAmbiguousWidth)
	}
}

func TestWithIndentWidth(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithIndentWidth(4)})
	if got.IndentWidth != 4 {
		t.Errorf("expected IndentWidth=4, got %d", got.IndentWidth)
	}
}

func TestWithBlankLinesBetweenDirectives(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithBlankLinesBetweenDirectives(2)})
	if got.BlankLinesBetweenDirectives != 2 {
		t.Errorf("expected BlankLinesBetweenDirectives=2, got %d", got.BlankLinesBetweenDirectives)
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
		t.Error("expected CommaGrouping to be true")
	}
	if got.AlignAmounts {
		t.Error("expected AlignAmounts to be false")
	}
	if got.AmountColumn != 60 {
		t.Errorf("expected AmountColumn=60, got %d", got.AmountColumn)
	}
	if got.IndentWidth != 4 {
		t.Errorf("expected IndentWidth=4, got %d", got.IndentWidth)
	}
	// Unchanged fields should keep defaults.
	if got.EastAsianAmbiguousWidth != 2 {
		t.Errorf("expected EastAsianAmbiguousWidth=2 (default), got %d", got.EastAsianAmbiguousWidth)
	}
	if got.BlankLinesBetweenDirectives != 1 {
		t.Errorf("expected BlankLinesBetweenDirectives=1 (default), got %d", got.BlankLinesBetweenDirectives)
	}
}
