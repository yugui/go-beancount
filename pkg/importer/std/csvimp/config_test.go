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
	cases := []struct {
		name   string
		src    string
		wantIn string
	}{
		{
			name: "missing date_col",
			src: `date_format = "2006-01-02"
[[amount]]
col = "Amount"`,
			wantIn: "date_col is required",
		},
		{
			name: "missing date_format",
			src: `date_col = "Date"
[[amount]]
col = "Amount"`,
			wantIn: "date_format is required",
		},
		{
			name: "bad date_format",
			src: `date_col = "Date"
date_format = "garbage"
[[amount]]
col = "Amount"`,
			wantIn: "date_format",
		},
		{
			name: "no amount entries",
			src: `date_col = "Date"
date_format = "2006-01-02"`,
			wantIn: "at least one [[amount]] entry",
		},
		{
			name: "amount missing col",
			src: `date_col = "Date"
date_format = "2006-01-02"
[[amount]]
negate = true`,
			wantIn: "amount[0].col is required",
		},
		{
			name: "bad match regex",
			src: `date_col = "Date"
date_format = "2006-01-02"
match = "(broken"
[[amount]]
col = "Amount"`,
			wantIn: "match",
		},
		{
			name: "multi-rune delimiter",
			src: `date_col = "Date"
date_format = "2006-01-02"
delimiter = ",;"
[[amount]]
col = "Amount"`,
			wantIn: "delimiter",
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
date_col      = "Date"
date_format   = "2006-01-02"
unknown_field = "bogus"

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
