package csvimp

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/yugui/go-beancount/pkg/ast"
)

// tomlDecoder returns a decoder closure mirroring the CLI's recommended
// shape: it decodes from src into dest and fails when the TOML document
// carries keys not present on dest.
func tomlDecoder(src string) func(dest any) error {
	return func(dest any) error {
		meta, err := toml.NewDecoder(bytes.NewBufferString(src)).Decode(dest)
		if err != nil {
			return err
		}
		if undec := meta.Undecoded(); len(undec) != 0 {
			keys := make([]string, len(undec))
			for i, k := range undec {
				keys[i] = k.String()
			}
			return fmt.Errorf("unknown keys: %s", strings.Join(keys, ", "))
		}
		return nil
	}
}

// permissiveDecoder mirrors tomlDecoder but does not fail on undecoded
// keys. Useful for asserting csvimp's own validation in isolation.
func permissiveDecoder(src string) func(dest any) error {
	return func(dest any) error {
		_, err := toml.NewDecoder(bytes.NewBufferString(src)).Decode(dest)
		return err
	}
}

func TestConfigure_HappyPath(t *testing.T) {
	imp := &Importer{}
	if err := imp.Configure(tomlDecoder(simpleTOML)); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	body := "Date,Amount\n2024-01-15,1\n"

	// Shape accepts comma-delimited files and date format "2006-01-02".
	if !imp.Identify(context.Background(), inputFromString("/tmp/x.csv", "", body)) {
		t.Fatal("Identify returned false after Configure")
	}
	out, err := imp.Extract(context.Background(), inputFromString("/tmp/x.csv", "", body))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}

	// Verify delimiter is comma: a tab-delimited file with the same
	// column names should not match (Identify returns false).
	tabBody := "Date\tAmount\n2024-01-15\t1\n"
	if imp.Identify(context.Background(), inputFromString("/tmp/x.tsv", "", tabBody)) {
		t.Error("Identify true for tab-delimited input; shape delimiter is comma")
	}
}

func TestConfigure_Errors(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		wantIn string
	}{
		{
			name:   "no shapes",
			src:    ``,
			wantIn: "no shapes defined",
		},
		{
			name: "missing date_col",
			src: `[shape.s]
date_format = "2006-01-02"
[[shape.s.amount]]
col = "Amount"`,
			wantIn: "date_col is required",
		},
		{
			name: "missing date_format",
			src: `[shape.s]
date_col = "Date"
[[shape.s.amount]]
col = "Amount"`,
			wantIn: "date_format is required",
		},
		{
			name: "bad date_format",
			src: `[shape.s]
date_col = "Date"
date_format = "garbage"
[[shape.s.amount]]
col = "Amount"`,
			wantIn: "date_format",
		},
		{
			name: "no amount entries",
			src: `[shape.s]
date_col = "Date"
date_format = "2006-01-02"`,
			wantIn: "at least one [[amount]] entry",
		},
		{
			name: "amount missing col",
			src: `[shape.s]
date_col = "Date"
date_format = "2006-01-02"
[[shape.s.amount]]
negate = true`,
			wantIn: "amount[0].col is required",
		},
		{
			name: "bad match regex",
			src: `[shape.s]
date_col = "Date"
date_format = "2006-01-02"
match = "(broken"
[[shape.s.amount]]
col = "Amount"`,
			wantIn: "match",
		},
		{
			name: "multi-rune delimiter",
			src: `[shape.s]
date_col = "Date"
date_format = "2006-01-02"
delimiter = ",;"
[[shape.s.amount]]
col = "Amount"`,
			wantIn: "delimiter",
		},
	}

	// First Configure a valid shape so we can verify rollback.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			imp := newConfigured(t, simpleTOML)
			err := imp.Configure(permissiveDecoder(tc.src))
			if err == nil {
				t.Fatalf("Configure: nil error, want one containing %q", tc.wantIn)
			}
			if !strings.HasPrefix(err.Error(), "csvimp: configure: ") {
				t.Errorf("error %q does not begin with %q", err, "csvimp: configure: ")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q does not contain %q", err, tc.wantIn)
			}
			// Prior config must still be intact: the original valid input still extracts.
			body := "Date,Amount\n2024-01-15,1\n"
			out, extractErr := imp.Extract(context.Background(), inputFromString("/tmp/x.csv", "", body))
			if extractErr != nil {
				t.Errorf("Extract after failed re-Configure: %v (prior config should be intact)", extractErr)
			}
			if len(out.Directives) != 1 {
				t.Errorf("Extract after failed re-Configure: got %d directives, want 1", len(out.Directives))
			}
		})
	}
}

