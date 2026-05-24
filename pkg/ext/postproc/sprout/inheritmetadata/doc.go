// Package inheritmetadata is the Go port of the beansprout
// inherit_metadata plugin. It fills in missing metadata on
// [ast.Open] directives by walking the parent account hierarchy.
//
// Upstream source:
//
//	https://github.com/yugui/beansprout/blob/main/beansprout/plugins/inherit_metadata.py
//
// # Behavior
//
// The plugin takes a multiline config string listing the metadata keys to
// track — one key per line. Blank lines and lines beginning with ';' are
// ignored. For each [ast.Open] directive the plugin checks each configured
// key:
//
//   - If the key is already present on the Open, it is left unchanged.
//   - Otherwise the plugin walks the parent chain (via [ast.Account.Parent])
//     looking for the nearest ancestor account whose own Open carries that
//     key. The first ancestor that has the key wins; its value is copied.
//   - If no ancestor carries the key, the Open is left unchanged for that
//     key.
//
// The plugin never modifies Open directives in place. When inheritance is
// needed a fresh *[ast.Open] is built with an independent copy of the
// metadata map. All other directive types pass through unchanged.
//
// Two passes over the directive list are made. The first pass builds an
// index of account → metadata (restricted to the configured keys). The
// second pass emits a replacement directive slice with inheritance applied.
// The replacement slice is only returned when at least one Open was
// modified; otherwise Result.Directives is nil.
//
// # Config
//
// The Config string is a newline-separated list of metadata key names:
//
//	plugin "beansprout.plugins.inherit_metadata" "region
//	tax_category"
//
// Lines are stripped of leading and trailing whitespace. Empty lines and
// lines beginning with ';' are skipped.
//
// # Diagnostics
//
// The plugin emits no diagnostics. Empty or malformed config yields a
// no-op rather than an error: a plugin with no configured keys simply
// passes every directive through unchanged.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beansprout.plugins.inherit_metadata" — upstream Python module path,
//     so existing ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/inheritmetadata"
//     — Go import path, matching the convention for Go-native plugins.
package inheritmetadata
