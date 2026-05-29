# `pkg/query` — BQL Query Engine Architecture

This document is the design record for the Beancount Query Language (BQL)
engine. It states the lean-vs-deferred boundary and **why** it sits where it
does, the cross-cutting invariants and where each is enforced, the
detailed-design decisions resolved while building, and — most importantly for a
future maintainer — **every deferred feature with its concrete attach point and
the hints known today**. Do not let this drift from the code; update it when a
deferred item lands or an invariant moves.

## 1. Overview and scope

The engine runs a BQL `SELECT` over an already-loaded, validated, booked,
pad-resolved `*ast.Ledger`. It never re-runs load/book/validate — that is
`pkg/loader`'s job. Full BQL compatibility is the long-term goal; the first
deliverable is a useful **lean subset** built on abstractions (a typed `Value`,
`Table`/`Column`, a polymorphic function registry) so the deferred features and
the remaining beanquery tables slot in mechanically, without reworking the core.

Downstream consumers are the planned `bean-daemon` (`POST /query`) and
`beansprout query`, plus the `beanquery` CLI shipped here. Because the daemon
serves concurrent requests, **safe concurrent read-only querying over one shared
immutable ledger is a first-class, tested requirement**, not an afterthought
(see §4, Decision 6).

### Lean subset (shipped)
`SELECT [DISTINCT] (<targets>|*) [FROM <table-name>|<filter-expr>] [WHERE <expr>]
[GROUP BY ...] [ORDER BY ... ASC|DESC] [LIMIT n]` over two virtual tables
(`postings` default, `entries`), including the `postings` `balance`
running-inventory column; a full value/type system with explicit NULL;
arithmetic, comparison, regex `~`, 3-valued boolean logic, `IN` over lists and
sets; a polymorphic, extensible function registry with a built-in library
(date/string/account/extractor scalars, `getitem`, and
count/sum/min/max/first/last aggregators); and the `beanquery` CLI.

### Deferred / excluded
See §7 (deferred, with hints) and §8 (excluded).

## 2. Package layout

| Package | Role |
|---|---|
| `pkg/query/types` | Sealed `Value` interface, `Type` enum, NULL model, total-order `Compare`, `Set`/`Dict` containers. |
| `pkg/query/api` | Thin function descriptor: `Function`, `Flavor`, `Scalar`, `Accumulator`, `NewAccumulator`. Depends only on `types` (+ast/inventory via types). |
| `pkg/query/env` | Global registry (`Register`, panic-on-conflict) + compile-time overload `Resolve`. |
| `pkg/query/env/std` | Built-in function library; self-registers in `init()`; activated by a blank import. |
| `pkg/query/parser` | Hand-written scanner + recursive-descent parser → untyped `*Select` AST. No CST/trivia. |
| `pkg/query/table` | Virtual tables (`Postings`, `Entries`) = typed `Column`s + lazy re-runnable `Rows`. |
| `pkg/query/exec` | Compiler (type-check, overload bind, aggregate classify), operators, execution pipeline. |
| `pkg/query` (facade) | `Compile`/`Run`/`Query`, `Result`/`Column`. Thin glue. |
| `cmd/beanquery` | CLI: loader → query → aligned table. Glue only. |

The `api`-vs-`env` split mirrors `pkg/ext/postproc/api`-vs-`postproc` exactly,
so the deferred `goplug` loader and the `std` library register against the thin
`api` + `env` surface with no rework (§7.4).

## 3. Canonical reference — why the boundary is where it is

The old `beancount.github.io` BQL doc is **superseded**; where descriptions
conflict, `github.com/beancount/beanquery` is canonical. The one deliberate
exception is the `balance` column (fact 2 below): there the documented BQL
behavior and the original `bean-query` are authoritative, because the modern
`beanquery` binary diverges from its own documentation. Four canonical facts
shaped this design and justify what was deferred:

1. **FROM-expr ≡ WHERE.** beanquery compiles a FROM *expression* against the
   *same* table columns as WHERE and ANDs them into one row predicate
   (`c_where = EvalAnd([c_from_expr, c_where])`). There is **no** entry-vs-posting
   two-level namespace. → Implemented in `exec.selectTable` + `compilePredicate`.
