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

// WithInsertBlankLinesBetweenDirectives controls whether blank lines are
// actively inserted between directives. When false (the default), existing
// blank lines are normalized but no new blank lines are created where none
// exist. When true, blank lines are always ensured between directives.
func WithInsertBlankLinesBetweenDirectives(v bool) Option {
	return func(o *formatopt.Options) { o.InsertBlankLinesBetweenDirectives = v }
}
