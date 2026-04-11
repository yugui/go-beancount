// Package format provides public formatting options for beancount output.
package format

import "github.com/yugui/go-beancount/internal/formatopt"

// Option configures formatting behavior.
type Option = func(*formatopt.Options)

func WithCommaGrouping(v bool) Option {
	return func(o *formatopt.Options) { o.CommaGrouping = v }
}

func WithAlignAmounts(v bool) Option {
	return func(o *formatopt.Options) { o.AlignAmounts = v }
}

func WithAmountColumn(col int) Option {
	return func(o *formatopt.Options) { o.AmountColumn = col }
}

func WithEastAsianAmbiguousWidth(w int) Option {
	return func(o *formatopt.Options) { o.EastAsianAmbiguousWidth = w }
}

func WithIndentWidth(w int) Option {
	return func(o *formatopt.Options) { o.IndentWidth = w }
}

func WithBlankLinesBetweenDirectives(n int) Option {
	return func(o *formatopt.Options) { o.BlankLinesBetweenDirectives = n }
}
