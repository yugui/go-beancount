# Plan: pad-aware redesign of `sprout/comprehensivebalance`

## Context

The current `pkg/ext/postproc/sprout/comprehensivebalance` plugin
(landed in PR #90 / commit `3c87ad0`) computes the per-currency
balance of each target account by itself, by summing the
`posting.Amount.Number` of every prior `*ast.Transaction` posting on
the account. This is a deliberate simplification documented in the
plugin's `doc.go` (Deviations section).

The simplification is not faithful to beansprout-Python in one
practical scenario: when an account's balance is established (in
whole or in part) by a `pad` directive. Python's reference
implementation routes through `beancount.core.realization.realize`,
which sees the `pad → balance` pair as part of the inventory pipeline
and therefore reflects pad-injected positions; our Go port walks
Transactions only and ignores Pads, so accounts that rely on padding
get a raw-sum balance that under-represents reality. A user who
writes:

```beancount
2024-01-01 pad Assets:Foo Equity:Opening
2024-01-02 balance Assets:Foo 1000 USD

2024-01-03 custom "comprehensive_balance" Assets:Foo "
  1000 USD
"
```

sees the Custom directive's assertion fail in Go (the plugin sees
zero USD in Foo) even though the same ledger validates cleanly in
beansprout-Python.

The pipeline that surrounds the plugin makes a better strategy
possible. From `pkg/loader/loader.go:144-172`:

```
1. preBuiltins (document scanning)
2. booking            ← fills auto-posting amounts, resolves Cost specs
3. postproc.Apply     ← this plugin runs here
4. postBuiltins: pad → balance → validations
```

Step 4 already contains the pad and balance machinery that knows how
to honor padding. If this plugin emits `*ast.Balance` directives
instead of evaluating balances itself, the postBuiltins pipeline does
the evaluation — including pad — and the Go port becomes faithful to
beansprout-Python by construction.

## Goal

Replace the self-computed balance check in
`sprout/comprehensivebalance` with a generator that emits one
`*ast.Balance` directive per `(account, commodity)` pair the user is
asserting against, and let the postBuiltins `pad → balance` pipeline
do the actual evaluation.

## Scope

**Included**:
- `pkg/ext/postproc/sprout/comprehensivebalance/{plugin.go,plugin_test.go,doc.go}` redesign.
- Verification against pad scenarios that currently produce false negatives.

**Excluded**:
- `sprout/fiscalincomeexpense`. That plugin asserts a **period delta**
  (`actual_change_over_period vs expected`), which `*ast.Balance`
  directives cannot express (they are cumulative-balance assertions).
  fiscalincomeexpense's current self-computed approach stays; the only
  cleanup there is the doc.go wording (already done in PR #90 — the
  "raw-sum lot mismatch / nil Amount" deviations are not real
  divergences since booking runs first).
