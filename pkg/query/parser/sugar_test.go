package parser_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/yugui/go-beancount/pkg/query/parser"
)

func fn(name string, args ...parser.Expr) *parser.FuncCall {
	return &parser.FuncCall{Name: name, Args: args}
}

// TestParseOperatorSugar covers the expression-level operators that desugar to,
// or extend, the comparison level: BETWEEN, NOT BETWEEN, NOT IN, IS [NOT] NULL.
func TestParseOperatorSugar(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  parser.Expr
	}{
		{
			name:  "between desugars to a conjunction",
			input: "SELECT a BETWEEN 1 AND 2",
			want: bin(parser.OpAnd,
				bin(parser.OpGe, col("a"), intLit(1)),
				bin(parser.OpLe, col("a"), intLit(2))),
		},
		{
			name:  "not between desugars to a disjunction",
			input: "SELECT a NOT BETWEEN 1 AND 2",
			want: bin(parser.OpOr,
				bin(parser.OpLt, col("a"), intLit(1)),
				bin(parser.OpGt, col("a"), intLit(2))),
		},
		{
			name:  "between bound AND does not consume trailing boolean AND",
			input: "SELECT a BETWEEN 1 AND 2 AND b",
			want: bin(parser.OpAnd,
				bin(parser.OpAnd,
					bin(parser.OpGe, col("a"), intLit(1)),
					bin(parser.OpLe, col("a"), intLit(2))),
				col("b")),
		},
		{
			name:  "not in carries the negation flag",
			input: "SELECT a NOT IN (1, 2)",
			want: &parser.In{
				X:    col("a"),
				List: &parser.ListLit{Elems: []parser.Expr{intLit(1), intLit(2)}},
				Neg:  true,
			},
		},
		{
			name:  "is null",
			input: "SELECT a IS NULL",
			want:  &parser.IsNull{X: col("a")},
		},
		{
			name:  "is not null",
			input: "SELECT a IS NOT NULL",
			want:  &parser.IsNull{X: col("a"), Neg: true},
		},
		{
			name:  "is null binds looser than add",
			input: "SELECT a + b IS NULL",
			want:  &parser.IsNull{X: bin(parser.OpAdd, col("a"), col("b"))},
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

func TestParseJournalDesugar(t *testing.T) {
	journalTargets := func(summary string) []parser.Target {
		var pos, bal parser.Expr = col("position"), col("balance")
		if summary != "" {
			pos = fn(summary, col("position"))
			bal = fn(summary, col("balance"))
		}
		return []parser.Target{
			{Expr: col("date")},
			{Expr: col("flag")},
			{Expr: fn("maxwidth", col("payee"), intLit(48))},
			{Expr: fn("maxwidth", col("narration"), intLit(80))},
			{Expr: col("account")},
			{Expr: pos},
			{Expr: bal},
		}
	}

	tests := []struct {
		name  string
		input string
		want  *parser.Select
	}{
		{
			name:  "bare journal",
			input: "JOURNAL",
			want:  &parser.Select{Targets: journalTargets("")},
		},
		{
			name:  "journal with account regex",
			input: `JOURNAL "Assets:Cash"`,
			want: &parser.Select{
				Targets: journalTargets(""),
				Where:   bin(parser.OpMatch, col("account"), strLit("Assets:Cash")),
			},
		},
		{
			name:  "journal at cost",
			input: "JOURNAL AT cost",
			want:  &parser.Select{Targets: journalTargets("cost")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.input, err)
			}
			if diff := cmp.Diff(tt.want, got, cmpOpts); diff != "" {
				t.Errorf("Parse(%q) mismatch (-want +got):\n%s", tt.input, diff)
			}
		})
	}
}

func TestParseBalancesDesugar(t *testing.T) {
	sortkey := fn("account_sortkey", col("account"))
	balances := func(sumArg parser.Expr) *parser.Select {
		return &parser.Select{
			Targets: []parser.Target{
				{Expr: col("account")},
				{Expr: fn("sum", sumArg)},
			},
			GroupBy: []parser.Expr{col("account"), sortkey},
			OrderBy: []parser.OrderItem{{Expr: sortkey}},
		}
	}

	tests := []struct {
		name  string
		input string
		want  *parser.Select
	}{
		{
			name:  "bare balances sums position",
			input: "BALANCES",
			want:  balances(col("position")),
		},
		{
			name:  "balances at cost sums cost(position)",
			input: "BALANCES AT cost",
			want:  balances(fn("cost", col("position"))),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.input, err)
			}
			if diff := cmp.Diff(tt.want, got, cmpOpts); diff != "" {
				t.Errorf("Parse(%q) mismatch (-want +got):\n%s", tt.input, diff)
			}
		})
	}
}

// TestParseContextualKeywordsNotReserved guards that the new statement and AT
// keywords stay usable as ordinary table/column identifiers.
func TestParseContextualKeywordsNotReserved(t *testing.T) {
	tests := []string{
		"SELECT * FROM balances",
		"SELECT at FROM postings",
		"SELECT journal FROM postings",
		"SELECT account WHERE balances = 1",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, err := parser.Parse(input); err != nil {
				t.Errorf("Parse(%q): unexpected error: %v", input, err)
			}
		})
	}
}

func TestParseSugarErrors(t *testing.T) {
	tests := []string{
		"SELECT a BETWEEN 1",            // missing AND hi
		"SELECT a IS 5",                 // IS without NULL
		"SELECT a NOT b",                // NOT not followed by IN/BETWEEN
		"SELECT a = b BETWEEN 1 AND 2",  // chained comparison
		"SELECT a BETWEEN 1 AND 2 IN c", // chained comparison
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, err := parser.Parse(input); err == nil {
				t.Errorf("Parse(%q): expected error, got nil", input)
			}
		})
	}
}
