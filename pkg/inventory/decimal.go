package inventory

import "github.com/cockroachdb/apd/v3"

// absDecimal sets dst = |src| using [apd.BaseContext]. Any decimal-context
// error is wrapped as a [CodeInternalError] [Error] prefixed with ctx, which
// callers pass as the full context string (e.g. "inventory reduce: abs units").
func absDecimal(dst, src *apd.Decimal, ctx string) error {
	_, err := apd.BaseContext.Abs(dst, src)
	return decimalError(err, ctx)
}

// addDecimal sets dst = a + b using [apd.BaseContext]. Errors are wrapped as in
// [absDecimal]. dst may alias a or b.
func addDecimal(dst, a, b *apd.Decimal, ctx string) error {
	_, err := apd.BaseContext.Add(dst, a, b)
	return decimalError(err, ctx)
}

// subDecimal sets dst = a - b using [apd.BaseContext]. Errors are wrapped as in
// [absDecimal]. dst may alias a or b.
func subDecimal(dst, a, b *apd.Decimal, ctx string) error {
	_, err := apd.BaseContext.Sub(dst, a, b)
	return decimalError(err, ctx)
}

// decimalError wraps a decimal-context error as a [CodeInternalError] [Error]
// prefixed with ctx, or returns nil when err is nil.
func decimalError(err error, ctx string) error {
	if err == nil {
		return nil
	}
	return Error{Code: CodeInternalError, Message: ctx + ": " + err.Error()}
}
