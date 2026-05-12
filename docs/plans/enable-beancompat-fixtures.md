# Plan: Enable Remaining Beancompat Fixtures

## Goal

Enable the beancompat fixtures the project has not yet turned on, ratcheting
the parity surface forward one fixture at a time. The repository ships the
upstream beancompat fixture archive in `third_party/beancompat`; the
beancompat test driver gates each fixture behind an allowlist in
`pkg/compat/beancompat/allowlist.go`. A fixture not in the allowlist is
reported as SKIP, so the green build today understates the actual matching
state — and so unblocking each one is a concrete, attributable change.

Concretely: 5 parse-tier and 2 check-tier fixtures are in scope. Each one
either matches against the current serializer / loader output, in which case
the work is "add it to the allowlist and confirm green", or it surfaces a
specific divergence that has to be fixed before the entry is added.

## Non-goals

- Implementing beancount option retention on the AST. The fixtures
  `display_precision_by_currency` and `options_coverage` both depend on it
  (the AST currently drops option directives at lower-tier, so the
  serializer has nothing to emit) and are excluded from this plan as a
  result; they are tracked separately as "Plan A".
- Implementing multi-lot posting expansion. The upstream form emits one
  posting per matched lot with its own `*ast.Cost`; the Go reducer
  compresses these into a single posting carrying a synthesized
  `spec.Total` (see `docs/plans/cost-holder-interface.md`'s open items).
  No fixture currently in scope exercises this divergence; if one does, it
  becomes a separate task rather than expanding this plan.
- Removing failed postings (the upstream `book_reductions` behaviour that
  drops the whole currency group on irreconcilable errors). Tracked as a
  TODO in `pkg/inventory/reducer.go`; none of the in-scope fixtures
  exercise an error path.

## Background

The fixture archive at `@beancompat//:parse_fixtures` and
`@beancompat//:check_fixtures` contains 10 parse-tier and 3 check-tier
JSON files. The current allowlist enables 3 parse-tier entries
(`open_single`, `price`, `transaction_balanced`) and 1 check-tier entry
(`transaction_with_cost`, enabled by PR #62 once the `kind:"cost"`
serializer path landed). The remaining state:

Parse-tier candidates (5 in scope, 2 deferred):
- `close.json` — close directive for an account previously opened.
- `commodity.json` — commodity directive with `name` metadata.
- `open_no_currency.json` — open directive with no currency constraint.
- `open_multi_currency.json` — open with multiple allowed currencies, order
  preserved.
- `missing_sentinel.json` — elided posting amount surfaces as
  `{"__missing__": true}` at parse tier (mentioned as Plan D's territory in
  `pkg/compat/beancompat/serialize.go`; the fixture's `expected` omits the
  `units` field for the elided posting, so containment may already pass).
- `display_precision_by_currency.json` — deferred (Plan A, option
  retention required).
- `options_coverage.json` — deferred (Plan A, option retention required).

Check-tier candidates (2 in scope, 1 already enabled):
- `balance.json` — balance assertion after a single transaction.
- `transaction_auto_balance.json` — transaction with an elided posting
  amount that the reducer auto-balances.
- `transaction_with_cost.json` — already enabled by PR #62.

## What the cost-holder-interface PR already accomplished for this plan

PR #62 (merged) was originally one slice of the same fixture-enablement
effort. It collapsed two of the originally-planned steps and partially
resolved a red flag this plan had to call out:

- **Step 3 (parse/check serializer-dispatch sharing) is done.** The
  beancompat serializer dispatches over the sealed `ast.CostHolder` union
  via `serializeCostHolder`, and `SerializeChecked` shares its body with
  `SerializeParsed` instead of erroring out. No tier-specific dispatch
  rework is required as part of this plan.
- **Step 4 (`SerializeChecked` implementation + `kind:"cost"` rendering)
  is done.** `costPayload` in `serialize.go` emits the
  `{kind, number, currency, date, label?}` envelope; the loader →
  reducer → serializer pipeline produces it for any augmenting posting and
  any single-lot reduction.
- **Resolved-cost-date preservation is partially done.** The original red
  flag was that `Posting.Cost` after `loader.Load` was a `*ast.CostSpec`
  with `Date == nil`, so the serializer had to fall back to `txn.Date` to
  emit the fixture's `cost.date`. After PR #62, augmenting postings and
  single-lot reductions carry `*ast.Cost` with `Date` filled by
  `ResolveCost`, so no fallback is needed for those cases. Multi-lot
  reductions still retain `*ast.CostSpec` and lose per-lot date identity;
  the fix for that is upstream-style posting expansion, out of this
  plan's scope.

## High-level approach

Per-fixture incremental ratcheting. For each candidate:

1. Add the entry to `enabledParseFixtures` or `enabledCheckFixtures`.
2. Run `bazel test //pkg/compat/beancompat:{parse,check}_fixtures_test` and
   inspect the per-subtest result.
3. If PASS, commit the allowlist entry with the `verified YYYY-MM-DD` note
   the convention already uses.
4. If FAIL, read the cmp diagnostic to identify the divergence, then
   either:
   a. Apply a small targeted fix in the serializer / loader and re-run.
   b. If the divergence requires substantive new work (e.g. option
      retention, posting expansion), defer the fixture and document the
      blocker in this plan's "Deferred fixtures" section.

The ordering is deliberately bottom-up: confirm the cheap parse-tier ones
first, then take on the check-tier ones whose pipeline is heavier. A
fixture that turns out to be a one-line allowlist change is shippable
alone; a fixture that surfaces a real gap gets its own commit (and
possibly its own follow-up plan).

## Slices

### Slice 1 — Parse-tier easy wins: open / close / commodity

Add `close`, `commodity`, `open_no_currency`, `open_multi_currency` to
`enabledParseFixtures`. These fixtures exercise directives the parse-tier
serializer already supports (open, close, commodity each have a dedicated
`*Payload` helper); the source forms are trivial and the expected JSON is
shallow.

Expected outcome: all four PASS without code changes. If any fails, the
divergence is almost certainly a known-divergence note in the fixture
itself (e.g. currency list ordering on `beancount-parser-lima`) and
addressable in serializer code with a small patch.

Verification: `bazel test //pkg/compat/beancompat:parse_fixtures_test
--test_arg=-test.v` shows each as PASS, not SKIP. Commit message records
the allowlist diff and any incidental fix.

### Slice 2 — Parse-tier elided-amount sentinel

Add `missing_sentinel` to `enabledParseFixtures`. The fixture's source has
a posting with no explicit Amount (`Equity:Opening` on a two-line
transaction); the expected payload omits the `units` field for that
posting. The current Go path either emits `units: null` or constructs the
posting with a nil Amount that the serializer skips.

Investigate which the serializer does today. If it emits `null`, the
fixture comparer ("containment") accepts that as long as the schema does
not require `units` to be absent — read `match.go`'s containment rules
to confirm. If it does emit `null` and the comparer rejects, the fix is
to emit nothing (drop the key) when `Posting.Amount == nil`. Either way
the change is local to `postingPayload` in `serialize.go`.

### Slice 3 — Check-tier auto-balance

Add `transaction_auto_balance` to `enabledCheckFixtures`. The fixture's
source has an elided second posting (`Assets:Bank` with no Amount); the
expected payload's check-tier output shows the auto-balanced Amount
filled in (`-12.50 USD`). The reducer's `fillAutoPosting` already does
this, so the loader-produced AST should match.

Risk: if `missing_sentinel`'s parse-tier elided-amount emission diverged
in Slice 2 and the fix changed the auto-balanced rendering, re-confirm
here. Otherwise expected to PASS straight off.

### Slice 4 — Check-tier balance directive

Add `balance` to `enabledCheckFixtures`. The fixture exercises a balance
assertion directive after a single deposit transaction. The parse-tier
serializer already handles balance via `balancePayload`; the check-tier
output is structurally the same (no cost-related fields on a balance).

Confirm the check-tier path emits the balance directive identically.
If the booked transaction's posting amounts differ from parse-tier (they
should not, since this fixture has no booking-time interpolation), reduce
the diff to its cause and fix locally.

