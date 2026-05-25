# Development Plan

This document describes the technical design and phased development roadmap for go-beancount.

## Architecture Overview

The system is layered. Higher layers depend on lower layers; each layer is independently usable as a library.

```
┌──────────────────────────────────────────────────────────────┐
│  beansprout CLI    beanfmt   beancount-lsp    bean-daemon     │  Commands
├───────────────┬────────────┬─────────────────┬──────────────┤
│  pkg/quote    │            │                 │  pkg/query    │  Mid-level
│  pkg/importer │ pkg/format │ pkg/validation  │  pkg/printer  │
│  pkg/ext      │            │                 │               │
├───────────────┴────────────┴─────────────────┴──────────────┤
│                        pkg/inventory                         │  Semantic
├──────────────────────────────────────────────────────────────┤
│                          pkg/ast                             │  Syntactic
├──────────────────────────────────────────────────────────────┤
│                         pkg/syntax                           │  Lexical
└──────────────────────────────────────────────────────────────┘
```

Shipped-phase entries below carry only a brief purpose statement and a
pointer to the package whose godoc owns the spec. Phases that have not
yet started keep their full design entries — they are still acting as
plans.

---

## Phase 1: CST Parser (`pkg/syntax`)

**Dependencies:** none.

**Status:** done.

The foundation of the entire system. The concrete syntax tree preserves
every byte of the source — whitespace, comments, blank lines — enabling
round-trip fidelity and lossless rewriting. Parser error recovery
isolates a syntax error to the directive that contains it; subsequent
directives parse unaffected.

**Spec:** `pkg/syntax` godoc (package doc on `pkg/syntax/file.go`,
node/token contracts on `pkg/syntax/node.go` and `pkg/syntax/token.go`).

---

## Phase 2: AST (`pkg/ast`)

**Dependencies:** Phase 1 (CST).

**Status:** done.

The abstract syntax tree represents the semantic structure of a ledger,
divorced from formatting. It is the primary data structure for all
analysis and transformation. `ast.Load` recursively resolves `include`
directives into a single `ast.Ledger` with origin tracking per directive
for diagnostics and source mapping.

**Spec:** `pkg/ast` godoc.

---

## Phase 3: Formatter and `beanfmt` (`pkg/format`, `cmd/beanfmt`)

**Dependencies:** Phase 1, Phase 2.

**Status:** done.

`pkg/format` is a CST-based formatter — it rewrites only the parts that
differ from canonical form, leaving every other source byte intact, so
the formatter is safe to run on version-controlled files. `pkg/printer`
is the parallel AST-based renderer for programmatic ledger generation.
`cmd/beanfmt` is the command-line driver.

**Spec:** `pkg/format`, `pkg/printer`, and `cmd/beanfmt` godoc. Display-
precision integration and option wiring are recorded in
`docs/architecture/display-precision.md`.

---

## Phase 4: Validation (`pkg/validation`)

**Dependencies:** Phase 2.

**Status:** done.

Validation is delivered as a three-stage pipeline (`pad` → `balance` →
`validations`) implemented as three `postproc/api.Plugin`
implementations in `pkg/validation/{pad,balance,validations}`. The
pipeline enforces account lifecycle, per-currency transaction
balancing, balance assertions, and pad/balance synthesis. Tolerance
inference is option-driven (`tolerance_multiplier`,
`inferred_tolerance_default`, `infer_tolerance_from_cost`).

**Spec:** `pkg/validation` godoc. Tolerance precedence and the
deliberate divergence from upstream's integer-assertion special case
are documented in `pkg/validation/internal/tolerance/doc.go` and
`docs/architecture/display-precision.md`.

---

## Phase 5: Inventory (`pkg/inventory`)

**Dependencies:** Phase 2, Phase 4.

**Status:** done.

Lot-based inventory tracking for capital gains, cost basis reporting,
and booking. Supports the STRICT / FIFO / LIFO / NONE booking methods.
The streaming `Reducer.Walk` API replays directives once and feeds a
visitor with deep-copied before/after snapshots, so memory cost is O(1)
in input size. Multi-lot reductions are expanded into per-lot
postings in the booking pipeline.

**Spec:** `pkg/inventory` godoc.

---

## Phase 6: Plugin System (`pkg/ext`)

**Dependencies:** none (developed in parallel with other phases).

