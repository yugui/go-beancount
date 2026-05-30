# Phase 9 — Deferred Work 7.1: OPEN/CLOSE/CLEAR entry-stream scoping

## Context

`pkg/query` (BQL engine) shipped its lean subset and the `balance` running-inventory column. The §7.1 deferred item in `pkg/query/ARCHITECTURE.md` describes the second half of that original entry: entry-stream **scoping**. Beanquery defines a sub-ledger via `table.evolve(...) → summarize` BEFORE row generation; it is not a FROM filter. Without scoping, BQL queries cannot express "as-of D" or income-clearing semantics that beanquery users routinely rely on; closing §7.1 brings the engine to BQL parity for date-range reporting and is a prerequisite for the daemon's reporting endpoints (Phase 10) and beansprout's query CLI (Phase 12).

The grammar `FROM [<expr>] [OPEN ON <date>] [CLOSE ON <date>] [CLEAR]` is already exercised verbatim in `pkg/compat/beancompat/serialize_test.go` (as Query-directive string content, not as a parsed query); landing the parse side closes a long-standing gap between what we serialize and what we can execute.

## Goal

Add full beanquery `OPEN ON D` / `CLOSE ON D` / `CLEAR` semantics to the BQL engine in `pkg/query`, parsed as a FROM-clause suffix and applied as a pre-pass over the directive stream consumed by `postings` and `entries`.

## Scope

### Included
- Grammar: `FROM [<filter-expr>] [OPEN ON <date>] [CLOSE ON <date>] [CLEAR]` (any subset, fixed order). Filter expression and scoping combine; filter still ANDs into the row predicate after scoping has reshaped the stream.
- A new internal subpackage `pkg/query/scope` exposing `Spec` + `View(*ast.Ledger, Spec) iter.Seq2[int, ast.Directive]` — pure function, fresh iterator per call, no ledger mutation.
- `CLOSE ON D`: drop entries dated `≥ D` (beanquery `summarize.truncate`).
- `OPEN ON D`: per-(account, position) opening-balance synthesis from balances of entries dated `< D`; income/expense routed to `account_previous_earnings`, other accounts to `account_previous_balances`; preserve `Open` directives dated `≤ D`; replace the prior tail with synthesized openings; keep entries dated `≥ D`.
- `CLEAR`: income+expense balances at the boundary date transferred to `account_current_earnings`. Boundary = `CLOSE ON Dc → Dc−1` else last-entry date else `time.Now().UTC()` truncated to date.
- Table integration via new `PostingsOver` / `EntriesOver` constructors taking an iterator factory; existing `Postings(l)` / `Entries(l)` become wrappers.
- Compiler plumbing: `selectTable` builds the scoped table from `scope.View(ledger, spec)`.
- Tests at parser, scope, table, and facade layers; extend `TestConcurrentRun` with a scoping query under `-race`.
- Update `ARCHITECTURE.md` §7.1 from "deferred" to "shipped" with the resolved-design summary.

### Excluded
- `PIVOT BY`, `HAVING`, subselects, `BETWEEN`, `BALANCES`/`JOURNAL`/`PRINT` statements, `prices` virtual table (§8).
- Price/valuation functions and `pass_context` (§7.2 — separate deferred item).
- Intra-query parallel executor (§7.3).
- Plugin loading (§7.4).

## User-supplied ideas (confirmed)
1. **All three operations together in one drop** — shared grammar, classifier, synthesizer, iterator wiring.
2. **Full beanquery semantics** — synthesized opening-balance transactions and close-to-equity transfers (not truncate-only).
3. **Allow FROM filter combined with scoping** — `FROM <expr> OPEN ON D CLOSE ON D CLEAR` valid; filter is independent of scoping.

## Red flags
- Synthetic transactions need `Span` (filename/lineno) — leave zero; existing `spanFilename`/`spanLineno` already render NULL on zero `Span`.
- `OptionValues` may be nil on hand-built test ledgers; its accessors are nil-safe (registry defaults) — verified.
- Several semantic edge cases must be confirmed against beanquery before merge (see §"Open items").

## Steps

