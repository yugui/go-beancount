// Package checkclosing is the Go port of upstream beancount's
// check_closing plugin — it expands a `closing: TRUE` posting metadata
// into a zero-balance assertion dated one day after the transaction.
//
// Upstream source:
//
//	https://github.com/beancount/beancount/blob/master/beancount/plugins/check_closing.py
//
// Upstream copyright: Copyright (C) 2018, 2020-2021, 2024-2026 Martin Blais.
// Upstream license: GNU GPLv2 (this repository is GPLv2 as well; see COPYING).
//
// # Behavior
//
// For every Transaction, the plugin scans postings for a truthy
// `closing` metadata key. For each such posting:
//
//   - The posting is cloned with the `closing` key stripped (upstream
//     does this so the expanded ledger has no leftover bookkeeping
//     metadata). The original input posting is not mutated.
//   - A zero-valued Balance assertion is synthesized on the same
//     account and currency, dated one day after the transaction.
//
// The containing Transaction is itself cloned so the per-posting edit
// does not leak back to the input. Transactions that contain no
// `closing` postings are passed through unchanged (no clone).
//
// # Accepted forms of the metadata value
//
// Upstream Python evaluates `posting.meta.get("closing", False)` in a
// boolean context, which treats both a real bool and a truthy string
// ("TRUE", "true", ...) as "closing". Go's typed metadata distinguishes
// these cases, so this port accepts:
//
//   - MetaBool with Bool == true;
//   - MetaString with a value that case-insensitively equals "true".
//
// Any other value (false, 0, "no", another type) leaves the posting
// untouched.
//
// # Posting without an Amount
//
// A posting whose Amount is nil (auto-balanced) has no declared
// currency, so a zero balance directive cannot be synthesized. The
// plugin leaves such a posting untouched — it is the caller's
// responsibility to apply `closing: TRUE` to concrete postings.
//
// # Registered names
//
// The plugin registers under two names:
//
//   - "beancount.plugins.check_closing" — upstream Python module path.
//   - "github.com/yugui/go-beancount/pkg/ext/postproc/std/checkclosing"
//     — Go import path.
package checkclosing
