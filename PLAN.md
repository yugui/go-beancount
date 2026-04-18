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
│  pkg/postproc │            │                 │               │
├───────────────┴────────────┴─────────────────┴──────────────┤
│                        pkg/inventory                         │  Semantic
├──────────────────────────────────────────────────────────────┤
│                          pkg/ast                             │  Syntactic
├──────────────────────────────────────────────────────────────┤
│                         pkg/syntax                           │  Lexical
└──────────────────────────────────────────────────────────────┘
```

---

## Phase 1: CST Parser (`pkg/syntax`)

**Dependencies:** none

The foundation of the entire system. The concrete syntax tree preserves every byte of the source — whitespace, comments, blank lines — enabling round-trip fidelity and lossless rewriting.

### Deliverables

- **Lexer:** tokenizes all beancount lexical elements (dates, flags, currencies, strings, numbers, indented postings, metadata, inline comments, block comments, directives keywords).
- **CST node types:** a node for every syntactic construct, carrying the exact source span and text.
- **Parser with error recovery:** when a syntax error is encountered, the parser emits an error node and skips forward to the next directive boundary (a line starting at column 0 that looks like a date or a recognized keyword, or a comment). Subsequent directives are unaffected.
- **Public API:** `Parse(r io.Reader) (*syntax.File, []syntax.Error)` returning the complete CST even on errors.

### Key design decisions

- Nodes store `[]byte` source slices, not re-encoded strings, so the printer can emit the original text unchanged.
- Error recovery is at the directive level: a bad posting inside a transaction corrupts only that transaction node.

---

## Phase 2: AST (`pkg/ast`)

**Dependencies:** Phase 1 (CST)

The abstract syntax tree represents the semantic structure of a ledger, divorced from formatting. It is the primary data structure for all analysis and transformation.

### Deliverables

- **Typed directive nodes** for every Beancount directive:
  - `Open`, `Close`, `Balance`, `Pad`, `Note`, `Document`, `Custom`
  - `Transaction` with `Posting` children
  - `Option`, `Plugin`, `Include`
  - `Price`
- **Metadata** attached to any directive or posting.
- **CST → AST lowering pass:** transforms a `syntax.File` into an `ast.File`, resolving syntactic ambiguities and filling typed fields (parsed `decimal.Decimal` amounts, `time.Time` dates, etc.).
- **Include resolution:** `ast.Load(filename string) (*ast.Ledger, []error)` recursively reads and merges files referenced by `include` directives, producing a single logical ledger. File boundaries are tracked for error reporting and source mapping.

### Key design decisions

- `ast.Ledger` holds directives in source order across all included files, with each directive carrying its origin file and line.
- The AST does not resolve semantic meaning (accounts are strings; amounts are not yet booked). That is left to higher layers.

---

## Phase 3: Formatter and `beanfmt` (`pkg/format`, `cmd/beanfmt`)

**Dependencies:** Phase 1 (CST), Phase 2 (AST)

### Deliverables

- **`pkg/format`:** implements canonical Beancount formatting rules:
  - Column-aligned amounts within a transaction
  - Consistent spacing around metadata
  - Normalized number of blank lines between directives
- **CST-based formatter:** rewrites only the parts that differ from canonical form, preserving all other source text (safe for version-controlled files).
- **AST-based printer (`pkg/printer`):** renders an `ast.File` or `ast.Ledger` to `io.Writer` from scratch. Used for programmatic ledger generation.
- **`beanfmt` command:** reads one or more `.beancount` files, writes formatted output in-place (with `-w`) or to stdout.

---

## Phase 4: Validation (`pkg/validation`)

**Dependencies:** Phase 2 (AST)

### Deliverables

- **Account lifecycle checker:** verifies every account referenced in a directive was opened before use and not yet closed; flags use-after-close.
- **Transaction balance checker:** verifies that postings in each transaction sum to zero (with at most one auto-computed posting per transaction). Residual tolerance is evaluated **per currency** — a tight-currency tolerance (e.g. JPY integer) must not mask an out-of-tolerance residual in a looser currency.
- **Balance assertion checker:** verifies `balance` directives match the running balance at their date. The balance syntax follows Beancount upstream: `Account Number [~ Number] Currency` (single trailing currency, shared by the main amount and the optional tolerance).
- **Pad computation:** computes the amount for `pad` directives such that the subsequent `balance` assertion holds.
- **Custom assertion extension point:** handles `custom` directives used as assertions. A public `CustomAssertion` interface plus `RegisterCustomAssertion` registry allows additional handlers to be plugged in from `init()` without modifying the core checker. A built-in `"assert"` handler is provided.
- **Tolerance inference:** default tolerance is `multiplier × 10^e` where `e` is the least-significant exponent of the relevant amount and `multiplier` defaults to `0.5`. Used uniformly by balance assertions, transaction residuals, and custom assertions.
- **Option-driven tolerance configuration:** the following Beancount options are honored:
  - `inferred_tolerance_multiplier` (decimal, default `0.5`) — scales the inferred tolerance.
  - `infer_tolerance_from_cost` (bool, default `false`) — when enabled, postings with a cost spec additionally contribute `|units| × (multiplier × 10^costExp)` to the residual tolerance of the cost currency.
  - `inferred_tolerance_default` is **not** supported.
- **Generic option directive registry:** a package-internal registry (`pkg/validation/options.go`) reads `option` directives in a pre-pass before the directive walk, with typed accessors (String/Bool/Decimal/StringList). Unknown keys are silently ignored (Beancount parity); malformed values emit `CodeInvalidOption`. `operating_currency` is registered but not yet consumed. New options are added by registering a spec, not by threading ad-hoc fields through the checker.
- **API:** `validation.Check(ledger *ast.Ledger) []validation.Error` returning structured diagnostics with source locations.

---

## Phase 5: Inventory (`pkg/inventory`)

**Dependencies:** Phase 2 (AST), Phase 4 (validation)

Lot-based inventory tracking is required for capital gains, cost basis reporting, and booking.

### Deliverables

- **`Lot` type:** commodity, number of units, cost per unit, cost currency, acquisition date, optional label.
- **`Inventory` type:** ordered set of lots per account per commodity.
- **Booking methods:**
  - `STRICT`: requires the posting to exactly identify a lot; error if ambiguous.
  - `FIFO`: reduces the oldest lot first.
  - `LIFO`: reduces the newest lot first.
- **`Reducer`:** processes transactions in date order, applies bookings, produces per-account inventories and realized gain/loss postings.
- **Compatibility target:** booking semantics match Beancount v2.

---

## Phase 6: Plugin System (`pkg/postproc`)

**Dependencies:** none (developed in parallel with other phases)

Beancount calls its post-parse/pre-validation transformation hooks "plugins". Each `plugin "name"` directive names a Go symbol that receives the directive list and returns a new one. go-beancount implements this in `pkg/postproc` (named to avoid collision with the Go standard library `plugin` package). Three sub-phases, delivered as independent PRs.

### Phase 6a: Narrow beancount plugins (in-process Go)

**Status:** done (branch `feature/phase6-plugin`).

The core postprocessor framework. Plugins are Go types registered at init time and invoked in source order on the parsed ledger.

- `pkg/postproc/api`: stable `Plugin` interface plus `Input`, `Result`, `Error` types. Kept minimal so 6b/6c loaders can compile against it without pulling in the runner.
- `pkg/postproc`: `Register` (init-time, panics on duplicate) and `Apply(ctx, *ast.Ledger)` (walks `*ast.Plugin` directives, invokes each registered plugin, commits `Result.Directives` via `ast.Ledger.ReplaceAll` so later plugins see earlier output).
- Plugin names follow Go fully-qualified package path convention (e.g. `github.com/yugui/go-beancount/plugins/auto_accounts`) to avoid collisions.
- Runner-emitted diagnostics: `plugin-not-registered`, `plugin-failed`, `plugin-canceled`.

### Phase 6b: Go `.so` loader (`pkg/postproc/goplug`)

Load plugins from `.so` files built with `go build -buildmode=plugin`, so third parties can ship plugins without forking go-beancount.

- Loader opens a `.so` via stdlib `plugin.Open`, looks up an exported `Plugin` symbol of type `api.Plugin`, and registers it.
- `.so` files must be built against the same `go-beancount` module version and Go toolchain — constraints documented explicitly.
- Opt-in: the loader is invoked only when the CLI (or an embedder) passes `--plugin-so=<path>`.

### Phase 6c: External-process loader (`pkg/postproc/extproc`)

For plugins that cannot be `.so` files (different Go toolchain, non-Go implementation, sandboxing).

- Protocol: newline-delimited JSON-encoded protobuf messages over stdin/stdout.
- Host spawns the subprocess, marshals `Input`, reads `Result`.
- Go SDK for the child side so plugin authors can write a plain `main` that wraps an `api.Plugin` implementation.
- Opt-in via `--plugin-extproc=<path>`.

### Deliverables

- `pkg/postproc/api`: stable interface (6a — done).
- `pkg/postproc`: registry + runner (6a — done).
- `pkg/postproc/goplug`: `.so` loader (6b).
- `pkg/postproc/extproc`: external-process host + SDK (6c).

---

## Phase 7: Quote Library (`pkg/quote`)

**Dependencies:** Phase 6 (plugin system)

### Deliverables

- **`Quoter` interface:**
  ```go
  type Quoter interface {
      Quote(ctx context.Context, commodity string, date time.Time) (Price, error)
  }
  ```
- **`Price` type:** commodity pair, numeric value, source, timestamp.
- **Built-in quoters:** at minimum one reference implementation (e.g., Yahoo Finance or a free market data API).
- **Plugin-backed quoter:** wraps a Go plugin implementing `Quoter`.
- **External-process quoter:** wraps a subprocess implementing the extproc protocol.
- **Fan-out / fallback quoter:** tries multiple sources in order; returns first success.
- **`pkg/quote/pricedb`:** converts a slice of `Price` values into `price` directives and merges them into an `ast.Ledger`, deduplicating by date and commodity.

---

## Phase 8: Transaction Import Framework (`pkg/importer`)

**Dependencies:** Phase 2 (AST), Phase 6 (plugin system)

Transaction importing is open-ended: sources range from bank CSV exports to OFX files to API-based feeds, and classification rules vary per user. `pkg/importer` defines the extensible framework that all importers plug into; it does not implement any specific source format itself.

### Deliverables

- **`Source` interface:** a plugin-implementable interface that reads raw transaction records from an external source:
  ```go
  type Source interface {
      Fetch(ctx context.Context, params SourceParams) ([]RawTransaction, error)
  }
  ```
- **`RawTransaction` type:** a normalized intermediate representation — date, description, amount, currency, and source-specific metadata — independent of any ledger concept.
- **`Classifier` interface:** maps a `RawTransaction` to a candidate `ast.Transaction` (assigning accounts, payee, narration, tags, links). The framework tries classifiers in priority order, applying the first match.
- **Deduplication:** hashes `RawTransaction` identity against already-imported transactions in the ledger; skips duplicates.
- **`Importer` orchestrator:** wires together a `Source`, a set of `Classifiers`, and deduplication; returns a slice of new `ast.Transaction` values ready to be written.
- **Plugin-backed sources and classifiers:** both `Source` and `Classifier` can be provided via Go plugin or external process.
- **Built-in classifiers:** simple rule-based classifier (regexp on description → account mapping) as a reference implementation.

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
  - `POST /directives` — append one or more directives to a specified target file within the ledger.
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
Phase 6:  pkg/postproc        (no deps)
Phase 7:  pkg/quote           (Phase 6)
Phase 8:  pkg/importer        (Phase 2, 6)
Phase 9:  pkg/query           (Phase 2, 4, 5)
Phase 10: cmd/bean-daemon     (Phase 2, 4, 5, 9)
Phase 11: cmd/beancount-lsp   (Phase 1, 2, 4)
Phase 12: cmd/beansprout      (Phase 7, 8, 10)
```

Phases 6 and 11 have no blocking dependencies on later phases and can be developed in parallel with other phases. Phases 7 and 8 can proceed concurrently once Phase 6 is complete.
