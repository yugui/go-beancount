package route

import (
	"github.com/yugui/go-beancount/internal/formatopt"
	"github.com/yugui/go-beancount/pkg/format"
)

// resolvedFormat carries the seven format fields with concrete values
// after merging the precedence chain (defaults → global → section →
// override). It is exposed via Decision.Format (as []format.Option) and
// via Decision's Resolved* spacing fields.
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

// options converts r into a fully-formed []format.Option slice covering
// all seven public fields. The merger silently overrides the two spacing
// fields with values from Plan, but emitting them here keeps Decision.Format
// self-describing for any other consumer (e.g. the printer in pass-through
// or dry-run modes).
func (r resolvedFormat) options() []format.Option {
	return []format.Option{
		format.WithCommaGrouping(r.CommaGrouping),
		format.WithAlignAmounts(r.AlignAmounts),
		format.WithAmountColumn(r.AmountColumn),
		format.WithEastAsianAmbiguousWidth(r.EastAsianAmbiguousWidth),
		format.WithIndentWidth(r.IndentWidth),
		format.WithBlankLinesBetweenDirectives(r.BlankLinesBetweenDirectives),
		format.WithInsertBlankLinesBetweenDirectives(r.InsertBlankLinesBetweenDirectives),
	}
}

// MergeFormatSections layers later sections on top of earlier ones in
// place. It is exported so the CLI can overlay flag-derived values onto
// a TOML-loaded section without rebuilding the route.Config types.
func MergeFormatSections(layers ...FormatSection) FormatSection {
	var out FormatSection
	for _, layer := range layers {
		if layer.CommaGrouping != nil {
			v := *layer.CommaGrouping
			out.CommaGrouping = &v
		}
		if layer.AlignAmounts != nil {
			v := *layer.AlignAmounts
			out.AlignAmounts = &v
		}
		if layer.AmountColumn != nil {
			v := *layer.AmountColumn
			out.AmountColumn = &v
		}
		if layer.EastAsianAmbiguousWidth != nil {
			v := *layer.EastAsianAmbiguousWidth
			out.EastAsianAmbiguousWidth = &v
		}
		if layer.IndentWidth != nil {
			v := *layer.IndentWidth
			out.IndentWidth = &v
		}
		if layer.BlankLinesBetweenDirectives != nil {
			v := *layer.BlankLinesBetweenDirectives
			out.BlankLinesBetweenDirectives = &v
		}
		if layer.InsertBlankLinesBetweenDirectives != nil {
			v := *layer.InsertBlankLinesBetweenDirectives
			out.InsertBlankLinesBetweenDirectives = &v
		}
	}
	return out
}
