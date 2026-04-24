// Package checkcommodity is the Go port of upstream beancount's
// check_commodity plugin — it diagnoses every currency that appears in
// the ledger without a matching Commodity directive.
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/check_commodity.py
//
// Upstream copyright: Copyright (C) 2015-2017, 2020-2021, 2024-2025 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// For every currency appearing in an Open's currency constraint, a
// Transaction posting's units/cost/price, a Balance assertion, or either
// side of a Price directive, the plugin checks whether the ledger has a
// Commodity directive declaring that currency. Missing declarations are
// reported once per currency via api.Error with code
// "missing-commodity". Errors are emitted first for
// account-contextualized occurrences (sorted by (account, currency)) and
// then for occurrences that only appear in Price directives, so errors
// are deterministic and a currency already reported in an account
// context is not repeated for a price context.
//
// # Configuration (deviation from upstream)
//
// Upstream accepts its configuration as a Python dict literal parsed
// with `eval`. This port accepts JSON instead, because parsing Python
// with eval is both unavailable and unsafe in Go:
//
//	plugin "beancount.plugins.check_commodity" "{\"Assets:Broker:.*\":\".*\"}"
//
// The configuration is a JSON object mapping account-regex to
// currency-regex. A pair matches an occurrence when both regexes match
// at the start of the account and currency strings respectively
// (matching Python `re.match` semantics). Invalid JSON or invalid
// regexes surface as api.Error values with codes "invalid-config" and
// "invalid-regexp"; invalid JSON aborts the plugin, an invalid regex
// only drops the offending pair.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.check_commodity" — upstream Python module path,
//     so existing ledgers activate the port without edits.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/checkcommodity" —
//     Go import path, matching Phase 6a's convention for Go-native
//     plugins.
package checkcommodity
