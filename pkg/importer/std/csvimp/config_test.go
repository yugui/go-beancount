package csvimp

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
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

func TestFactory_HappyPath(t *testing.T) {
	imp, err := newImporter("test", tomlDecoder(simpleTOML))
	if err != nil {
		t.Fatalf("newImporter: %v", err)
	}
	body := "Date,Amount\n2024-01-15,1\n"

	if !imp.Identify(context.Background(), inputFromString("/tmp/x.csv", "", body)) {
		t.Fatal("Identify returned false after factory construction")
	}
	out, err := imp.Extract(context.Background(), inputFromString("/tmp/x.csv", "", body))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Directives) != 1 {
		t.Fatalf("got %d directives, want 1", len(out.Directives))
	}

	// delimiter=comma: tab-delimited must not match.
	tabBody := "Date\tAmount\n2024-01-15\t1\n"
	if imp.Identify(context.Background(), inputFromString("/tmp/x.tsv", "", tabBody)) {
		t.Error("Identify true for tab-delimited input; shape delimiter is comma")
	}
}

func TestFactory_Errors(t *testing.T) {
	// minimal* helpers build TOML bodies that are otherwise valid so each
	// test case can target a single error class.
	const minimalDate = `
[date]
col    = "Date"
format = "2006-01-02"
`
	const minimalAccount = `
[account]
default = "Assets:X"
`
	const minimalCurrency = `
[currency]
default = "USD"
`
	const minimalAmount = `
[[amount]]
col = "Amount"
`
	cases := []struct {
		name   string
		src    string
		wantIn string
	}{
		{
			name: "missing date.col",
			src: `
[date]
format = "2006-01-02"
` + minimalAccount + minimalCurrency + minimalAmount,
			wantIn: "[date].col is required",
		},
		{
			name: "missing date.format",
			src: `
[date]
col = "Date"
` + minimalAccount + minimalCurrency + minimalAmount,
			wantIn: "[date].format is required",
		},
		{
			name: "bad date.format",
			src: `
[date]
col = "Date"
format = "garbage"
` + minimalAccount + minimalCurrency + minimalAmount,
			wantIn: "[date].format",
		},
		{
			name: "date.format year only",
			src: `
[date]
col = "Date"
format = "2006"
` + minimalAccount + minimalCurrency + minimalAmount,
			wantIn: "must include year, month and day",
		},
		{
			name: "date.format missing day",
			src: `
[date]
col = "Date"
format = "2006-01"
` + minimalAccount + minimalCurrency + minimalAmount,
			wantIn: "must include year, month and day",
		},
		{
			name: "date.format missing year",
			src: `
[date]
col = "Date"
format = "01-02"
` + minimalAccount + minimalCurrency + minimalAmount,
			wantIn: "must include year, month and day",
		},
		{
			name:   "no amount entries",
			src:    minimalDate + minimalAccount + minimalCurrency,
			wantIn: "at least one [[amount]] entry",
		},
		{
			name: "amount missing col",
			src: minimalDate + minimalAccount + minimalCurrency + `
[[amount]]
negate = true`,
			wantIn: "amount[0].col is required",
		},
		{
			name: "bad match regex",
			src: `match = "(broken"
` + minimalDate + minimalAccount + minimalCurrency + minimalAmount,
			wantIn: "match",
		},
		{
			name: "multi-rune delimiter",
			src: `delimiter = ",;"
` + minimalDate + minimalAccount + minimalCurrency + minimalAmount,
			wantIn: "delimiter",
		},
		{
			name:   "account requires col or default",
			src:    minimalDate + minimalCurrency + minimalAmount,
			wantIn: "[account] requires col or default",
		},
		{
			name: "account col without map or default",
			src: minimalDate + `
[account]
col = "Acct"
` + minimalCurrency + minimalAmount,
			wantIn: "[account].col without map or default",
		},
		{
			name: "account col with explicit empty map and no default",
			src: minimalDate + `
[account]
col = "Acct"

[account.map]
` + minimalCurrency + minimalAmount,
			wantIn: "[account].col without map or default",
		},
		{
			name: "account default invalid",
			src: minimalDate + `
[account]
default = "not a valid path"
` + minimalCurrency + minimalAmount,
			wantIn: "[account].default",
		},
		{
			name: "account map value invalid",
			src: minimalDate + `
[account]
col = "Acct"

[account.map]
"x" = "bogus root"
` + minimalCurrency + minimalAmount,
			wantIn: "[account.map][\"x\"]",
		},
		{
			name:   "currency requires col or default",
			src:    minimalDate + minimalAccount + minimalAmount,
			wantIn: "[currency] requires col or default",
		},
		{
			name: "currency map blank value",
			src: minimalDate + minimalAccount + `
[currency]
col = "Cur"

[currency.map]
"foo" = "  "
` + minimalAmount,
			wantIn: "[currency.map][\"foo\"]",
		},
		{
			name: "currency default blank",
			src: minimalDate + minimalAccount + `
[currency]
default = "   "
` + minimalAmount,
			wantIn: "[currency].default is blank",
		},
		{
			name: "account map without account.col",
			src: minimalDate + `
[account]
default = "Assets:X"

[account.map]
"x" = "Assets:X"
` + minimalCurrency + minimalAmount,
			wantIn: "[account.map] is set but [account].col is not",
		},
		{
			name: "payee map without payee.col",
			src: minimalDate + minimalAccount + minimalCurrency + `
[payee.map]
"x" = "y"
` + minimalAmount,
			wantIn: "[payee.map] is set but [payee].col is not",
		},
		{
			name: "currency map without currency.col",
			src: minimalDate + minimalAccount + `
[currency]
default = "USD"

[currency.map]
"x" = "y"
` + minimalAmount,
			wantIn: "[currency.map] is set but [currency].col is not",
		},
		{
			name: "narration map without narration.cols",
			src: minimalDate + minimalAccount + minimalCurrency + `
[narration.map]
"x" = "y"
` + minimalAmount,
			wantIn: "[narration.map] is set but [narration].cols is empty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			imp, err := newImporter("test", permissiveDecoder(tc.src))
			if err == nil {
				t.Fatalf("newImporter: nil error, want one containing %q", tc.wantIn)
			}
			if imp != nil {
				t.Error("newImporter: non-nil Importer on error")
			}
			if !strings.HasPrefix(err.Error(), "csvimp: configure: ") {
				t.Errorf("error %q does not begin with %q", err, "csvimp: configure: ")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q does not contain %q", err, tc.wantIn)
			}
		})
	}
}

func TestFactory_UnknownKeyRejectedByCLIDecoder(t *testing.T) {
	const src = `
unknown_field = "bogus"

[date]
col    = "Date"
format = "2006-01-02"

[account]
default = "Assets:X"

[currency]
default = "USD"

[[amount]]
col = "Amount"
`
	imp, err := newImporter("test", tomlDecoder(src))
	if err == nil {
		t.Fatal("newImporter: nil error, want one citing unknown_field")
	}
	if imp != nil {
		t.Error("newImporter: non-nil Importer on error")
	}
	if !strings.HasPrefix(err.Error(), "csvimp: configure: ") {
		t.Errorf("error %q does not begin with %q", err, "csvimp: configure: ")
	}
	if !strings.Contains(err.Error(), "unknown_field") {
		t.Errorf("error %q does not cite unknown_field", err)
	}
}

func TestFactory_NilDecoder(t *testing.T) {
	imp, err := newImporter("test", nil)
	if err == nil {
		t.Fatal("newImporter(nil decoder): no error")
	}
	if imp != nil {
		t.Error("newImporter: non-nil Importer on error")
	}
	if got, want := err.Error(), "csvimp: configure: nil decoder"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}