2. **`balance` is the cumulative inventory of the *selected* rows.** Per the BQL
   doc, `balance` renders "the cumulative balance of the selected postings rows
   … based on the previous selected rows", and the original `bean-query` folds
   each posting into the running inventory *inside* the `if c_where` block — so
   it accumulates only over rows passing FROM/WHERE, in scan order, inclusive of
   the current row. It is therefore **filter-dependent** (a single-account
   filter yields a per-account-looking register). The modern `beanquery` binary
   instead accumulates over the full pre-filter stream (a global inventory); we
   do **not** follow that. Because "selected" is only known after filtering,
   this is computed in the executor's row scan, not the table. → Realized by the
   shipped `balance` column (§6; §7.1).
3. **Entry-stream scoping = OPEN/CLOSE/CLEAR**, applied via
   `table.evolve(...)` → `summarize` *before* row generation — this, not the
   FROM expression, defines a sub-ledger. → Deferred with `balance` (§7.1).
4. **`meta` is a `dict` column read via `getitem`.** beanquery's `meta()`
   *function* is a stub (`raise NotImplementedError`); real access is
   `getitem(dict, key[, default])`. And **`pass_context` functions** inject a
   query-wide, **init-time, immutable** context (price map, open/close maps,
   options) once — *not* a per-row entry handle. → We expose `meta` as a Dict
   column, make `meta('k')` compiler sugar for `getitem`, and reserve a
   `PassContextFlavor` seam for the deferred price/directive functions (§7.2),
   which keeps per-row evaluation pure and therefore concurrency-safe.

## 4. Cross-cutting invariants (and where enforced)

These are binding. Changing one ripples across packages; the "enforced in"
column is where to look before touching it.

1. **Sealed `Value` interface; exact, immutable values.** Enforced in
   `types/value.go` (`sealedValue()` marker; deep-copied decimals/inventories)
   and `types/compare.go` (total order; NULL-last; never float). ORDER BY,
   min/max, first/last, and DISTINCT all bind to `Compare`.
2. **Compile-time polymorphic dispatch.** Columns carry a static `Type`
   (`table.Column.Type`); `exec` types expressions bottom-up; `env.Resolve`
   binds one overload (exact > fewest-widenings; widening set = `Int→Decimal`).
   No per-row resolution.
3. **Two hot-path flavors: scalar + aggregator**, plus a reserved
   `PassContextFlavor` (`api.Flavor`). `env.validate` *rejects* PassContext
   today — see §7.2 for the one-line relaxation that re-enables it.
4. **Mergeable aggregator contract** `Add`/`Merge`/`Result` with the law
   "Add-then-Merge ≡ Add-all" (`api.Accumulator`). The lean executor uses one
   accumulator per group and **never calls `Merge`** (verified: no `.Merge(`
   call in `exec`). The law is what makes the deferred parallel executor (§7.3)
   additive; it is tested directly in `env/std/aggregate_internal_test.go`
   because no query-level path exercises it yet.
5. **Registry split mirrors `pkg/ext/postproc`.** `api` is types-only; `env`
   holds the map + `Register` + `Resolve`. `std`/`goplug` import both, exactly
   as postproc plugins import `api` + `postproc`.
6. **Read-only concurrency (tested).** `table` accessors are pure over an
   immutable row handle; `Table.Rows` returns a fresh iterator each call;
   `Compiled` is immutable and `Run` allocates all per-execution state locally.
   N goroutines may `Compile`/`Run`/`Query` one shared ledger with no locking.
   Proven by `table.TestConcurrentReadIsRaceFree` and the facade's
   `TestConcurrentRun` (both under `-race`).
7. **FROM table-vs-filter = parse-then-classify.** The parser stays
   catalog-free (`parser.FromClause{Expr, Name, IsBareName}`); the compiler
   (`exec.selectTable`) classifies a bare identifier as a table reference iff it
   names a catalog table, otherwise treats FROM as a filter over `postings` and
   ANDs it with WHERE (`compilePredicate`).
8. **Hand-written recursive-descent parser, no CST/trivia; typed plan over a
   lazy `Table`/`RowSource`.** Buffering happens only at GROUP BY / ORDER BY /
   DISTINCT (`exec/run.go`).
9. **Subpackage layout** as in §2; keep `api` thin and free of `env`.

## 5. Pipeline

**Compile** (`exec.Compile`): select table + classify FROM (Decision 7) →
AND-merge FROM-filter and WHERE into one Bool predicate over the table columns →
compile GROUP BY / targets / ORDER BY into typed `cexpr` trees → resolve column
refs against `Table.Column` (case-insensitive), operators (type-checked), and
function overloads (`env.Resolve`) → rewrite `meta('k')` to `getitem(meta,'k')`
→ classify aggregate slots, reject misplaced/nested aggregates and ungrouped
columns. Produces an immutable `*Compiled`. Errors are positioned, never panics.

