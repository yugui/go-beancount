package csvbase

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

// IsBlank reports whether in's value is empty after TrimSpace. A soft-failed
// input propagates its diagnostic.
func IsBlank(b *Builder, in Key[string]) Key[bool] {
	return AddStep(b, func(c *MappingState) (bool, *ast.Diagnostic, error) {
		v, d := Value(c, in)
		if d != nil {
			return false, d, nil
		}
		return strings.TrimSpace(v) == "", nil, nil
	})
}

// StrEqual reports whether lhs and rhs resolve to identical strings. The
// comparison is exact (no trimming); compose with Trim when needed. A
// soft-failed input propagates its diagnostic, lhs first.
func StrEqual(b *Builder, lhs, rhs Key[string]) Key[bool] {
	return AddStep(b, func(c *MappingState) (bool, *ast.Diagnostic, error) {
		lv, ld := Value(c, lhs)
		if ld != nil {
			return false, ld, nil
		}
		rv, rd := Value(c, rhs)
		if rd != nil {
			return false, rd, nil
		}
		return lv == rv, nil, nil
	})
}

// MatchRegexp reports whether re matches in's value. A soft-failed input
// propagates its diagnostic.
func MatchRegexp(b *Builder, in Key[string], re *regexp.Regexp) Key[bool] {
	return AddStep(b, func(c *MappingState) (bool, *ast.Diagnostic, error) {
		v, d := Value(c, in)
		if d != nil {
			return false, d, nil
		}
		return re.MatchString(v), nil, nil
	})
}

// And reports whether every input is true. With no inputs it is true. The first
// soft-failed input propagates its diagnostic. Inputs are plain step results
// already evaluated by the pipeline, so And combines values without
// side-effect short-circuiting.
func And(b *Builder, ins ...Key[bool]) Key[bool] {
	return AddStep(b, func(c *MappingState) (bool, *ast.Diagnostic, error) {
		for _, in := range ins {
			v, d := Value(c, in)
			if d != nil {
				return false, d, nil
			}
			if !v {
				return false, nil, nil
			}
		}
		return true, nil, nil
	})
}

// Or reports whether any input is true. With no inputs it is false. The first
// soft-failed input propagates its diagnostic. See And on short-circuiting.
func Or(b *Builder, ins ...Key[bool]) Key[bool] {
	return AddStep(b, func(c *MappingState) (bool, *ast.Diagnostic, error) {
		for _, in := range ins {
			v, d := Value(c, in)
			if d != nil {
				return false, d, nil
			}
			if v {
				return true, nil, nil
			}
		}
		return false, nil, nil
	})
}

// Not reports the negation of in. A soft-failed input propagates its
// diagnostic.
func Not(b *Builder, in Key[bool]) Key[bool] {
	return AddStep(b, func(c *MappingState) (bool, *ast.Diagnostic, error) {
		v, d := Value(c, in)
		if d != nil {
			return false, d, nil
		}
		return !v, nil, nil
	})
}

// IsNegative reports whether in's amount is strictly less than zero. A nil
// amount is undecidable and yields false. A soft-failed input propagates its
// diagnostic.
func IsNegative(b *Builder, in Key[*csvkit.Amount]) Key[bool] {
	return amountSign(b, in, func(s int) bool { return s < 0 })
}

// IsPositive reports whether in's amount is strictly greater than zero. A nil
// amount yields false. A soft-failed input propagates its diagnostic.
func IsPositive(b *Builder, in Key[*csvkit.Amount]) Key[bool] {
	return amountSign(b, in, func(s int) bool { return s > 0 })
}

// IsZero reports whether in's amount is zero. A nil amount yields false. A
// soft-failed input propagates its diagnostic.
func IsZero(b *Builder, in Key[*csvkit.Amount]) Key[bool] {
	return amountSign(b, in, func(s int) bool { return s == 0 })
}

func amountSign(b *Builder, in Key[*csvkit.Amount], pred func(int) bool) Key[bool] {
	return AddStep(b, func(c *MappingState) (bool, *ast.Diagnostic, error) {
		v, d := Value(c, in)
		if d != nil {
			return false, d, nil
		}
		if v == nil {
			return false, nil, nil
		}
		return pred(v.Number.Sign()), nil, nil
	})
}

// AmountLess reports whether lhs < rhs under same-currency comparison. A nil
// operand or conflicting non-empty CurrencyHints soft-fail with code (default
// DiagBadAmount). A soft-failed input propagates its diagnostic.
func AmountLess(b *Builder, lhs, rhs Key[*csvkit.Amount], code string) Key[bool] {
	return amountCmp(b, lhs, rhs, code, func(cmp int) bool { return cmp < 0 })
}

// AmountGreater reports whether lhs > rhs; see AmountLess for failure modes.
func AmountGreater(b *Builder, lhs, rhs Key[*csvkit.Amount], code string) Key[bool] {
	return amountCmp(b, lhs, rhs, code, func(cmp int) bool { return cmp > 0 })
}

// AmountEqual reports whether lhs == rhs numerically; see AmountLess for
// failure modes.
func AmountEqual(b *Builder, lhs, rhs Key[*csvkit.Amount], code string) Key[bool] {
	return amountCmp(b, lhs, rhs, code, func(cmp int) bool { return cmp == 0 })
}

func amountCmp(b *Builder, lhs, rhs Key[*csvkit.Amount], code string, pred func(int) bool) Key[bool] {
	if code == "" {
		code = DiagBadAmount
	}
	return AddStep(b, func(c *MappingState) (bool, *ast.Diagnostic, error) {
		lv, ld := Value(c, lhs)
		if ld != nil {
			return false, ld, nil
		}
		rv, rd := Value(c, rhs)
		if rd != nil {
			return false, rd, nil
		}
		if lv == nil || rv == nil {
			info := c.Info()
			diag := ErrorDiag(code, info.Path, info.Line, "cannot compare a missing amount")
			return false, &diag, nil
		}
		if _, ok := combineHint(lv.CurrencyHint, rv.CurrencyHint); !ok {
			info := c.Info()
			diag := ErrorDiag(code, info.Path, info.Line,
				fmt.Sprintf("conflicting currency hints: %q vs %q", lv.CurrencyHint, rv.CurrencyHint))
			return false, &diag, nil
		}
		return pred(lv.Number.Cmp(&rv.Number)), nil, nil
	})
}

// combineHint merges two CurrencyHints. The result is the non-empty hint when
// at most one is set; ok is false when both are set and differ.
func combineHint(lhs, rhs string) (string, bool) {
	switch {
	case lhs == "":
		return rhs, true
	case rhs == "":
		return lhs, true
	case lhs == rhs:
		return lhs, true
	default:
		return "", false
	}
}

// If selects then when cond is true and els otherwise, yielding the chosen
// branch's value and diagnostic. The unchosen branch is never read, so a
// soft-fail there is harmless. A soft-failed cond propagates its diagnostic.
func If[T any](b *Builder, cond Key[bool], then, els Key[T]) Key[T] {
	return AddStep(b, func(c *MappingState) (T, *ast.Diagnostic, error) {
		cv, cd := Value(c, cond)
		if cd != nil {
			var zero T
			return zero, cd, nil
		}
		if cv {
			v, d := Value(c, then)
			return v, d, nil
		}
		v, d := Value(c, els)
		return v, d, nil
	})
}
