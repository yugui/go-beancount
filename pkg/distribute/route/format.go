package route

import (
	"github.com/yugui/go-beancount/internal/formatopt"
	"github.com/yugui/go-beancount/pkg/format"
)

// resolvedFormat carries the seven format fields with concrete values
// after merging the precedence chain (defaults → global → section →
// override). The five body-level fields feed Decision.Format; the two
// spacing fields feed Decision's BlankLines* fields directly.
type resolvedFormat struct {
	CommaGrouping                     bool
	AlignAmounts                      bool
	AmountColumn                      int
	EastAsianAmbiguousWidth           int
	IndentWidth                       int
	BlankLinesBetweenDirectives       int
	InsertBlankLinesBetweenDirectives bool
}

// resolveFormat merges the format chain field-wise. Later layers (section
// then override) replace earlier ones only where their pointers are
// non-nil; fields left nil at every layer fall back to formatopt.Default().
func resolveFormat(global, section, override FormatSection) resolvedFormat {
	d := formatopt.Default()
	r := resolvedFormat{
		CommaGrouping:                     d.CommaGrouping,
		AlignAmounts:                      d.AlignAmounts,
		AmountColumn:                      d.AmountColumn,
		EastAsianAmbiguousWidth:           d.EastAsianAmbiguousWidth,
		IndentWidth:                       d.IndentWidth,
		BlankLinesBetweenDirectives:       d.BlankLinesBetweenDirectives,
		InsertBlankLinesBetweenDirectives: d.InsertBlankLinesBetweenDirectives,
	}
	for _, layer := range []FormatSection{global, section, override} {
		applyFormatLayer(&r, layer)
	}
	return r
}

func applyFormatLayer(r *resolvedFormat, layer FormatSection) {
	if layer.CommaGrouping != nil {
		r.CommaGrouping = *layer.CommaGrouping
	}
	if layer.AlignAmounts != nil {
		r.AlignAmounts = *layer.AlignAmounts
	}
	if layer.AmountColumn != nil {
		r.AmountColumn = *layer.AmountColumn
	}
	if layer.EastAsianAmbiguousWidth != nil {
		r.EastAsianAmbiguousWidth = *layer.EastAsianAmbiguousWidth
	}
	if layer.IndentWidth != nil {
		r.IndentWidth = *layer.IndentWidth
	}
	if layer.BlankLinesBetweenDirectives != nil {
		r.BlankLinesBetweenDirectives = *layer.BlankLinesBetweenDirectives
	}
	if layer.InsertBlankLinesBetweenDirectives != nil {
		r.InsertBlankLinesBetweenDirectives = *layer.InsertBlankLinesBetweenDirectives
	}
}

// options converts r into body-level format options. Spacing options
// live on Decision directly, not in the returned slice — they describe
// file-level layout, not how a single directive is rendered.
func (r resolvedFormat) options() []format.Option {
	return []format.Option{
		format.WithCommaGrouping(r.CommaGrouping),
		format.WithAlignAmounts(r.AlignAmounts),
		format.WithAmountColumn(r.AmountColumn),
		format.WithEastAsianAmbiguousWidth(r.EastAsianAmbiguousWidth),
		format.WithIndentWidth(r.IndentWidth),
	}
}
