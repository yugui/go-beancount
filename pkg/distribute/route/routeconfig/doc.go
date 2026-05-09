// Package routeconfig loads a beanfile TOML routing configuration into a
// [route.Config]. Decoding is strict: unknown keys, unsupported order /
// file-pattern values, and unsupported transaction strategies are
// rejected at load time so that a typo in a user's config surfaces
// immediately rather than silently routing directives to a wrong
// destination.
//
// # Schema
//
// The TOML schema mirrors the Go [route.Config] / [route.Routes]
// structures field-for-field. The four [routes.*] tables are:
//
//	[routes.account]
//	template              = "transactions/{account}/{date}.beancount"
//	file_pattern          = "YYYYmm"        # YYYY | YYYYmm | YYYYmmdd
//	order                 = "ascending"     # ascending | descending | append
//	equivalence_meta_keys = ["import-id"]   # cross-source dedup keys
//
//	[routes.price]
//	template              = "quotes/{commodity}/{date}.beancount"
//	file_pattern          = "YYYYmm"
//	order                 = "ascending"
//	equivalence_meta_keys = []
//
//	[routes.transaction]
//	default_strategy  = "first-posting"     # first-posting | last-posting |
//	                                        # first-debit | first-credit
//	override_meta_key = "route-account"
//
//	[routes.format]                         # global format defaults
//	comma_grouping                        = false
//	align_amounts                         = true
//	amount_column                         = 52
//	east_asian_ambiguous_width            = 2
//	indent_width                          = 2
//	blank_lines_between_directives        = 1
//	insert_blank_lines_between_directives = false
//
// # Overrides
//
// [routes.account] and [routes.price] each accept an array of
// overrides under [[routes.account.override]] and
// [[routes.price.override]]. Account overrides match by longest
// account-segment prefix:
//
//	[[routes.account.override]]
//	prefix       = "Assets:JP"      # matches Assets:JP and Assets:JP:*,
//	                                # but not Assets:JPN
//	file_pattern = "YYYY"
//
//	[routes.account.override.format]
//	east_asian_ambiguous_width = 2
//
// Price overrides match a commodity by exact-string equality:
//
//	[[routes.price.override]]
//	commodity    = "JPY"
//	file_pattern = "YYYY"
//
// Each override may set the same fields as its parent section
// (template, file_pattern, order, equivalence_meta_keys, format,
// and — for account overrides — txn_strategy); missing fields fall
// through to the parent.
//
// # Inheritance rules
//
//   - Format inherits field-wise: setting just amount_column in an
//     override leaves all other format fields at their inherited
//     values. Resolution order, low to high precedence, is built-in
//     defaults → [routes.format] → section-specific
//     [routes.account.format] / [routes.price.format] → per-override
//     format → caller-applied per-field overrides.
//   - equivalence_meta_keys inherits by replacement, not concatenation.
//     Use equivalence_meta_keys = [] in an override to silence
//     inherited keys.
//   - Account override prefixes are required to match on
//     account-segment boundaries. Ties resolve in TOML order; longest
//     prefix wins.
package routeconfig
