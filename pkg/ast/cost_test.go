package ast

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/syntax"
)

func TestLowerCostSpec_PerUnit(t *testing.T) {
	cst := syntax.Parse("2024-01-01 * \"Test\"\n  Assets:Bank 10 HOOL {100 USD}\n  Expenses:Other\n")
	txnNode := cst.Root.FindNode(syntax.TransactionDirective)
	if txnNode == nil {
		t.Fatal("no transaction found")
	}
	postingNodes := txnNode.FindAllNodes(syntax.PostingNode)
	if len(postingNodes) == 0 {
		t.Fatal("no posting found")
	}
	costNode := postingNodes[0].FindNode(syntax.CostSpecNode)
	if costNode == nil {
		t.Fatal("no cost spec found")
	}
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}
	cs, ok := l.lowerCostSpec(costNode)
	if !ok {
		t.Fatalf("lowerCostSpec failed: %v", l.file.Diagnostics)
	}
	if cs.IsTotal {
		t.Error("IsTotal = true, want false")
	}
	if cs.Amount == nil {
		t.Fatal("Amount is nil")
	}
	if got := cs.Amount.Number.String(); got != "100" {
		t.Errorf("Amount.Number = %q, want %q", got, "100")
	}
	if cs.Amount.Currency != "USD" {
		t.Errorf("Amount.Currency = %q, want %q", cs.Amount.Currency, "USD")
	}
	if cs.Date != nil {
		t.Errorf("Date = %v, want nil", cs.Date)
	}
	if cs.Label != "" {
		t.Errorf("Label = %q, want empty", cs.Label)
	}
}

func TestLowerCostSpec_Total(t *testing.T) {
	cst := syntax.Parse("2024-01-01 * \"Test\"\n  Assets:Bank 10 HOOL {{1000 USD}}\n  Expenses:Other\n")
	txnNode := cst.Root.FindNode(syntax.TransactionDirective)
	if txnNode == nil {
		t.Fatal("no transaction found")
	}
	postingNodes := txnNode.FindAllNodes(syntax.PostingNode)
	if len(postingNodes) == 0 {
		t.Fatal("no posting found")
	}
	costNode := postingNodes[0].FindNode(syntax.CostSpecNode)
	if costNode == nil {
		t.Fatal("no cost spec found")
	}
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}
	cs, ok := l.lowerCostSpec(costNode)
	if !ok {
		t.Fatalf("lowerCostSpec failed: %v", l.file.Diagnostics)
	}
	if !cs.IsTotal {
		t.Error("IsTotal = false, want true")
	}
	if cs.Amount == nil {
		t.Fatal("Amount is nil")
	}
	if got := cs.Amount.Number.String(); got != "1000" {
		t.Errorf("Amount.Number = %q, want %q", got, "1000")
	}
	if cs.Amount.Currency != "USD" {
		t.Errorf("Amount.Currency = %q, want %q", cs.Amount.Currency, "USD")
	}
}

func TestLowerCostSpec_WithDateAndLabel(t *testing.T) {
	cst := syntax.Parse("2024-01-01 * \"Test\"\n  Assets:Bank 10 HOOL {100 USD, 2023-06-15, \"lot1\"}\n  Expenses:Other\n")
	txnNode := cst.Root.FindNode(syntax.TransactionDirective)
	if txnNode == nil {
		t.Fatal("no transaction found")
	}
	postingNodes := txnNode.FindAllNodes(syntax.PostingNode)
	if len(postingNodes) == 0 {
		t.Fatal("no posting found")
	}
	costNode := postingNodes[0].FindNode(syntax.CostSpecNode)
	if costNode == nil {
		t.Fatal("no cost spec found")
	}
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}
	cs, ok := l.lowerCostSpec(costNode)
	if !ok {
		t.Fatalf("lowerCostSpec failed: %v", l.file.Diagnostics)
	}
	if cs.Amount == nil {
		t.Fatal("Amount is nil")
	}
	if got := cs.Amount.Number.String(); got != "100" {
		t.Errorf("Amount.Number = %q, want %q", got, "100")
	}
	if cs.Date == nil {
		t.Fatal("Date is nil")
	}
	if got := cs.Date.Format("2006-01-02"); got != "2023-06-15" {
		t.Errorf("Date = %q, want %q", got, "2023-06-15")
	}
	if cs.Label != "lot1" {
		t.Errorf("Label = %q, want %q", cs.Label, "lot1")
	}
}

