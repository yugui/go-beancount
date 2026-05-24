// Package pricecompletion is the Go port of beansprout's
// price_completion plugin — it synthesizes missing [ast.Price]
// directives by running Dijkstra's shortest-path algorithm over a
// temporally-weighted commodity graph rooted at each operating
// currency.
//
// Upstream source:
//
//	https://github.com/yugui/beansprout/blob/main/beansprout/plugins/price_completion.py
//
// Upstream copyright: Copyright (C) 2024-2025 Yuki Yugui Sonoda.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// For every date that already carries at least one [ast.Price]
// directive, the plugin builds a graph whose nodes are commodities and
// whose edges are the most-recent price observation for each unordered
// commodity pair on or before that date. Each price contributes two
// directed edges: the forward edge `from -> to` with the recorded
// number, and the inverse edge `to -> from` with `1 / number` (skipped
// when number is zero). Edge weights penalize stale observations: an
// edge whose price was recorded on the target date has weight 1.0; an
// edge whose price was recorded `d` days earlier has weight
// `temporal_base + temporal_scale * ln(d)`. With the defaults of
// `temporal_base=1.0` and `temporal_scale=0.1` a one-day-old quote
// weighs 1.0 (ln 1 = 0), a four-day-old quote ~1.139, a 30-day-old
// quote ~1.340, and so on — fresh data is always preferred when
// available.
//
// Dijkstra from each operating currency yields the shortest-path
// product of edge values from that currency to every reachable
// commodity. The traversed-edge product expresses "1 operating
// currency = X target commodity"; the plugin inverts to obtain the
// canonical "1 target commodity = Y operating currency" form recorded
// by [ast.Price]. To suppress derivations from purely historical data
// (which beansprout views as already covered by the source quote), the
// plugin only synthesizes a Price when the resolved shortest path
// traverses at least one edge dated on the current day. A direct
// fresh edge always qualifies; a multi-hop path qualifies as long as
// any single edge along the chain is fresh.
//
// Synthesized prices preserve the metadata of the edge directly
// incident on the target commodity (the "closest" edge along the
// path), matching upstream's metadata-propagation choice.
//
// The plugin is synthesizing: it returns a Result with Directives set
// to a fresh slice containing every original directive followed by the
// derived Prices. Input directives are never mutated.
//
// # Usage
//
// Configuration is optional. The empty string applies the defaults.
// The parameter string is a comma-separated list of `key=value` pairs
// drawn from
//
//   - `temporal_base=<float>`  default 1.0
//   - `temporal_scale=<float>` default 0.1
//
// Either registered name works:
//
//	plugin "beansprout.plugins.price_completion"
//
// or, with custom temporal parameters:
//
//	plugin "beansprout.plugins.price_completion" "temporal_base=2.0,temporal_scale=0.5"
//
// or, using the Go import path:
//
//	plugin "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/pricecompletion"
//
// Given the ledger
//
//	option "operating_currency" "USD"
//	plugin "beansprout.plugins.price_completion"
//
//	2023-01-01 price BTC 50000 USD
//	2023-01-01 price ETH    2 BTC
//
// the plugin appends one Price directive for ETH/USD derived as
// 2 BTC * 50000 USD/BTC = 100000 USD per ETH:
//
//	2023-01-01 price ETH 100000 USD
//
// # Operating currencies
//
// The list is read from [ast.OptionValues.StringList]("operating_currency").
// When the list is empty the plugin emits no diagnostic and no derived
// prices — matching upstream's behavior of silently skipping when
// `options_map["operating_currency"]` is absent or empty.
//
// # Configuration parsing
//
// Unrecognised keys, malformed pairs, and non-numeric values are
// reported with code "price-completion-invalid-config" and the
// affected parameter falls back to its default. The plugin still runs
// with whichever values were successfully parsed.
//
// # Diagnostic codes
//
//   - "price-completion-invalid-config" — a key=value pair in the
//     Config string was malformed (unknown key, non-numeric value, or
//     missing '=').
//
// # Date arithmetic
//
// Edge weights are computed from the day-count between two
// [time.Time] values via duration subtraction (`a.Sub(b).Hours()/24`).
// Upstream Python subtracts truncated dates (`datetime.date` values).
// The two agree exactly when both timestamps share the same wall-clock
// time of day, which holds for the UTC-midnight times produced by the
// beancount parser. Ledgers that hand-construct Price directives with
// non-midnight times may see different weights for borderline-day
// edges; the parser does not produce such values today.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beansprout.plugins.price_completion" — upstream Python module
//     path (with the underscore), so existing ledgers activate the
//     port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/sprout/pricecompletion" —
//     Go import path (no underscore, since Go package identifiers
//     cannot contain underscores).
package pricecompletion