## Slicing rationale

- **One allowlist entry per commit.** When a fixture passes without code
  changes, the commit subject is "Enable beancompat fixture X"; when it
  pulls in a fix, the fix is co-committed and the message explains the
  divergence. Bisecting "which fixture started failing" stays a `git log
  --oneline` away.
- **Parse before check.** Parse fixtures exercise the lower-tier
  serializer alone; check fixtures additionally exercise the loader and
  the reducer. Catching a parse-tier serializer bug while looking at a
  parse fixture is cheaper than catching it while looking at a check
  fixture's full pipeline.
- **Easy-then-hard within a tier.** open / close / commodity are
  direct-payload directives the existing serializer already produces.
  `missing_sentinel` and the two check-tier fixtures each have one
  specific question to resolve, so they get their own slices.

## Deferred fixtures (excluded from this plan)

- `display_precision_by_currency.json` and `options_coverage.json`:
  require AST-level retention of `option` directives so the serializer
  has something to emit under the top-level `options` key. The current
  lowerer drops options at parse time. Tracked as Plan A; a separate
  plan file should be opened when option retention work begins.

## Done criteria

- All five in-scope parse-tier and both in-scope check-tier fixtures
  appear in `pkg/compat/beancompat/allowlist.go` with a verification
  date, OR the entry that did not make it has a documented blocker in
  this plan's "Deferred fixtures" section.
- `bazel test //pkg/compat/beancompat:{parse,check}_fixtures_test
  --test_arg=-test.v` shows the corresponding subtests as PASS, not
  SKIP.
- No new TODOs introduced beyond those already tracked in
  `docs/plans/cost-holder-interface.md` (multi-lot posting expansion,
  failed-posting removal).

## Open items for slice-level design

- `missing_sentinel` interaction with containment matching: confirm
  whether emitting `units: null` for an elided posting passes the
  fixture's expectation (which omits the key entirely). Decide at Slice
  2 implementation time.
- Whether to enable additional currently-disabled parse fixtures
  encountered while reading the archive (none expected, but the
  inventory at the time of plan writing is "10 parse / 3 check"; if
  upstream lands a new fixture mid-PR, the plan's slice list should
  grow to match).
