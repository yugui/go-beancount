package parser_test

import (
	"errors"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"

	"github.com/yugui/go-beancount/pkg/query/parser"
)

// ignorePos drops every Position field so structural comparisons focus on tree
// shape and values; positions are asserted separately in the error tests.
var ignorePos = cmp.FilterPath(func(p cmp.Path) bool {
	return p.Last().String() == ".Pos" || p.Last().String() == ".Position"
}, cmp.Ignore())

// apdEqual compares apd.Decimal by exact value, since go-cmp cannot reflect
// across its unexported fields.
var apdEqual = cmp.Comparer(func(a, b apd.Decimal) bool { return a.Cmp(&b) == 0 })

// cmpOpts is the option set for every structural tree comparison in this
// package's tests.
var cmpOpts = cmp.Options{ignorePos, apdEqual}

func col(name string) *parser.ColumnRef { return &parser.ColumnRef{Name: name} }

func intLit(v int64) *parser.IntLit { return &parser.IntLit{Value: v} }

func strLit(v string) *parser.StringLit { return &parser.StringLit{Value: v} }

func decLit(t *testing.T, s string) *parser.DecimalLit {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString(s); err != nil {
		t.Fatalf("apd SetString(%q): %v", s, err)
	}
	return &parser.DecimalLit{Value: d}
}

func bin(op parser.BinaryOp, l, r parser.Expr) *parser.Binary {
	return &parser.Binary{Op: op, L: l, R: r}
}

func unary(op parser.UnaryOp, x parser.Expr) *parser.Unary {
	return &parser.Unary{Op: op, X: x}
}