Beancount calls its post-parse/pre-validation transformation hooks
"plugins". Each `plugin "name"` directive names a Go symbol that
receives the directive list and returns a new one. go-beancount
implements this under `pkg/ext`, a neutral umbrella for plugin framework
packages. The name avoids collision with the Go standard library
`plugin` package.

### Phase 6a: Narrow beancount plugins (in-process Go)

**Status:** done.

`pkg/ext/postproc/api` carries the stable `Plugin` interface and the
`Input` / `Result` types kept minimal so the 6b/6c loaders can compile
against it without pulling in the runner. `pkg/ext/postproc` provides
`Register` (init-time, panics on duplicate) and `Apply` (walks
`*ast.Plugin` directives, invokes each registered plugin, commits
`Result.Directives` and appends `Result.Diagnostics` to the ledger).

**Spec:** `pkg/ext/postproc` and `pkg/ext/postproc/api` godoc.

### Phase 6b: Go `.so` loader (`pkg/ext/goplug`)

**Status:** done.

Loads plugins from `.so` files built with `go build -buildmode=plugin`,
so third parties can ship plugins without forking go-beancount. `.so`
files must be built against the same `go-beancount` module version and
Go toolchain.

**Spec:** `pkg/ext/goplug` godoc.

### Phase 6c: External-process loader (`pkg/ext/extproc`)

**Status:** not started.

For plugins that cannot be `.so` files (different Go toolchain, non-Go
implementation, sandboxing).

- Protocol: newline-delimited JSON-encoded protobuf messages over stdin/stdout.
- Host spawns the subprocess, marshals `Input`, reads `Result`.
- Go SDK for the child side so plugin authors can write a plain `main` that wraps an `api.Plugin` implementation.
- Opt-in via `--plugin-extproc=<path>`.

### Phase 6d: Standard plugin library (`pkg/ext/postproc/std`)

**Status:** done.

Go ports of plugins shipped in upstream `beancount/plugins/*.py`. Each
port is an independent subpackage under `pkg/ext/postproc/std/<name>/`,
dual-registered under the upstream Python module path (e.g.
`beancount.plugins.check_commodity`) and the Go import path. The
umbrella `pkg/ext/postproc/std` blank-imports every port for one-line
activation.

**Spec:** `pkg/ext/postproc/std` godoc plus each subpackage's `doc.go`
for per-plugin behavior, upstream attribution, and deviations.

### Phase 6e: Beansprout postproc port (`pkg/ext/postproc/sprout`)

**Status:** done.

