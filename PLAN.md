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
- **API:** the three `postproc/api.Plugin` implementations `pad.Apply`, `balance.Apply`, and `validations.Apply` exported from `pkg/validation/{pad,balance,validations}`. Callers invoke them in that order against a ledger snapshot (`ledger.All()` fed into `api.Input.Directives`), committing any non-nil `Result.Directives` back via `ast.Ledger.ReplaceAll` between stages and appending `Result.Diagnostics` to `ast.Ledger.Diagnostics` for structured diagnostics with source locations.

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

## Phase 6: Plugin System (`pkg/ext`)

**Dependencies:** none (developed in parallel with other phases)

Beancount calls its post-parse/pre-validation transformation hooks "plugins". Each `plugin "name"` directive names a Go symbol that receives the directive list and returns a new one. go-beancount implements this under `pkg/ext`, a neutral umbrella for plugin framework packages. The name avoids collision with the Go standard library `plugin` package. Three sub-phases, delivered as independent PRs.

### Phase 6a: Narrow beancount plugins (in-process Go)

**Status:** done (branch `feature/phase6-plugin`).

The core postprocessor framework. Plugins are Go types registered at init time and invoked in source order on the parsed ledger.

- `pkg/ext/postproc/api`: stable `Plugin` interface plus `Input` and `Result` types. Kept minimal so 6b/6c loaders can compile against it without pulling in the runner. Plugin diagnostics flow through `Result.Diagnostics` ([]ast.Diagnostic).
- `pkg/ext/postproc`: `Register` (init-time, panics on duplicate) and `Apply(ctx, *ast.Ledger) error` (walks `*ast.Plugin` directives, invokes each registered plugin, commits `Result.Directives` via `ast.Ledger.ReplaceAll` and appends `Result.Diagnostics` to `ast.Ledger.Diagnostics` so later plugins see earlier output and the ledger carries all findings).
- Plugin names follow Go fully-qualified package path convention (e.g. `github.com/yugui/go-beancount/plugins/auto_accounts`) to avoid collisions.
- Runner-emitted diagnostics: only `plugin-not-registered` (a `plugin "foo"` directive whose name has no registered implementation — a ledger-content issue). System-level failures — a plugin's non-nil error from `Apply`, or context cancellation — halt the pipeline and propagate to the caller as a Go `error`, NOT as a diagnostic.

### Phase 6b: Go `.so` loader (`pkg/ext/goplug`)

Load plugins from `.so` files built with `go build -buildmode=plugin`, so third parties can ship plugins without forking go-beancount.

- Loader opens a `.so` via stdlib `plugin.Open`, looks up an exported `Plugin` symbol of type `api.Plugin`, and registers it.
- `.so` files must be built against the same `go-beancount` module version and Go toolchain — constraints documented explicitly.
- Opt-in: the loader is invoked only when the CLI (or an embedder) passes `--plugin-so=<path>`.

### Phase 6c: External-process loader (`pkg/ext/extproc`)

For plugins that cannot be `.so` files (different Go toolchain, non-Go implementation, sandboxing).

- Protocol: newline-delimited JSON-encoded protobuf messages over stdin/stdout.
- Host spawns the subprocess, marshals `Input`, reads `Result`.
- Go SDK for the child side so plugin authors can write a plain `main` that wraps an `api.Plugin` implementation.
- Opt-in via `--plugin-extproc=<path>`.

### Phase 6d: Standard plugin library (`pkg/ext/postproc/std`)

Go ports of the plugins shipped in upstream `beancount/plugins/*.py`. Each
port lives in its own subpackage under `pkg/ext/postproc/std/<name>/` so
that users can depend on an individual plugin without pulling in the rest.
The umbrella `pkg/ext/postproc/std` package has no runtime code; its sole
purpose is to blank-import every port so a single import activates the
whole library.

