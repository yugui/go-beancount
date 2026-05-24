// Package infermetadata is the Go port of the beansprout
// infer_metadata plugin. It fills in missing metadata on individual
// directives based on a rule DSL that may either copy from another
// metadata key, derive from the directive's own account or currency,
// or look up a value through an external YAML mapping table.
//
// Upstream source:
//
//	https://github.com/yugui/beansprout/blob/main/beansprout/plugins/infer_metadata.py
//
// # Rule syntax
//
// Config is a multi-line string. Each non-blank, non-comment line is a
// rule of the form
//
//	<directive_type> <target_meta> <source>[ file:<path.yaml>]
//
// where:
//
//   - <directive_type> selects which directives a rule applies to.
//     Recognized values are the lowercase directive names: "open",
//     "close", "balance", "pad", "document", "note", "commodity",
//     "transaction".
//   - <target_meta> is the metadata key the plugin will populate when
//     missing.
//   - <source> names the source of the value. Three forms are supported:
//   - a metadata key — the source value is the value of that key on
//     the directive (including values inferred by earlier rules in
//     the same Apply call). Missing → the rule is silently skipped
//     for this directive.
//   - "__commodity__" — special token for Commodity directives. The
//     source value is the directive's currency code. On any other
//     directive the rule is silently skipped.
//   - "__account__" — special token for account-bearing directives
//     (Open, Close, Balance, Pad, Document, Note). The source value
//     is the directive's leaf account name (the final colon-separated
//     component). On any other directive the rule is silently skipped.
//   - "file:<path>" is optional. When present the source value is
//     looked up as a key in the YAML mapping loaded from <path>, and
//     the YAML value is what gets written to <target_meta>. Paths are
//     resolved relative to the directory of [api.Input.SourceFilename].
//
// Lines beginning with ';' are comments and ignored. Inline ';' comments
// truncate the rest of the line.
//
// # Behavior
//
//   - For every directive, the plugin applies the rules registered for
//     that directive type in declaration order.
//   - A rule whose target_meta is already present on the directive is
//     skipped (existing metadata is never overwritten).
//   - YAML files are loaded at most once per Apply call regardless of
//     how many rules reference them.
//   - When [api.Input.SourceFilename] is empty and any rule carries
//     "file:", the plugin emits an "infer-metadata-no-source-file"
//     diagnostic and ignores those rules; non-file rules still run.
//
// # Example
//
//	plugin "beansprout.plugins.infer_metadata" "
//	  ; copy the commodity name into a 'unit' meta key
//	  commodity unit __commodity__
//	  ; use the leaf account as a short display name
//	  open name __account__
//	  ; look up volatility class via YAML mapping
//	  open volatility account_class file:volatility.yaml
//	  ; copy a uuid into an 'id' field
//	  transaction id uuid
//	"
//
// With volatility.yaml:
//
//	checking: low
//	savings: low
//	stocks: high
//
// an Open directive on Assets:Investments:Stocks whose metadata carries
// "account_class: stocks" gains "volatility: high" automatically.
//
// # Diagnostics
//
// The plugin emits diagnostics with the following codes:
//
//   - "infer-metadata-invalid-config" — a config line could not be
//     parsed as a rule (fewer than three whitespace-separated fields
//     after comment stripping).
//   - "infer-metadata-no-source-file" — a rule referenced a "file:"
//     YAML map but [api.Input.SourceFilename] is empty, so the path
//     cannot be resolved. File-bearing rules are skipped; other rules
//     still run.
//   - "infer-metadata-yaml-read-error" — a referenced YAML file could
//     not be opened or parsed. All rules tied to that file are skipped
//     for the remainder of the Apply call.
//   - "infer-metadata-yaml-key-not-found" — a YAML mapping was loaded
//     but the looked-up key is absent from it. The rule is skipped for
//     that directive; other directives continue to be processed.
//
// Directives that the plugin replaces are rebuilt with freshly
// allocated metadata maps; input directives are never mutated in place.
// Result.Directives is nil when no rule fires.
//
// # Deviations
//
// When a YAML mapping file fails to load (read or parse error), the Go
// port emits one read-error diagnostic for that file, caches the failure
// so subsequent rules tied to the same file are silently skipped, and
// continues processing every other rule and directive in the call.
// Upstream Python's infer_metadata returns early on the first file-load
// failure, so any rules later in the config — including rules unrelated
// to the failing file — would not run. The Go port's behavior is
// strictly more useful (every diagnosable problem is surfaced in one
// pass) and never silently masks an error.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beansprout.plugins.infer_metadata" — upstream Python module
//     path, so existing ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/infermetadata"
//     — Go import path, matching the convention for Go-native plugins.
package infermetadata