**Run** (`Compiled.Run`): scan `Table.Rows()` → predicate filter (only TRUE
passes; NULL/FALSE excluded) → fold the passing row's `position` into the
running balance when the query reads `balance` (§3.2) → either scalar projection
or group+aggregate (one accumulator per slot per group, first-seen group order)
→ DISTINCT (true value-equality via `Compare`, NULL==NULL) → stable ORDER BY
(NULL-last asc, `Desc` negates) → LIMIT. `ctx` is checked once per input row.

The **single parallel-executor insertion point** is the input-row scan in
`run.go`, documented there (§7.3).

## 6. Resolved detailed-design decisions

- **`meta` on `postings` is posting-only** (not merged with the parent
  transaction). The `meta` column is always a (possibly empty) Dict, never
  NULL; `getitem` returns NULL on a missing key or NULL dict. A merged variant
  is a future option (§7.5).
- **`payee` empty → NULL; `narration` always String** (even if empty). On
  `entries`, `flag`/`payee`/`narration` are NULL for non-Transaction directives.
- **`flag` (postings)** = posting flag if set, else transaction flag; NULL if 0.
- **`tags`/`links`** = a (possibly empty) Set where the directive carries them;
  typed NULL on `entries` for directive kinds with no tags/links concept.
- **`@@` (total) price → per-unit** = total ÷ |units| at apd precision 34;
  NULL if units are zero/absent. **`cost_number`** uses `ast.PerUnitCost`
  (handles both booked `*ast.Cost` and `*ast.CostSpec`), not the literal
  `GetPerUnit().Number`, so it stays consistent with the other cost columns.
- **`balance` is the cumulative inventory of the *selected* rows** (canonical
  §3.2): the executor folds each predicate-passing row's `position` into one
  running inventory with `inventory.Inventory.Add`, in input-scan order,
  inclusive of the current row, and a `balanceExpr` reads a snapshot. It is
  **filter-dependent** (a single-account WHERE yields that account's register;
  no filter accumulates over everything), **not** per-account and **not** the
  full-stream global inventory the modern `beanquery` binary produces.
  Implementation: `balance` is a declared postings column with a NULL
  placeholder accessor (the value is executor-supplied); the compiler rewrites a
  reference to it into `balanceExpr` and records the position accessor to fold.
  `ORDER BY`/`DISTINCT`/`LIMIT` run on rows whose balance was already computed in
  scan order. `ectx.balance` is per-`Run` local state, preserving Decision 6.
- **NULL-literal typing**: a bare `NULL` has static type `types.Invalid`,
  treated as compatible with any operand; the result takes the sibling operand's
  type. This and `getitem` are the only dynamically-typed paths.
- **`getitem` is dynamically typed**: declared `Out` is `types.Invalid` because
  metadata values are heterogeneous; it returns each value with its own runtime
  kind. The optional default is restricted to a **String** in the lean subset
  (the registered overloads are `getitem(Dict,String)` and
  `getitem(Dict,String,String)`), which is exactly what `meta('k','fallback')`
  needs.
- **Arithmetic result types**: `Int⊕Int → Int` for `+ - * %`; `/` always →
  `Decimal`; any `Decimal` operand → `Decimal`. Division/modulo by zero is a
  runtime query error.
- **Comparisons are non-associative**: the parser rejects `a = b = c` (matches
  beanquery). The predicate must be Bool (or untyped NULL).
- **`Set`/`Dict` ordering** in `Compare` is deterministic and total
  (lexicographic over sorted elements / keyed pairs); it is internal and need
  not match beanquery byte-for-byte.
- **Date-function divergences from beanquery (flagged to revisit)**:
  `weekday(date)` returns the English weekday name (upstream: an int index);
  `quarter(date)` returns int 1..4 (upstream: `"YYYY-Qn"`); `yearmonth(date)`
  returns `"YYYY-MM"` (upstream: a month-truncated date). Chosen for lean
  pragmatism; revisit for parity when a consumer needs it.
- **Aggregate-mixing check** runs over the *compiled* tree (aggregate calls
  already replaced by slot refs), matching group keys by bare column name.
  Limitation: `GROUP BY year(date)` does not cover a bare `date` in a target;
  generalize when non-trivial grouped expressions are needed.