- Changes to `pkg/validation/pad` itself. The plan assumes pad already
  handles multi-commodity balance assertions correctly for the
  same-(account, date) case; that needs verification (see "Open
  questions" below) before implementation.

## Recommended approach

### Per-directive algorithm (replaces `apply()`)

For each `*ast.Custom` with `TypeName == "comprehensive_balance"`:

1. Parse the directive's `(account, body)` pair (unchanged).
2. Parse the body into a `[]assertion` slice (unchanged) — each
   assertion is `(amount, optional tolerance, currency)`.
3. **Compute the commodity universe** for `(account, date)`:
   - `txnCurrencies`: every `Amount.Currency` of every prior
     `*ast.Transaction` posting whose `Account == account`. Walk
     `in.Directives` in source order; stop at the Custom.
   - `balanceCurrencies`: every `Amount.Currency` of every prior
     `*ast.Balance` directive whose `Account == account`. Same walk.
   - `listedCurrencies`: every currency mentioned in the Custom's
     body.
   - `universe = txnCurrencies ∪ balanceCurrencies ∪ listedCurrencies`
4. **Generate one `*ast.Balance` per currency in `universe`**:
   - If `currency ∈ listedCurrencies`: use the user's `(amount,
     tolerance)`.
   - Otherwise: `amount = 0`, `tolerance = nil`.
   - All generated balances carry the Custom's `Date` and `Span`
     (re-anchored for diagnostics).
5. **Remove the original Custom** from `Result.Directives`.

The generated `*ast.Balance` directives are observed in step 4 of the
loader pipeline (the `balance` postBuiltin), which knows how to
consult pending pads, look up tolerance defaults, and emit the right
diagnostic on mismatch.

### Pending-pad interaction (working as intended)

`pkg/validation/pad/plugin.go:96-181` tracks at most one pending pad
per account; the pad fires against the **first** subsequent
`*ast.Balance` it encounters on that account, and `pp.used = true`
afterward.

When this plugin's generated balance is the first balance after a
pad, the pad fires against our generated balance — re-routing the
pad's "destination" away from a later user-written balance. This is
the **intended** semantic: `comprehensive_balance` is itself a
balance assertion, and the user placing it after a pad is asserting
"this account must equal X right here." A later user-written balance
on the same account, if any, evaluates without that pad's help —
which is the right outcome.

### Commodity-universe rule rationale

The three-way union (`txn ∪ prior balance ∪ listed`) is the smallest
set that covers every commodity the user could reasonably expect to
be checked:

- `txn`: commodities the user added directly.
- `prior balance`: commodities the user asserted exist (potentially
  pad-bridged). Restricting to balance directives strictly **before**
  our Custom (not all balance directives in the ledger) avoids using
  future facts to drive present assertions, and matches the
  "snapshot at directive's date" model the rest of beancount uses.
- `listed`: explicit user intent — always honored even if the
  commodity has never been touched (e.g. user wants to assert the
  account holds exactly `0 GBP`).

Commodities that exist on the account **only** because of a pad+
balance pair that is itself in the future (after our Custom) will
not be in the universe and therefore will not get a zero-assertion.
That is the correct outcome: the user has not yet declared those
commodities at our directive's date.

## Implementation sketch

### Files modified

- `pkg/ext/postproc/sprout/comprehensivebalance/plugin.go`
  - Remove `accumulate` + the per-currency running-sum logic.
  - Remove the `unlisted-currency zero-balance` synthesis that walks
    the accumulated `balances` map.
  - Add `collectUniverse(directives []ast.Directive, target ast.Account, before time.Time) map[string]struct{}` that walks Transactions and Balances strictly before the target date.
  - Rewrite `apply()` around `collectUniverse` + a single pass that
    emits one `*ast.Balance` per commodity in the universe.
- `pkg/ext/postproc/sprout/comprehensivebalance/plugin_test.go`
  - Keep the existing tests (they cover the no-pad case and should
    still pass).
  - Add: `TestPadBridgedAccount` — `pad → balance → custom`, verify
    the Custom-driven balance reads through the pad correctly.
  - Add: `TestPendingPadAtCustomDate` — `pad → custom (no
    intervening user balance)`, verify the Custom's generated
    balance becomes the pad's destination and a later user-written
    balance evaluates without pad's help.
  - Add: `TestPriorBalanceContributesCommodity` — commodity appears
    only in a prior `*ast.Balance` (no txn before the Custom);
    verify zero-assertion still emitted (user listed it as zero
    expected) and that the assertion either passes (account is
    actually zero) or fails (account is non-zero) per `balance`
    plugin's diagnostic.
  - Add: `TestFutureBalanceIgnored` — commodity appears in a
    `*ast.Balance` that is **after** our Custom; verify it is **not**
    in the universe.
- `pkg/ext/postproc/sprout/comprehensivebalance/doc.go`
  - Replace the "raw-sum vs realization" Deviations bullet with a
    section explaining the delegation model (this plugin emits
    Balance directives; pad+balance evaluate them).
  - Document the commodity-universe rule (txn ∪ prior balance ∪
    listed) and explicitly call out the pending-pad semantic
    (comprehensive_balance is a balance assertion and pad-eligible).

### Files unchanged

- `pkg/ext/postproc/sprout/comprehensivebalance/BUILD.bazel` — no new
  deps (the generated `*ast.Balance` is part of pkg/ast already).
- `pkg/validation/pad/plugin.go` — assumes existing
  multi-commodity-per-date handling. See Open questions.

## Open questions to resolve before implementation

1. **Multi-commodity pad fan-out**: when a single pad is followed by
   N `*ast.Balance` directives on the same `(account, date)` (one per
   commodity), can `pkg/validation/pad` inject **multiple** per-
   commodity adjustment postings to satisfy all N, or does it fire
   once against the first balance and leave the rest unbridged?
   Inspect `pkg/validation/pad/plugin.go:resolveBalance` and its
   `pp.used` clearing logic. If the answer is "fires once", the
   redesign needs either:
   - (a) a small change to `pkg/validation/pad` to support per-
     commodity pad fan-out (preferred — fixes pad in general), or
   - (b) this plugin emits at most one generated balance per pending-
     pad situation (worse — defeats the comprehensive coverage goal
     in pad scenarios).

2. **Ordering of generated balances at the same date**: this plugin
   emits up to N balances dated at the Custom's date. The output
   slice must place them in a stable order (alphabetical by
   currency?) so test golden output is deterministic. The pad
   plugin's fan-out logic, if it exists, must consume them in that
   same order.

3. **Tolerance defaults for zero-assertions**: when this plugin emits
   `Balance(account, 0, currency)` for an unlisted commodity, what
   tolerance does the downstream `balance` plugin apply? The default
   is exponent-derived from the asserted amount; for an exact-zero
   integer that yields a very loose tolerance. Verify that the
   downstream tolerance is sensible for zero-assertions (it should be
   — `balance` handles zero specially via per-account / per-commodity
   inference). If not, the generated balance might need an explicit
   tight tolerance.

## Verification

- `bazel test //pkg/ext/postproc/sprout/comprehensivebalance/...` —
  all existing tests must still pass; the new pad-aware tests
  exercise the new behavior.
- `bazel test //...` — no regression elsewhere; in particular
  `pkg/validation/pad` and `pkg/validation/balance` tests must still
  pass.
- Manual smoke: a fixture that combines `pad`, `balance`, and
  `comprehensive_balance` in various orderings, run through
  `cmd/beancheck`, with output diff'd against beansprout-Python's
  output on the same fixture for at least one pad-heavy ledger.

## Commit strategy

Single commit on a fresh branch (`claude/<slug>`), subject:
`sprout/comprehensivebalance: delegate balance evaluation to pad→balance pipeline`.
Body explains the divergence-from-Python problem, the delegation
model, the commodity-universe rule, and the pending-pad semantic.
Open as separate PR from #90.

## Risks

- If Open question (1) resolves to "pad needs a fix too," scope grows
  to include `pkg/validation/pad`. Estimate is small (one helper to
  iterate per-commodity), but it shifts the PR from "plugin tweak" to
  "plugin tweak + pad enhancement." Evaluate before committing to the
  branch.
- Changing the generated Balance ordering could break golden tests
  in adjacent packages that use comprehensive_balance fixtures.
  Search for such fixtures and update goldens in the same commit.
