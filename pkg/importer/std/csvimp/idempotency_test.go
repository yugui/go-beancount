package csvimp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/printer"
)

// fixtureInput builds an importer.Input that opens the CSV fixture at
// testdata/<shape>/statement.csv. The Opener returns a fresh reader on
// every call.
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

func loadFixtureConfig(t *testing.T, shape string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", shape, "config.toml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return string(b)
}

func printDirectives(t *testing.T, dirs []ast.Directive) string {
	t.Helper()
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, dirs); err != nil {
		t.Fatalf("printer.Fprint: %v", err)
	}
	return buf.String()
}

func runOnce(t *testing.T, instanceName, src string, in importer.Input) []ast.Directive {
	t.Helper()
	raw, err := newImporter(instanceName, permissiveDecoder(src))
	if err != nil {
		t.Fatalf("newImporter: %v", err)
	}
	imp := raw.(*Importer)
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
	return out.Directives
}

func TestIdempotency_SimpleShape(t *testing.T) {
	checkIdempotency(t, "simple")
}

func TestIdempotency_DebitCreditShape(t *testing.T) {
	checkIdempotency(t, "debitcredit")
}

func TestIdempotency_MultiAccount(t *testing.T) {
	checkIdempotency(t, "multiaccount")
}

func TestIdempotency_Translations(t *testing.T) {
	checkIdempotency(t, "translations")
}

func checkIdempotency(t *testing.T, shape string) {
	t.Helper()
	src := loadFixtureConfig(t, shape)
	in := fixtureInput(t, shape)

	// Instance name equals the shape directory name to preserve golden-file rowhash bytes.
	first := printDirectives(t, runOnce(t, shape, src, in))
	second := printDirectives(t, runOnce(t, shape, src, in))
	if first != second {
		t.Errorf("two-run mismatch:\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	// Re-run on the same Importer instance: immutability means repeated
	// Extract calls on the same value must produce identical output.
	raw, err := newImporter(shape, permissiveDecoder(src))
	if err != nil {
		t.Fatalf("newImporter: %v", err)
	}
	imp := raw.(*Importer)
	if !imp.Identify(context.Background(), in) {
		t.Fatal("Identify false")
	}
	out1, err := imp.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract 1: %v", err)
	}
	out2, err := imp.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract 2: %v", err)
	}
	p1 := printDirectives(t, out1.Directives)
	p2 := printDirectives(t, out2.Directives)
	if p1 != p2 {
		t.Errorf("repeated-extract mismatch:\nfirst:\n%s\nsecond:\n%s", p1, p2)
	}

	// Golden file: matches if present, skipped only when absent.
	expPath := filepath.Join("testdata", shape, "expected.beancount")
	exp, err := os.ReadFile(expPath)
	switch {
	case err == nil:
		if first != string(exp) {
			t.Errorf("output differs from %s:\ngot:\n%s\nwant:\n%s", expPath, first, exp)
		}
	case errors.Is(err, os.ErrNotExist):
		// golden file not yet created; skip comparison
	default:
		t.Fatalf("read golden file %s: %v", expPath, err)
	}
}