- **Location and naming.** Plugins live at
  `pkg/ext/postproc/std/<name>/`, where `<name>` is the upstream Python
  module's base name with underscores removed (e.g. `check_commodity` →
  `checkcommodity`). Package identifiers may not contain underscores in
  idiomatic Go; concatenating the upstream name preserves traceability
  without introducing a parallel naming scheme.
- **Dual registration.** Each ported plugin registers under two names:
  the upstream Python module path (e.g. `beancount.plugins.check_commodity`)
  so existing ledger files referencing beancount's plugin directives work
  unchanged, and the Go import path
  (`github.com/yugui/go-beancount/pkg/ext/postproc/std/checkcommodity`) so
  go-beancount-native ledgers can follow Phase 6a's package-path
  convention. This is an explicit exception to the single-name policy in
  Phase 6a for upstream ports; it exists so the drop-in compatibility
  that motivates porting these plugins is available by default.
- **Umbrella package.** `pkg/ext/postproc/std/doc.go` blank-imports every
  ported subpackage. Programs that want the entire standard library
  available write `import _ "github.com/yugui/go-beancount/pkg/ext/postproc/std"`;
  programs that want only a subset blank-import individual subpackages.
- **Upstream attribution.** Every ported plugin preserves the upstream
  `__copyright__` and `__license__` in its package `doc.go`. The project
  is GPL-2, matching upstream.
- **Deviations policy.** Any semantic or configuration-format departure
  from upstream is documented in the plugin's `doc.go`. For example,
  `check_commodity` takes JSON instead of a Python `eval`-parsed dict for
  its `{account_regex: currency_regex}` ignore map — safer and more
  idiomatic in Go. `check_drained` currently hardcodes the beancount
  default balance-sheet roots (`Assets`, `Liabilities`, `Equity`) pending
  a go-beancount options-registry extension for
  `name_assets`/`name_liabilities`/`name_equity`.
- **Initial scope.** The first batch ports three plugins:
  - `checkcommodity` — diagnostic for commodities used without a
    matching `Commodity` directive, with ignore-map support.
  - `checkdrained` — synthesizes zero-balance assertions after every
    `close` of a balance-sheet account.
  - `checkclosing` — expands `closing: TRUE` posting metadata into a
    zero-balance assertion dated transaction+1 day, stripping the
    metadata key from a cloned posting.
- **Future ports.** The upstream library has ~25 plugins; they fall into
  three shapes that share boilerplate:
  - *Diagnostic-only* (no new directives): `coherent_cost`, `leafonly`,
    `noduplicates`, `nounused`, `onecommodity`, `sellgains`,
    `unique_prices`, `valid_acctconfig`, `pedantic` (meta).
  - *Synthesizing* (insert directives): `auto`, `auto_accounts`,
    `close_tree`, `fill_account`, `ira_contribs`, `mark_unverified`,
    `tag_pending`, `unrealized`, `forecast`, `implicit_prices`,
    `split_expenses`, `book_conversions`.
  - *Filtering / transforming*: `exclude_tag`, `commodity_attr`.
- **Acceptance criteria for each future port.** (a) registered under
  both the upstream and Go-path names, (b) blank-imported from the
  umbrella `std` package, (c) unit tests alongside the plugin, (d)
  deviations documented in `doc.go`, (e) upstream copyright preserved.
  Ports that can't follow one of these criteria (e.g. behavior that
  depends on unported framework features) note the gap in their `doc.go`
  and open a TODO rather than silently diverging.

### Deliverables

- `pkg/ext/postproc/api`: stable interface (6a — done).
- `pkg/ext/postproc`: registry + runner (6a — done).
- `pkg/ext/goplug`: `.so` loader (6b).
- `pkg/ext/extproc`: external-process host + SDK (6c).
- `pkg/ext/postproc/std`: Go ports of upstream's standard plugin
  library, each as a blank-importable subpackage, aggregated by the
  umbrella `std` package (6d).

---

## Phase 7: Quote Library (`pkg/quote`)

**Dependencies:** Phase 6 (plugin system)

