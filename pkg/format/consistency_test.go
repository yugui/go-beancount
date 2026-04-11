package format_test

import (
	"bytes"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/format"
	"github.com/yugui/go-beancount/pkg/printer"
	"github.com/yugui/go-beancount/pkg/syntax"
)

// consistencyCase holds a canonical beancount input and its name.
type consistencyCase struct {
	name          string
	src           string
	hasBlankLines bool // true when the input has blank lines between directives.
}

// canonicalInputs returns test inputs that are already in canonical format.
// These must avoid known divergences between CST formatter and AST printer:
// no comments, no pushtag/poptag, alphabetical metadata keys, and numbers
// that round-trip cleanly through apd.Decimal.Text('f').
var canonicalInputs = []consistencyCase{
	{
		name: "simple transaction with two postings",
		src: `2024-01-15 * "Grocery Store" "Weekly groceries"
  Expenses:Food                            50.00 USD
  Assets:Bank:Checking                    -50.00 USD
`,
	},
	{
		name: "transaction with cost spec and price annotation",
		src: `2024-03-01 * "Broker" "Sell stocks"
  Assets:Investments                         -5 HOOL {150.00 USD} @ 175.00 USD
  Assets:Bank:Checking                    875.00 USD
`,
	},
	{
		name: "transaction with posting flags",
		src: `2024-08-01 * "Mixed flags"
  ! Expenses:Food                          30.00 USD
  * Assets:Bank:Checking                  -30.00 USD
`,
	},
	{
		name: "transaction with metadata",
		src: `2024-07-01 * "Detailed purchase"
  category: "grocery"
  ref: "invoice-2024-07-001"
  Expenses:Food                            42.00 USD
    receipt: "/receipts/2024-07-01.pdf"
  Assets:Bank:Checking
`,
	},
	{
		name:          "multiple directives with blank line separation",
		hasBlankLines: true,
		src: `2024-01-01 open Assets:Bank:Checking USD

2024-01-15 * "Store" "Groceries"
  Expenses:Food                            50.00 USD
  Assets:Bank:Checking                    -50.00 USD

2024-01-31 balance Assets:Bank:Checking 5000.00 USD
`,
	},
	{
		name: "transaction with unicode account names",
		src: `2024-09-01 * "Store" "Groceries"
  Expenses:Food                          1500.00 JPY
  Assets:Bank:Checking
`,
	},
	{
		name: "balance directive with tolerance",
		src: `2024-02-28 balance Assets:Bank:Checking 3500.00 USD ~ 0.01 USD
`,
	},
	{
		name: "japanese payee narration and metadata",
		src: `2024-09-15 * "東京マーケット" "日用品の購入"
  store: "東京マーケット"
  Expenses:日用品                           3200 JPY
    category: "日用品"
  Assets:現金
`,
	},
	{
		name: "japanese account names alignment",
		src: `2024-10-01 * "居酒屋やまと" "忘年会"
  Expenses:交際費                          15000 JPY
  Assets:銀行:三菱UFJ
`,
	},
}

// optionCombo defines a named set of format options.
type optionCombo struct {
	name             string
	opts             []format.Option
	insertBlankLines bool // true when InsertBlankLinesBetweenDirectives is enabled.
}

var optionCombos = []optionCombo{
	{
		name: "default",
		opts: nil,
	},
	{
		name:             "insert enabled",
		opts:             []format.Option{format.WithInsertBlankLinesBetweenDirectives(true)},
		insertBlankLines: true,
	},
	{
		name:             "comma grouping",
		opts:             []format.Option{format.WithCommaGrouping(true), format.WithInsertBlankLinesBetweenDirectives(true)},
		insertBlankLines: true,
	},
	{
		name:             "amount column 60",
		opts:             []format.Option{format.WithAmountColumn(60), format.WithInsertBlankLinesBetweenDirectives(true)},
		insertBlankLines: true,
	},
	{
		name:             "indent width 4",
		opts:             []format.Option{format.WithIndentWidth(4), format.WithInsertBlankLinesBetweenDirectives(true)},
		insertBlankLines: true,
	},
	{
		name:             "blank lines between directives 2",
		opts:             []format.Option{format.WithBlankLinesBetweenDirectives(2), format.WithInsertBlankLinesBetweenDirectives(true)},
		insertBlankLines: true,
	},
}

func TestCSTASTPrinterConsistency(t *testing.T) {
	for _, tc := range canonicalInputs {
		for _, oc := range optionCombos {
			name := tc.name + "/" + oc.name
			t.Run(name, func(t *testing.T) {
				// When insert is disabled, CST preserves existing blank
				// lines but the AST printer cannot — skip this known divergence.
				if tc.hasBlankLines && !oc.insertBlankLines {
					t.Skip("blank-line preservation diverges without InsertBlankLinesBetweenDirectives")
				}

				// Reformat the source with the given options so both
				// paths start from identical canonical input.
				src := format.Format(tc.src, oc.opts...)

				// CST path: format the (already-formatted) source again.
				cstOutput := format.Format(src, oc.opts...)

				// AST path: parse -> lower -> print.
				cst := syntax.Parse(src)
				astFile := ast.Lower("test.beancount", cst)
				var buf bytes.Buffer
				if err := printer.Fprint(&buf, astFile, oc.opts...); err != nil {
					t.Fatalf("printer.Fprint failed: %v", err)
				}
				astOutput := buf.String()

				if cstOutput != astOutput {
					t.Errorf("CST formatter and AST printer produced different output\n%s",
						diffStrings(cstOutput, astOutput, "cst", "ast"))
				}
			})
		}
	}
}
