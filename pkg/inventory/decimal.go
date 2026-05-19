package inventory

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"
)

// absDecimal sets dst = |src| using [apd.BaseContext]. Any decimal-context
// error is wrapped as a system error prefixed with ctx, which callers
// pass as the full context string (e.g. "inventory.Reduce: abs units").
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

// decimalError wraps a decimal-context error as a system error prefixed
// with ctx, or returns nil when err is nil. The returned value is never
// an [ast.Diagnostic]: BaseContext arithmetic only fails for inputs the
// beancount grammar cannot produce (NaN, signaling-NaN), so any failure
// is an implementation bug rather than a user finding.
func decimalError(err error, ctx string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", ctx, err)
}