`pkg/quote` fetches commodity and FX prices from external sources and emits them as `ast.Price` directives that the rest of the pipeline can consume directly. There is no parallel "Quote" type: the wire-out shape is `ast.Price`, with per-source attribution carried on `Price.Meta`. Bean-price's `price` meta grammar on Commodity directives is accepted as-is by `pkg/quote/meta`, so existing ledgers carry over without rewriting; outside that single point of compatibility the layer is Go-native.

The library is split so that out-of-tree quoter authors only need to depend on a small declarative package, while the orchestration code stays internal to the host:

- **`pkg/quote/api`** — declarative interface package shared with out-of-tree plugins. Holds `Pair`, `SourceRef`, `PriceRequest`, `Mode`, `SourceQuery`, and the `Source` / `LatestSource` / `AtSource` / `RangeSource` interfaces. Any incompatible change here requires a goplug `APIVersion` bump.
- **`pkg/quote`** — the orchestrator: `Register`/`Lookup`/`Names` plus `Fetch(ctx, registry, spec, opts...)`, `WithConcurrency`, `WithClock`, `WithObserver`.
- **`pkg/quote/meta`** — bean-price-compatible `price:` meta parser; returns `[]PriceRequest` from a Commodity directive. The default key is `"price"`, overridable via `--meta-key`.
- **`pkg/quote/sourceutil`** — composable author-side decorators (`WrapSingleCell`, `DateRangeIter`, `BatchPairs`, `Concurrency`, `RateLimit`, `RetryOnError`, `Cache`) that each preserve the wrapped source's capability sub-interfaces so they stack freely (typical: `Cache(RateLimit(RetryOnError(source)))`).
- **`pkg/quote/pricedb`** — `Dedup` (key: `(Date.UTC, Commodity, Amount.Currency)`) and `FormatStream` (sort + canonical print). It deliberately does not merge prices into existing ledger files; ledger write-back belongs to bean-daemon (Phase 10).
- **`pkg/quote/std/ecb`** — the Phase 7 reference source.
- **`cmd/beanprice`** — the CLI driver.

Real-world sources have one natural batching axis: a single (pair, date) cell, a row keyed by date, a column keyed by commodity, a full matrix, or latest-only. Forcing every source to implement a single all-shapes method either drowns callers in `unsupported`-error handling or forces stub implementations. Phase 7 instead uses a hybrid: a base `Source` interface (just `Name`) plus optional `LatestSource` / `AtSource` / `RangeSource` sub-interfaces. A source declares only what it natively serves — by implementing the matching sub-interface — and the orchestrator detects support via type assertions, with documented demotion paths (e.g. `ModeRange` against an `AtSource` becomes a per-day loop). `Pair` is the logical request unit; `SourceQuery` adds the source-specific symbol so the same commodity can have different tickers across sources without polluting the output.

`Fetch` walks each `PriceRequest`'s priority-ordered `Sources[]` in synchronised levels. At level k every still-unresolved unit contributes its k-th source name to a `{name → []unit}` grouping; each named source is consulted exactly once on the level (potentially expanding into several physical calls per `BatchPairs` and `RangePerCall`), the entire level finishes, and then unresolved units advance to k+1. The barrier between levels is what makes fallback safe under shared batch sources: two requests with opposing priorities over the same two batch sources cannot deadlock, because at every level each source is hit exactly once with the union of pending queries that named it. A speculative fan-out that ran primary and fallback in parallel was rejected because it triggers unbounded fallback explosion the moment several priority chains share downstream sources. There is no `FallbackSource` decorator — chains are first-class on `PriceRequest.Sources`.

`pkg/quote/meta.ParsePriceMeta` accepts the bean-price grammar `value := psource (WS+ psource)*`, `psource := CCY ":" entry ("," entry)*`, `entry := SOURCE "/" SYMBOL`. The quote currency (`CCY:`) is required; the bean-price `^` inverted-quote prefix and CCY-less forms are surfaced as `quote-meta-unsupported` rather than silently accepted, leaving them as a typed extension point. There is no `--quote` override flag — the meta is the single source of truth and `--source` mirrors the same psource grammar one psource at a time.