func TestParseStructural(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  *parser.Select
	}{
		{
			name:  "select star",
			input: "SELECT *",
			want:  &parser.Select{Star: true},
		},
		{
			name:  "distinct star",
			input: "SELECT DISTINCT *",
			want:  &parser.Select{Distinct: true, Star: true},
		},
		{
			name:  "case insensitive keywords",
			input: "select Distinct *",
			want:  &parser.Select{Distinct: true, Star: true},
		},
		{
			name:  "single target column",
			input: "SELECT account",
			want: &parser.Select{
				Targets: []parser.Target{{Expr: col("account")}},
			},
		},
		{
			name:  "target with alias",
			input: "SELECT account AS acct",
			want: &parser.Select{
				Targets: []parser.Target{{Expr: col("account"), As: "acct"}},
			},
		},
		{
			name:  "multiple targets",
			input: "SELECT account, number, currency",
			want: &parser.Select{
				Targets: []parser.Target{
					{Expr: col("account")},
					{Expr: col("number")},
					{Expr: col("currency")},
				},
			},
		},
		{
			name:  "func call zero args",
			input: "SELECT now()",
			want: &parser.Select{
				Targets: []parser.Target{{Expr: &parser.FuncCall{Name: "now"}}},
			},
		},
		{
			name:  "func call one arg",
			input: "SELECT year(date)",
			want: &parser.Select{
				Targets: []parser.Target{{Expr: &parser.FuncCall{Name: "year", Args: []parser.Expr{col("date")}}}},
			},
		},
		{
			name:  "func call many args",
			input: "SELECT getitem(meta, 'k', 'def')",
			want: &parser.Select{
				Targets: []parser.Target{{Expr: &parser.FuncCall{
					Name: "getitem",
					Args: []parser.Expr{col("meta"), strLit("k"), strLit("def")},
				}}},
			},
		},
		{
			name:  "aggregate with alias",
			input: "SELECT sum(position) AS total",
			want: &parser.Select{
				Targets: []parser.Target{{
					Expr: &parser.FuncCall{Name: "sum", Args: []parser.Expr{col("position")}},
					As:   "total",
				}},
			},
		},
		{
			name:  "from bare name",
			input: "SELECT * FROM postings",
			want: &parser.Select{
				Star: true,
				From: &parser.FromClause{Expr: col("postings"), Name: "postings", IsBareName: true},
			},
		},
		{
			name:  "from expression",
			input: "SELECT * FROM date >= 2020-01-01",
			want: &parser.Select{
				Star: true,
				From: &parser.FromClause{
					Expr: bin(parser.OpGe, col("date"), &parser.DateLit{
						Value: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
					}),
				},
			},
		},
		{
			name:  "where clause",
			input: "SELECT account WHERE number > 0",
			want: &parser.Select{
				Targets: []parser.Target{{Expr: col("account")}},
				Where:   bin(parser.OpGt, col("number"), intLit(0)),
			},
		},
		{
			name:  "group by",
			input: "SELECT account GROUP BY account, currency",
			want: &parser.Select{
				Targets: []parser.Target{{Expr: col("account")}},
				GroupBy: []parser.Expr{col("account"), col("currency")},
			},
		},
		{
			name:  "order by default asc",
			input: "SELECT account ORDER BY account",
			want: &parser.Select{
				Targets: []parser.Target{{Expr: col("account")}},
				OrderBy: []parser.OrderItem{{Expr: col("account"), Desc: false}},
			},
		},
		{
			name:  "order by explicit directions",
			input: "SELECT account ORDER BY account ASC, number DESC",
			want: &parser.Select{
				Targets: []parser.Target{{Expr: col("account")}},
				OrderBy: []parser.OrderItem{
					{Expr: col("account"), Desc: false},
					{Expr: col("number"), Desc: true},
				},
			},
		},
		{
			name:  "limit",
			input: "SELECT * LIMIT 10",
			want: &parser.Select{
				Star:  true,
				Limit: ptr(int64(10)),
			},
		},
		{
			name:  "trailing semicolon",
			input: "SELECT * ;",
			want:  &parser.Select{Star: true},
		},
		{
			name:  "in list",
			input: "SELECT * WHERE account IN ('A', 'B', 'C')",
			want: &parser.Select{
				Star: true,
				Where: &parser.In{
					X: col("account"),
					List: &parser.ListLit{Elems: []parser.Expr{
						strLit("A"), strLit("B"), strLit("C"),
					}},
				},
			},
		},
		{
			name:  "parenthesized expr is not a list",
			input: "SELECT (1 + 2) * 3",
			want: &parser.Select{
				Targets: []parser.Target{{
					Expr: bin(parser.OpMul, bin(parser.OpAdd, intLit(1), intLit(2)), intLit(3)),
				}},
			},
		},
		{
			name:  "literals",
			input: "SELECT 42, 4.20, 'hi', 2020-01-01, TRUE, FALSE, NULL",
			want: &parser.Select{
				Targets: []parser.Target{
					{Expr: intLit(42)},
					{Expr: decLit(t, "4.20")},
					{Expr: strLit("hi")},
					{Expr: &parser.DateLit{Value: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}},
					{Expr: &parser.BoolLit{Value: true}},
					{Expr: &parser.BoolLit{Value: false}},
					{Expr: &parser.NullLit{}},
				},
			},
		},
		{
			name:  "decimal forms",
			input: "SELECT .5, 10.",
			want: &parser.Select{
				Targets: []parser.Target{
					{Expr: decLit(t, ".5")},
					{Expr: decLit(t, "10.")},
				},
			},
		},
		{
			name: "full query",
			input: "SELECT account, sum(position) AS total " +
				"FROM year >= 2020 WHERE not flag = '*' " +
				"GROUP BY account ORDER BY total DESC LIMIT 10",
			want: &parser.Select{
				Targets: []parser.Target{
					{Expr: col("account")},
					{Expr: &parser.FuncCall{Name: "sum", Args: []parser.Expr{col("position")}}, As: "total"},
				},
				From: &parser.FromClause{Expr: bin(parser.OpGe, col("year"), intLit(2020))},
				Where: unary(parser.OpNot,
					bin(parser.OpEq, col("flag"), strLit("*"))),
				GroupBy: []parser.Expr{col("account")},
				OrderBy: []parser.OrderItem{{Expr: col("total"), Desc: true}},
				Limit:   ptr(int64(10)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.input, err)
			}
			if diff := cmp.Diff(tt.want, got, cmpOpts); diff != "" {
				t.Errorf("Parse(%q) tree mismatch (-want +got):\n%s", tt.input, diff)
			}
		})
	}
}

