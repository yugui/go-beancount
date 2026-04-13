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
	if cs.Total != nil {
		t.Errorf("Total = %v, want nil", cs.Total)
	}
	if cs.PerUnit == nil {
		t.Fatal("PerUnit is nil")
	}
	if got := cs.PerUnit.Number.String(); got != "100" {
		t.Errorf("PerUnit.Number = %q, want %q", got, "100")
	}
	if cs.PerUnit.Currency != "USD" {
		t.Errorf("PerUnit.Currency = %q, want %q", cs.PerUnit.Currency, "USD")
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
	if cs.PerUnit != nil {
		t.Errorf("PerUnit = %v, want nil", cs.PerUnit)
	}
	if cs.Total == nil {
		t.Fatal("Total is nil")
	}
	if got := cs.Total.Number.String(); got != "1000" {
		t.Errorf("Total.Number = %q, want %q", got, "1000")
	}
	if cs.Total.Currency != "USD" {
		t.Errorf("Total.Currency = %q, want %q", cs.Total.Currency, "USD")
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
	if cs.PerUnit == nil {
		t.Fatal("PerUnit is nil")
	}
	if got := cs.PerUnit.Number.String(); got != "100" {
		t.Errorf("PerUnit.Number = %q, want %q", got, "100")
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

func TestLowerCostSpec_TotalWithDateAndLabel(t *testing.T) {
	cst := syntax.Parse("2024-01-01 * \"Test\"\n  Assets:Bank 10 HOOL {{1000 USD, 2023-06-15, \"lot1\"}}\n  Expenses:Other\n")
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
	if cs.PerUnit != nil {
		t.Errorf("PerUnit = %v, want nil", cs.PerUnit)
	}
	if cs.Total == nil {
		t.Fatal("Total is nil")
	}
	if got := cs.Total.Number.String(); got != "1000" {
		t.Errorf("Total.Number = %q, want %q", got, "1000")
	}
	if cs.Total.Currency != "USD" {
		t.Errorf("Total.Currency = %q, want %q", cs.Total.Currency, "USD")
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
	if cs.PerUnit != nil {
		t.Errorf("PerUnit = %v, want nil", cs.PerUnit)
	}
	if cs.Total != nil {
		t.Errorf("Total = %v, want nil", cs.Total)
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
	// {{}} parses to an empty CostSpec; both PerUnit and Total are nil.
	// The total-form brace is not preserved across lower/print: the printer
	// normalizes the empty form back to "{}".
	if cs.PerUnit != nil {
		t.Errorf("PerUnit = %v, want nil", cs.PerUnit)
	}
	if cs.Total != nil {
		t.Errorf("Total = %v, want nil", cs.Total)
	}
}

// parseCostNodeForTest parses the given source and returns the parsed file
// along with the first CostSpecNode found in the first posting of the first
// transaction. It fails the test if any of those are missing.
//
// Note: the `{{...#...}}` rejection path in lowerCostSpec is not unit-tested
// here because the parser prevents that input from reaching the lowerer.
func parseCostNodeForTest(t *testing.T, src string) (*syntax.File, *syntax.Node) {
	t.Helper()
	cst := syntax.Parse(src)
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
	return cst, costNode
}

func TestLowerCostSpec_Combined(t *testing.T) {
	_, costNode := parseCostNodeForTest(t, "2024-01-01 * \"Test\"\n  Assets:Bank 10 HOOL {502.12 # 9.95 USD}\n  Expenses:Other\n")
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}
	cs, ok := l.lowerCostSpec(costNode)
	if !ok {
		t.Fatalf("lowerCostSpec failed: %v", l.file.Diagnostics)
	}
	if cs.PerUnit == nil {
		t.Fatal("PerUnit is nil")
	}
	if got := cs.PerUnit.Number.String(); got != "502.12" {
		t.Errorf("PerUnit.Number = %q, want %q", got, "502.12")
	}
	if cs.PerUnit.Currency != "USD" {
		t.Errorf("PerUnit.Currency = %q, want %q (inherited)", cs.PerUnit.Currency, "USD")
	}
	if cs.Total == nil {
		t.Fatal("Total is nil")
	}
	if got := cs.Total.Number.String(); got != "9.95" {
		t.Errorf("Total.Number = %q, want %q", got, "9.95")
	}
	if cs.Total.Currency != "USD" {
		t.Errorf("Total.Currency = %q, want %q", cs.Total.Currency, "USD")
	}
}

func TestLowerCostSpec_CombinedExplicitPerUnitCurrency(t *testing.T) {
	_, costNode := parseCostNodeForTest(t, "2024-01-01 * \"Test\"\n  Assets:Bank 10 HOOL {502.12 USD # 9.95 USD}\n  Expenses:Other\n")
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}
	cs, ok := l.lowerCostSpec(costNode)
	if !ok {
		t.Fatalf("lowerCostSpec failed: %v", l.file.Diagnostics)
	}
	if cs.PerUnit == nil || cs.Total == nil {
		t.Fatalf("PerUnit=%v Total=%v, want both set", cs.PerUnit, cs.Total)
	}
	if cs.PerUnit.Currency != "USD" {
		t.Errorf("PerUnit.Currency = %q, want %q", cs.PerUnit.Currency, "USD")
	}
	if cs.Total.Currency != "USD" {
		t.Errorf("Total.Currency = %q, want %q", cs.Total.Currency, "USD")
	}
	if got := cs.PerUnit.Number.String(); got != "502.12" {
		t.Errorf("PerUnit.Number = %q, want %q", got, "502.12")
	}
	if got := cs.Total.Number.String(); got != "9.95" {
		t.Errorf("Total.Number = %q, want %q", got, "9.95")
	}
}

func TestLowerCostSpec_CombinedWithDateAndLabel(t *testing.T) {
	_, costNode := parseCostNodeForTest(t, "2024-01-01 * \"Test\"\n  Assets:Bank 10 HOOL {502.12 # 9.95 USD, 2024-01-15, \"lot1\"}\n  Expenses:Other\n")
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}
	cs, ok := l.lowerCostSpec(costNode)
	if !ok {
		t.Fatalf("lowerCostSpec failed: %v", l.file.Diagnostics)
	}
	if cs.PerUnit == nil || cs.Total == nil {
		t.Fatalf("PerUnit=%v Total=%v, want both set", cs.PerUnit, cs.Total)
	}
	if got := cs.PerUnit.Number.String(); got != "502.12" {
		t.Errorf("PerUnit.Number = %q, want %q", got, "502.12")
	}
	if cs.PerUnit.Currency != "USD" {
		t.Errorf("PerUnit.Currency = %q, want %q", cs.PerUnit.Currency, "USD")
	}
	if got := cs.Total.Number.String(); got != "9.95" {
		t.Errorf("Total.Number = %q, want %q", got, "9.95")
	}
	if cs.Total.Currency != "USD" {
		t.Errorf("Total.Currency = %q, want %q", cs.Total.Currency, "USD")
	}
	if cs.Date == nil {
		t.Fatal("Date is nil")
	}
	if got := cs.Date.Format("2006-01-02"); got != "2024-01-15" {
		t.Errorf("Date = %q, want %q", got, "2024-01-15")
	}
	if cs.Label != "lot1" {
		t.Errorf("Label = %q, want %q", cs.Label, "lot1")
	}
}

func TestLowerCostSpec_CombinedMismatchedCurrenciesError(t *testing.T) {
	_, costNode := parseCostNodeForTest(t, "2024-01-01 * \"Test\"\n  Assets:Bank 10 HOOL {502.12 EUR # 9.95 USD}\n  Expenses:Other\n")
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}
	if _, ok := l.lowerCostSpec(costNode); ok {
		t.Fatal("lowerCostSpec succeeded, want failure due to mismatched currencies")
	}
	if len(l.file.Diagnostics) == 0 {
		t.Fatal("expected a diagnostic for mismatched currencies, got none")
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

func TestLowerPosting_Simple(t *testing.T) {
	cst := syntax.Parse("2024-01-01 * \"Test\"\n  Assets:Bank  100 USD\n  Expenses:Food\n")
	txnNode := cst.Root.FindNode(syntax.TransactionDirective)
	if txnNode == nil {
		t.Fatal("no transaction found")
	}
	postings := txnNode.FindAllNodes(syntax.PostingNode)
	if len(postings) < 2 {
		t.Fatalf("expected 2 postings, got %d", len(postings))
	}
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}

	// First posting: with amount
	p1, ok := l.lowerPosting(postings[0])
	if !ok {
		t.Fatalf("lowerPosting failed for first posting: %v", l.file.Diagnostics)
	}
	if p1.Account != "Assets:Bank" {
		t.Errorf("Account = %q, want %q", p1.Account, "Assets:Bank")
	}
	if p1.Flag != 0 {
		t.Errorf("Flag = %d, want 0", p1.Flag)
	}
	if p1.Amount == nil {
		t.Fatal("Amount is nil")
	}
	if got := p1.Amount.Number.String(); got != "100" {
		t.Errorf("Amount.Number = %q, want %q", got, "100")
	}
	if p1.Amount.Currency != "USD" {
		t.Errorf("Amount.Currency = %q, want %q", p1.Amount.Currency, "USD")
	}

	// Second posting: no amount (auto-balanced)
	p2, ok := l.lowerPosting(postings[1])
	if !ok {
		t.Fatalf("lowerPosting failed for second posting: %v", l.file.Diagnostics)
	}
	if p2.Account != "Expenses:Food" {
		t.Errorf("Account = %q, want %q", p2.Account, "Expenses:Food")
	}
	if p2.Amount != nil {
		t.Errorf("Amount = %v, want nil", p2.Amount)
	}
}

func TestLowerPosting_WithFlag(t *testing.T) {
	cst := syntax.Parse("2024-01-01 * \"Test\"\n  ! Assets:Bank  100 USD\n  Expenses:Food\n")
	txnNode := cst.Root.FindNode(syntax.TransactionDirective)
	if txnNode == nil {
		t.Fatal("no transaction found")
	}
	postings := txnNode.FindAllNodes(syntax.PostingNode)
	if len(postings) == 0 {
		t.Fatal("no posting found")
	}
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}

	p, ok := l.lowerPosting(postings[0])
	if !ok {
		t.Fatalf("lowerPosting failed: %v", l.file.Diagnostics)
	}
	if p.Flag != '!' {
		t.Errorf("Flag = %c, want %c", p.Flag, '!')
	}
	if p.Account != "Assets:Bank" {
		t.Errorf("Account = %q, want %q", p.Account, "Assets:Bank")
	}
}

func TestLowerPosting_WithCostAndPrice(t *testing.T) {
	cst := syntax.Parse("2024-01-01 * \"Test\"\n  Assets:Bank 10 HOOL {100 USD} @ 105 USD\n  Expenses:Other\n")
	txnNode := cst.Root.FindNode(syntax.TransactionDirective)
	if txnNode == nil {
		t.Fatal("no transaction found")
	}
	postings := txnNode.FindAllNodes(syntax.PostingNode)
	if len(postings) == 0 {
		t.Fatal("no posting found")
	}
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}

	p, ok := l.lowerPosting(postings[0])
	if !ok {
		t.Fatalf("lowerPosting failed: %v", l.file.Diagnostics)
	}
	if p.Amount == nil {
		t.Fatal("Amount is nil")
	}
	if got := p.Amount.Number.String(); got != "10" {
		t.Errorf("Amount.Number = %q, want %q", got, "10")
	}
	if p.Amount.Currency != "HOOL" {
		t.Errorf("Amount.Currency = %q, want %q", p.Amount.Currency, "HOOL")
	}
	if p.Cost == nil {
		t.Fatal("Cost is nil")
	}
	if p.Cost.PerUnit == nil {
		t.Fatal("Cost.PerUnit is nil")
	}
	if got := p.Cost.PerUnit.Number.String(); got != "100" {
		t.Errorf("Cost.PerUnit.Number = %q, want %q", got, "100")
	}
	if p.Cost.PerUnit.Currency != "USD" {
		t.Errorf("Cost.PerUnit.Currency = %q, want %q", p.Cost.PerUnit.Currency, "USD")
	}
	if p.Price == nil {
		t.Fatal("Price is nil")
	}
	if got := p.Price.Amount.Number.String(); got != "105" {
		t.Errorf("Price.Amount.Number = %q, want %q", got, "105")
	}
	if p.Price.Amount.Currency != "USD" {
		t.Errorf("Price.Amount.Currency = %q, want %q", p.Price.Amount.Currency, "USD")
	}
}

