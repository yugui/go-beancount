package csvimp

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
)

// TestFactory_EncodingResolvesIANAName checks that a valid IANA charset
// name compiles to a non-nil decoder on the shape. We inspect the
// unexported field directly: the encoding decision is internal state
// the public API only exposes indirectly via Extract on byte streams,
// so a direct assertion keeps the test surface small (CLAUDE.md
// unexported-test exception: package-internal building block test).
func TestFactory_EncodingResolvesIANAName(t *testing.T) {
	cases := []string{
		"Shift_JIS",
		"EUC-JP",
		"windows-1252",
		"ISO-8859-1",
		"MS_Kanji", // alias of Shift_JIS in the IANA registry
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			src := `
encoding = "` + name + `"
` + simpleTOML
			imp := newConfigured(t, src)
			if imp.s.inputEncoding == nil {
				t.Errorf("encoding %q: shape.inputEncoding is nil, want non-nil", name)
			}
		})
	}
}

func TestFactory_EncodingUnsetLeavesNil(t *testing.T) {
	imp := newConfigured(t, simpleTOML)
	if imp.s.inputEncoding != nil {
		t.Errorf("default shape.inputEncoding = %v, want nil", imp.s.inputEncoding)
	}
}

func TestFactory_EncodingInvalidName(t *testing.T) {
	const src = `
encoding = "no-such-encoding"
` + simpleTOML
	imp, err := newImporter("test", permissiveDecoder(src))
	if err == nil {
		t.Fatal("newImporter: nil error, want one citing the bad encoding name")
	}
	if imp != nil {
		t.Error("newImporter: non-nil Importer on error")
	}
	if !strings.HasPrefix(err.Error(), "csvimp: configure: ") {
		t.Errorf("error %q does not begin with %q", err, "csvimp: configure: ")
	}
	if !strings.Contains(err.Error(), "is not a recognised IANA charset name") {
		t.Errorf("error %q does not cite IANA charset name", err)
	}
	if !strings.Contains(err.Error(), `"no-such-encoding"`) {
		t.Errorf("error %q does not quote the offending value", err)
	}
}

// TestExtract_ShiftJISDecodesNonASCII feeds a CSV body that has been
// encoded to Shift_JIS at the byte level and checks that payee and
// narration round-trip back to UTF-8 in the emitted Transaction.
func TestExtract_ShiftJISDecodesNonASCII(t *testing.T) {
	const src = `
encoding = "Shift_JIS"

[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:Checking"

[currency]
default = "JPY"

[payee]
col = "支払先"

[narration]
col = "摘要"

[[amount]]
col = "金額"
`
	const utf8Body = "Date,支払先,摘要,金額\n2024-01-15,コーヒー店,ラテ,-450\n"
	sjisBody := mustEncodeShiftJIS(t, utf8Body)

	imp, err := newImporter("test", permissiveDecoder(src))
	if err != nil {
		t.Fatalf("newImporter: %v", err)
	}

	in := inputFromBytes("/tmp/sjis.csv", "", sjisBody)
	if !imp.Identify(context.Background(), in) {
		t.Fatal("Identify returned false for valid Shift_JIS CSV with matching header")
	}

	out, err := imp.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Diagnostics) != 0 {
		t.Fatalf("got diagnostics %v, want none", out.Diagnostics)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx, ok := out.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("directive type %T, want *ast.Transaction", out.Directives[0])
	}
	if got, want := tx.Payee, "コーヒー店"; got != want {
		t.Errorf("payee = %q, want %q", got, want)
	}
	if got, want := tx.Narration, "ラテ"; got != want {
		t.Errorf("narration = %q, want %q", got, want)
	}
	if got, want := tx.Postings[0].Amount.Currency, "JPY"; got != want {
		t.Errorf("currency = %q, want %q", got, want)
	}
	if got, want := tx.Postings[0].Amount.Number.String(), "-450"; got != want {
		t.Errorf("amount = %q, want %q", got, want)
	}
}

// TestExtract_NoEncodingPassesUTF8Through is a guard: an encoding-less
// shape must continue to handle a UTF-8 body containing non-ASCII bytes
// without any transformation.
func TestExtract_NoEncodingPassesUTF8Through(t *testing.T) {
	const src = `
[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:Checking"

[currency]
default = "JPY"

[payee]
col = "支払先"

[[amount]]
col = "金額"
`
	body := "Date,支払先,金額\n2024-01-15,コーヒー店,-450\n"

	imp := newConfigured(t, src)
	in := inputFromString("/tmp/utf8.csv", "", body)
	out, err := imp.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx := out.Directives[0].(*ast.Transaction)
	if got, want := tx.Payee, "コーヒー店"; got != want {
		t.Errorf("payee = %q, want %q", got, want)
	}
}

func mustEncodeShiftJIS(t *testing.T, utf8 string) []byte {
	t.Helper()
	r := transform.NewReader(strings.NewReader(utf8), japanese.ShiftJIS.NewEncoder())
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("encode to Shift_JIS: %v", err)
	}
	return b
}

func inputFromBytes(path, mime string, body []byte) importer.Input {
	return importer.Input{
		Path: path,
		MIME: mime,
		Opener: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		},
	}
}
