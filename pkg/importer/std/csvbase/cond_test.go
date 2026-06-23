package csvbase_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

// singleBool runs a pipeline that yields a single bool key and returns it.
func singleBool(t *testing.T, rec csvbase.RowContext, build func(*csvbase.Builder) csvbase.Key[bool]) (bool, *ast.Diagnostic) {
	t.Helper()
	b := csvbase.NewBuilder()
	k := build(b)
	var gotV bool
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		gotV, gotD = csvbase.Value(c, k)
		return nil, nil, nil
	})
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	return gotV, gotD
}

func amtConst(b *csvbase.Builder, num, hint string) csvbase.Key[*csvkit.Amount] {
	n, _, err := apd.BaseContext.SetString(new(apd.Decimal), num)
	if err != nil {
		panic(err)
	}
	return csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
		return &csvkit.Amount{Number: *n, CurrencyHint: hint}, nil, nil
	})
}

func nilAmt(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
	return csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
		return nil, nil, nil
	})
}

func emptyRow() csvbase.RowContext {
	return csvbase.RowContext{Fields: []string{}, Index: map[string]int{}}
}

// ---------------------------------------------------------------------------
// IsBlank / StrEqual / MatchRegexp
// ---------------------------------------------------------------------------

func TestIsBlank(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{{"   ", true}, {"", true}, {" x ", false}} {
		v, d := singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
			return csvbase.IsBlank(b, csvbase.Const(b, tc.in))
		})
		if d != nil {
			t.Fatalf("IsBlank(%q) diag: %v", tc.in, d)
		}
		if v != tc.want {
			t.Errorf("IsBlank(%q) = %v, want %v", tc.in, v, tc.want)
		}
	}
}

func TestIsBlank_SoftFailPropagates(t *testing.T) {
	_, d := singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
		return csvbase.IsBlank(b, csvbase.Require(b, csvbase.Const(b, ""), "up"))
	})
	if d == nil || d.Code != "up" {
		t.Errorf("IsBlank soft-fail = %v, want up", d)
	}
}

func TestStrEqual(t *testing.T) {
	v, d := singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
		return csvbase.StrEqual(b, csvbase.Const(b, "AB"), csvbase.Const(b, "AB"))
	})
	if d != nil || !v {
		t.Errorf("StrEqual(AB,AB) = %v,%v want true,nil", v, d)
	}
	v, _ = singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
		return csvbase.StrEqual(b, csvbase.Const(b, "AB"), csvbase.Const(b, "ab"))
	})
	if v {
		t.Error("StrEqual(AB,ab) = true, want false (exact)")
	}
}

func TestMatchRegexp(t *testing.T) {
	re := regexp.MustCompile(`^\d+$`)
	v, d := singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
		return csvbase.MatchRegexp(b, csvbase.Const(b, "1234"), re)
	})
	if d != nil || !v {
		t.Errorf("MatchRegexp digits = %v,%v want true,nil", v, d)
	}
	v, _ = singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
		return csvbase.MatchRegexp(b, csvbase.Const(b, "12a"), re)
	})
	if v {
		t.Error("MatchRegexp(12a) = true, want false")
	}
}

// ---------------------------------------------------------------------------
// And / Or / Not
// ---------------------------------------------------------------------------

func TestAndOrNot(t *testing.T) {
	tru := func(b *csvbase.Builder) csvbase.Key[bool] { return csvbase.IsBlank(b, csvbase.Const(b, "")) }
	fls := func(b *csvbase.Builder) csvbase.Key[bool] { return csvbase.IsBlank(b, csvbase.Const(b, "x")) }

	cases := []struct {
		name  string
		build func(*csvbase.Builder) csvbase.Key[bool]
		want  bool
	}{
		{"and-empty", func(b *csvbase.Builder) csvbase.Key[bool] { return csvbase.And(b) }, true},
		{"and-tt", func(b *csvbase.Builder) csvbase.Key[bool] { return csvbase.And(b, tru(b), tru(b)) }, true},
		{"and-tf", func(b *csvbase.Builder) csvbase.Key[bool] { return csvbase.And(b, tru(b), fls(b)) }, false},
		{"or-empty", func(b *csvbase.Builder) csvbase.Key[bool] { return csvbase.Or(b) }, false},
		{"or-ff", func(b *csvbase.Builder) csvbase.Key[bool] { return csvbase.Or(b, fls(b), fls(b)) }, false},
		{"or-ft", func(b *csvbase.Builder) csvbase.Key[bool] { return csvbase.Or(b, fls(b), tru(b)) }, true},
		{"not-t", func(b *csvbase.Builder) csvbase.Key[bool] { return csvbase.Not(b, tru(b)) }, false},
		{"not-f", func(b *csvbase.Builder) csvbase.Key[bool] { return csvbase.Not(b, fls(b)) }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, d := singleBool(t, emptyRow(), tc.build)
			if d != nil {
				t.Fatalf("%s diag: %v", tc.name, d)
			}
			if v != tc.want {
				t.Errorf("%s = %v, want %v", tc.name, v, tc.want)
			}
		})
	}
}

func TestAnd_SoftFailPropagates(t *testing.T) {
	_, d := singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
		bad := csvbase.IsBlank(b, csvbase.Require(b, csvbase.Const(b, ""), "andfail"))
		return csvbase.And(b, csvbase.IsBlank(b, csvbase.Const(b, "")), bad)
	})
	if d == nil || d.Code != "andfail" {
		t.Errorf("And soft-fail = %v, want andfail", d)
	}
}

// ---------------------------------------------------------------------------
// Amount sign predicates
// ---------------------------------------------------------------------------

