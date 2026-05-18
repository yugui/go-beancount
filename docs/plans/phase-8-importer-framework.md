# Phase 8: Transaction Import Framework (`pkg/importer`) — Redesign

## Context

`PLAN.md` (lines 310-331) currently describes Phase 8 as a RawTransaction
+ Classifier framework. This shape has problems we want to fix before
implementation:

- It assumes each input file maps to a single fixed account.
- It interposes a `RawTransaction` middle layer between file bytes and
  `ast.Directive`, even though the natural pipeline is file → directives.
- The extension surface (importer & classifier registration) is not
  aligned with the registry pattern already in `pkg/quote` and
  `pkg/ext/postproc`.
- It does not include a generic post-process hook step where the
  open-ended "fill in counterpart account" work belongs.

beangulp ships two ideas worth keeping — an Identify+Extract dispatch
(registered importers vote on which one handles a file) and a pluggable
post-hook chain. It also has weak points we deliberately improve on:
file-level account binding, whitespace-tokenized narration similarity,
not using auto-postings as learning signal, and rough txn similarity.

This plan adopts the strong ideas, structurally aligns the framework
with go-beancount's existing registry/goplug pattern, leaves explicit
room for the weak-point improvements as later-phase work (Phase 8.1
and 8.2), and ships exactly one reference importer (CSV) plus one
reference hook (regex table) plus a small `importerutil` building-block
package to make `beanimport <file> | beanfile --ledger root.beancount`
immediately useful end-to-end.

## Goal

Build a registry-driven, plugin-aware Beancount import framework that
turns a local file into a stream of directives printed to stdout.
Deliverables: framework + reference CSV importer + reference
classifier hook + decorator building blocks + `cmd/beanimport` CLI.
Downstream file placement is already covered by `cmd/beanfile`
(Phase 7.5). Cross-source dedup is a `pkg/distribute` concern, not
this phase's.

## Scope

**In scope:**

- `pkg/importer` — `Importer` interface, `Input`/`Output` types,
  optional `Configurable` and `Streaming` sub-interfaces, plus
  `Register`/`Lookup`/`Names`/`GlobalRegistry`, `Dispatch`, and
  `Apply`. Single package (no `/api` split).
- `pkg/importer/hook` — `Hook` interface, `HookInput`/`HookResult`
  types, registry, and `Chain` runner. Single package (no `/api`
  split).
- `pkg/importer/importerutil` — author-side decorator building blocks
  used by importer implementations to compose common behaviour.
  Initially: `BalanceWith` and `StampMetadata`.
- `pkg/importer/std/csvimp` — reference data-driven CSV/TSV importer.
- `pkg/importer/hook/std/classify` — reference regex/table classifier
  hook that completes single-leg transactions.
- `cmd/beanimport` — CLI: single positional input file, Identify-based
  dispatch, optional `--hook` chain, stdout output.
- PLAN.md Phase 8 section rewrite, plus new Phase 8.1 (ML / improved
  classification hooks) and Phase 8.2 (XML / OFX / QIF importers) as
  documented future scope.

**Out of scope (deferred):**

- A framework-defined `import-id` metadata convention. Identity
  recording is left to each importer, and cross-source deduplication
  (e.g. the same txn arriving via both a bank statement and a credit
  card statement) is `pkg/distribute`'s concern, not this phase's.
- ML / embedding-based classification — recorded as PLAN.md Phase 8.1.
- Multilingual / non-whitespace tokenization for narration similarity
  — Phase 8.1.
- Ledger-corpus learning, auto-postings as learning signal — Phase 8.1.
- XML xpath importer, OFX, QIF — PLAN.md Phase 8.2.
- API-based fetcher integration beyond shell process substitution.
- Synthetic `Equity:Unclassified` balancing posting in the framework
  (importer authors may opt into `BalanceWith(account)` from
  `importerutil`, but the framework does not impose it).

## Architecture

