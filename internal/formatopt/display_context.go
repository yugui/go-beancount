package formatopt

// DisplayContext provides per-currency display precision for the formatter.
// MostCommon returns the most-common fractional-digit count for currency and
// ok=true, or (0, false) when the currency is unknown or the context is absent.
type DisplayContext interface {
	MostCommon(currency string) (int, bool)
}