func TestAmountSignPredicates(t *testing.T) {
	cases := []struct {
		name           string
		num            string
		neg, pos, zero bool
	}{
		{"negative", "-5", true, false, false},
		{"positive", "5", false, true, false},
		{"zero", "0", false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			callPred := func(f func(*csvbase.Builder, csvbase.Key[*csvkit.Amount]) csvbase.Key[bool]) bool {
				v, d := singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
					return f(b, amtConst(b, tc.num, "USD"))
				})
				if d != nil {
					t.Fatalf("diag: %v", d)
				}
				return v
			}
			if got := callPred(csvbase.IsNegative); got != tc.neg {
				t.Errorf("IsNegative(%s) = %v, want %v", tc.num, got, tc.neg)
			}
			if got := callPred(csvbase.IsPositive); got != tc.pos {
				t.Errorf("IsPositive(%s) = %v, want %v", tc.num, got, tc.pos)
			}
			if got := callPred(csvbase.IsZero); got != tc.zero {
				t.Errorf("IsZero(%s) = %v, want %v", tc.num, got, tc.zero)
			}
		})
	}
}

func TestAmountSign_NilIsFalse(t *testing.T) {
	v, d := singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
		return csvbase.IsNegative(b, nilAmt(b))
	})
	if d != nil || v {
		t.Errorf("IsNegative(nil) = %v,%v want false,nil", v, d)
	}
}

// ---------------------------------------------------------------------------
// Amount comparisons
// ---------------------------------------------------------------------------

func TestAmountCompare(t *testing.T) {
	v, d := singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
		return csvbase.AmountLess(b, amtConst(b, "3", "USD"), amtConst(b, "5", "USD"), "")
	})
	if d != nil || !v {
		t.Errorf("AmountLess(3,5) = %v,%v want true,nil", v, d)
	}
	v, _ = singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
		return csvbase.AmountGreater(b, amtConst(b, "3", ""), amtConst(b, "5", ""), "")
	})
	if v {
		t.Error("AmountGreater(3,5) = true, want false")
	}
	v, d = singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
		return csvbase.AmountEqual(b, amtConst(b, "5.0", "USD"), amtConst(b, "5", "USD"), "")
	})
	if d != nil || !v {
		t.Errorf("AmountEqual(5.0,5) = %v,%v want true,nil", v, d)
	}
}

func TestAmountCompare_ConflictingHintSoftFails(t *testing.T) {
	_, d := singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
		return csvbase.AmountLess(b, amtConst(b, "1", "JPY"), amtConst(b, "2", "EUR"), "cmp-conflict")
	})
	if d == nil || d.Code != "cmp-conflict" {
		t.Errorf("conflicting-hint compare diag = %v, want cmp-conflict", d)
	}
}

func TestAmountCompare_NilSoftFails(t *testing.T) {
	_, d := singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
		return csvbase.AmountEqual(b, nilAmt(b), amtConst(b, "1", ""), "")
	})
	if d == nil || d.Code != csvbase.DiagBadAmount {
		t.Errorf("nil compare diag = %v, want %s", d, csvbase.DiagBadAmount)
	}
}

// ---------------------------------------------------------------------------
// If
// ---------------------------------------------------------------------------

func TestIf_SelectsBranch(t *testing.T) {
	v, d := singleString(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[string] {
		cond := csvbase.IsBlank(b, csvbase.Const(b, "")) // true
		return csvbase.If(b, cond, csvbase.Const(b, "then"), csvbase.Const(b, "else"))
	})
	if d != nil || v != "then" {
		t.Errorf("If(true) = %q,%v want then,nil", v, d)
	}
	v, d = singleString(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[string] {
		cond := csvbase.IsBlank(b, csvbase.Const(b, "x")) // false
		return csvbase.If(b, cond, csvbase.Const(b, "then"), csvbase.Const(b, "else"))
	})
	if d != nil || v != "else" {
		t.Errorf("If(false) = %q,%v want else,nil", v, d)
	}
}

func TestIf_UntakenBranchSoftFailIgnored(t *testing.T) {
	// cond false -> picks else; the then branch soft-fails but must not surface.
	v, d := singleString(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[string] {
		cond := csvbase.IsBlank(b, csvbase.Const(b, "x")) // false
		bad := csvbase.Require(b, csvbase.Const(b, ""), "untaken")
		return csvbase.If(b, cond, bad, csvbase.Const(b, "ok"))
	})
	if d != nil {
		t.Fatalf("untaken soft-fail leaked: %v", d)
	}
	if v != "ok" {
		t.Errorf("If untaken = %q, want ok", v)
	}
}

func TestIf_TakenBranchSoftFailPropagates(t *testing.T) {
	v, d := singleString(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[string] {
		cond := csvbase.IsBlank(b, csvbase.Const(b, "")) // true
		bad := csvbase.Require(b, csvbase.Const(b, ""), "taken")
		return csvbase.If(b, cond, bad, csvbase.Const(b, "ok"))
	})
	if d == nil || d.Code != "taken" {
		t.Errorf("If taken soft-fail = %q,%v want taken", v, d)
	}
}

func TestIf_CondSoftFailPropagates(t *testing.T) {
	_, d := singleBool(t, emptyRow(), func(b *csvbase.Builder) csvbase.Key[bool] {
		cond := csvbase.Not(b, csvbase.IsBlank(b, csvbase.Require(b, csvbase.Const(b, ""), "condfail")))
		return csvbase.If(b, cond, csvbase.IsBlank(b, csvbase.Const(b, "")), csvbase.IsBlank(b, csvbase.Const(b, "")))
	})
	if d == nil || d.Code != "condfail" {
		t.Errorf("If cond soft-fail = %v, want condfail", d)
	}
}
