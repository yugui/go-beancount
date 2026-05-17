package format

import (
	"strings"

	"github.com/cockroachdb/apd/v3"
)

// quantizeCtx is the arithmetic context for Quantize: 34-digit precision,
// half-even rounding.
var quantizeCtx = func() apd.Context {
	c := apd.BaseContext.WithPrecision(34)
	c.Rounding = apd.RoundHalfEven
	return *c
}()

// Quantize returns s rewritten to exactly digits fractional places using
// half-even rounding. Thousands-separator commas in s are removed before
// parsing. Returns s unchanged on parse failure or when digits < 0.
func Quantize(s string, digits int) string {
	if digits < 0 {
		return s
	}
	plain := strings.ReplaceAll(s, ",", "")
	var d apd.Decimal
	if _, _, err := apd.BaseContext.SetString(&d, plain); err != nil {
		return s
	}
	var result apd.Decimal
	if _, err := quantizeCtx.Quantize(&result, &d, -int32(digits)); err != nil {
		return s
	}
	return result.Text('f')
}
