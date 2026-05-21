package main

import (
	"fmt"
	"io"

	"github.com/yugui/go-beancount/pkg/ast"
)

const (
	// codeIdentifyForced marks the Warning emitted when -importer NAME is
	// set but the named importer's Identify returned false.
	codeIdentifyForced = "beanimport-identify-forced"
	// codeCancelled marks the Error emitted when the context is cancelled.
	codeCancelled = "beanimport-cancelled"
	// codeExtract marks the Error promoted from a non-nil error returned
	// by Extract (when the context is not cancelled).
	codeExtract = "beanimport-extract"
	// codeHook marks the Error promoted from a non-nil error returned by
	// hook.Chain (when the context is not cancelled).
	codeHook = "beanimport-hook"
)

// printDiagnostics writes one line per diagnostic to w via
// ast.Diagnostic.String. It is a no-op when diags is empty.
func printDiagnostics(w io.Writer, diags []ast.Diagnostic) {
	for _, d := range diags {
		fmt.Fprintln(w, d.String())
	}
}
