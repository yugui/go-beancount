package format_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/internal/formatopt"
	"github.com/yugui/go-beancount/pkg/format"
)

func TestWithCommaGrouping(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithCommaGrouping(true)})
	want := formatopt.Default()
	want.CommaGrouping = true
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Resolve(WithCommaGrouping) mismatch (-want +got):\n%s", diff)
	}
}

func TestWithAlignAmounts(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithAlignAmounts(false)})
	want := formatopt.Default()
	want.AlignAmounts = false
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Resolve(WithAlignAmounts) mismatch (-want +got):\n%s", diff)
	}
}

func TestWithAmountColumn(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithAmountColumn(80)})
	want := formatopt.Default()
	want.AmountColumn = 80
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Resolve(WithAmountColumn) mismatch (-want +got):\n%s", diff)
	}
}

func TestWithEastAsianAmbiguousWidth(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithEastAsianAmbiguousWidth(1)})
	want := formatopt.Default()
	want.EastAsianAmbiguousWidth = 1
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Resolve(WithEastAsianAmbiguousWidth) mismatch (-want +got):\n%s", diff)
	}
}

func TestWithIndentWidth(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithIndentWidth(4)})
	want := formatopt.Default()
	want.IndentWidth = 4
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Resolve(WithIndentWidth) mismatch (-want +got):\n%s", diff)
	}
}

func TestWithBlankLinesBetweenDirectives(t *testing.T) {
	got := formatopt.Resolve([]format.Option{format.WithBlankLinesBetweenDirectives(2)})
	want := formatopt.Default()
	want.BlankLinesBetweenDirectives = 2
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Resolve(WithBlankLinesBetweenDirectives) mismatch (-want +got):\n%s", diff)
	}
}

func TestComposedOptions(t *testing.T) {
	got := formatopt.Resolve([]format.Option{
		format.WithCommaGrouping(true),
		format.WithAlignAmounts(false),
		format.WithAmountColumn(60),
		format.WithIndentWidth(4),
	})
	want := formatopt.Default()
	want.CommaGrouping = true
	want.AlignAmounts = false
	want.AmountColumn = 60
	want.IndentWidth = 4
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Resolve(composed) mismatch (-want +got):\n%s", diff)
	}
}