func TestParsePrecedence(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  parser.Expr
	}{
		{
			name:  "and binds tighter than or",
			input: "SELECT a AND b OR c",
			want:  bin(parser.OpOr, bin(parser.OpAnd, col("a"), col("b")), col("c")),
		},
		{
			name:  "unary neg binds tighter than add",
			input: "SELECT -x + y",
			want:  bin(parser.OpAdd, unary(parser.OpNeg, col("x")), col("y")),
		},
		{
			name:  "match binds tighter than and",
			input: "SELECT a ~ 'r' AND b",
			want:  bin(parser.OpAnd, bin(parser.OpMatch, col("a"), strLit("r")), col("b")),
		},
		{
			name:  "mul binds tighter than add",
			input: "SELECT 2 + 3 * 4",
			want:  bin(parser.OpAdd, intLit(2), bin(parser.OpMul, intLit(3), intLit(4))),
		},
		{
			name:  "add is left associative",
			input: "SELECT a - b - c",
			want:  bin(parser.OpSub, bin(parser.OpSub, col("a"), col("b")), col("c")),
		},
		{
			name:  "mul and div left associative same precedence",
			input: "SELECT a / b * c",
			want:  bin(parser.OpMul, bin(parser.OpDiv, col("a"), col("b")), col("c")),
		},
		{
			name:  "not is prefix over comparison",
			input: "SELECT NOT a = b",
			want:  unary(parser.OpNot, bin(parser.OpEq, col("a"), col("b"))),
		},
		{
			name:  "comparison binds tighter than not arg but looser than add",
			input: "SELECT a + b > c",
			want:  bin(parser.OpGt, bin(parser.OpAdd, col("a"), col("b")), col("c")),
		},
		{
			name:  "double negation",
			input: "SELECT - - x",
			want:  unary(parser.OpNeg, unary(parser.OpNeg, col("x"))),
		},
		{
			name:  "in binds at comparison level",
			input: "SELECT a IN (1, 2) AND b",
			want: bin(parser.OpAnd,
				&parser.In{X: col("a"), List: &parser.ListLit{Elems: []parser.Expr{intLit(1), intLit(2)}}},
				col("b")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.input, err)
			}
			if len(got.Targets) != 1 {
				t.Fatalf("Parse(%q): expected 1 target, got %d", tt.input, len(got.Targets))
			}
			if diff := cmp.Diff(tt.want, got.Targets[0].Expr, cmpOpts); diff != "" {
				t.Errorf("Parse(%q) expr mismatch (-want +got):\n%s", tt.input, diff)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantPos parser.Position
	}{
		{
			name:    "empty input",
			input:   "",
			wantPos: parser.Position{Offset: 0, Line: 1, Column: 1},
		},
		{
			name:    "missing from expr",
			input:   "SELECT * FROM",
			wantPos: parser.Position{Offset: 13, Line: 1, Column: 14},
		},
		{
			name:    "unclosed paren",
			input:   "SELECT (1 + 2",
			wantPos: parser.Position{Offset: 13, Line: 1, Column: 14},
		},
		{
			name:    "unexpected token at primary",
			input:   "SELECT ,",
			wantPos: parser.Position{Offset: 7, Line: 1, Column: 8},
		},
		{
			name:    "trailing garbage",
			input:   "SELECT * foo",
			wantPos: parser.Position{Offset: 9, Line: 1, Column: 10},
		},
		{
			name:    "chained comparison",
			input:   "SELECT a = b = c",
			wantPos: parser.Position{Offset: 13, Line: 1, Column: 14},
		},
		{
			name:    "unterminated string",
			input:   "SELECT 'abc",
			wantPos: parser.Position{Offset: 7, Line: 1, Column: 8},
		},
		{
			name:    "missing select",
			input:   "FROM postings",
			wantPos: parser.Position{Offset: 0, Line: 1, Column: 1},
		},
		{
			name:    "alias not identifier",
			input:   "SELECT a AS 1",
			wantPos: parser.Position{Offset: 12, Line: 1, Column: 13},
		},
		{
			name:    "missing by after group",
			input:   "SELECT * GROUP account",
			wantPos: parser.Position{Offset: 15, Line: 1, Column: 16},
		},
		{
			name:    "limit not integer",
			input:   "SELECT * LIMIT 1.5",
			wantPos: parser.Position{Offset: 15, Line: 1, Column: 16},
		},
		{
			name:    "lone bang",
			input:   "SELECT a ! b",
			wantPos: parser.Position{Offset: 9, Line: 1, Column: 10},
		},
		{
			name:    "calendar invalid date",
			input:   "SELECT 2020-13-40",
			wantPos: parser.Position{Offset: 7, Line: 1, Column: 8},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.Parse(tt.input)
			if err == nil {
				t.Fatalf("Parse(%q) = %+v, want error", tt.input, got)
			}
			var perr *parser.Error
			if !errors.As(err, &perr) {
				t.Fatalf("Parse(%q) error %v is not *parser.Error", tt.input, err)
			}
			if perr.Pos != tt.wantPos {
				t.Errorf("Parse(%q) error at %+v, want %+v (msg: %s)", tt.input, perr.Pos, tt.wantPos, perr.Msg)
			}
		})
	}
}

func TestErrorString(t *testing.T) {
	err := &parser.Error{Pos: parser.Position{Offset: 5, Line: 2, Column: 3}, Msg: "boom"}
	if got, want := err.Error(), "2:3: boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// TestParseNoPanic asserts robustness: a corpus of malformed inputs must each
// return an error without panicking.
func TestParseNoPanic(t *testing.T) {
	inputs := []string{
		"", " ", ";", "SELECT", "SELECT * FROM", "SELECT (",
		"SELECT ((((", "SELECT ))))", "SELECT 'unterminated", `SELECT "`,
		"SELECT a IN", "SELECT a IN (", "SELECT not", "SELECT - ",
		"SELECT a +", "SELECT a,", "SELECT * WHERE", "SELECT * GROUP BY",
		"SELECT * ORDER BY", "SELECT * LIMIT", "SELECT 99999999999999999999999",
		"SELECT \\", "SELECT @", "SELECT a ~ ", "SELECT date >= 2020-01-",
		"SELECT * FROM WHERE", "select select select",
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Parse(%q) panicked: %v", in, r)
				}
			}()
			if _, err := parser.Parse(in); err == nil {
				t.Errorf("Parse(%q) = nil error, want error for malformed input", in)
			}
		}()
	}
}