```
                      ┌─────────────────────────┐
positional file ───► │  cmd/beanimport          │ ───► stdout
   --account A         │   1. Dispatch (Identify)│       (beancount text)
   --hook NAME[,…]     │   2. Importer.Extract   │             │
   --config PATH       │   3. Hook.Chain         │             ▼
   --importer NAME     │   4. printer.Fprint     │      cmd/beanfile
   --plugin PATH.so    └─────────────────────────┘      (pkg/distribute)
   --strict                        ▲   ▲
                                   │   │
                  pkg/importer registry pkg/importer/hook registry
                  (Identify + Extract,  (Apply chain in --hook order)
                  importerutil helpers)
                                   ▲
                          goplug.Load(<plugin>.so)
                              InitPlugin() registers
                                   into both
```

## Sub-phase decomposition

Seven sub-phases (8a–8g). Each is independently testable; PR boundaries
follow these sub-phases. Phase 4 (per-step detailed design) will lock
the Contract for each as it is picked up.

### 8a — `pkg/importer` interface + registry

- **Modules:** `pkg/importer/importer.go` (`Importer` interface:
  `Name() string`, `Identify(ctx, Input) bool`, `Extract(ctx, Input)
  (Output, error)`; `Input` {path, opener closure returning
  `io.ReadCloser`, sniffed first ~4 KiB, MIME hint, `Hints
  map[string]string`}; `Output` {`Directives []ast.Directive`,
  `Diagnostics []ast.Diagnostic`}; optional `Configurable`
  (`Configure(json.RawMessage) error`); optional `Streaming`
  (`StreamExtract(ctx, Input) iter.Seq2[ast.Directive, error]`)).
  `pkg/importer/registry.go` (`Register`/`Lookup`/`Names`/`GlobalRegistry`
  + `sync.RWMutex` + init-time duplicate panic, modelled on
  `pkg/quote/registry.go`). `pkg/importer/dispatch.go` (`Dispatch(ctx,
  reg, input) (Importer, bool, []ast.Diagnostic)` walking sorted-by-name,
  returning the first Identify=true). `pkg/importer/apply.go` (Apply
  convenience).
- **Functional requirements:** safe to populate from `init()` and from
  goplug.Load callbacks; declaration-only types form the goplug ABI
  (later breaking changes require `goplug.APIVersion` bump); diagnostic
  codes `importer-not-registered`, `importer-ambiguous`,
  `importer-none`.
- **Verification:** unit tests for registration, Lookup, Names,
  concurrency stress on RWMutex, Dispatch ordering, ambiguity
  diagnostic.
- **Quality requirements:** godoc on every exported symbol (project
  convention); Lookup is lock-light; Dispatch is O(N) with N small;
  panic messages name the duplicate registration site.

### 8b — `pkg/importer/hook` interface + registry + Chain

