package formatopt

// DisplayContext supplies the display precision (fractional-digit count) a
// formatter should use when rendering an amount in a given currency. Precision
// returns (0, false) when the currency has no configured precision; callers
// pass the number through unchanged in that case.
type DisplayContext interface {
	Precision(currency string) (int, bool)
}
