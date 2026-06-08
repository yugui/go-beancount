package csvimp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
)

func compileFixture(t *testing.T, shape string) *csvbase.Driver {
	t.Helper()
	src := loadFixtureConfig(t, shape)
	var sc shapeConfig
	if err := permissiveDecoder(src)(&sc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	s, err := validateShape(shape, sc)
	if err != nil {
		t.Fatalf("validateShape: %v", err)
	}
	drv, err := compile(shape, s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return drv
}

// TestCompile_Parity runs compile() against each core fixture and asserts that
// Identify is true, Extract produces zero diagnostics, and the printed output
// matches expected.beancount exactly. This proves the csvbase path reproduces
// the established golden output for the core feature set.
func TestCompile_Parity(t *testing.T) {
	fixtures := []string{
		"simple",
		"debitcredit",
		"banner",
		"bom",
		"commaamount",
		"counteraccount",
		"counteraccount_multicol",
		"currencysuffix",
		"exclude",
		"headerless",
		"multiaccount",
		"multiaccount_multicol",
		"placeholder",
		"translations",
		"cost",
		"split",
		"template",
	}
	for _, shape := range fixtures {
		t.Run(shape, func(t *testing.T) {
			drv := compileFixture(t, shape)
			in := fixtureInput(t, shape)

			if !drv.Identify(context.Background(), in) {
				t.Fatal("Identify returned false")
			}

			out, err := drv.Extract(context.Background(), in)
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if len(out.Diagnostics) != 0 {
				t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
			}

			got := printDirectives(t, out.Directives)
			expPath := filepath.Join("testdata", shape, "expected.beancount")
			exp, err := os.ReadFile(expPath)
			if err != nil {
				t.Fatalf("read golden file %s: %v", expPath, err)
			}
			if got != string(exp) {
				t.Errorf("output differs from %s:\ngot:\n%s\nwant:\n%s", expPath, got, exp)
			}
		})
	}
}

// runCompiled decodes tomlSrc into a shape, compiles it, builds an
// importer.Input from csv, asserts Identify is true, and returns Extract output.
func runCompiled(t *testing.T, name, tomlSrc, csv string, hints map[string]string) importer.Output {
	t.Helper()
	var sc shapeConfig
	if err := permissiveDecoder(tomlSrc)(&sc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	s, err := validateShape(name, sc)
	if err != nil {
		t.Fatalf("validateShape: %v", err)
	}
	drv, err := compile(name, s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	in := importer.Input{
		Path:  "/x.csv",
		Hints: hints,
		Opener: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(csv)), nil
		},
	}
	if !drv.Identify(context.Background(), in) {
		t.Fatal("Identify returned false")
	}
	out, err := drv.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return out
}

const wiringBase = `
[date]
col    = "Date"
format = "2006-01-02"

[currency]
default = "USD"

[[amount]]
col = "Amount"
`

// TestCompile_HintsAccountOverride verifies that Hints["account"] takes
// priority over [account].default when resolving the primary posting account.
func TestCompile_HintsAccountOverride(t *testing.T) {
	const toml = wiringBase + `
[account]
default = "Assets:Default"
`
	out := runCompiled(t, "test", toml, "Date,Amount\n2024-01-01,10.00\n",
		map[string]string{"account": "Assets:Override"})
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx := out.Directives[0].(*ast.Transaction)
	if got := string(tx.Postings[0].Account); got != "Assets:Override" {
		t.Errorf("account = %q, want Assets:Override", got)
	}
}

// TestCompile_MissingAccount verifies that a row with no account source
// produces a csvbase-missing-account Error diagnostic and no directive.
func TestCompile_MissingAccount(t *testing.T) {
	const toml = wiringBase + `
[account]
default = ""
col     = "Acct"

[account.map]
"x" = "Assets:X"
`
	// Blank Acct cell → no account resolved → missing-account
	out := runCompiled(t, "test", toml, "Date,Amount,Acct\n2024-01-01,10.00,\n", nil)
	if len(out.Directives) != 0 {
		t.Fatalf("got %d directives, want 0", len(out.Directives))
	}
	if len(out.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(out.Diagnostics), out.Diagnostics)
	}
	if got := out.Diagnostics[0].Code; got != csvbase.DiagMissingAccount {
		t.Errorf("code = %q, want %q", got, csvbase.DiagMissingAccount)
	}
	if out.Diagnostics[0].Severity != ast.Error {
		t.Errorf("severity = %v, want Error", out.Diagnostics[0].Severity)
	}
}

// TestCompile_UnmappedAccount verifies that a non-blank account cell absent
// from a strict [account.map] emits csvbase-unmapped-account (Error) and
// drops the row — even when [account].default is set.
func TestCompile_UnmappedAccount(t *testing.T) {
	const toml = wiringBase + `
[account]
col     = "Acct"
default = "Assets:Default"

[account.map]
"known" = "Assets:Known"
`
	// "unknown" is absent from the map; default must NOT be used because
	// MapValue in Strict mode soft-fails and Else propagates the soft-fail.
	out := runCompiled(t, "test", toml, "Date,Amount,Acct\n2024-01-01,10.00,unknown\n", nil)
	if len(out.Directives) != 0 {
		t.Fatalf("got %d directives, want 0 (row must be dropped)", len(out.Directives))
	}
	if len(out.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(out.Diagnostics), out.Diagnostics)
	}
	if got := out.Diagnostics[0].Code; got != csvbase.DiagUnmappedAccount {
		t.Errorf("code = %q, want %q", got, csvbase.DiagUnmappedAccount)
	}
	if out.Diagnostics[0].Severity != ast.Error {
		t.Errorf("severity = %v, want Error", out.Diagnostics[0].Severity)
	}
}

