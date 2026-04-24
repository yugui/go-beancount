// Package checkdrained is the Go port of upstream beancount's
// check_drained plugin — it enforces that balance-sheet accounts are
// empty at the moment they are closed.
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/check_drained.py
//
// Upstream copyright: Copyright (C) 2022, 2024-2026 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// For every Close directive on a balance-sheet account (Assets,
// Liabilities, Equity), the plugin synthesizes one zero-valued Balance
// assertion per currency that account has ever held. The synthesized
// assertions are dated one day after the Close so they sit after every
// transaction that could have legitimately touched the account. The
// currencies considered for an account are the union of:
//
//   - currencies declared in the account's Open directive's constraint
//     (if any);
//   - currencies seen in any transaction posting against the account.
//
// If the ledger already contains a Balance assertion on that
// (account, date+1, currency) tuple, the synthesized one is skipped so
// the plugin does not duplicate user-authored assertions. Duplication
// on (account, *exactly close.date*, currency) is skipped to mirror
// upstream's behavior (upstream tests user-authored balances at
// entry.date, not entry.date + 1 day).
//
// The plugin is synthesizing: it returns a Result with Directives set
// to a fresh slice containing all original directives plus the
// synthesized Balance assertions. Input directives are never mutated.
//
// # Deviation: balance-sheet root names
//
// Upstream reads the `name_assets`, `name_liabilities`, and
// `name_equity` options from the beancount options map to identify
// balance-sheet accounts. go-beancount's options registry does not
// currently expose these names; this port hardcodes the defaults
// ([ast.Assets], [ast.Liabilities], [ast.Equity]) and leaves a TODO to
// consult the registry once those options are supported. This matches
// upstream behavior for ledgers that accept the defaults.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.check_drained" — upstream Python module path.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/checkdrained"
//     — Go import path.
package checkdrained