Six ordered steps. CLOSE goes first to validate the scaffold against the simplest semantics; then OPEN (the bulkiest); then CLEAR (reuses OPEN's classifier).

### Step 1 — Parser tokens, AST node, `parseFrom` extension

**Functional requirements**
- Add `tokOpen`/`tokClose`/`tokClear`/`tokOn`; entries in `tokenKindNames` and `keywords` (lowercase: `"open"`, `"close"`, `"clear"`, `"on"`).
- Add `parser.Scoping{Open *time.Time, Close *time.Time, Clear bool, Pos Position}`; add `FromClause.Scoping *Scoping`.
- Grammar:
  ```
  FROM ::= 'FROM' [ expr ] [ 'OPEN' 'ON' date ] [ 'CLOSE' 'ON' date ] [ 'CLEAR' ]
  ```
  Fixed order. `parseFrom` tries to parse the expression only if the next token is not one of `tokOpen/tokClose/tokClear` (so `FROM CLEAR` works with no expr).
- Positioned errors: `OPEN` without `ON`, `OPEN ON` without date, `CLEAR ON <date>`, duplicate `OPEN`/`CLOSE`/`CLEAR`, empty FROM (`FROM` followed immediately by `WHERE`/EOF).

**Modules**
- `pkg/query/parser/token.go`, `pkg/query/parser/ast.go`, `pkg/query/parser/parser.go`.
- `pkg/query/parser/parser_test.go`, `pkg/query/parser/scanner_test.go`.

**Verification**
- Structural tests for each new form and the combined form; error-message + position tests for each rejection case.

**Quality**
- Positioned errors only; no panic on any input.

---

### Step 2 — `pkg/query/scope` skeleton + `CLOSE` truncation

**Functional requirements**
- New internal subpackage with public surface: `type Spec struct { Open, Close time.Time; Clear bool }` and `func View(l *ast.Ledger, s Spec) iter.Seq2[int, ast.Directive]`.
- Zero `Spec` returns `l.All()` directly (no allocation, no wrapping).
- `CLOSE`: filter to entries with `DirDate().Before(s.Close)` (strict `<`, matches beanquery `summarize.truncate`).
- `View` returns a fresh iterator each call; indices are 0-based dense (re-indexed).
- OPEN and CLEAR ignored in Step 2 with a guard in compile so tests cannot pass through prematurely.

**Modules**
- `pkg/query/scope/scope.go`, `pkg/query/scope/doc.go` (new), `pkg/query/scope/scope_test.go` (new).

**Verification**
- CLOSE drops entries on/after D and keeps all directive kinds dated `< D`; replayability (iterate twice, identical sequence); zero-`Spec` is identity; race-free under `-race`.

**Quality**
- Pure function of immutable inputs; no ledger mutation.

---

### Step 3 — Table integration

**Functional requirements**
- Add `PostingsOver(name string, all func() iter.Seq2[int, ast.Directive]) *Table` and `EntriesOver(name string, all func() iter.Seq2[int, ast.Directive]) *Table`.
- Existing `Postings(l) / Entries(l)` become one-line wrappers calling `…Over` with `l.All`.
- All existing column accessors work unchanged on synthesized directives (real `*ast.Transaction` values with zero `Span`).

**Modules**
- `pkg/query/table/postings.go`, `pkg/query/table/entries.go`, `pkg/query/table/doc.go`.
- `pkg/query/table/postings_test.go`, `pkg/query/table/entries_test.go`.

**Verification**
- One test per table that passes a hand-built iterator yielding a zero-`Span` synthetic transaction; confirm `filename`/`lineno` return NULL and all other accessors return non-NULL values.

**Quality**
- Pure-accessor contract preserved; fresh iterator per `Rows()` call.

---

### Step 4 — Compiler plumbing

**Functional requirements**
- `selectTable` builds the table over `scope.View(ledger, spec)` where `spec` is derived from `sel.From.Scoping` (or zero when nil).
- For zero `spec`, the result is observably identical to today's table.
- New compile-time errors: none specifically (parser handles syntactic rejections; existing "unknown table or column" still covers misnamed bare-name FROM).
- Scoping on `entries` is allowed; synthesized rows surface as `type='transaction'` entries.

**Modules**
- `pkg/query/exec/compile.go`.
- `pkg/query/errors_test.go` (smoke tests for facade-visible errors).

**Verification**
- Existing tests still pass (no-FROM, FROM-as-filter, FROM-table); a smoke test for `FROM CLOSE ON D` end-to-end through `Compile` (with Step 2's CLOSE semantics).

**Quality**
- Decision 6 preserved: scoping spec fixed at Compile; `scope.View` allocates only per-`Run` iterator state.

---

### Step 5 — `OPEN ON D` summarization

**Functional requirements**
- Algorithm (mirrors beanquery `summarize.summarize`):
  1. Walk `l.All()`, fold every `*ast.Transaction.Posting` dated `< D` into per-account `*inventory.Inventory` (`inv[acct].Add(pos)`); preserve booked `Cost` lots.
  2. Classify each account by `Account.Root()` against `opts.String("name_income")` / `opts.String("name_expenses")`. Income+expense → `account_previous_earnings` (default `Earnings:Previous`); other → `account_previous_balances` (default `Opening-Balances`).
  3. For each non-empty account inventory, synthesize one `*ast.Transaction` dated D, `Flag='*'`, `Narration="Opening balance for '<acct>'"`, posting pairs (credit acct preserving Cost, debit routing equity). Zero `Span`; meta marker `{__synthetic__: 'opening'}`.
  4. Preserve `Open` directives dated `≤ D` from the original stream.
  5. Stream order: kept `Open` directives → synthesized opening transactions (accounts sorted lexicographically) → original directives dated `≥ D` in canonical order.
- Boundary date convention: synthesized transactions dated `D` (matches beanquery; flagged in Open items).

**Modules**
- `pkg/query/scope/open.go` (new) — `openSummarize`, `accountClassifier`, `synthesizeOpenTxn`.
- `pkg/query/scope/scope_test.go` — semantic tests.

**Verification**
- Multi-year fixture; `OPEN ON 2022-01-01` produces one synthesized txn per (account, position) carrying as-of-2021-12-31 inventory; income/expense routes to `Earnings:Previous`, asset/liability/equity to `Opening-Balances`; classification respects a custom `OptionValues.name_income`.
- End-to-end in `pkg/query/query_test.go`: `SELECT account, sum(number) FROM postings OPEN ON 2022-01-01 GROUP BY account`.

**Quality**
- Inventory walk uses `inventory.Inventory.Add` (decimal-exact, no float); per-`Run` allocation; nil-safe `OptionValues`.

---

### Step 6 — `CLEAR`

**Functional requirements**
- Boundary date: `Close.AddDate(0,0,-1)` if `Spec.Close` set; else last entry's `DirDate()` if non-empty; else `time.Now().UTC()` truncated to date.
- Algorithm (mirrors `summarize.clear`):
  1. Walk the post-OPEN-and-CLOSE stream, accumulating per-account inventories for income- and expense-rooted accounts only.
  2. For each non-empty account, synthesize one `*ast.Transaction` at boundary date, narration `"Clear balance for '<acct>'"`, posting pairs: credit acct (zero it out), debit `account_current_earnings` (default `Earnings:Current`). Meta marker `{__synthetic__: 'clearing'}`.
- Output order: kept directives → clearing transactions sorted by account.

**Modules**
- `pkg/query/scope/clear.go` (new) — `clearTail`, boundary-date resolution.
- `pkg/query/scope/scope_test.go` — semantic tests.
- `pkg/query/query_test.go` — end-to-end `CLEAR` + combined scoping tests.
- `pkg/query/concurrency_test.go` — add a scoping query (`OPEN ON D CLEAR GROUP BY account`) to `TestConcurrentRun` under `-race`.
- `pkg/query/ARCHITECTURE.md` §7.1 — rewrite as shipped.

**Verification**
- Income/expense `sum(number)` is zero per-account after `CLEAR`; `Earnings:Current` carries the offset; concurrent run with scoping is race-free.

**Quality**
- Reuses Step 5 classifier; per-`Run` allocation; deterministic ordering.

## Alternatives discussed

### A. Where the summarize logic lives
- **(Chosen)** New `pkg/query/scope` subpackage. Self-contained input/output; clean dependency direction (`exec → scope → ast, inventory`); leaves `table` as a passive schema layer.
- Rejected: inside `exec` (forces `exec → inventory` coupling); inside `table` (muddies schema layer with business logic).

### B. How the scoped stream flows into the table
- **(Chosen)** New `*Over` constructors taking an iterator factory. Existing `Postings`/`Entries` become thin wrappers; no `scope.Spec` leaks into `pkg/query/table`.
- Rejected: passing a `Spec` directly to existing constructors (couples lowest layer to scoping concept); per-call closure ownership inside `exec` (this is what we do anyway — the table sees only a factory).

### C. Synthesizing rows: real `*ast.Transaction` vs. new directive kind
- **(Chosen)** Real `*ast.Transaction` values with zero `Span` and preserved `Cost`. All existing accessors work unchanged; `balance` running inventory folds correctly; `filename`/`lineno` already NULL-safe on zero `Span`. Mark with `meta['__synthetic__']` for distinguishability.
- Rejected: new `*ast.SyntheticTransaction` kind. Ripples through `pkg/ast`, validation, printing, every iterator; too high a cost for a labeling problem solvable in meta.

### D. Driving OPEN's balance walk
- **(Chosen)** Direct walk + `inventory.Inventory.Add` on already-booked postings.
- Rejected: re-run `inventory.NewReducer(l.All()).Walk(...)`. Re-books an already-booked ledger; the Reducer is not concurrent-safe; we'd pay an allocation tax to construct a fresh one per `Run`.

### E. Boundary date for OPEN synthesis (D vs. D−1)
- **(Chosen)** `D` — matches beanquery `summarize.summarize`. Synthesized openings sit at the start of the kept window.
- Rejected: `D−1` — only attractive if some downstream strict `≥ D` filter were assumed; diverging silently from beanquery is a worse default.

### F. Grammar ordering of `OPEN`/`CLOSE`/`CLEAR`
- **(Chosen)** Fixed order (OPEN before CLOSE before CLEAR). Matches beanquery; tighter grammar = simpler errors.
- Rejected: any order. No real upside; humans write them in canonical order.

## Recommended approach

The six-step plan above, with subpackage `pkg/query/scope` owning the pre-pass, `*Over` table constructors as the integration seam, real synthesized `*ast.Transaction` values, and beanquery-canonical semantics throughout. Decision 6 (concurrent read-only) is preserved by keeping `scope.View` a pure function of immutable inputs, building the scoped `*Table` once at `Compile` time, and allocating only per-`Run` iterator state.

## Critical files

- `pkg/query/parser/{token,ast,parser}.go` — Step 1.
- `pkg/query/scope/{scope,doc,open,clear}.go` (new) — Steps 2, 5, 6.
- `pkg/query/table/{postings,entries,doc}.go` — Step 3.
- `pkg/query/exec/compile.go` — Step 4.
- `pkg/query/{query_test,concurrency_test,errors_test}.go` — facade-level tests across steps.
- `pkg/query/ARCHITECTURE.md` §7.1 — knowledge migration at end.

## Reused existing functions / utilities

- `ast.Ledger.All()` — canonical-order directive iteration.
- `ast.Account.Root()` — account classification for income/expense detection.
- `ast.OptionValues.String("name_income"|"name_expenses"|"account_previous_balances"|"account_previous_earnings"|"account_current_earnings")` — nil-safe option lookups with registered defaults.
- `inventory.Inventory.Add(Position)` — decimal-exact balance accumulation; preserves booked `Cost` lots.
- `inventory.LotFromCost(*ast.Cost)` — for posting positions during the balance walk.
- `table.spanFilename` / `table.spanLineno` — NULL-safe on zero `Span`, so synthetic directives need no new branches.
- `table.postingPosition` — already builds an `inventory.Position` from a posting (units + lot).

## Open items (verify against beanquery before merge)

1. OPEN-synthesized transaction date: `D` vs. `D−1`. Assumed `D`.
2. CLEAR boundary when no CLOSE specified and ledger non-empty: assumed last entry's date.
3. OPEN preserves `Open` directives dated `≤ D` (vs. only `< D`).
4. CLEAR groups by full account + currency (vs. by root + currency).
5. `Before(D)` vs. `<= D` predicate on the OPEN boundary day for transactions dated exactly `D`.
6. Whether to include the `__synthetic__` meta marker on synthesized transactions (low-risk insurance; decide before merge).

## Verification (end-to-end)

- `bazel build //pkg/query/... && bazel test //pkg/query/...` — all green.
- `bazel test //pkg/query:concurrency_test --runs_per_test=10 --test_arg=-race` — concurrent scoping queries race-free.
- Manual: `bazel run //cmd/beanquery -- 'SELECT account, sum(number) FROM postings OPEN ON 2024-01-01 GROUP BY account ORDER BY account'` on a sample multi-year ledger; visually confirm opening balances appear on income/expense accounts as transfers to `Earnings:Previous` and on asset/liability accounts as transfers from `Opening-Balances`.
- Manual: `bazel run //cmd/beanquery -- 'SELECT account, sum(number) FROM postings CLEAR GROUP BY account'` — income+expense subtree sums to zero; `Earnings:Current` carries the offset.