func TestLowerCostSpec_Empty(t *testing.T) {
	cst := syntax.Parse("2024-01-01 * \"Test\"\n  Assets:Bank 10 HOOL {}\n  Expenses:Other\n")
	txnNode := cst.Root.FindNode(syntax.TransactionDirective)
	if txnNode == nil {
		t.Fatal("no transaction found")
	}
	postingNodes := txnNode.FindAllNodes(syntax.PostingNode)
	if len(postingNodes) == 0 {
		t.Fatal("no posting found")
	}
	costNode := postingNodes[0].FindNode(syntax.CostSpecNode)
	if costNode == nil {
		t.Fatal("no cost spec found")
	}
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}
	cs, ok := l.lowerCostSpec(costNode)
	if !ok {
		t.Fatalf("lowerCostSpec failed: %v", l.file.Diagnostics)
	}
	if cs.Amount != nil {
		t.Errorf("Amount = %v, want nil", cs.Amount)
	}
	if cs.IsTotal {
		t.Error("IsTotal = true, want false")
	}
}

func TestLowerCostSpec_EmptyTotal(t *testing.T) {
	cst := syntax.Parse("2024-01-01 * \"Test\"\n  Assets:Bank 10 HOOL {{}}\n  Expenses:Other\n")
	txnNode := cst.Root.FindNode(syntax.TransactionDirective)
	if txnNode == nil {
		t.Fatal("no transaction found")
	}
	postingNodes := txnNode.FindAllNodes(syntax.PostingNode)
	if len(postingNodes) == 0 {
		t.Fatal("no posting found")
	}
	costNode := postingNodes[0].FindNode(syntax.CostSpecNode)
	if costNode == nil {
		t.Fatal("no cost spec found")
	}
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}
	cs, ok := l.lowerCostSpec(costNode)
	if !ok {
		t.Fatalf("lowerCostSpec failed: %v", l.file.Diagnostics)
	}
	if cs.Amount != nil {
		t.Errorf("Amount = %v, want nil", cs.Amount)
	}
	if !cs.IsTotal {
		t.Error("IsTotal = false, want true")
	}
}

func TestLowerPriceAnnotation_PerUnit(t *testing.T) {
	cst := syntax.Parse("2024-01-01 * \"Test\"\n  Assets:Bank 100 USD @ 1.1 EUR\n  Expenses:Other\n")
	txnNode := cst.Root.FindNode(syntax.TransactionDirective)
	if txnNode == nil {
		t.Fatal("no transaction found")
	}
	postingNodes := txnNode.FindAllNodes(syntax.PostingNode)
	if len(postingNodes) == 0 {
		t.Fatal("no posting found")
	}
	priceNode := postingNodes[0].FindNode(syntax.PriceAnnotNode)
	if priceNode == nil {
		t.Fatal("no price annotation found")
	}
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}
	pa, ok := l.lowerPriceAnnotation(priceNode)
	if !ok {
		t.Fatalf("lowerPriceAnnotation failed: %v", l.file.Diagnostics)
	}
	if pa.IsTotal {
		t.Error("IsTotal = true, want false")
	}
	if got := pa.Amount.Number.String(); got != "1.1" {
		t.Errorf("Amount.Number = %q, want %q", got, "1.1")
	}
	if pa.Amount.Currency != "EUR" {
		t.Errorf("Amount.Currency = %q, want %q", pa.Amount.Currency, "EUR")
	}
}

func TestLowerPriceAnnotation_Total(t *testing.T) {
	cst := syntax.Parse("2024-01-01 * \"Test\"\n  Assets:Bank 100 USD @@ 110 EUR\n  Expenses:Other\n")
	txnNode := cst.Root.FindNode(syntax.TransactionDirective)
	if txnNode == nil {
		t.Fatal("no transaction found")
	}
	postingNodes := txnNode.FindAllNodes(syntax.PostingNode)
	if len(postingNodes) == 0 {
		t.Fatal("no posting found")
	}
	priceNode := postingNodes[0].FindNode(syntax.PriceAnnotNode)
	if priceNode == nil {
		t.Fatal("no price annotation found")
	}
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}
	pa, ok := l.lowerPriceAnnotation(priceNode)
	if !ok {
		t.Fatalf("lowerPriceAnnotation failed: %v", l.file.Diagnostics)
	}
	if !pa.IsTotal {
		t.Error("IsTotal = false, want true")
	}
	if got := pa.Amount.Number.String(); got != "110" {
		t.Errorf("Amount.Number = %q, want %q", got, "110")
	}
	if pa.Amount.Currency != "EUR" {
		t.Errorf("Amount.Currency = %q, want %q", pa.Amount.Currency, "EUR")
	}
}
