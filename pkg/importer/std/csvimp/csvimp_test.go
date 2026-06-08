package csvimp

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
)

// inputFromString constructs an importer.Input whose Opener returns a
// fresh reader over body on each call.
func inputFromString(path, mime, body string) importer.Input {
	return importer.Input{
		Path: path,
		MIME: mime,
		Opener: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(body)), nil
		},
	}
}

const simpleTOML = `
[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:Checking"

[currency]
default = "USD"

[[amount]]
col = "Amount"
`

func newConfigured(t *testing.T, src string) importer.Importer {
	t.Helper()
	imp, err := newImporter("test", permissiveDecoder(src))
	if err != nil {
		t.Fatalf("newImporter: %v", err)
	}
	return imp
}

func TestName_ReturnsInstanceName(t *testing.T) {
	imp, err := newImporter("my-instance", permissiveDecoder(simpleTOML))
	if err != nil {
		t.Fatalf("newImporter: %v", err)
	}
	if got := imp.Name(); got != "my-instance" {
		t.Errorf("Name() = %q, want %q", got, "my-instance")
	}
}

func TestIdentify_ExtensionGate(t *testing.T) {
	imp := newConfigured(t, simpleTOML)
	body := "Date,Amount\n2024-01-01,1\n"
	cases := []struct {
		name string
		in   importer.Input
		want bool
	}{
		{".csv extension", inputFromString("/tmp/file.csv", "", body), true},
		{".CSV extension uppercase", inputFromString("/tmp/file.CSV", "", body), true},
		{".tsv extension (header still comma)", inputFromString("/tmp/file.tsv", "", body), true},
		{"MIME text/csv", inputFromString("/tmp/file.dat", "text/csv", body), true},
		{"MIME text/tsv", inputFromString("/tmp/file.dat", "text/tab-separated-values", body), true},
		{"no extension no mime", inputFromString("/tmp/file.dat", "", body), false},
		{"empty path and mime", inputFromString("", "", body), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := imp.Identify(context.Background(), tc.in); got != tc.want {
				t.Errorf("Identify = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIdentify_MissingColumnRejected(t *testing.T) {
	imp := newConfigured(t, simpleTOML)
	body := "Date,Wrong\n2024-01-01,1\n"
	if imp.Identify(context.Background(), inputFromString("/tmp/file.csv", "", body)) {
		t.Error("Identify true despite missing Amount column")
	}
}

// TestIdentify_MatchRegexGatesShapeSelection verifies that match regex gates
// shape selection via multi-instance dispatch: instance "a_specific" matches
// only paths beginning with "specific"; instance "b_other" matches only paths
// beginning with "other". Both instances share the same columns, so selection
// is driven by regex alone. The correct instance is confirmed by observing
// the account on the emitted posting.
func TestIdentify_MatchRegexGatesShapeSelection(t *testing.T) {
	const srcSpecific = `
match = "specific.*"

[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:A"

[currency]
default = "USD"

[[amount]]
col = "Amount"
`
	const srcOther = `
match = "other.*"

[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:B"

[currency]
default = "USD"

[[amount]]
col = "Amount"
`
	impA, err := newImporter("a_specific", permissiveDecoder(srcSpecific))
	if err != nil {
		t.Fatalf("newImporter a_specific: %v", err)
	}
	impB, err := newImporter("b_other", permissiveDecoder(srcOther))
	if err != nil {
		t.Fatalf("newImporter b_other: %v", err)
	}
	reg, err := importer.NewRegistry([]importer.Importer{impA, impB})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	body := "Date,Amount\n2024-01-01,1\n"

	// "specific.csv" must select a_specific (account "Assets:A").
	inSpecific := inputFromString("specific.csv", "", body)
	matched, ok, diags := importer.Dispatch(context.Background(), reg, inSpecific)
	if !ok || len(diags) != 0 {
		t.Fatalf("Dispatch(specific.csv): ok=%v diags=%v", ok, diags)
	}
	if matched.Name() != "a_specific" {
		t.Errorf("Dispatch(specific.csv) matched %q, want a_specific", matched.Name())
	}
	outSpec, err := matched.Extract(context.Background(), inSpecific)
	if err != nil {
		t.Fatalf("Extract(specific.csv): %v", err)
	}
	if len(outSpec.Directives) != 1 {
		t.Fatalf("Extract(specific.csv): got %d directives, want 1", len(outSpec.Directives))
	}
	txSpec, ok := outSpec.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("directive type %T, want *ast.Transaction", outSpec.Directives[0])
	}
	if got := string(txSpec.Postings[0].Account); got != "Assets:A" {
		t.Errorf("specific.csv account = %q, want Assets:A (instance a_specific)", got)
	}

	// "other.csv" must select b_other (account "Assets:B").
	inOther := inputFromString("other.csv", "", body)
	matched, ok, diags = importer.Dispatch(context.Background(), reg, inOther)
	if !ok || len(diags) != 0 {
		t.Fatalf("Dispatch(other.csv): ok=%v diags=%v", ok, diags)
	}
	if matched.Name() != "b_other" {
		t.Errorf("Dispatch(other.csv) matched %q, want b_other", matched.Name())
	}
	outOther, err := matched.Extract(context.Background(), inOther)
	if err != nil {
		t.Fatalf("Extract(other.csv): %v", err)
	}
	if len(outOther.Directives) != 1 {
		t.Fatalf("Extract(other.csv): got %d directives, want 1", len(outOther.Directives))
	}
	txOther, ok := outOther.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("directive type %T, want *ast.Transaction", outOther.Directives[0])
	}
	if got := string(txOther.Postings[0].Account); got != "Assets:B" {
		t.Errorf("other.csv account = %q, want Assets:B (instance b_other)", got)
	}
}

// TestExtract_HeaderColumnMismatchEmitsDiagnostic pins the new contract: when
// ext/MIME passes and the match regex passes (or is unset), but the header is
// missing a required column, Extract returns DiagMissingColumn with err == nil.
func TestExtract_HeaderColumnMismatchEmitsDiagnostic(t *testing.T) {
	imp := newConfigured(t, simpleTOML)
	// Header lacks "Amount" — Identify would return false, but Extract is called directly.
	in := inputFromString("/tmp/x.csv", "", "Date,Other\n2024-01-01,1\n")
	out, err := imp.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract: unexpected error %v", err)
	}
	if len(out.Directives) != 0 {
		t.Errorf("got %d directives, want 0", len(out.Directives))
	}
	if len(out.Diagnostics) == 0 {
		t.Fatal("got no diagnostics, want DiagMissingColumn")
	}
	if out.Diagnostics[0].Code != csvbase.DiagMissingColumn {
		t.Errorf("diagnostic code = %q, want %q", out.Diagnostics[0].Code, csvbase.DiagMissingColumn)
	}
}

func TestIdentify_HeaderColumnTrim(t *testing.T) {
	imp := newConfigured(t, simpleTOML)
	body := " Date , Amount \n2024-01-01,1\n"
	if !imp.Identify(context.Background(), inputFromString("/tmp/x.csv", "", body)) {
		t.Error("Identify false; header-side trim should match")
	}
}

func TestIdentify_SkipLinesBanner(t *testing.T) {
	const src = `
skip_lines = 2

[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:Checking"

[currency]
default = "USD"

[[amount]]
col = "Amount"
`
	imp := newConfigured(t, src)
	body := "MyBank Statement\nGenerated 2024-01-20\nDate,Amount\n2024-01-01,1\n"
	if !imp.Identify(context.Background(), inputFromString("/tmp/x.csv", "", body)) {
		t.Error("Identify false; expected skip_lines to step over banner")
	}
}

// TestDispatch_DeclarationOrderWins verifies that when three instances
// with overlapping matches are added to a registry in declaration order
// alpha, bravo, charlie, Dispatch selects the first declared instance
// (alpha) regardless of lexicographic ordering.
func TestDispatch_DeclarationOrderWins(t *testing.T) {
	makeSrc := func(account string) string {
		return `
[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "` + account + `"

[currency]
default = "USD"

[[amount]]
col = "A"
`
	}
	impAlpha, err := newImporter("alpha", permissiveDecoder(makeSrc("Assets:Alpha")))
	if err != nil {
		t.Fatalf("newImporter alpha: %v", err)
	}
	impBravo, err := newImporter("bravo", permissiveDecoder(makeSrc("Assets:Bravo")))
	if err != nil {
		t.Fatalf("newImporter bravo: %v", err)
	}
	impCharlie, err := newImporter("charlie", permissiveDecoder(makeSrc("Assets:Charlie")))
	if err != nil {
		t.Fatalf("newImporter charlie: %v", err)
	}
	// Declared in order alpha, bravo, charlie — Dispatch must pick alpha.
	reg, err := importer.NewRegistry([]importer.Importer{impAlpha, impBravo, impCharlie})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	body := "Date,A\n2024-01-01,1\n"
	in := inputFromString("/tmp/x.csv", "", body)

	matched, ok, diags := importer.Dispatch(context.Background(), reg, in)
	if !ok || len(diags) != 0 {
		t.Fatalf("Dispatch: ok=%v diags=%v", ok, diags)
	}
	if matched.Name() != "alpha" {
		t.Errorf("Dispatch selected %q, want alpha (declaration order)", matched.Name())
	}
	out, err := matched.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx, ok := out.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("directive type %T, want *ast.Transaction", out.Directives[0])
	}
	if got := string(tx.Postings[0].Account); got != "Assets:Alpha" {
		t.Errorf("account = %q, want Assets:Alpha (declaration-first instance wins)", got)
	}
}
