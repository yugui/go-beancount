// Package route resolves an [ast.Directive] to a destination file path
// under the standard convention.
//
// [Decide] is a pure function: it inspects the directive's kind and
// routing key (account or commodity) and returns a [Decision]
// describing where the directive should be written and how it should
// be merged. Non-routable directive kinds yield a [Decision] with
// PassThrough set; the caller decides how to surface them (typically
// error out, or echo to stdout under a pass-through flag).
//
// # Routing convention
//
// Each directive is filed by its kind and a key:
//
//	Open, Close, Balance, Note, Document, Pad, Transaction
//	    routed by Account →
//	    transactions/{account}/{date}.beancount
//	Price
//	    routed by Commodity →
//	    quotes/{commodity}/{date}.beancount
//	Option, Plugin, Include, Event, Query, Custom, Commodity
//	    not routable → PassThrough = true
//
// Templates support three tokens: {account} expands to slash-separated
// path segments (Assets:Foo:Bar:Baz → Assets/Foo/Bar/Baz), {commodity}
// to the currency name verbatim, {date} to the directive's date
// formatted under the configured file pattern (YYYY, YYYYmm, or
// YYYYmmdd). Calendar fields are read directly from the time value to
// avoid timezone conversion: beancount dates are date-only and
// reading Year/Month/Day directly side-steps any time.Local vs
// time.UTC ambiguity.
//
// Pad's PadAccount field does not participate in routing — only the
// padded Account does.
//
// Commodity carries a date and a currency name and could in principle
// be routed (for example, to commodities/<CCY>.beancount), but no
// convention has been agreed in this iteration so it is treated as
// non-routable for now.
//
// # Transaction routing override
//
// A Transaction touches multiple accounts; the destination account is
// chosen by a four-rule precedence chain:
//
//  1. Transaction-level metadata route-account: "Assets:Foo:Bar"
//     (string) — the value is taken as the destination account
//     verbatim.
//  2. The first posting whose metadata contains route-account: TRUE
//     (bool); a posting whose route-account is FALSE is treated as if
//     the entry were absent.
//  3. The configured DefaultStrategy (first-posting, last-posting,
//     first-debit, or first-credit).
//  4. Fallback: the first posting's account.
//
// The override metadata key (default route-account) is configurable
// via TransactionSection.OverrideMetaKey; whatever key is in effect
// is recorded in [Decision.StripMetaKeys] so downstream emit code can
// remove it from the printed directive — on every output, on both the
// transaction and every posting, even entries that were not selected
// by resolution. The original input directive is never mutated;
// stripping happens on a deep copy made by the merger before
// printing.
//
// # Format inheritance
//
// Format options resolve field-wise across five scopes, from low to
// high precedence:
//
//  1. The pkg/format built-in defaults.
//  2. [routes.format] (the global section in TOML).
//  3. [routes.account.format] / [routes.price.format].
//  4. [[routes.account.override]] and [[routes.price.override]] entries
//     under [.format].
//  5. Per-field overrides applied by the caller after Decide.
//
// The two file-level spacing options
// (BlankLinesBetweenDirectives, InsertBlankLinesBetweenDirectives) are
// exposed as typed fields on [Decision] so the merge.Plan builder can
// read them without resolving opaque format closures. The five
// body-level options (comma_grouping, align_amounts, amount_column,
// east_asian_ambiguous_width, indent_width) live on [Decision.Format]
// as a slice of format.Option closures suitable for passing to
// printer.Fprint directly.
//
// # Account overrides
//
// Account overrides match by longest account-segment prefix. A prefix
// of "Assets:JP" matches both "Assets:JP" itself and "Assets:JP:Cash"
// but not "Assets:JPN". Ties resolve in TOML order. Commodity
// overrides match by exact-string equality.
//
// EquivalenceMetaKeys is *[]string (rather than []string) so callers
// can distinguish "not declared" from "declared as empty". On an
// override, a non-nil empty slice silences inherited keys; a nil
// pointer falls back to the parent scope.
package route