func TestLowerPosting_WithMetadata(t *testing.T) {
	cst := syntax.Parse("2024-01-01 * \"Test\"\n  Assets:Bank  100 USD\n    note: \"test\"\n  Expenses:Food\n")
	txnNode := cst.Root.FindNode(syntax.TransactionDirective)
	if txnNode == nil {
		t.Fatal("no transaction found")
	}
	postings := txnNode.FindAllNodes(syntax.PostingNode)
	if len(postings) == 0 {
		t.Fatal("no posting found")
	}
	l := &lowerer{filename: "test.beancount", file: &File{Filename: "test.beancount"}}

	p, ok := l.lowerPosting(postings[0])
	if !ok {
		t.Fatalf("lowerPosting failed: %v", l.file.Diagnostics)
	}
	if p.Account != "Assets:Bank" {
		t.Errorf("Account = %q, want %q", p.Account, "Assets:Bank")
	}
	if len(p.Meta.Props) == 0 {
		t.Fatal("expected metadata, got none")
	}
	val, ok := p.Meta.Props["note"]
	if !ok {
		t.Fatal("metadata key 'note' not found")
	}
	if val.Kind != MetaString {
		t.Errorf("metadata kind = %v, want MetaString", val.Kind)
	}
	if val.String != "test" {
		t.Errorf("metadata value = %q, want %q", val.String, "test")
	}
}
