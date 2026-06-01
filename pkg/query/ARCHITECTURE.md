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
(date/string/account/extractor scalars, `getitem`,
directive-context scalars (`open_date`, `close_date`, `open_meta`,
`currency_meta`/`commodity_meta`, `account_sortkey`, `has_account`, `possign`),
price/valuation scalars (`getprice`, `convert`, `value`), and
count/sum/min/max/first/last aggregators); and the `beanquery` CLI.

### Deferred / excluded
See §7 (deferred, with hints) and §8 (excluded).

## 2. Package layout

| Package | Role |
|---|---|
| `pkg/query/types` | Sealed `Value` interface, `Type` enum, NULL model, total-order `Compare`, `Set`/`Dict` containers. |
| `pkg/query/price` | Immutable, lazily-built price `Map` (nearest-on-or-before, one-hop inverse) + `QueryContext` (the query-wide read-only bundle injected into scalars). Leaf; imports `ast`+`apd`. |
| `pkg/query/api` | Thin function descriptor: `Function`, `Flavor`, `Scalar` (takes the `price.QueryContext`), `Pure`, `Accumulator`, `NewAccumulator`. Depends only on `types` and `price` (both leaf; neither is `env`). |
| `pkg/query/env` | Global registry (`Register`, panic-on-conflict) + compile-time overload `Resolve`. |
| `pkg/query/env/std` | Built-in function library; self-registers in `init()`; activated by a blank import. |
| `pkg/query/parser` | Hand-written scanner + recursive-descent parser → untyped `*Select` AST. No CST/trivia. |
| `pkg/query/table` | Virtual tables (`Postings`, `Entries`) = typed `Column`s + lazy re-runnable `Rows`. |
| `pkg/query/exec` | Compiler (type-check, overload bind, aggregate classify), operators, execution pipeline. |
| `pkg/query` (facade) | `Compile`/`Run`/`Query`, `Result`/`Column`. Thin glue. |
| `cmd/beanquery` | CLI: loader → query → aligned table. Glue only. |

The `api`-vs-`env` split mirrors `pkg/ext/postproc/api`-vs-`postproc` exactly,
so the `std` library and out-of-tree `goplug` plugins register against the thin
`api` + `env` surface with no rework — the same `pkg/ext/goplug` loader serves
both post-processors and query functions (§7.4, shipped).

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
   column, make `meta('k')` compiler sugar for `getitem`, and inject that
   immutable context into every `api.Scalar` as a `price.QueryContext`; the
   price/valuation functions (§7.2) read it, and context-free scalars ignore it
   via `api.Pure`. Because the context is built once and shared read-only,
   per-row evaluation stays pure and therefore concurrency-safe.

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
3. **Two flavors: scalar + aggregator** (`api.Flavor`), matching the two
   genuine execution modes (per-row vs per-group). Whether a scalar reads the
   query-wide context is an *orthogonal* property, not a flavor: every
   `api.Scalar` receives a `*price.QueryContext`, and a context-free function
   is adapted with `api.Pure` (which ignores it). The price/valuation functions
   (§7.2) consult it; everything else uses `Pure`. Enforced in `api/function.go`
   and `env/registry.go`.
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
9. **Subpackage layout** as in §2; keep `api` thin and free of `env`. `api`
   depends on `types` and `price` (both leaf packages, neither is `env`), so
   the "free of env" guarantee for plugins/`std` is unchanged.

## 5. Pipeline

