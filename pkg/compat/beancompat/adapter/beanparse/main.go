// Command beanparse reads a beancount file through the parse-only path and
// writes its contents to stdout as a JSON object in beancompat's portable
// {directives, errors, options} schema.
//
// Parse-tier only — no plugin / validation pipeline. The serialized result
// reflects exactly what the parser and lowering pass see, before any
// booking, padding, balance-checking, or plugin transforms are applied.
//
// Usage:
//
//	beanparse <file.beancount>
//
// Exit codes:
//
//	0  success (including when the ledger itself has beancount-level errors;
//	   those appear in the JSON "errors" array)
//	2  usage error, I/O failure, or internal serializer failure
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/compat/beancompat"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Usage: beanparse <file.beancount>")
		return 2
	}
	filename := args[0]

	// Read+Load (not ast.LoadFile) so I/O failures keep exiting 2 instead
	// of being absorbed into the ledger's diagnostic stream — ast.LoadFile
	// would report a missing file as an Error Diagnostic and return nil
	// err, conflating infrastructure failure with ledger content errors.
	// The exit-code contract is pinned by TestRun_MissingFile and documented
	// in the package header; the I/O / ledger distinction is what lets the
	// Python adapter and ad-hoc CLI users tell "test harness broken" from
	// "ledger has errors". As a side benefit, ast.Load(string) uses the
	// default "<input>" virtual filename, matching parse_fixtures_test.go
	// and main_test.go's reference path.
	src, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(stderr, "beanparse: %v\n", err)
		return 2
	}
	ledger, err := ast.Load(string(src))
	if err != nil {
		fmt.Fprintf(stderr, "beanparse: %v\n", err)
		return 2
	}
	result, err := beancompat.SerializeParsed(ledger)
	if err != nil {
		fmt.Fprintf(stderr, "beanparse: serialize: %v\n", err)
		return 2
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(stderr, "beanparse: write: %v\n", err)
		return 2
	}
	return 0
}
