package csvsexp

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/google/go-cmp/cmp"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/printer"
)

// programDecoder decodes only the program field from a config.toml document,
// ignoring the kind/name bookkeeping keys.
func programDecoder(src string) func(dest any) error {
	return func(dest any) error {
		_, err := toml.NewDecoder(bytes.NewBufferString(src)).Decode(dest)
		return err
	}
}

// importerFromProgram builds an importer directly from a program string,
// bypassing TOML so behavioural tests can pin the program inline.
func importerFromProgram(t *testing.T, name, program string) (importer.Importer, error) {
	t.Helper()
	return newImporter(name, func(dest any) error {
		cfg, ok := dest.(*config)
		if !ok {
			t.Fatalf("decode dest is %T, want *config", dest)
		}
		cfg.Program = program
		return nil
	})
}

func fixtureInput(t *testing.T, shape string) importer.Input {
	t.Helper()
	path := filepath.Join("testdata", shape, "statement.csv")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return importer.Input{
		Path: path,
		Opener: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		},
	}
}

func printDirectives(t *testing.T, dirs []ast.Directive) string {
	t.Helper()
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, dirs); err != nil {
		t.Fatalf("printer.Fprint: %v", err)
	}
	return buf.String()
}

// TestGolden compiles each testdata program and checks that the extracted
// ledger matches the golden output, demonstrating feature parity with csvimp
// across its representative shapes.
func TestGolden(t *testing.T) {
	shapes := []string{
		"simple",
		"debitcredit",
		"currencysuffix",
		"translations",
		"counteraccount",
		"cost",
		"split",
		"template",
		"exclude",
		"headerless",
		"conditional",
		"cond",
	}
	for _, shape := range shapes {
		t.Run(shape, func(t *testing.T) {
			cfgSrc, err := os.ReadFile(filepath.Join("testdata", shape, "config.toml"))
			if err != nil {
				t.Fatalf("read config: %v", err)
			}
			imp, err := newImporter(shape, programDecoder(string(cfgSrc)))
			if err != nil {
				t.Fatalf("newImporter: %v", err)
			}
			in := fixtureInput(t, shape)
			if !imp.Identify(context.Background(), in) {
				t.Fatal("Identify returned false")
			}
			out, err := imp.Extract(context.Background(), in)
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if len(out.Diagnostics) != 0 {
				t.Fatalf("unexpected diagnostics: %+v", out.Diagnostics)
			}
			got := printDirectives(t, out.Directives)

			want, err := os.ReadFile(filepath.Join("testdata", shape, "expected.beancount"))
			if err != nil {
				t.Fatalf("read expected: %v", err)
			}
			if diff := cmp.Diff(string(want), got); diff != "" {
				t.Errorf("ledger mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestName(t *testing.T) {
	imp, err := importerFromProgram(t, "my-instance",
		`(csv-import (emit-transaction :date (parse-date (column "D") "2006-01-02") :amount (parse-amount (column "A")) :currency (const "USD") :account (const "Assets:X")))`)
	if err != nil {
		t.Fatalf("newImporter: %v", err)
	}
	if got := imp.Name(); got != "my-instance" {
		t.Errorf("Name() = %q, want my-instance", got)
	}
}

func TestMatchGate(t *testing.T) {
	const prog = `(csv-import
  :match "specific.*"
  (emit-transaction
    :date (parse-date (column "Date") "2006-01-02")
    :amount (parse-amount (column "Amount"))
    :currency (const "USD") :account (const "Assets:X")))`
	imp, err := importerFromProgram(t, "m", prog)
	if err != nil {
		t.Fatalf("newImporter: %v", err)
	}
	body := "Date,Amount\n2024-01-01,1\n"
	mk := func(path string) importer.Input {
		return importer.Input{Path: path, Opener: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader([]byte(body))), nil
		}}
	}
	if !imp.Identify(context.Background(), mk("specific.csv")) {
		t.Error("Identify(specific.csv) = false, want true")
	}
	if imp.Identify(context.Background(), mk("other.csv")) {
		t.Error("Identify(other.csv) = true, want false")
	}
}