// TestCompile_UnmappedCounterAccount verifies that a non-blank counter-account
// cell absent from a strict map emits csvbase-unmapped-counter-account (Warning)
// and keeps the transaction with a single posting.
func TestCompile_UnmappedCounterAccount(t *testing.T) {
	const toml = wiringBase + `
[account]
default = "Assets:Checking"

[counter_account]
col = "Cat"

[counter_account.map]
"Food" = "Expenses:Food"
`
	out := runCompiled(t, "test", toml, "Date,Amount,Cat\n2024-01-01,10.00,Unknown\n", nil)
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1 (row must be kept)", len(out.Directives))
	}
	tx := out.Directives[0].(*ast.Transaction)
	if len(tx.Postings) != 1 {
		t.Errorf("got %d postings, want 1 (single posting when counter unmapped)", len(tx.Postings))
	}
	if len(out.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(out.Diagnostics), out.Diagnostics)
	}
	if got := out.Diagnostics[0].Code; got != csvbase.DiagUnmappedCounterAccount {
		t.Errorf("code = %q, want %q", got, csvbase.DiagUnmappedCounterAccount)
	}
	if out.Diagnostics[0].Severity != ast.Warning {
		t.Errorf("severity = %v, want Warning", out.Diagnostics[0].Severity)
	}
}

// TestCompile_BadDate verifies that an unparseable date cell emits
// csvbase-bad-date (Error) and drops the row.
func TestCompile_BadDate(t *testing.T) {
	const toml = wiringBase + `
[account]
default = "Assets:Checking"
`
	out := runCompiled(t, "test", toml, "Date,Amount\nnot-a-date,10.00\n", nil)
	if len(out.Directives) != 0 {
		t.Fatalf("got %d directives, want 0", len(out.Directives))
	}
	if len(out.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(out.Diagnostics), out.Diagnostics)
	}
	if got := out.Diagnostics[0].Code; got != csvbase.DiagBadDate {
		t.Errorf("code = %q, want %q", got, csvbase.DiagBadDate)
	}
	if out.Diagnostics[0].Severity != ast.Error {
		t.Errorf("severity = %v, want Error", out.Diagnostics[0].Severity)
	}
}