func TestConfigure_UnknownKeyRejectedByCLIDecoder(t *testing.T) {
	const src = `
[shape.s]
date_col      = "Date"
date_format   = "2006-01-02"
unknown_field = "bogus"

[[shape.s.amount]]
col = "Amount"
`
	imp := &Importer{}
	err := imp.Configure(tomlDecoder(src))
	if err == nil {
		t.Fatal("Configure: nil error, want one citing unknown_field")
	}
	if !strings.Contains(err.Error(), "unknown_field") {
		t.Errorf("error %q does not cite unknown_field", err)
	}
}

func TestConfigure_NilDecoder(t *testing.T) {
	imp := &Importer{}
	if err := imp.Configure(nil); err == nil {
		t.Fatal("Configure(nil): no error")
	}
}

func TestConfigure_Reconfigure(t *testing.T) {
	// First Configure installs shape "alpha" (USD), which is lex-first.
	// If Configure mistakenly merged shapes instead of replacing them,
	// a subsequent shape lookup would still pick "alpha" (lex-first), so
	// the JPY assertion below would fail and expose the bug.
	const first = `
[shape.alpha]
date_col         = "Date"
date_format      = "2006-01-02"
default_currency = "USD"
account          = "Assets:Alpha"
[[shape.alpha.amount]]
col = "Amount"
`
	imp := newConfigured(t, first)

	// Second Configure installs shape "zulu" (JPY) — lex-last. After a
	// correct full replacement, only "zulu" exists, so extraction must yield JPY.
	const second = `
[shape.zulu]
date_col         = "Date"
date_format      = "2006-01-02"
default_currency = "JPY"
account          = "Assets:Zulu"
[[shape.zulu.amount]]
col = "Amount"
`
	if err := imp.Configure(permissiveDecoder(second)); err != nil {
		t.Fatalf("Configure second: %v", err)
	}

	// New shape is active: extract on standard input succeeds with JPY currency.
	body := "Date,Amount\n2024-01-15,100\n"
	out, err := imp.Extract(context.Background(), inputFromString("/tmp/x.csv", "", body))
	if err != nil {
		t.Fatalf("Extract after second Configure: %v", err)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}
	tx, ok := out.Directives[0].(*ast.Transaction)
	if !ok {
		t.Fatalf("directive type %T, want *ast.Transaction", out.Directives[0])
	}
	if got := tx.Postings[0].Amount.Currency; got != "JPY" {
		t.Errorf("currency = %q, want JPY (new shape active)", got)
	}
}

func TestConfigure_LexicographicShapeOrder(t *testing.T) {
	// Three shapes all matching the same path and header columns, but each
	// with a distinct account. Lex-first shape wins, so the emitted account
	// must be "Assets:Alpha".
	const src = `
[shape.charlie]
date_col         = "Date"
date_format      = "2006-01-02"
default_currency = "USD"
account          = "Assets:Charlie"
[[shape.charlie.amount]]
col = "A"

[shape.alpha]
date_col         = "Date"
date_format      = "2006-01-02"
default_currency = "USD"
account          = "Assets:Alpha"
[[shape.alpha.amount]]
col = "A"

[shape.bravo]
date_col         = "Date"
date_format      = "2006-01-02"
default_currency = "USD"
account          = "Assets:Bravo"
[[shape.bravo.amount]]
col = "A"
`
	imp := newConfigured(t, src)
	body := "Date,A\n2024-01-01,1\n"
	in := inputFromString("/tmp/x.csv", "", body)

	if !imp.Identify(context.Background(), in) {
		t.Fatal("Identify returned false")
	}
	out, err := imp.Extract(context.Background(), in)
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
		t.Errorf("account = %q, want Assets:Alpha (lex-first shape alpha wins)", got)
	}
}
