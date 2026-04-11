// Package formatopt defines formatting options for beancount output.
package formatopt

// Options controls the formatting of beancount output.
type Options struct {
	CommaGrouping               bool // Insert commas in numbers (1,000.00).
	AlignAmounts                bool // Column-align posting amounts.
	AmountColumn                int  // Right-edge column for amounts.
	EastAsianAmbiguousWidth     int  // EA Ambiguous char width: 1 or 2.
	IndentWidth                 int  // Spaces per indent level.
	BlankLinesBetweenDirectives int  // Blank lines between directives.
}

// Default returns Options with sensible defaults.
func Default() Options {
	return Options{
		CommaGrouping:               false,
		AlignAmounts:                true,
		AmountColumn:                52,
		EastAsianAmbiguousWidth:     2,
		IndentWidth:                 2,
		BlankLinesBetweenDirectives: 1,
	}
}

// Resolve starts from Default(), applies each option func, and returns the result.
func Resolve(opts []func(*Options)) Options {
	o := Default()
	for _, fn := range opts {
		fn(&o)
	}
	return o
}
