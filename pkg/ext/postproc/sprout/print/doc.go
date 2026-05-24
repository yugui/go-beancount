// Package print is the Go port of beansprout's print plugin — a debugging aid
// that writes the option map and every directive in the processed ledger to
// os.Stderr, then returns the ledger unchanged.
//
// Upstream source:
//
//	https://github.com/yugui/beansprout/blob/main/beansprout/plugins/print.py
//
// # Behavior
//
// On each invocation the plugin writes to os.Stderr:
//  1. Every option key-value pair from [ast.OptionValues.Snapshot], one per
//     line, formatted as "<key>: <value>".
//  2. Every directive from [api.Input.Directives], formatted via
//     [github.com/yugui/go-beancount/pkg/printer.Fprint].
//
// It then returns [api.Result]{} — no directives are changed and no
// diagnostics are emitted.
//
// # Deliberate deviation: direct os.Stderr write
//
// Upstream writes to sys.stderr directly (print(..., file=sys.stderr)). This
// port does the same: it writes to [os.Stderr] rather than threading a writer
// through the plugin API. This is a deliberate, scoped deviation from the
// "no side effects" convention that governs most plugins. The plugin exists
// solely for debugging; callers who need a different output destination should
// not use this plugin.
//
// # Usage
//
// The plugin takes no Config string. Either registered name works:
//
//	plugin "beansprout.plugins.print"
//
// or, equivalently:
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/print"
//
// Adding this directive to a beancount file causes beancheck (or any other
// runner that blank-imports the sprout umbrella) to dump the full processed
// ledger to stderr each time the file is checked.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beansprout.plugins.print" — upstream Python module path, so existing
//     ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/print" —
//     Go import path, matching the project's convention for Go-native plugins.
package print
