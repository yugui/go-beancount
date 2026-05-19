package csvimp

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
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

func TestImporterRegisteredUnderBothNames(t *testing.T) {
	a, ok := importer.Lookup("csv")
	if !ok {
		t.Fatal(`Lookup("csv"): not registered`)
	}
	b, ok := importer.Lookup("github.com/yugui/go-beancount/pkg/importer/std/csvimp")
	if !ok {
		t.Fatal("Lookup(go-path): not registered")
	}
	if a != b {
		t.Errorf("csv and go-path lookups returned different instances (%p vs %p)", a, b)
	}
	if a.Name() != "csv" {
		t.Errorf("Name() = %q, want %q", a.Name(), "csv")
	}
}

func TestName_Constant(t *testing.T) {
	imp := &Importer{}
	if imp.Name() != "csv" {
		t.Errorf("Name() = %q, want %q", imp.Name(), "csv")
	}
}

const simpleTOML = `
[shape.simple]
date_col         = "Date"
date_format      = "2006-01-02"
default_currency = "USD"
account          = "Assets:Checking"

[[shape.simple.amount]]
col = "Amount"
`

func newConfigured(t *testing.T, src string) *Importer {
	t.Helper()
	imp := &Importer{}
	if err := imp.Configure(permissiveDecoder(src)); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	return imp
}

func TestIdentify_NoShapesConfigured(t *testing.T) {
	imp := &Importer{}
	in := inputFromString("/tmp/file.csv", "", "Date,Amount\n2024-01-01,1\n")
	if imp.Identify(context.Background(), in) {
		t.Error("Identify true with no shapes configured")
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

// TestIdentify_MatchRegexGatesShapeSelection verifies that the match regex
// gates shape selection: shape a_specific matches only paths beginning with
// "specific"; shape b_other matches only paths beginning with "other". Both
// shapes share the same columns, so selection is driven by regex alone. We
// confirm correct selection by observing the account on the emitted posting.
func TestIdentify_MatchRegexGatesShapeSelection(t *testing.T) {
	const src = `
[shape.b_other]
match            = "other.*"
date_col         = "Date"
date_format      = "2006-01-02"
default_currency = "USD"
account          = "Assets:B"
[[shape.b_other.amount]]
col = "Amount"

[shape.a_specific]
match            = "specific.*"
date_col         = "Date"
date_format      = "2006-01-02"
default_currency = "USD"
account          = "Assets:A"
[[shape.a_specific.amount]]
col = "Amount"
`
	imp := newConfigured(t, src)
	body := "Date,Amount\n2024-01-01,1\n"

	// "specific.csv" must select a_specific (account "Assets:A").
	inSpecific := inputFromString("specific.csv", "", body)
	if !imp.Identify(context.Background(), inSpecific) {
		t.Fatal("Identify(specific.csv) = false; want true")
	}
	outSpec, err := imp.Extract(context.Background(), inSpecific)
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
		t.Errorf("specific.csv account = %q, want Assets:A (shape a_specific)", got)
	}

	// "other.csv" must select b_other (account "Assets:B").
	inOther := inputFromString("other.csv", "", body)
	if !imp.Identify(context.Background(), inOther) {
		t.Fatal("Identify(other.csv) = false; want true")
	}
	outOther, err := imp.Extract(context.Background(), inOther)
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
		t.Errorf("other.csv account = %q, want Assets:B (shape b_other)", got)
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
[shape.banner]
skip_lines  = 2
date_col    = "Date"
date_format = "2006-01-02"
default_currency = "USD"
account     = "Assets:Checking"
[[shape.banner.amount]]
col = "Amount"
`
	imp := newConfigured(t, src)
	body := "MyBank Statement\nGenerated 2024-01-20\nDate,Amount\n2024-01-01,1\n"
	if !imp.Identify(context.Background(), inputFromString("/tmp/x.csv", "", body)) {
		t.Error("Identify false; expected skip_lines to step over banner")
	}
}

func TestExtract_NoShapeMatched(t *testing.T) {
	imp := newConfigured(t, simpleTOML)
	// Mismatching header so Extract's selector finds nothing.
	in := inputFromString("/tmp/x.csv", "", "Foo,Bar\n1,2\n")
	out, err := imp.Extract(context.Background(), in)
	if err == nil {
		t.Fatal("Extract: nil error, want framework error")
	}
	if len(out.Directives) != 0 || len(out.Diagnostics) != 0 {
		t.Errorf("unexpected output: %+v", out)
	}
}

func TestExtract_NotConfigured(t *testing.T) {
	imp := &Importer{}
	in := inputFromString("/tmp/x.csv", "", "Date,Amount\n2024-01-01,1\n")
	if _, err := imp.Extract(context.Background(), in); err == nil {
		t.Fatal("Extract: nil error, want not-configured error")
	}
}
