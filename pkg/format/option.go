// Package format provides public formatting options for beancount output.
package format

import "github.com/yugui/go-beancount/internal/formatopt"

// Option configures formatting behavior.
type Option = func(*formatopt.Options)

// WithCommaGrouping controls whether thousands separators are inserted in
// formatted numbers.
func WithCommaGrouping(v bool) Option {
	return func(o *formatopt.Options) { o.CommaGrouping = v }
}

// WithAlignAmounts controls whether posting amounts are column-aligned.
func WithAlignAmounts(v bool) Option {
	return func(o *formatopt.Options) { o.AlignAmounts = v }
}

// WithAmountColumn sets the right-edge column at which posting amounts align
// when [WithAlignAmounts] is true. It is a no-op without [WithAlignAmounts].
func WithAmountColumn(col int) Option {
	return func(o *formatopt.Options) { o.AmountColumn = col }
}

// WithEastAsianAmbiguousWidth sets the display width assigned to East Asian
// Ambiguous characters when computing visual columns. Use 2 for terminals
// that render them as full-width and 1 for half-width.
func WithEastAsianAmbiguousWidth(w int) Option {
	return func(o *formatopt.Options) { o.EastAsianAmbiguousWidth = w }
}

// WithIndentWidth sets the number of spaces per indent level (e.g. for
// posting indentation under a transaction).
func WithIndentWidth(w int) Option {
	return func(o *formatopt.Options) { o.IndentWidth = w }
}

// WithBlankLinesBetweenDirectives sets the number of blank lines retained
// between adjacent directives when normalizing whitespace. See also
// [WithInsertBlankLinesBetweenDirectives], which controls whether new blank
// lines are inserted where none exist.
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