Go ports of [`beansprout`](https://github.com/yugui/beansprout)'s
plugin library, following the same layout and dual-registration
convention as Phase 6d. Ten plugins are ported: `checkmetadata`,
`commoditypattern`, `comprehensivebalance`, `fiscalincomeexpense`,
`infermetadata`, `inheritmetadata`, `leafonly`, `pricecompletion`,
`print`, `tradingvalidation`.

**Spec:** `pkg/ext/postproc/sprout` godoc plus each subpackage's
`doc.go`.

---

## Phase 7: Quote Library (`pkg/quote`)

**Dependencies:** Phase 6.

**Status:** done (7a, 7b shipped; 7c held until Phase 6c).

`pkg/quote` fetches commodity and FX prices from external sources and
emits them as `ast.Price` directives. The wire-out shape is `ast.Price`
itself — there is no parallel "Quote" type. bean-price's `price:` meta
grammar on Commodity directives is accepted as-is by `pkg/quote/meta`
so existing ledgers carry over without rewriting; outside that
compatibility point the layer is Go-native. Sources declare only the
batching shape they natively serve via optional `LatestSource` /
`AtSource` / `RangeSource` sub-interfaces, with documented demotion
paths. The orchestrator's level-by-level fallback scheduler is
deadlock-safe under shared batch sources. ECB is the Phase 7 reference
source.

Sub-phase 7c (external-process `Source` via extproc) is deferred until
Phase 6c lands.

**Spec:** `pkg/quote` godoc (locked design decisions, scheduler
contract, fallback semantics), plus per-subpackage godoc under
`pkg/quote/{api,meta,sourceutil,pricedb,std/ecb}` and the
`cmd/beanprice` command doc.

---

## Phase 7.5: Directive distribution CLI (`cmd/beanfile`)

**Dependencies:** Phase 1, Phase 2, Phase 3.

**Status:** done.

A stateless offline CLI that merges a directive stream (stdin or files)
into the appropriate file of a multi-file ledger, bridging directive
producers (`beanprice` today, importers tomorrow) with the multi-file
layout without requiring a long-running daemon. A three-way decision
(skip / comment-out marker / write active) makes re-runs idempotent.

**Spec:** the user-facing specification (invocation, routing
convention, dedup decision, merge semantics, stats) lives in the godoc
on `cmd/beanfile`; the routing layer, dedup index, comment recognizer,
merger spacing rules, and TOML schema live in their respective package
godocs under `pkg/distribute/`.

---

## Phase 8: Transaction Import Framework (`pkg/importer`)

**Dependencies:** Phase 2, Phase 6.

**Status:** done.

`pkg/importer` defines a factory-based framework for plugging
transaction importers into the library. Each importer kind registers a
factory; a TOML config declares one or more instances of those kinds;
the framework drives `Identify` + `Extract` over a single input file
with declaration-order selection. `pkg/importer/hook` provides
post-extract transformation hooks (e.g. classify postings against
rules) following the same factory pattern. Both surfaces accept
Go-plugin (`.so`) extensions via `pkg/ext/goplug`.

### Shipped surface

- **`cmd/beanimport`**: CLI that drives the importer + hook pipeline against a single input file. Reads a flat `[[importer]]` / `[[hook]]` TOML config; emits beancount directives to stdout and diagnostics to stderr in the canonical `<path>:<line>:<col>: <severity>: <message>` form.
  - Flags: `-config PATH` (required), `-hook NAME[,...]` (repeatable; user-controlled chain order), `-importer NAME` (bypass Dispatch), `-account NAME` (overrides shape `account` via `Hints["account"]`), `-plugin PATH.so` (repeatable), `-strict` (treat warnings as errors).
  - Exit codes: `0` clean / `1` any Error diagnostic, or `-strict` + any Warning / `2` CLI failure (bad flags, missing config, plugin load failure, unknown name, etc.).
  - Example: `beanimport -config config.toml statement.csv`.
- **`pkg/importer/std/csvimp`**: in-tree CSV importer kind (`kind = "csv"`). One instance per declared shape; optional `match` regex filters files; `[[importer.amount]]` sub-tables describe amount columns.
- **`pkg/importer/hook/std/classify`**: in-tree classification hook kind (`kind = "classify"`). Per-instance rule list; matches `payee_regex` / `narration_regex` against postings to assign a target `account`.

**Spec:** `pkg/importer` godoc carries the interface concurrency
contract, the `Factory.New`-as-Configure pattern with the decode
callback, declaration-order Dispatch, the framework diagnostic codes,
and the error-vs-Diagnostic split. `pkg/importer/hook`,
`pkg/importer/importerutil`, `pkg/importer/std/csvimp`, and
`pkg/importer/hook/std/classify` carry their own package-level docs.

---

## Phase 9: BQL Query Engine (`pkg/query`)

**Dependencies:** Phase 2 (AST), Phase 4 (validation), Phase 5 (inventory)

Full Beancount Query Language (BQL) compatibility is the ultimate goal. Initial delivery is a useful subset.

### Deliverables

- **BQL lexer and parser:** parses BQL `SELECT`, `FROM`, `WHERE`, `GROUP BY`, `ORDER BY`, `LIMIT`.
- **Query planner:** compiles a parsed query to a plan against an in-memory table model.
- **Table model:** exposes `entries`, `postings`, `prices` as virtual tables matching beancount's BQL semantics.
- **Execution engine:** evaluates plans against `ast.Ledger` + computed inventories.
- **BQL compatibility:** initially targets the most commonly used subset (filtering and aggregating postings); full compatibility added incrementally.

---

## Phase 10: Bean Daemon (`cmd/bean-daemon`)

**Dependencies:** Phase 2 (AST), Phase 4 (validation), Phase 5 (inventory), Phase 9 (BQL)

A long-running background process that owns an in-memory ledger and serves API requests. Designed for editor integrations and frontend tools.

### Ledger model

`bean-daemon` is started with a root ledger file. It loads the whole logical ledger — the root file plus all files transitively referenced via `include` directives — as a single `ast.Ledger`. The daemon tracks which physical file each directive originated from, so it can write mutations back to the correct file. New directives are appended to a designated target file (configurable, defaults to the root).

### API design

- Schema defined in `.proto` files.
- Wire format: HTTP/1.1 with `application/json` bodies using protobuf JSON encoding (`protojson`).
- No gRPC; plain HTTP endpoints, one per RPC.

### Deliverables

- **Protobuf schema** (`proto/`) for all request/response types.
- **In-memory store:** loads the whole ledger at startup; keeps a dirty-tracking layer so edits can be applied without full reload. Efficient indexing by account, date, commodity, origin file.
- **Endpoints:**
  - `POST /query` — execute a BQL query against the whole ledger; returns rows as JSON.
  - `GET /prices` — query price history for a commodity pair and date range.
  - `POST /directives` — append one or more directives to a specified target file within the ledger. Implementation reuses the `pkg/distribute/{route,merge,dedup,comment}` libraries from Phase 7.5 so daemon and CLI behave identically.
  - `DELETE /directives/{id}` — remove a directive by ID (tracked by source file + location).
  - `POST /files` — create a new ledger file and optionally add an `include` reference to it from an existing file.
  - `GET /accounts` — list open accounts with metadata.
  - `POST /reload` — reload the whole ledger from disk.
- **File watching:** optionally watches all ledger files in the transitive include set for changes and reloads automatically.
- **Plugin support:** loads transformation plugins at startup.

---

## Phase 11: LSP Server (`cmd/beancount-lsp`)

**Dependencies:** Phase 1 (CST), Phase 2 (AST), Phase 4 (validation)

### Deliverables

- LSP 3.17 implementation using a Go LSP library.
- **Diagnostics:** syntax errors (from CST parser) and validation errors surfaced as LSP diagnostics, updated on file save and optionally on change.
- **Formatting:** `textDocument/formatting` delegates to `pkg/format`.
- **Completion:** account names, commodity names, directive keywords, flag characters.
- **Go-to-definition:** for `include` paths and account names (jumps to `open` directive).
- **Hover:** shows account metadata, balance at cursor date, commodity info.
- **Document symbols:** list of directives in the current file.
- **Multi-file awareness:** resolves `include` directives for cross-file diagnostics.

---

## Phase 12: Beansprout CLI (`cmd/beansprout`)

**Dependencies:** Phase 7 (quote library), Phase 8 (importer framework), Phase 10 (bean-daemon client)

`beansprout` is the primary user-facing command. It communicates with `bean-daemon` for ledger operations and uses `pkg/quote` directly for price fetching.

### Subcommands

- **`beansprout quote`**
  - Fetches current or historical prices for specified commodities.
  - Writes `price` directives to a target file (via `bean-daemon` or directly).
  - Supports multiple quoter plugins; falls back through sources in configured order.

- **`beansprout import`**
  - Orchestrates the `pkg/importer` framework: selects a source plugin, runs classifiers, deduplicates.
  - Writes new `txn` directives to the ledger via `bean-daemon`.
  - Source and classifier plugins are loaded as Go plugins or external processes.

- **Future subcommands** (not yet planned in detail):
  - `beansprout check` — run validation and print a report.
  - `beansprout query` — run a BQL query via `bean-daemon` and print results.

---

## Dependency Summary

```
Phase 1:  pkg/syntax          (no deps)
Phase 2:  pkg/ast             (Phase 1)
Phase 3:  pkg/format          (Phase 1, 2)
          pkg/printer         (Phase 2)
          cmd/beanfmt         (Phase 3)
Phase 4:  pkg/validation      (Phase 2)
Phase 5:  pkg/inventory       (Phase 2, 4)
Phase 6:  pkg/ext             (no deps)
Phase 7:  pkg/quote           (Phase 6)
Phase 7.5: pkg/distribute     (Phase 1, 2, 3)
           cmd/beanfile       (Phase 7.5)
Phase 8:  pkg/importer        (Phase 2, 6)
Phase 9:  pkg/query           (Phase 2, 4, 5)
Phase 10: cmd/bean-daemon     (Phase 2, 4, 5, 9)
Phase 11: cmd/beancount-lsp   (Phase 1, 2, 4)
Phase 12: cmd/beansprout      (Phase 7, 8, 10)
```

Phases 6 and 11 have no blocking dependencies on later phases and can be developed in parallel with other phases. Phases 7 and 8 can proceed concurrently once Phase 6 is complete.