`pkg/quote/std/ecb` registers the `ecb` source. It was chosen for the Phase 7 reference slot because it is public, unauthenticated, stable, natively range-capable (one HTTP call returns many days), and rate-limit-free in practice, so CI runs hermetically against checked-in XML fixtures. ECB only publishes EUR-base reference rates, so the source serves only `Pair.Commodity == "EUR"`; this is a useful start, with more standard sources (yahoo, google, AlphaVantage, ...) landing in later phases. There is no quote-specific loader: out-of-tree quoters ship as goplug `.so` files whose `InitPlugin` callback calls `quote.Register(name, source)`, and `--plugin PATH` on `cmd/beanprice` walks the supplied list through `pkg/ext/goplug`. An out-of-process `Source` (extproc) is deferred until Phase 6c lands.

Sub-phase status:

- **7a — landed in this branch.** All packages above plus `cmd/beanprice` and the ECB reference source.
- **7b — landed in this branch.** A goplug fixture under `cmd/beanprice/testdata` exercises the `--plugin` path end-to-end and locks in the plugin ABI surface.
- **7c — deferred.** External-process `Source` is held until Phase 6c (extproc) lands and is out of scope for Phase 7.

Acceptance is covered by tests in this branch: a deadlock-regression test for the level-by-level scheduler under shared batch sources, a table-driven test of the bean-price meta grammar (including the rejected `^` and CCY-less forms), hermetic ECB tests against checked-in XML fixtures (with a `live`-tagged smoke test held separately), and a `cmd/beanprice` CLI suite covering flag parsing, exit codes, and the `--plugin` fixture path. `cmd/beansprout quote` (Phase 12) will wrap this library as a user-facing subcommand; persisting prices back into ledger source files is bean-daemon's responsibility (Phase 10), so `pricedb` deliberately stops at producing a printable, deduplicated stream.

---

## Phase 7.5: Directive distribution CLI (`cmd/beanfile`)

**Dependencies:** Phase 1 (CST), Phase 2 (AST), Phase 3 (printer / format),
Phase 7 (quote, as a representative directive producer).

`cmd/beanfile` is a stateless offline CLI that reads a directive stream
(stdin or files) and merges each directive into the appropriate file in a
multi-file beancount ledger. It bridges directive *producers* (`beanprice`
today, importers tomorrow) with the multi-file layout, without requiring
`bean-daemon` to be running.

The supporting libraries live under `pkg/distribute/`:

- `pkg/distribute/route` — directive → destination path resolution, with
  per-account-tree and per-commodity overrides.
- `pkg/distribute/dedup` — ledger-wide equivalence index covering both
  active and commented-out directives.
- `pkg/distribute/comment` — recognizer and emitter for commented-out
  directives.
- `pkg/distribute/merge` — CST round-trip insertion preserving every byte
  outside the inserted region, atomic write, order-driven binary search for
  the insertion offset.

A directive is filed by a three-way decision: skip if an equivalent already
exists at the destination (active or commented-out); write as a
commented-out marker if an active equivalent exists elsewhere in the
ledger; otherwise write as a normal active directive.

See [docs/beanfile-design.md](docs/beanfile-design.md) for the full
specification, architecture, configuration schema, and sub-phase plan
(7.5a–7.5i).

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
           cmd/beanfile       (Phase 7.5, optionally Phase 7 for upstream)
Phase 8:  pkg/importer        (Phase 2, 6)
Phase 9:  pkg/query           (Phase 2, 4, 5)
Phase 10: cmd/bean-daemon     (Phase 2, 4, 5, 9)
Phase 11: cmd/beancount-lsp   (Phase 1, 2, 4)
Phase 12: cmd/beansprout      (Phase 7, 8, 10)
```

Phases 6 and 11 have no blocking dependencies on later phases and can be developed in parallel with other phases. Phases 7 and 8 can proceed concurrently once Phase 6 is complete.