## 7. Deferred work — attach points and hints

Each item below is deferred *by design*, with the concrete seam already in place
so it lands without reworking the core. These are not vague TODOs.

### 7.1 OPEN/CLOSE/CLEAR entry-stream scoping
The `balance` running-inventory column — the first half of the original §7.1 —
**shipped**. It is the cumulative inventory of the *selected* rows, computed in
the executor's row scan after the predicate filter (see §3.2 and §6 for the
resolved semantics): `balance` is a declared postings column whose value is
supplied by `balanceExpr`/`Compiled.accumulateBalance`, not the table accessor.
The earlier hints here — to drive `inventory.Reducer.Walk`, and (in the first
shipped attempt) to accumulate a global pre-filter inventory in the table
producer — were both **superseded**: the former is per-account, the latter
matched only the modern `beanquery` binary, which contradicts the BQL doc.

What remains deferred is the entry-stream **scoping** that the original item
bundled with `balance` (canonical fact §3.3):
- **Scoping**: implement OPEN/CLOSE/CLEAR as a pre-pass that summarizes/opens
  the directive stream before row generation (beanquery's `table.evolve` →
  `summarize`), not as a FROM expression. This needs FROM-clause grammar
  (`OPEN ON <date>` / `CLOSE ON <date>` / `CLEAR`), a summarize/clamp/
  close-to-equity pre-pass, and compiler plumbing to pass the dates to the table
  constructor.

### 7.2 Price / valuation (`value`, `convert`, `getprice`) + `pass_context`
- **Seam**: `api.PassContextFlavor` already exists. `env.validate` currently
  rejects it ("uses reserved PassContextFlavor"); relaxing that check is the
  enabling change. Give the descriptor a context-aware implementation field
  (e.g. `func(ctx *QueryContext, args []types.Value) (types.Value, error)`),
  where `QueryContext` is a **query-wide, init-time, immutable** bundle
  (price map, open/close directive maps, options) built once at `Compile`/`Run`
  start and shared read-only — *not* a per-row entry handle (canonical §3.4).
  Because it is immutable and built once, it preserves Decision 6 concurrency.
- Plumb the price map from the loaded ledger (prices live in the directive
  stream); `value`/`convert`/`getprice` then read it from the context.

### 7.3 Intra-query parallel executor
- **Seam**: the input-row scan in `exec/run.go` (documented there). Partition
  the scan → per-shard filter/project, or per-group accumulators per shard →
  merge: aggregate partials via `api.Accumulator.Merge` (the law makes this
  exact), scalar outputs by stable concat → then DISTINCT/ORDER BY/LIMIT run on
  the merged rows unchanged.
- The mergeable contract (Decision 4) is already implemented and tested, so this
  is purely additive. **Measure first** — there is no first-deliverable consumer
  that needs it; `cmd/beanquery` doubles as the benchmark harness.

### 7.4 `goplug` dynamic loading of query functions
- **Seam**: mirror `pkg/ext/goplug`. A `.so` exports a `Manifest` + an
  `InitPlugin func() error` that calls `env.Register` with `api.Function`
  descriptors. `api` is types-only, so a plugin compiles against `api` + `env`
  with no runner dependency. Use the dual-registration convention (upstream
  name + Go import path). No first-deliverable consumer; build when needed.

### 7.5 Remaining beanquery tables and columns
- **Adding a table** = a new constructor like `table.Postings`/`Entries` (typed
  `Column`s + a lazy `Rows` over `ast.Ledger.All()`); the compiler's
  `tableCatalog` gains one entry.
- **Adding a column** = a new `Column` with a pure accessor over the row handle.
- Known deferred columns: on `postings` — `value`/`convert` (§7.2), `entry`,
  `id`, `location`, `description`, `other_accounts`, `accounts`, `posting_flag`;
  on `entries` — `id`, `description`, `accounts`. (`balance` shipped — §7.1, §6.)
- A **merged posting/transaction `meta`** variant (§6) is a column-level change.

## 8. Excluded (initially)

`CREATE TABLE` / `INSERT` (beanquery's generic non-beancount data sources);
`BALANCES` / `JOURNAL` / `PRINT` shortcut statements; `PIVOT BY`, `HAVING`,
subselects, `BETWEEN` / `ANY` / `ALL`, query placeholders; the `prices` virtual
table. These are out of scope for the lean subset, not blocked seams; revisit
per consumer demand.
