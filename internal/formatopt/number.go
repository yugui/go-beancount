package formatopt

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

// StripCommas removes commas from a number string.
func StripCommas(s string) string {
	return strings.ReplaceAll(s, ",", "")
}

// InsertCommas adds thousand-separator commas to the integer part of a number.
func InsertCommas(s string) string {
	// Handle negative sign.
	neg := false
	num := s
	if len(num) > 0 && num[0] == '-' {
		neg = true
		num = num[1:]
	}

	// Strip existing commas first.
	num = strings.ReplaceAll(num, ",", "")

	// Split at decimal point.
	intPart := num
	decPart := ""
	if i := strings.IndexByte(num, '.'); i >= 0 {
		intPart = num[:i]
		decPart = num[i:]
	}

	// Insert commas in the integer part.
	if len(intPart) > 3 {
		var b strings.Builder
		remainder := len(intPart) % 3
		if remainder > 0 {
			b.WriteString(intPart[:remainder])
		}
		for i := remainder; i < len(intPart); i += 3 {
			if b.Len() > 0 {
				b.WriteByte(',')
			}
			b.WriteString(intPart[i : i+3])
		}
		intPart = b.String()
	}

	result := intPart + decPart
	if neg {
		result = "-" + result
	}
	return result
}