// TestCompile_AllBlankAmount verifies that a row with all amount cells blank
// emits csvbase-all-blank-amount (Error) and drops the row.
func TestCompile_AllBlankAmount(t *testing.T) {
	const toml = wiringBase + `
[account]
default = "Assets:Checking"
`
	out := runCompiled(t, "test", toml, "Date,Amount\n2024-01-01,\n", nil)
	if len(out.Directives) != 0 {
		t.Fatalf("got %d directives, want 0", len(out.Directives))
	}
	if len(out.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(out.Diagnostics), out.Diagnostics)
	}
	if got := out.Diagnostics[0].Code; got != csvbase.DiagAllBlankAmount {
		t.Errorf("code = %q, want %q", got, csvbase.DiagAllBlankAmount)
	}
	if out.Diagnostics[0].Severity != ast.Error {
		t.Errorf("severity = %v, want Error", out.Diagnostics[0].Severity)
	}
}

// TestCompile_CounterDefaultVsSinglePosting verifies the blank-counter-cell
// fallback logic: with a [counter_account].default the blank cell yields two
// postings; without a default a blank cell yields a single posting.
func TestCompile_CounterDefaultVsSinglePosting(t *testing.T) {
	withDefault := fmt.Sprintf(`%s
[account]
default = "Assets:Checking"

[counter_account]
col     = "Cat"
default = "Expenses:Misc"

[counter_account.map]
"Food" = "Expenses:Food"
`, wiringBase)

	withoutDefault := fmt.Sprintf(`%s
[account]
default = "Assets:Checking"

[counter_account]
col = "Cat"

[counter_account.map]
"Food" = "Expenses:Food"
`, wiringBase)

	csv := "Date,Amount,Cat\n2024-01-01,10.00,\n"

	t.Run("blank counter with default: two postings", func(t *testing.T) {
		out := runCompiled(t, "test", withDefault, csv, nil)
		if len(out.Diagnostics) != 0 {
			t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
		}
		if len(out.Directives) != 1 {
			t.Fatalf("got %d directives, want 1", len(out.Directives))
		}
		tx := out.Directives[0].(*ast.Transaction)
		if len(tx.Postings) != 2 {
			t.Errorf("got %d postings, want 2", len(tx.Postings))
		}
		if got := string(tx.Postings[1].Account); got != "Expenses:Misc" {
			t.Errorf("counter account = %q, want Expenses:Misc", got)
		}
	})

	t.Run("blank counter without default: single posting", func(t *testing.T) {
		out := runCompiled(t, "test", withoutDefault, csv, nil)
		if len(out.Diagnostics) != 0 {
			t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
		}
		if len(out.Directives) != 1 {
			t.Fatalf("got %d directives, want 1", len(out.Directives))
		}
		tx := out.Directives[0].(*ast.Transaction)
		if len(tx.Postings) != 1 {
			t.Errorf("got %d postings, want 1", len(tx.Postings))
		}
	})
}

// TestCompile_BlankAccountCellUsesDefault verifies that a blank account cell
// under a strict [account.map] resolves to [account].default rather than
// erroring: MapValue returns "" for a blank input without consulting the map,
// so Else falls through to the default. This is the blank != miss branch.
func TestCompile_BlankAccountCellUsesDefault(t *testing.T) {
	const toml = wiringBase + `
[account]
col     = "Acct"
default = "Assets:Default"

[account.map]
"known" = "Assets:Known"
`
	out := runCompiled(t, "test", toml, "Date,Amount,Acct\n2024-01-01,10.00,\n", nil)
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx := out.Directives[0].(*ast.Transaction)
	if got := string(tx.Postings[0].Account); got != "Assets:Default" {
		t.Errorf("account = %q, want Assets:Default", got)
	}
}