- **Modules:** `pkg/importer/hook/hook.go` (`Hook` interface
  `Apply(ctx, HookInput) (HookResult, error)`; `HookInput`
  {`Directives []ast.Directive`, `Hints map[string]string`,
  `Options *ast.Options`}; `HookResult` {`Directives`, `Diagnostics`}).
  `pkg/importer/hook/registry.go` (mirrors 8a's registry).
  `pkg/importer/hook/chain.go` (`Chain(ctx, reg, names []string, in
  HookInput) (HookResult, error)` runs hooks in caller-supplied order,
  halts on non-nil error, propagates diagnostics).
- **Functional requirements:** users compose pipelines explicitly via
  `--hook a,b,c`; no implicit "run all registered hooks". Hooks may
  add/replace/drop/annotate directives via `HookResult.Directives`.
- **Verification:** registration, chain-ordering, error-halt, and
  diagnostic-propagation tests.
- **Quality requirements:** chain is non-allocating in the no-op case;
  godoc states the no-auto-run contract.

### 8c — `pkg/importer/importerutil` building blocks

- **Modules:** `pkg/importer/importerutil/balancewith.go` (`BalanceWith(d
  ast.Directive, account string, currency string) ast.Directive` —
  given a Transaction with a single Posting, returns a clone with a
  counterpart Posting at `account` with the negated amount;
  no-op on already balanced or non-Transaction directives).
  `pkg/importer/importerutil/stampmetadata.go` (`StampMetadata(d
  ast.Directive, key string, value string) ast.Directive` — returns a
  clone with the given metadata key/value set; idempotent on re-stamp
  with the same value, overwrites on different value).
- **Functional requirements:** decorators are immutable transforms on
  `ast.Directive` — they always return a deep-cloned directive, never
  mutate input. Importer authors compose them by chaining inside
  Extract; the framework neither enforces nor injects them. This is
  the importer-side analogue of `pkg/quote/sourceutil` for `pkg/quote`.
- **Verification:** unit tests for BalanceWith (single-posting txn
  case, already-balanced no-op case, non-Transaction no-op),
  StampMetadata (set, overwrite, idempotency), clone safety (input
  unchanged).
- **Quality requirements:** each decorator is small, allocation-light,
  and has godoc making clear it is for use **inside** importer
  implementations (not run automatically by the framework).
  Adding new decorators in later phases must follow the same shape:
  pure functions, no global state.

### 8d — `pkg/importer/std/csvimp` reference importer

- **Modules:** `pkg/importer/std/csvimp/csvimp.go` (importer impl;
  `Identify` checks `.csv`/`.tsv` extension + optional TOML `match`
  regex + presence of declared columns in first non-blank line),
  `pkg/importer/std/csvimp/config.go` (TOML schema, see below),
  `pkg/importer/std/csvimp/doc.go` (dual registration: `csv` +
  Go-path). Built-in transforms: parse-date, parse-decimal,
  per-column-negate, concat-with-separator, sum-across-columns. Uses
  `importerutil.StampMetadata` to stamp **`csvimp-rowhash`** =
  short SHA-256 prefix of the canonical row bytes on every emitted
  Transaction. Account for the primary posting comes from
  `Hints["account"]` (set by `cmd/beanimport --account`) or TOML
  `account` if no Hints value present.

- **TOML schema (per-shape subtable):**

  ```toml
  [shape.mybank]
  match            = "mybank.*\\.csv$"     # optional path regex
  delimiter        = ","                    # or "\t"
  skip_lines       = 1                      # header lines to skip
  date_col         = "Date"
  date_format      = "2006-01-02"
  payee_col        = "Payee"                # optional
  currency_col     = "Currency"             # optional
  default_currency = "JPY"

  # Narration: one or more source columns concatenated with a separator.
  # Empty values are skipped so the separator does not double up.
  narration_cols      = ["Description", "Memo"]
  narration_separator = " / "

  # Amount: one or more source columns summed into a single signed
  # amount on the (single) emitted posting. Each entry has an
  # independent `negate` flag, supporting layouts where:
  #   - single signed column                  → one entry, negate=false
  #   - single column that needs flipping     → one entry, negate=true
  #   - separate debit/credit columns         → two entries with
  #     opposite `negate` flags (typical for Japanese bank statements
  #     with お支払金額 / お預入金額)
  # Blank cells in an amount column contribute 0; a row with all
  # amount columns blank emits a diagnostic and is skipped.
  [[shape.mybank.amount]]
  col    = "Withdrawal"   # お支払金額
  negate = true

  [[shape.mybank.amount]]
  col    = "Deposit"      # お預入金額
  negate = false
  ```

  A degenerate single-column shape is supported by a single `[[amount]]`
  entry. The schema is documented in full in package godoc.

- **Functional requirements:** no counterpart posting (left to the
  classify hook or to an importerutil decorator the user composes via
  Go); per-row failure (bad date, unparseable amount in any non-blank
  amount column, all amount columns blank) emits a diagnostic and
  skips that row without aborting; running twice on the same input
  produces byte-identical stdout (idempotency). The `csvimp-rowhash`
  metadata key is documented in package godoc as the importer-specific
  identity key that `pkg/distribute/dedup` callers can list in
  `eqKeys` if they want cross-run dedup. Multi-column narration
  concatenation skips empty cells so the separator never doubles up
  and never appears at the leading/trailing edge.
- **Verification:** table-driven tests for date/amount parsing,
  per-column negate (debit/credit layout), multi-column narration
  concat (including empty-cell handling), sum-across-columns;
  fixture CSVs with golden directive output covering at least
  (a) single signed amount column, (b) separate debit/credit
  columns, (c) multi-column narration; per-row diagnostic tests;
  idempotency test (run twice, output identical).
- **Quality requirements:** depends only on stdlib `encoding/csv` and
  the existing `decimal.Decimal`; row buffer reuse where cheap; TOML
  schema fully documented in package godoc with at least the two
  canonical layouts (single-signed-column and debit/credit) shown as
  examples.

### 8e — `pkg/importer/hook/std/classify` reference hook

- **Modules:** `pkg/importer/hook/std/classify/classify.go` (hook
  walks directives; for each Transaction with exactly one Posting,
  iterates the rule list and adds a counterpart Posting with the
  negated amount on the first match — implemented on top of
  `importerutil.BalanceWith`), `pkg/importer/hook/std/classify/config.go`
  (TOML schema: list of `[[rule]]` entries with `payee_regex`,
  `narration_regex`, `currency`, `account`),
  `pkg/importer/hook/std/classify/doc.go` (dual registration:
  `classify` + Go-path).
- **Functional requirements:** idempotent on already-balanced txns
  (predicate fails); deterministic ordering (rules walked in
  declaration order); `classify-no-rule` warning diagnostic for
  unmatched single-leg txns; no tokenization, no ledger learning.
- **Verification:** rule-matching tests (payee, narration, currency
  filter, ordering), idempotency test, no-rule diagnostic test.
- **Quality requirements:** O(rules) per single-leg txn; regex
  compilation cached at `Configure` time.

### 8f — `cmd/beanimport` CLI

- **Modules:** `cmd/beanimport/main.go`, `cmd/beanimport/flags.go`,
  `cmd/beanimport/run.go`. Flags: `--importer NAME` (forces a
  specific importer, skipping Identify), `--hook NAME[,NAME...]`
  (ordered chain, default empty), `--config PATH` (TOML; per-importer
  subtables), `--account ACCOUNT` (populates `Hints["account"]`,
  **accepted at most once**), `--plugin PATH.so` (repeatable, runs
  through `pkg/ext/goplug.Load`), `--strict` (warnings → errors).
  **Single positional argument** (input file path). CSV importer and
  classify hook are blank-imported so the default binary works
  out of the box.
- **Functional requirements:** canonical pipeline is
  `beanimport --account Assets:Foo --hook classify --config foo.toml
  statement.csv | beanfile --ledger root.beancount`. Exit codes mirror
  `cmd/beanprice` (0 ok, 1 user-error, 2 internal/plugin-load failure).
  Diagnostics in `<path>:<line>:<col>: severity: msg [code]` form.
  **No warning** is emitted when output contains single-leg txns and
  no hook is wired — the user explicitly chose silent behaviour.
- **Verification:** CLI integration tests for argument parsing, exit
  codes, plugin loading, end-to-end pipeline against a fixture CSV.
- **Quality requirements:** single-file input keeps the CLI lean (no
  paired-flag bookkeeping). The package doc and CLI `--help` text
  explicitly point users at the hook chain as the recommended way to
  balance.

### 8g — goplug fixtures + PLAN.md rewrite

- **Modules:** `cmd/beanimport/testdata/staticimporter/plugin.go`
  (out-of-tree importer fixture mirroring
  `cmd/beanprice/testdata/staticquoter`),
  `cmd/beanimport/testdata/statichook/plugin.go` (hook fixture),
  `cmd/beanimport/testdata/csv/<fixtures>/...` (CSV + TOML + golden
  output). `PLAN.md` rewritten so:
  - Phase 8 section (currently lines 310-331) is replaced by the
    outline at the end of this document.
  - **Phase 8.1** is added: ML / improved classification hooks (corpus
    learning, multilingual tokenization, embedding-based similarity,
    auto-postings as learning signal). Sketch only; not designed yet.
  - **Phase 8.2** is added: additional importers — XML xpath, OFX,
    QIF. Sketch only; not designed yet.
- **Functional requirements:** goplug fixtures lock the ABI for both
  importer and hook plugins; PLAN.md accurately describes what shipped
  in Phase 8 and what is reserved for 8.1 / 8.2.
- **Verification:** `bazel test //cmd/beanimport/...` includes the
  goplug-loaded fixture path; `bazel build //...` passes; `bazel run
  //:gazelle` is clean.
- **Quality requirements:** PLAN.md prose follows the project's
  commit/PR style (why and intent, not mechanical narration of
  implementation).

## Design decisions and alternatives

### 1. Package decomposition

Single packages per registry: `pkg/importer` and `pkg/importer/hook`,
each holding both the interfaces and the registry/runner. Plus
`pkg/importer/importerutil` for author-side decorators (analogue of
`pkg/quote/sourceutil`) and the `std/*` reference subpackages.
**Alternatives considered:** (a) mirror `pkg/quote` exactly with an
`/api` subpackage per registry; (b) flat layout with no separation
between framework and reference implementations. **Why rejected:**
(a) the `api` package name is too generic, collides badly across the
tree, and the split between `hook` and `hook/api` has no real
content — there is one interface and one registry, the `/api` split
adds a directory without earning it; (b) reference implementations
under `std/*` need to be optional imports so that programs depending
on `pkg/importer` alone don't drag in the CSV reader.

### 2. Post-hook registry placement

Stand up a dedicated `pkg/importer/hook` registry rather than reusing
`pkg/ext/postproc`. **Alternatives considered:** (a) reuse postproc's
`Plugin` interface and synthesise a `plugin "classify"` directive into
the output stream; (b) adapt postproc plugins at registration time.
**Why rejected:** postproc plugins fire over a fully loaded ledger and
need access to option/plugin directives. Import hooks fire over a
freshly extracted slice and need access to `Hints` (CLI overrides) that
postproc deliberately does not carry. Co-mingling would force `Hints`
into `postproc/api.Input` for no benefit to its existing users. The
two registries will look similar by accident, not by design; future
ML hooks need to extend `HookInput` (corpus path, model file) without
affecting postproc.

### 3. Importer interface shape

`Identify(ctx, in Input) bool` where `Input` carries path, an `Opener`
closure, pre-sniffed first ~4 KiB, MIME hint, and `Hints
map[string]string`. **Alternative considered:** path-only
`Identify(path string) bool` (beangulp-style). **Why rejected:**
CSV files without canonical extensions and binary formats with magic
headers need the sniff bytes; reopening the file inside each Identify
implementation is wasteful and surprising. `Extract` returns
`(Output, error)`; an optional `Streaming` sub-interface offers an
iterator-based variant for importers that handle very large files.
Same hybrid trick `pkg/quote` uses for `LatestSource`/`AtSource`/
`RangeSource`.

### 4. CLI shape

Single positional file + flags, `--account` accepted at most once.
**Alternatives considered:** (a) multiple positional files with paired
`--account` flags; (b) pure TOML-driven invocation listing inputs in
`[[inputs]]` blocks. **Why rejected:** (a) custom flag pairing is
error-prone and the most common case is one file at a time;
(b) ad-hoc one-shot imports would have to write a TOML file. The
chosen design keeps `beanimport` ad-hoc-friendly; users batching
multiple files invoke `beanimport` once per file (shell loop, Makefile).
Matches the `beanprice` CLI shape already in the codebase.

`--pass-through` is not added (importers do not emit directives outside
their own scope; the concept does not apply). `--dry-run` is not added
(stdout is inspectable by pipe).

### 5. CSV importer model

One registry entry named `csv` + a TOML configuration file with one
subtable per bank/source shape. Identify disambiguates via the optional
`match` regex on input path, falling back to checking declared columns
against the file's first non-blank line. **Alternatives considered:**
(a) one registry entry per CSV shape via `csvimp.RegisterShape(name,
config)` Go API; (b) single-line DSL on the command line. **Why
rejected:** (a) forces every CSV variant to be a Go-code change;
(b) becomes unreadable once date formats and concat rules are
involved.

Built-in transforms intentionally limited to a small set:
parse-date, parse-decimal, per-column-negate, concat-with-separator,
and sum-across-columns. The amount section is **a list of column
entries each with an independent `negate` flag**, summed into a
single signed amount on the emitted posting — this covers both the
single-column-with-signed-amount layout and the
separate-debit-credit-column layout commonly seen on Japanese bank
statements (お支払金額/お預入金額) without per-bank Go code.
Narration is **a list of source columns concatenated with a
separator**, with empty cells skipped so the separator never doubles
up or appears at the edges. Anything fancier (lookup tables,
multi-row joins, regex extraction beyond column name matching)
belongs in a hook.

### 6. Identity metadata convention (importer-specific, not framework-wide)

The framework does **not** define an `import-id` metadata key.
Identity recording is each importer's concern, using importer-specific
metadata keys that the importer documents. CSV importer uses
`csvimp-rowhash` (short SHA-256 prefix of canonical row bytes).
Future importers will use their own keys (e.g. `ofximp-fitid`,
`xmlimp-pathhash`).

**Alternatives considered:** (a) framework-defined `import-id`
convention; (b) sequential-index stamping inside `pkg/importer.Apply`.
**Why rejected:** (a) the same logical txn can arrive via different
import paths (bank statement + credit card statement) with different
input formats, so a single universal id is unachievable; cross-source
dedup is `pkg/distribute`'s concern, not Phase 8's. (b) sequential
indices are not stable across re-runs, so they don't help dedup at
all.

Downstream consumers (`pkg/distribute/dedup`) accept a list of
metadata keys in `eqKeys`; a user wiring up dedup specifies whichever
importer-specific keys are relevant for their pipeline.
`pkg/importer/importerutil.StampMetadata` is the helper importers use
to set these keys without depending on `pkg/ast` builder internals.

### 7. Single-leg txn behaviour

Importers emit txns with a single posting when they cannot determine
the counterpart. **No synthetic balancing posting** is added by the
framework. **No warning** is emitted by `cmd/beanimport` when the
output contains single-leg txns and `--hook` is empty (user choice).
The package godoc on `pkg/importer/std/csvimp` and the help text of
`cmd/beanimport` document that wiring a classifier hook is the
recommended path. Importer authors who want their importer to always
balance against a fixed account can compose `importerutil.BalanceWith`
inside Extract — that is a hard-coded importer-author decision, not a
framework default.

### 8. XML / OFX / QIF scope

These importers do not ship in Phase 8. PLAN.md records them under a
new **Phase 8.2** so the project board tracks them as known follow-up
work. ML / corpus-learning / multilingual / improved-similarity hooks
go under a new **Phase 8.1**. The Phase 8 framework is designed so
these phases can land without breaking the goplug ABI (extending
interfaces only via optional sub-interfaces, not by re-shaping the
base `Importer` or `Hook` interfaces).

## Verification (end-to-end)

After 8f lands:

```bash
# Build
bazel build //cmd/beanimport //pkg/importer/...

# Run
bazel run //cmd/beanimport -- \
    --account Assets:Bank:Foo \
    --hook classify \
    --config testdata/csv/foo/config.toml \
    testdata/csv/foo/statement.csv \
  | bazel run //cmd/beanfile -- \
    --ledger /tmp/ledger/root.beancount \
    --dry-run

# Test (all sub-phases combined)
bazel test //pkg/importer/... //cmd/beanimport/...
```

End-to-end success criteria:

1. CSV in → directives out on stdout, parseable by
   `pkg/ast.LoadReader`.
2. Every emitted directive carries the importer-specific identity
   metadata key (`csvimp-rowhash` for CSV).
3. Running `beanimport` twice on the same input produces byte-identical
   stdout (idempotency).
4. `beanimport | beanfile --dry-run` reports the right destination
   files via `pkg/distribute/route` and (when `eqKeys` is configured
   with `csvimp-rowhash`) does not duplicate already-imported rows on a
   second run.
5. goplug fixture under `cmd/beanimport/testdata/staticimporter` loads
   and registers under `--plugin PATH.so`.
6. `bazel test //...` and `bazel run //:gazelle` are clean.

## PLAN.md rewrite skeleton (delivered as part of 8g)

```
## Phase 8: Transaction Import Framework (`pkg/importer`)

Dependencies: Phase 2 (AST), Phase 3 (printer), Phase 6 (plugin
system), Phase 7.5 (cmd/beanfile, for downstream dedup integration).

[Opening paragraph: framework purpose, file-only input shape, shell
process substitution for API fetchers, structural parallel to
pkg/quote, identity metadata is importer-specific rather than
framework-wide.]

[Inline ASCII pipeline diagram identical to the one in this plan.]

### Architecture
  - pkg/importer
  - pkg/importer/hook
  - pkg/importer/importerutil
  - pkg/importer/std/csvimp
  - pkg/importer/hook/std/classify
  - cmd/beanimport

### Sub-phase status (8a–8g)

### Key design decisions
  [Decisions 1–8 from this plan, one short paragraph each.]

### Out of scope (deferred)
  - Framework-wide identity metadata key
  - Synthetic Equity:Unclassified placeholder
  - API fetcher beyond shell substitution
  - See Phase 8.1 and 8.2 below

## Phase 8.1: Improved Classification Hooks (`pkg/importer/hook/std/...`)

Dependencies: Phase 8.

Reference hooks that go beyond regex/table matching: corpus-learning
from existing ledger entries, embedding-based txn similarity,
multilingual tokenization (non-whitespace languages), and using
auto-postings as learning signal. Implementation deferred; design will
be done when this phase is picked up. The Phase 8 hook interface and
HookInput shape are designed to accommodate these extensions
(corpus path, tokenizer choice, model reference) via new optional
fields, not interface-level changes.

## Phase 8.2: Additional Importers (`pkg/importer/std/...`)

Dependencies: Phase 8.

Reference importers for additional file formats: XML (xpath-driven),
OFX, QIF. Implementation deferred. The Phase 8 Importer interface and
the importerutil decorators are designed to support these without
interface-level changes.
```

## Open risks (acknowledged, not blocking)

- **goplug ABI freeze.** Once 8a ships, the base `Importer` interface
  (Name + Identify + Extract) is API v1. The optional-sub-interface
  pattern is the hedge against future evolution. Phase 4 for 8a will
  re-confirm the exact signature list before committing.
- **CSV Identify ambiguity** across multiple bank configs: covered by
  TOML `match` regex as the documented precedence rule. Phase 4 for
  8d will lock the precedence order.
- **`csvimp-rowhash` semantic ambiguity** for retry-of-same-day reports:
  same row → same hash → downstream dedup fires. This is the intended
  behaviour and is documented in `pkg/importer/std/csvimp`'s godoc.
- **Single-leg txn surviving past `cmd/beanimport`** when no hook is
  wired: user's responsibility, no warning, documented in godoc and
  CLI help.
- **Cross-source identity** (same txn arriving via multiple importers
  with different formats): out of scope; `pkg/distribute` is the
  natural home for that kind of cross-cutting dedup.

## Workflow ahead (after plan approval)

Once this plan is approved via ExitPlanMode and copied to
`docs/plans/<slug>.md`, the orchestration skill loops Phases 4–8 over
each sub-phase 8a → 8g. The first iteration starts at Phase 4
(per-step detailed design) for 8a.
