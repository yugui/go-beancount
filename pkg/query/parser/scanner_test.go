package parser_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/yugui/go-beancount/pkg/query/parser"
)

// firstTarget parses input and returns the single target expression, failing
// the test on parse error or an unexpected target count.
func firstTarget(t *testing.T, input string) parser.Expr {
	t.Helper()
	got, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("Parse(%q): %v", input, err)
	}
	if len(got.Targets) != 1 {
		t.Fatalf("Parse(%q): expected 1 target, got %d", input, len(got.Targets))
	}
	return got.Targets[0].Expr
}

func TestScanDateVsSubtraction(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  parser.Expr
	}{
		{
			name:  "date literal",
			input: "SELECT 2020-01-01",
			want:  &parser.DateLit{Value: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
		},
		{
			name:  "subtraction of integers",
			input: "SELECT 2020 - 01 - 01",
			want: &parser.Binary{
				Op: parser.OpSub,
				L: &parser.Binary{
					Op: parser.OpSub,
					L:  &parser.IntLit{Value: 2020},
					R:  &parser.IntLit{Value: 1},
				},
				R: &parser.IntLit{Value: 1},
			},
		},
		{
			name:  "column subtraction",
			input: "SELECT a - b",
			want: &parser.Binary{
				Op: parser.OpSub,
				L:  &parser.ColumnRef{Name: "a"},
				R:  &parser.ColumnRef{Name: "b"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstTarget(t, tt.input)
			if diff := cmp.Diff(tt.want, got, cmpOpts); diff != "" {
				t.Errorf("Parse(%q) (-want +got):\n%s", tt.input, diff)
			}
		})
	}
}

func TestScanStarTargetVsMultiply(t *testing.T) {
	star, err := parser.Parse("SELECT *")
	if err != nil {
		t.Fatalf("Parse(SELECT *): %v", err)
	}
	if !star.Star || len(star.Targets) != 0 {
		t.Errorf("SELECT * = %+v, want Star with no targets", star)
	}

	mul := firstTarget(t, "SELECT a * b")
	want := &parser.Binary{Op: parser.OpMul, L: &parser.ColumnRef{Name: "a"}, R: &parser.ColumnRef{Name: "b"}}
	if diff := cmp.Diff(want, mul, cmpOpts); diff != "" {
		t.Errorf("SELECT a * b (-want +got):\n%s", diff)
	}
}

func TestScanStringEscapes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "single quoted", input: `SELECT 'hello'`, want: "hello"},
		{name: "double quoted", input: `SELECT "hello"`, want: "hello"},
		{name: "doubled single quote", input: `SELECT 'it''s'`, want: "it's"},
		{name: "doubled double quote", input: `SELECT "say ""hi"""`, want: `say "hi"`},
		{name: "backslash escapes quote", input: `SELECT 'a\'b'`, want: "a'b"},
		{name: "backslash escapes backslash", input: `SELECT 'a\\b'`, want: `a\b`},
		{name: "double quote inside single", input: `SELECT 'a"b'`, want: `a"b`},
		{name: "empty string", input: `SELECT ''`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstTarget(t, tt.input)
			lit, ok := got.(*parser.StringLit)
			if !ok {
				t.Fatalf("Parse(%q) target = %T, want *StringLit", tt.input, got)
			}
			if lit.Value != tt.want {
				t.Errorf("Parse(%q) value = %q, want %q", tt.input, lit.Value, tt.want)
			}
		})
	}
}

func TestScanIntVsDecimal(t *testing.T) {
	if _, ok := firstTarget(t, "SELECT 42").(*parser.IntLit); !ok {
		t.Errorf("42 did not parse as IntLit")
	}
	if _, ok := firstTarget(t, "SELECT 4.20").(*parser.DecimalLit); !ok {
		t.Errorf("4.20 did not parse as DecimalLit")
	}
	if _, ok := firstTarget(t, "SELECT .5").(*parser.DecimalLit); !ok {
		t.Errorf(".5 did not parse as DecimalLit")
	}
	if _, ok := firstTarget(t, "SELECT 10.").(*parser.DecimalLit); !ok {
		t.Errorf("10. did not parse as DecimalLit")
	}
}

func TestPositionTracking(t *testing.T) {
	// A multi-line query: the WHERE column ref must report line 2.
	const input = "SELECT account\nWHERE number > 0"
	got, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	where, ok := got.Where.(*parser.Binary)
	if !ok {
		t.Fatalf("Where = %T, want *Binary", got.Where)
	}
	if pos := where.L.Pos(); pos.Line != 2 {
		t.Errorf("number ref at line %d, want 2 (pos %+v)", pos.Line, pos)
	}
}

func TestOpStrings(t *testing.T) {
	binCases := map[parser.BinaryOp]string{
		parser.OpOr: "OR", parser.OpAnd: "AND", parser.OpAdd: "+", parser.OpSub: "-",
		parser.OpMul: "*", parser.OpDiv: "/", parser.OpMod: "%", parser.OpEq: "=",
		parser.OpNe: "!=", parser.OpLt: "<", parser.OpLe: "<=", parser.OpGt: ">",
		parser.OpGe: ">=", parser.OpMatch: "~",
	}
	for op, want := range binCases {
		if got := op.String(); got != want {
			t.Errorf("BinaryOp(%d).String() = %q, want %q", op, got, want)
		}
	}
	unaryCases := map[parser.UnaryOp]string{
		parser.OpNeg: "-", parser.OpPos: "+", parser.OpNot: "NOT",
	}
	for op, want := range unaryCases {
		if got := op.String(); got != want {
			t.Errorf("UnaryOp(%d).String() = %q, want %q", op, got, want)
		}
	}
}