// TestCompile_MissingCurrency verifies the compiled currency path drops a row
// with csvbase-missing-currency when [currency].col is set but the cell is
// blank and there is no default or from_amount source.
func TestCompile_MissingCurrency(t *testing.T) {
	const toml = `
[date]
col    = "Date"
format = "2006-01-02"

[currency]
col = "Cur"

[[amount]]
col = "Amount"

[account]
default = "Assets:Checking"
`
	out := runCompiled(t, "test", toml, "Date,Amount,Cur\n2024-01-01,10.00,\n", nil)
	if len(out.Directives) != 0 {
		t.Fatalf("got %d directives, want 0", len(out.Directives))
	}
	if len(out.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(out.Diagnostics), out.Diagnostics)
	}
	if got := out.Diagnostics[0].Code; got != csvbase.DiagMissingCurrency {
		t.Errorf("code = %q, want %q", got, csvbase.DiagMissingCurrency)
	}
	if out.Diagnostics[0].Severity != ast.Error {
		t.Errorf("severity = %v, want Error", out.Diagnostics[0].Severity)
	}
}

// TestCompile_BadCost verifies that an unparseable cost number soft-fails with
// csvbase-bad-cost and drops the row.
func TestCompile_BadCost(t *testing.T) {
	const toml = wiringBase + `
[account]
default = "Assets:Checking"

[cost]
per_unit         = "Price"
default_currency = "USD"
`
	out := runCompiled(t, "test", toml, "Date,Amount,Price\n2024-01-01,10.00,not-a-number\n", nil)
	if len(out.Directives) != 0 {
		t.Fatalf("got %d directives, want 0", len(out.Directives))
	}
	if len(out.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(out.Diagnostics), out.Diagnostics)
	}
	if got := out.Diagnostics[0].Code; got != csvbase.DiagBadCost {
		t.Errorf("code = %q, want %q", got, csvbase.DiagBadCost)
	}
	if out.Diagnostics[0].Severity != ast.Error {
		t.Errorf("severity = %v, want Error", out.Diagnostics[0].Severity)
	}
}

// TestCompile_CostMissingCurrency verifies that a cost whose currency column is
// blank with no default_currency soft-fails with csvbase-bad-cost and drops the
// row. The posting currency still resolves (via [currency].default) so the
// failure is specifically the cost currency.
func TestCompile_CostMissingCurrency(t *testing.T) {
	const toml = wiringBase + `
[account]
default = "Assets:Broker:Stock"

[cost]
per_unit = "Price"
currency = "CostCur"
`
	out := runCompiled(t, "test", toml, "Date,Amount,Price,CostCur\n2024-01-01,10.00,150.00,\n", nil)
	if len(out.Directives) != 0 {
		t.Fatalf("got %d directives, want 0", len(out.Directives))
	}
	if len(out.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(out.Diagnostics), out.Diagnostics)
	}
	if got := out.Diagnostics[0].Code; got != csvbase.DiagBadCost {
		t.Errorf("code = %q, want %q", got, csvbase.DiagBadCost)
	}
	if out.Diagnostics[0].Severity != ast.Error {
		t.Errorf("severity = %v, want Error", out.Diagnostics[0].Severity)
	}
}

// TestCompile_CostBlankNumberNoCost verifies that a blank cost-number cell
// yields no cost: the transaction is emitted with a bare primary posting (no
// cost annotation) and no diagnostic.
func TestCompile_CostBlankNumberNoCost(t *testing.T) {
	const toml = wiringBase + `
[account]
default = "Assets:Broker:Stock"

[cost]
per_unit         = "Price"
default_currency = "USD"
`
	out := runCompiled(t, "test", toml, "Date,Amount,Price\n2024-01-01,10.00,\n", nil)
	if len(out.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx := out.Directives[0].(*ast.Transaction)
	if tx.Postings[0].Cost != nil {
		t.Errorf("primary posting cost = %v, want nil (blank cost number)", tx.Postings[0].Cost)
	}
}