**Compile** (`exec.Compile`): select table + classify FROM (Decision 7) →
AND-merge FROM-filter and WHERE into one Bool predicate over the table columns →
compile GROUP BY / targets / ORDER BY into typed `cexpr` trees → resolve column
refs against `Table.Column` (case-insensitive), operators (type-checked), and
function overloads (`env.Resolve`) → rewrite `meta`/`entry_meta`/`any_meta`
sugar `fn('k')` to `getitem(<col>,'k')`
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
  NULL; `getitem` returns NULL on a missing key or NULL dict. Two companion
  columns expose the enclosing entry's metadata and the merged view
  (§7.5, shipped): `entry_meta` (the parent transaction's own meta) and
  `any_meta` (transaction meta overlaid by the posting's meta, posting wins on
  conflict). Both are always Dicts (never NULL). On the `entries` table, all
  three columns equal the directive's own metadata — there is no posting
  concept there. The compiler sugar (`meta('k')`, `entry_meta('k')`,
  `any_meta('k')`) rewrites each to `getitem(<that column>, 'k'[, default])`
  via a shared `metaSugarColumns` map.
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
- **Date-function return types - corrected to upstream parity (shipped)**: an
  earlier draft recorded `weekday`/`quarter`/`yearmonth` return types that
  diverged from upstream beanquery (English weekday name / int 1..4 / "YYYY-MM"
  string) and framed the divergence as a deliberate lean choice. That was a
  planning mistake. The std scalars now match upstream: `weekday` is a 3-letter
  abbreviation (strftime `%a`, e.g. "Mon"), `quarter` is a "YYYY-Qn" string,
  and `yearmonth` is a month-truncated date. The convenient variants are
  re-provided under distinct names in the sprout library
  (`pkg/query/env/sprout`): `weekday_name` (full English weekday name),
  `quarter_index` (int 1..4), and `yearmonth_str` ("YYYY-MM"), so users keep
  them without shadowing the upstream-parity std names.

- **Aggregate-mixing check** runs over the *compiled* tree (aggregate calls
  already replaced by slot refs), matching group keys by bare column name.
  Limitation: `GROUP BY year(date)` does not cover a bare `date` in a target;
  generalize when non-trivial grouped expressions are needed.

## 7. Deferred work — attach points and hints

Each item below is deferred *by design*, with the concrete seam already in place
so it lands without reworking the core. These are not vague TODOs.

### 7.0 interval / date_bin — shipped

`types.Interval` is a new value kind: an immutable `(years, months, days)`
calendar offset. Its ordinal sits between Inventory and the container kinds
(Set, Dict); it participates in the sealed-`Value` order and the NULL model as
other concrete kinds do, with one deliberate restriction described under
*Comparison* below.

`interval(str)` parses a stride string and returns an Interval. It accepts
only `day`, `month`, and `year` units (optional trailing `s`, optional leading
sign), matching the observable behavior of upstream beanquery's regex
`([-+]?[0-9]+)\s+(day|month|year)s?`. Input that does not match — any other
unit, malformed syntax — yields NULL. (Upstream's function body contains
unreachable week/decade/century/millennium branches that the regex never feeds;
we match what the regex actually produces, not those dead branches.)

`date_bin` is registered with two overloads (Interval stride and String stride)
in `pkg/query/env/std/dateops.go`. The resolved alignment semantics:

- **Day strides** align by floor division toward −∞, matching `//` in Python:
  `floor((date − origin) / stride_days) * stride_days + origin`.
- **Month and year strides** iterate relativedelta-style addition from origin
  with end-of-month clamping that *accumulates* across steps. The key invariant
  is that advancing from Jan 31 by one month lands on Feb 28 (or Feb 29 in a
  leap year), and advancing again from that Feb 28 lands on Mar 28 — not
  Mar 31. The boundary does not snap back to 31 each step. This is why
  alignment iterates forward from origin rather than computing `origin + k·stride`
  directly: the accumulated clamp matches Python's `dateutil.relativedelta` and
  upstream beanquery.
- **Non-advancing stride** (zero or negative): the function returns NULL rather
  than looping forever or producing a meaningless result.
- **Zero-day stride**: yields NULL (safe divergence from an upstream edge-case
  bug that would divide by zero).

Comparison: Interval supports **equality only** (`=`, `!=`); it is deliberately
**not ordered**. A lexicographic `(years, months, days)` order would be stable
but meaningless as a duration — interval `'700 days'` is longer than `'1 year'`
yet sorts below it — so the ordering operators (`<` `<=` `>` `>=`), `ORDER BY`,
and the `min`/`max`/`first`/`last` aggregates reject an interval operand at
compile time (`checkComparable` and the ORDER BY key check in
`exec/compile.go`; the aggregates simply omit Interval from `orderedTypes`).
`types.Value.Compare` still returns a structural, stable result so that `=`,
`!=`, `DISTINCT`, and `GROUP BY` distinguish distinct intervals correctly —
note this makes equality structural, so `interval('12 months')` does not equal
`interval('1 year')`. Upstream beanquery supports no interval comparison at
all; restricting to equality keeps the well-defined operation while dropping
the ill-defined order (the SQL year-month vs day-interval distinction was
considered and rejected as over-engineered for single-unit interval values).

Cast behavior: the Any-taking cast functions treat Interval via their generic
default branches. `str` and `repr` render the canonical form (non-zero
components only, ordered years→months→days, each `<n> <unit[s]>` with singular
unit when |n|==1, joined by `", "`; all-zero → `"0 days"`). `bool` is always
`true` — `truthy`'s default does not inspect components, so even an all-zero
Interval is a non-null value and therefore truthy. `int`, `decimal`, and `date`
yield their typed NULL — no numeric conversion is defined for a calendar offset.

### 7.1 OPEN/CLOSE/CLEAR entry-stream scoping — shipped
Both halves of the original §7.1 have landed: the `balance` running-inventory
column **and** entry-stream scoping. `balance`'s resolved semantics are
recorded in §6 (the precedent style for shipped decisions); scoping's
resolved semantics live below alongside the seam where each piece lives,
because the design splits naturally across four packages (parser → scope →
table → exec) and is easier to read as one entry.

`balance` is the cumulative inventory of the *selected* rows, computed in the
executor's row scan after the predicate filter (see §3.2 and §6): a declared
postings column whose value is supplied by `balanceExpr` /
`Compiled.accumulateBalance`, not the table accessor. The earlier hints here —
to drive `inventory.Reducer.Walk`, and (in the first shipped attempt) to
accumulate a global pre-filter inventory in the table producer — were both
**superseded**: the former is per-account, the latter matched only the modern
`beanquery` binary, which contradicts the BQL doc.

Entry-stream scoping (canonical fact §3.3) is the parse-then-pre-pass-then-table
pipeline:
- **Grammar** (`pkg/query/parser`): `FROM [<expr>] [OPEN ON <date>] [CLOSE ON
  <date>] [CLEAR]`, fixed order. The optional filter expression and any subset
  of scoping clauses combine; positioned errors cover missing `ON`, missing
  date, `CLEAR ON <date>`, duplicates, and empty FROM.
- **Pre-pass** (`pkg/query/scope`): `Spec{Open, Close time.Time; Clear bool}`
  and `View(*ast.Ledger, Spec) iter.Seq2[int, ast.Directive]`. Zero `Spec`
  returns `l.All()` unchanged. `CLOSE ON D` drops directives with
  `DirDate() >= D` (strict `<`, matching beanquery's `summarize.truncate`).
  `OPEN ON D` walks pre-D postings into per-account `inventory.Inventory`,
  classifies each account via `Account.Root()` against `name_income` /
  `name_expenses`, and emits one synthesized `*ast.Transaction` per
  non-empty account at D in lexicographic order. Asset/liability openings
  post the source account itself paired with `account_previous_balances`;
  income/expense openings do not post the source account (its running total
  resets across the boundary, matching beanquery `summarize`) — the
  cumulative balance transfers to `account_previous_earnings` with the
  opposing leg on `account_previous_balances`. The equity targets
  (`account_previous_balances`, `account_previous_earnings`,
  `account_current_earnings`) follow beancount's convention: the option
  value is the portion below `name_equity`, joined via
  `OptionValues.EquityAccount` so the defaults yield
  `Equity:Opening-Balances`, `Equity:Earnings:Previous`, and
  `Equity:Earnings:Current`. `Open`
  directives dated `< D` are preserved; the kept tail is the original directive
  stream dated `>= D` (bounded above by `CLOSE` when set). `CLEAR` walks the
  OPEN- and CLOSE-shaped stream, accumulates per-account inventories for
  income/expense roots only, and appends one balanced clearing transaction per
  non-empty account, routing the original units to
  `account_current_earnings`. Boundary date: `Close − 1 day` if set, else the
  last kept directive's `DirDate()`, else `time.Now().UTC()` truncated to
  midnight. Synthesized transactions carry `meta['__synthetic__'] = 'opening'`
  or `'clearing'`.
- **Table integration** (`pkg/query/table`): `PostingsOver` / `EntriesOver`
  constructors accept a `func() iter.Seq2[int, ast.Directive]` factory;
  existing `Postings(l)` / `Entries(l)` wrap `l.All`. No `scope.Spec` leaks
  into `pkg/query/table`.
- **Compiler plumbing** (`pkg/query/exec/compile.go`): `selectTable` builds
  the `Spec` from `sel.From.Scoping`, wires `scope.View(ledger, spec)` as the
  table's source, and the FROM filter expression continues to AND into the
  row predicate independently — scoping reshapes the stream *before* the
  predicate sees it.

The pre-pass is a pure function of immutable inputs (Decision 6): `scope.View`
allocates only per-call iterator state. OPEN-only materializes only the
opening prefix (`preOpens`, `accounts`, `openings` slices); the post-D tail
streams. CLEAR materializes the full intermediate stream because it walks twice
(boundary detection + inventory accumulation). All materializations are
per-Run, never shared. Synthesized transactions are real
`*ast.Transaction` values with zero `Span`, so all existing column accessors
work unchanged and `filename`/`lineno` render NULL via the established
zero-`Span` path. The `balance` column folds them like any other row.

Resolved boundary-day conventions (matches beanquery `summarize`):
- `OPEN ON D` synthesized transactions are dated `D` (start of the kept window).
- `CLOSE ON D` is strict `<` on `DirDate`.
- `CLEAR` defaults to `Close − 1` when CLOSE bounds the stream; falls through
  to the last kept entry's date else `time.Now().UTC()` truncated to midnight.
- Income/expense classification respects `name_income` / `name_expenses`
  (nil-safe via `OptionValues` registry defaults).
- CLEAR groups by full account (leaf), not by income/expense root: one
  synthesized transaction per non-empty income- or expense-rooted leaf account,
  with per-currency posting pairs inside.

### 7.2 Price / valuation (`value`, `convert`, `getprice`) — shipped
Shipped. The original `PassContextFlavor` reservation was **superseded**: the
flavor axis is for the two real execution modes (scalar/aggregator), and
context-dependence is orthogonal to it. So instead of a third flavor, the
single `api.Scalar` signature now receives the context —
`func(ctx *price.QueryContext, args []types.Value) (types.Value, error)` — and
context-free functions are adapted with `api.Pure` (the registrant's
responsibility). This removes the compiler's context branch and the registry's
PassContext rejection; the executor calls every scalar one way.

- **Context** (`pkg/query/price`): `QueryContext{Prices *Map}` is the
  query-wide, init-time, **immutable** bundle (canonical §3.4), built once in
  `exec.Compile` from the ledger, stored on `Compiled`, and threaded into
  `evalCtx.qctx` per Run. The `Map` is **lazily built** (one `sync.Once` on
  first lookup): a query that calls no price function never scans for prices.
  Immutable-after-build + built-once preserves Decision 6 — `concurrency`
  tests run a price query from 32 goroutines under `-race`.
- **Price map semantics**: indexes `*ast.Price` as forward (`Commodity` →
  `Amount.Currency` at `Amount.Number`) and provides nearest-on-or-before-date
  lookup, latest when no date, and a **one-hop inverse** (`1/rate`, divided at
  precision 34); `base == quote` is 1. **No multi-hop transitive conversion.**
- **Functions** (`env/std/price.go`): `getprice(base,quote[,date]) → Decimal`
  (currencies upper-cased); `convert(Amount|Position,cur[,date]) → Amount`,
  `convert(Inventory,cur[,date]) → Inventory`; `value(Position[,date]) → Amount`
  (units → cost currency at market), `value(Inventory[,date]) → Inventory`.
  Default date is **latest** (no per-row entry date — keeps evaluation pure).
  A missing rate or NULL argument yields the declared `Out` type's typed NULL;
  Inventory conversions **pass through** unconvertible positions unchanged.

### 7.2.1 Directive-context functions — shipped
`open_date`, `close_date`, `open_meta`, `currency_meta`/`commodity_meta`,
`account_sortkey`, `has_account`, `possign` — the second consumer of the
`price.QueryContext` seam. All register as context-reading `api.Scalar` in
`env/std/context.go` (same shape as the price functions; no new flavor).

- **Infrastructure** (`pkg/query/directives`): `Index` is an immutable,
  lazily-built (one `sync.Once`) map of account→`*ast.Open`, account→`*ast.Close`,
  currency→`*ast.Commodity`, plus account-type classification driven by the
  ledger's `name_*` options. `price.QueryContext` gained a `Dirs *directives.Index`
  field; `NewQueryContext` wires both maps from the same ledger. Metadata is
  surfaced as `types.Dict` via the shared `pkg/query/metaval` helper.
- **NULL discipline**: a NULL or non-string argument → typed NULL of the
  declared Out (except `has_account`, which yields `false` — never NULL — on
  a missing account or NULL argument, matching beanquery semantics).
- **`possign` sign convention**: +1 (Assets/Expenses) leaves the value
  unchanged; −1 (Liabilities/Equity/Income) negates the number component
  (Amount/Position/Inventory) while preserving cost lots and currencies; 0
  (unknown root) passes the value through unchanged, same as +1.

### 7.3 Intra-query parallel executor
- **Seam**: the input-row scan in `exec/run.go` (documented there). Partition
  the scan → per-shard filter/project, or per-group accumulators per shard →
  merge: aggregate partials via `api.Accumulator.Merge` (the law makes this
  exact), scalar outputs by stable concat → then DISTINCT/ORDER BY/LIMIT run on
  the merged rows unchanged.
- The mergeable contract (Decision 4) is already implemented and tested, so this
  is purely additive. **Measure first** — there is no first-deliverable consumer
  that needs it; `cmd/beanquery` doubles as the benchmark harness.

### 7.4 `goplug` dynamic loading of query functions — shipped
Shipped, by **reusing the existing `pkg/ext/goplug`** rather than building a
parallel `pkg/query/goplug`. The earlier hint to "mirror `pkg/ext/goplug`"
was superseded once it was clear that loader is registry-agnostic: `goplug.Load`
only verifies the `Manifest` and invokes `InitPlugin func() error`; *what* the
plugin registers is its own concern. A query-function plugin therefore exports
the same `Manifest` + `InitPlugin` and, inside `InitPlugin`, calls
`pkg/query/env.Register` with `api.Function` descriptors. Because `api` depends
only on `types`+`price` (both leaf), the plugin compiles against `api` + `env`
with no executor/runner dependency. A plugin may register query functions,
post-processors, or both from one `InitPlugin`. The dual-registration
convention (upstream name + Go import path) carries over.

- **CLI wiring** (`cmd/beanquery`): mirrors `cmd/beancheck`. `goplugflag.Var`
  registers the repeatable `-plugin` flag (seeded from `BEANCOUNT_PLUGINS`),
  and `goplug.LoadAll(paths)` runs **before** `loader.LoadFile` so both
  post-processor directives and query functions are registered before the
  ledger loads and the query compiles. A load failure maps to the CLI's
  exit-2 (setup-failure) category, not exit-1 (invalid ledger content). The
  registries' existing `sync.RWMutex` (env) makes the after-`main`
  registration safe.
- **Test fixture**: `cmd/beanquery/testdata/queryfn` is a `linkmode=plugin`
  `.so` registering the niladic scalar `plugin_answer() → 42`; the
  `testhelpers`-tagged `beanquery_plugin_test` loads it via `-plugin` and
  asserts `SELECT plugin_answer()` renders `42`, proving the seam end to end
  on plugin-capable platforms (constrained via
  `PLUGIN_COMPATIBLE_PLATFORMS`, skipped elsewhere).

### 7.5 Remaining beanquery tables and columns
- **Adding a table** = a new constructor like `table.Postings`/`Entries` (typed
  `Column`s + a lazy `Rows` over `ast.Ledger.All()`); the compiler's
  `tableCatalog` gains one entry.
- **Adding a column** = a new `Column` with a pure accessor over the row handle.

**Shipped tables.** Beyond `postings`/`entries`, seven single-directive tables
ship via the shared `directiveRows[T]` filter spine (keep the directives of one
concrete type from the stream; map fields to scalar/Set/Dict columns — no
realization pass): `prices`, `commodities`, `transactions`, `notes`, `events`,
`documents`, `balances`. Decisions worth recording:
- `transactions` is the whole-transaction view (one row per `Transaction`) with
  **no `postings` column** (upstream deletes it); its `accounts` column is the
  Set of all posting accounts.
- Upstream column renames are honored: `events.type`/`events.description` carry
  our `Event.Name`/`Value`; `documents.filename` carries `Document.Path`;
  `commodities.currency`/`prices.currency` expose the currency name.
- `balances` exposes `Balance.DiffAmount` (actual − expected, nil when within
  tolerance or unchecked) as the `discrepancy` column — the upstream rename from
  `diff_amount`. The balance-assertion pass records the residual on
  `Balance.DiffAmount`; both `tolerance` and `discrepancy` are typed NULL when
  the respective field is nil.

**Shipped columns** (previously deferred): on `postings` — `id`, `location`,
`description`, `other_accounts`, `accounts`, `posting_flag`; on `entries` —
`id`, `description`, `accounts`. Decisions:
- **`id`** is a stable, unique 32-hex id per directive — a purpose-built
  canonical encoding (`pkg/query/table/entry_id.go`) hashed with MD5, **not**
  byte-identical to upstream's Python id. Meta- and span-insensitive;
  collection fields (tags/links) sorted before hashing; all postings of one
  transaction share its id (`postings.id == entries.id` for the parent).
- **`location`** is `"{filename}:{lineno}:"` (trailing colon), typed NULL on a
  zero span.
- **`description`** joins payee and narration with `" | "`, dropping empties;
  all-empty → NULL.
- **`other_accounts`** (postings) excludes the current posting **by index**, so
  a sibling sharing its account is still included.
- **`accounts`** is a Set of referenced accounts: a `Transaction`'s posting
  accounts; the single account of Open/Close/Balance/Note/Document; **both** the
  target and source account of a `Pad`; typed NULL for kinds with no account
  concept (mirrors the §6 tags/links convention).
- **`posting_flag`** is the posting's **own** flag only — no transaction-flag
  fallback (unlike the existing `flag` column).
- The **merged posting/transaction `meta`** variant is shipped as `any_meta`
  (posting over transaction), and the transaction-only view as `entry_meta`
  (see §6). No further column-level meta work is deferred.

**Directive-as-value (shipped).** The `entry` column (postings/entries) and the
`accounts` table both expose a directive *as a value*; they are built on the
now-constructible `types.Entry` kind (`NewEntry`/`AsEntry`). Decisions worth
recording:
- **`types.Entry`** wraps an `ast.Directive`. Unlike Interval (which rejects all
  ordering), Entry is *totally ordered by identity*: `Compare` ranks by source
  span then canonical `EntryID`, so two values are equal iff they denote the
  same directive and DISTINCT/GROUP BY/ORDER BY are deterministic (the order is
  stable but not semantically meaningful). `Format`/`marshalTree` render the
  directive as a JSON object (type, date, identifying fields, meta, location);
  NULL uses the standard typed-NULL model.
- The **canonical id** (`EntryID`), the **type name** (`DirectiveTypeName`), and
  the **meta→value** coercion (`MetaValue`/`MetaDict`) now live in
  `pkg/query/types` as the single source of truth; `table` delegates its
  `id`/`type` columns there and `metaval` re-exports the coercion (stable API,
  unchanged callers).
- The **`entry` column** is the parent transaction on postings and the row
  directive on entries. The **`accounts` table** (`account`/`open`/`close`, the
  upstream column set) maps each account with an Open or Close to its first of
  each (first-wins, mirroring `get_account_open_close`), ascending by name;
  `open`/`close` are typed NULL when absent.
- **Field access** makes entries queryable, matching upstream
  `open.meta['rate']` / `entry.narration`. The parser gained a postfix layer
  (`parsePostfix`, binding tighter than unary): `.attr` and `["key"]`. `.attr`
  resolves against a purpose-built directive-attribute namespace
  (`table.EntryAttribute`: account/date/currencies/amount/narration/meta/… with
  a typed NULL for kinds that lack the field) and fixes the static result type;
  `["key"]` is getitem sugar over a dict (e.g. `entry.meta`). `open`/`close`/
  `clear` are soft keywords: column references in expression position, scoping
  keywords only in the FROM clause's own production.

## 8. Excluded (initially)

`CREATE TABLE` / `INSERT` (beanquery's generic non-beancount data sources);
the `PRINT` shortcut statement; `PIVOT BY`, `HAVING`, subselects, `ANY` / `ALL`,
query placeholders. These are out of scope for the lean subset, not blocked
seams; revisit per consumer demand.

**Shipped since.** Several initially-excluded sugar items now ship, all as
desugaring over the existing SELECT machinery (no executor changes):

- `BETWEEN` / `NOT BETWEEN`, `NOT IN`, and `IS [NOT] NULL`. BETWEEN desugars in
  the parser to a comparison conjunction (`x BETWEEN a AND b` ≡
  `x >= a AND x <= b`, the De Morgan dual for the negation), matching SQL's
  definition including 3-valued NULL behavior. `NOT IN` reuses the 3-valued
  `NOT` over the IN test. `IS [NOT] NULL` is the one new operator that needs a
  dedicated compiled node, since equality against NULL yields NULL rather than a
  boolean.
- `BALANCES` / `JOURNAL` shortcut statements. Both desugar **in the parser** to
  a `*Select` (so the compiler/executor are untouched), mirroring upstream's
  `transform_balances` / `transform_journal`: JOURNAL is a postings register
  (date, flag, MAXWIDTH(payee/narration), account, position, running balance);
  BALANCES is a per-account trial balance (`SUM(position)` grouped/ordered by
  `account_sortkey`). The `AT <func>` modifier wraps the position/balance with
  any registered scalar function resolved through the normal function registry
  (`AT cost`, `AT value`, `AT units`; an unregistered name is a compile error,
  matching upstream). To support `AT`-modified BALANCES, `sum(Amount)→Inventory`
  was added alongside the existing `sum(Position)`. The statement words and `AT`
  are **contextual**, not reserved, so `FROM balances` (the Balance-directive
  table) still resolves as a table name.

(The `prices` virtual table, formerly listed here, ships — see §7.5.)
