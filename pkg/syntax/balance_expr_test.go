package syntax

import (
	"strings"
	"testing"
)

func TestParseBalanceAmount_Simple(t *testing.T) {
	node, errs := ParseBalanceAmount("100 USD")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if node == nil {
		t.Fatal("ParseBalanceAmount: nil node")
	}
	if node.Kind != BalanceAmountNode {
		t.Errorf("Kind = %v, want BalanceAmountNode", node.Kind)
	}
	if len(node.FindAllNodes(ArithExprNode)) != 1 {
		t.Errorf("got %d ArithExprNode children, want 1", len(node.FindAllNodes(ArithExprNode)))
	}
	if node.FindToken(CURRENCY) == nil {
		t.Error("missing CURRENCY token")
	}
}

func TestParseBalanceAmount_Tolerance(t *testing.T) {
	node, errs := ParseBalanceAmount("100 ~ 1 USD")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got := len(node.FindAllNodes(ArithExprNode)); got != 2 {
		t.Errorf("ArithExprNode count = %d, want 2", got)
	}
	if node.FindToken(TILDE) == nil {
		t.Error("missing TILDE token")
	}
}

func TestParseBalanceAmount_TrailingInput(t *testing.T) {
	src := "100 USD junk"
	node, errs := ParseBalanceAmount(src)
	if node == nil {
		t.Fatal("nil node")
	}
	if len(errs) == 0 {
		t.Fatal("expected at least one error, got none")
	}
	// At least one error should reference the trailing token offset (start of "junk").
	off := strings.Index(src, "junk")
	hasTrailing := false
	for _, e := range errs {
		if e.Pos == off {
			hasTrailing = true
			break
		}
	}
	if !hasTrailing {
		t.Errorf("no error reported at offset %d (start of trailing input); errors: %v", off, errs)
	}
}

func TestParseAmountExpression_Simple(t *testing.T) {
	node, errs := ParseAmountExpression("1,234.56 EUR")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if node.Kind != AmountNode {
		t.Errorf("Kind = %v, want AmountNode", node.Kind)
	}
	if node.FindToken(CURRENCY) == nil {
		t.Error("missing CURRENCY token")
	}
}

func TestParseAmountExpression_TrailingInput(t *testing.T) {
	src := "100 USD trailing"
	node, errs := ParseAmountExpression(src)
	if node == nil {
		t.Fatal("nil node")
	}
	if len(errs) == 0 {
		t.Fatal("expected trailing-input error")
	}
	off := strings.Index(src, "trailing")
	hasTrailing := false
	for _, e := range errs {
		if e.Pos == off {
			hasTrailing = true
			break
		}
	}
	if !hasTrailing {
		t.Errorf("no error reported at offset %d (start of trailing input); errors: %v", off, errs)
	}
}
