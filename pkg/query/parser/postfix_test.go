package parser_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/yugui/go-beancount/pkg/query/parser"
)

func attr(e parser.Expr, name string) *parser.AttributeAccess {
	return &parser.AttributeAccess{Expr: e, Attr: name}
}

func index(e, idx parser.Expr) *parser.IndexAccess {
	return &parser.IndexAccess{Expr: e, Index: idx}
}

func TestParsePostfix(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  parser.Expr
	}{
		{
			name:  "attribute access",
			input: "SELECT entry.narration",
			want:  attr(col("entry"), "narration"),
		},
		{
			name:  "nested attribute access",
			input: "SELECT a.b.c",
			want:  attr(attr(col("a"), "b"), "c"),
		},
		{
			name:  "subscript",
			input: "SELECT meta['k']",
			want:  index(col("meta"), strLit("k")),
		},
		{
			name:  "attribute then subscript",
			input: "SELECT open.meta['rate']",
			want:  index(attr(col("open"), "meta"), strLit("rate")),
		},
		{
			name:  "soft keyword open as column",
			input: "SELECT open.date",
			want:  attr(col("open"), "date"),
		},
		{
			name:  "postfix binds tighter than unary",
			input: "SELECT -x.y",
			want:  unary(parser.OpNeg, attr(col("x"), "y")),
		},
		{
			name:  "postfix binds tighter than add",
			input: "SELECT a.b + c",
			want:  bin(parser.OpAdd, attr(col("a"), "b"), col("c")),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.input, err)
			}
			if diff := cmp.Diff(tt.want, got.Targets[0].Expr, cmpOpts); diff != "" {
				t.Errorf("Parse(%q) mismatch (-want +got):\n%s", tt.input, diff)
			}
		})
	}
}

func TestParsePostfixSoftKeywordColumns(t *testing.T) {
	got, err := parser.Parse("SELECT open, close FROM accounts")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got.Targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(got.Targets))
	}
	if diff := cmp.Diff([]parser.Target{
		{Expr: col("open")}, {Expr: col("close")},
	}, got.Targets, cmpOpts); diff != "" {
		t.Errorf("targets mismatch (-want +got):\n%s", diff)
	}
}

func TestParsePostfixErrors(t *testing.T) {
	for _, input := range []string{
		"SELECT entry.",     // missing attribute name
		"SELECT entry.123",  // attribute must be an identifier
		"SELECT meta['k'",   // unterminated subscript
		"SELECT meta[]",     // empty subscript
		"SELECT .narration", // leading dot
	} {
		if _, err := parser.Parse(input); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", input)
		}
	}
}
