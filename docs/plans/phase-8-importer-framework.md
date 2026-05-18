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

### Detailed Design

#### Contract

##### Package and import path

Package path `github.com/yugui/go-beancount/pkg/importer`, package name
`importer`. Single Go package; no `/api` split (per plan decision 1).
All symbols listed below are exported from this package.

##### `Importer` interface

```go
type Importer interface {
    Name() string
    Identify(ctx context.Context, in Input) bool
    Extract(ctx context.Context, in Input) (Output, error)
}
```

- `Name()` returns the registry key. By convention, the upstream
  tool's name (e.g. `"csv"`, `"ofx"`) for canonical reference
  importers and the Go fully-qualified package path otherwise — the
  same convention `pkg/quote` uses. Two registrations under the same
  name panic; see `Register` below.
- `Identify` is a **pure, side-effect-free, cheap** check. It MUST
  NOT consume `in.Opener` unless the implementation cannot make a
  decision from `in.Path`, `in.MIME`, and `in.Sniff` alone. If it
  does call `Opener`, it MUST close the returned `io.ReadCloser`
  before returning. Identify returning `true` is a non-binding
  preference — Dispatch may still pick a different importer if this
  one is not the first match in sorted-by-name order.
- `Extract` is the work-doing call. It returns:
  - a possibly-empty `Output.Directives` slice in source-encounter
    order;
  - a possibly-empty `Output.Diagnostics` slice for **per-row /
    per-record** problems the importer recovered from (bad date,
    unparseable amount, malformed row), each carrying its own
    `Severity`;
  - a non-nil `error` ONLY when the import as a whole could not
    proceed (Opener failed before any directive was produced, the
    file format is structurally broken beyond per-row recovery, the
    context was cancelled). When `error` is non-nil, `Output.Directives`
    SHOULD be nil; `Output.Diagnostics` MAY still be non-nil and is
    composed by `Apply` into the final diagnostic stream.
  - `error` is reserved for system-level / framework-level failures
    (ctx cancellation, I/O, programmer errors). Anything attributable
    to ledger contents — including "row 14 has no date column" — is
    a Diagnostic.

##### `Input` struct

```go
type Input struct {
    Path    string
    Opener  func() (io.ReadCloser, error)
    Sniff   []byte
    MIME    string
    Hints   map[string]string
}
```

- `Path` — display name passed to diagnostics and to `Identify`'s
  extension/regex checks. SHOULD be the user-visible path the CLI
  was invoked with (not a temp-file path); the data on disk is
  reached via `Opener`, not via `os.Open(Path)`. Empty Path is
  permitted (stdin invocation, `--importer NAME` forcing a specific
  importer); importers MUST tolerate `Path == ""`.
- `Opener` — closure returning a fresh `io.ReadCloser` on each call.
  - MAY be called zero, one, or many times by `Identify` and
    `Extract` combined. Each call returns a reader positioned at the
    start of the file; closing one reader does not invalidate
    subsequent Opener calls.
  - MUST NOT be `nil` for any Input reaching a registered Importer.
    `cmd/beanimport` (8f) is responsible for installing a working
    Opener; tests and plugin authors construct one explicitly.
  - If Opener returns a non-nil error, the caller (Identify or
    Extract) is responsible for handling it. Identify treats Opener
    failure as "cannot identify" and returns false. Extract returns
    a non-nil framework error wrapping the Opener error.
- `Sniff` — pre-read prefix of the input, **up to 4096 bytes** (the
  guarantee is "up to", not "at least": short files yield a
  short `Sniff`). Callers MUST NOT mutate the byte slice; importers
  MUST treat it as read-only. The caller (`cmd/beanimport`) is
  responsible for populating Sniff before calling Identify; an
  empty Sniff is permitted (for example when `--importer NAME`
  bypasses Identify entirely) and importers MUST tolerate it.
- `MIME` — best-effort hint sourced from the OS / HTTP header /
  user override. Empty string means "no hint"; importers MUST NOT
  treat empty MIME as a refusal signal.
- `Hints` — caller-supplied free-form key/value bag for parameters
  that survive across Identify and Extract. The framework reserves
  these keys, listed here as ABI-level surface:
  - `"account"` — primary account override (set by `cmd/beanimport
    --account`). Used by the CSV reference importer in 8d; available
    to any importer that wants to honor it.
  - All other keys are importer-specific. Importers that consume
    Hints MUST document the keys they read in their package godoc.
  - Adding new framework-reserved keys in later phases does not
    break ABI because consumers tolerate unknown keys.
  - Hints MAY be nil; importers MUST treat nil Hints identically
    to an empty map.

##### `Output` struct

```go
type Output struct {
    Directives  []ast.Directive
    Diagnostics []ast.Diagnostic
}
```

- `Directives` is in source-encounter order. Empty `Output` is a
  legal successful return (e.g. a CSV with header only).
- `Diagnostics` carries per-row problems. Severity is whatever the
  importer chose; `--strict` in 8f is responsible for warning→error
  promotion, not the framework.
- `Output` is **not** mutated by the framework after Extract
  returns; `Apply` composes the importer's Output with diagnostics
  it generates itself by appending into a fresh slice, never by
  mutating the slices Extract returned. Importers are likewise
  free to return shared / cached slices as long as they treat them
  as logically immutable after return.

##### `Configurable` optional sub-interface

```go
type Configurable interface {
    Importer
    Configure(decode func(dest any) error) error
}
```

- Detected via type assertion; importers that do not implement it
  receive no configuration call.
- `decode` is a caller-supplied callback that decodes whatever the
  caller is holding (a TOML primitive, a JSON byte slice, a
  pre-parsed map, …) into the destination value the importer
  provides. The importer writes a small fixed pattern:
  ```go
  func (i *MyImporter) Configure(decode func(any) error) error {
      var c MyConfig
      if err := decode(&c); err != nil {
          return err
      }
      i.cfg = c
      return nil
  }
  ```
  The CLI / harness chooses the decoder:
  ```go
  imp.Configure(func(dest any) error {
      return tomlMeta.PrimitiveDecode(subtable, dest)
  })
  ```
  Rationale: encoding-agnostic. The framework does not lock plugins
  into JSON, and plugin authors inherit no extra encoding
  dependency. The host stays free to swap decoders (TOML now,
  YAML/CBOR later) without an ABI bump.
- The `decode` callback MUST NOT be `nil` when Configure is invoked.
  Callers that have no configuration to supply do not call
  `Configure` at all; an importer that needs configuration to
  function is responsible for returning a meaningful error from
  Identify / Extract when its `cfg` field is the zero value.
- `Configure` MUST be safe to call at most once per Importer
  instance before any `Identify` or `Extract` call. Calling
  Configure twice on the same instance has undefined behaviour;
  callers SHOULD NOT do so. A non-nil error fails the dispatch
  pipeline early — `Apply` propagates it as a framework error
  (not a Diagnostic), since unconfigurable means the importer
  cannot be asked to do work.
- Importers MAY call `decode` zero times (e.g. to react to "is
  config available?" without actually decoding) or exactly once.
  Calling `decode` multiple times is permitted; the caller's
  decoder MUST be idempotent (each call re-decodes from the same
  source), but a sensible importer decodes once.

##### `Streaming` optional sub-interface

```go
type Streaming interface {
    Importer
    StreamExtract(ctx context.Context, in Input) iter.Seq2[ast.Directive, error]
}
```

- `iter.Seq2[ast.Directive, error]`: each yield is either
  `(directive, nil)` for a successful directive or `(zero, err)`
  for a per-record problem the consumer decides whether to
  continue past.
- Diagnostics in the streaming path: the iterator MUST NOT be
  asked to carry diagnostics inline. Instead, an importer that
  also wants to emit Diagnostics in streaming mode SHOULD
  implement the optional method `StreamDiagnostics() []ast.Diagnostic`
  which the caller invokes AFTER the iterator has been fully
  consumed (or the consumer abandons it). This keeps the iterator
  signature clean and matches `iter.Seq2`'s native shape. Importers
  that have no diagnostics in streaming mode simply do not
  implement `StreamDiagnostics`.

  ```go
  // optional companion method on a Streaming importer
  type StreamDiagnoser interface {
      StreamDiagnostics() []ast.Diagnostic
  }
  ```

- **Streaming vs. Extract precedence in Apply:** if an importer
  implements `Streaming`, `Apply` MAY call either `StreamExtract`
  or `Extract`. Apply's documented choice for ABI v1 is to call
  `Extract` (i.e. the buffered path) by default, because the
  Phase 8 CLI ultimately materialises the full directive slice for
  printing and the streaming path's only payoff is at very large
  scale. The Contract reserves the right for a future
  `--stream` flag (or for downstream consumers that genuinely
  stream) to call `StreamExtract` when the importer supports it.
  Importers MAY implement both; if an importer implements ONLY
  `Streaming` (no `Extract` … not possible because Streaming
  embeds Importer which has Extract; so every Streaming importer
  also has Extract).
- An importer implementing both MUST produce equivalent directive
  sequences from `Extract` and `StreamExtract` for the same Input.
  This equivalence is the importer author's obligation; the
  framework does not cross-check.

##### `Registry` interface and package-level functions

```go
type Registry interface {
    Lookup(name string) (Importer, bool)
    Names() []string
}

func Register(name string, imp Importer)
func Lookup(name string) (Importer, bool)
func Names() []string
func GlobalRegistry() Registry
```

- Shape and concurrency model are an exact mirror of
  `pkg/quote/registry.go`:
  - `sync.RWMutex`, Lookup/Names take the read lock, Register takes
    the write lock.
  - Safe to call from `init()` and from goplug `InitPlugin()`
    callbacks; safe to call concurrently with Lookup/Names from
    the main goroutine.
- `Register` panics with a message containing the duplicate name
  when a name is already registered. Panic message format:
  `importer: duplicate Importer registration for %q`. This matches
  `pkg/quote`'s panic discipline (init-time duplicate is a
  programmer error, not a runtime error).
- `Names()` returns the registered names sorted in ascending order.
  The sort order is part of the Contract because `Dispatch` walks
  in this order and the picked-first-match behaviour is only
  meaningful with a stable order.
- `GlobalRegistry()` returns a `Registry` view over the
  package-global state. The `Registry` interface (exported, with
  exported methods `Lookup` and `Names`) lets callers — notably
  Dispatch — accept a `Registry` argument so tests can substitute
  a fake. **Difference from `pkg/quote`**: pkg/quote's Registry
  interface exposes only `Lookup`; this Registry adds `Names`
  because Dispatch needs ordered iteration, not just point
  lookups. This is a deliberate, justified divergence.

##### Diagnostic codes

The following code strings are exported as package constants in
`pkg/importer`. They are not lifted into `pkg/ast/ast.go` because
they are framework-specific and `pkg/ast.Diagnostic.Code` is a
free-form string by design.

```go
const (
    DiagImporterNotRegistered = "importer-not-registered"
    DiagImporterNone          = "importer-none"
    DiagImporterAmbiguous     = "importer-ambiguous"
)
```

- `DiagImporterNotRegistered` — emitted by `Apply` when the user
  forced an importer name via `--importer` (the upcoming 8f flag)
  but no such name is registered. Severity: Error.
- `DiagImporterNone` — emitted by `Dispatch` when every registered
  importer's `Identify` returned false. Severity: Error.
- `DiagImporterAmbiguous` — RESERVED for future use. Phase 8a's
  `Dispatch` does NOT emit this; see the "Dispatch ambiguity
  policy" subsection below.

##### `Dispatch` function

```go
func Dispatch(ctx context.Context, reg Registry, in Input) (Importer, bool, []ast.Diagnostic)
```

- Walks `reg.Names()` in returned order (sorted ascending, per
  Registry contract) and calls `Identify(ctx, in)` on each.
- Returns `(importer, true, nil)` on the **first** importer whose
  Identify returns true.
- Returns `(nil, false, []ast.Diagnostic{ {Code: DiagImporterNone, …} })`
  when no importer identifies the input. Severity Error;
  Diagnostic.Span carries the input Path in Filename with line/col
  zero (no source location inside the file).
- ctx cancellation is observed between Identify calls; on
  cancellation Dispatch returns `(nil, false, nil)` and the caller
  (`Apply`) is responsible for converting ctx.Err() into a
  framework-level error.

**Dispatch ambiguity policy.** Dispatch does NOT probe every
registered importer to detect collisions. The first match wins
and Dispatch returns immediately. Rationale and alternatives are
in "Alternatives discussed". `DiagImporterAmbiguous` is reserved
in the diagnostic code namespace so a future opt-in strict mode
can use it without an ABI bump.

##### `Apply` convenience

```go
func Apply(ctx context.Context, reg Registry, in Input) (Output, error)
```

- Composition of Dispatch + Extract:
  1. Call `Dispatch(ctx, reg, in)`.
  2. If Dispatch returned no importer, return
     `Output{Diagnostics: <dispatch's diagnostics>}, nil`. The
     absence of a matching importer is a ledger-content problem,
     not a framework error.
  3. Otherwise call `imp.Extract(ctx, in)`.
  4. Compose the result: returned Output has Directives from
     Extract and Diagnostics = append(dispatch diagnostics, extract
     diagnostics…) in that order.
  5. If Extract returned a non-nil error, return
     `Output{Diagnostics: <composed so far>}, err`. The error is
     the system-level signal; Diagnostics still reflect the partial
     progress.
- `Apply` always uses the buffered `Extract` path in ABI v1, even
  if the importer satisfies `Streaming`. See the Streaming section
  for the rationale.
- `Apply` does NOT call `Configure` on a `Configurable` importer.
  Configuration is the CLI's / caller's responsibility before
  Apply is invoked; this keeps `Apply` re-entrant on a
  pre-configured importer instance.

##### goplug ABI considerations

- All Contract-level types declared above are declaration-only
  (no methods that would be sensitive to private state): `Input`,
  `Output`, `Registry`, and the diagnostic code constants. The
  `Importer`, `Configurable`, `Streaming`, and `StreamDiagnoser`
  interfaces are pure interfaces. This means a plugin built
  against `pkg/importer` shares the same type identities as the
  host through the standard Go plugin-load mechanism, exactly as
  `pkg/quote`'s ABI does today.
- Optional sub-interfaces (`Configurable`, `Streaming`,
  `StreamDiagnoser`) are the chosen evolution hedge per plan
  decision 8: Phase 8.1/8.2 extensions add new optional
  sub-interfaces rather than modifying `Importer`. The
  diagnostic-code constants are append-only (new codes can be
  added; existing codes never change).
- Once 8a ships, the following changes require a
  `goplug.APIVersion` bump:
  - Adding a method to `Importer`, `Configurable`, `Streaming`,
    or `StreamDiagnoser`.
  - Adding a field to `Input` or `Output` in a position that
    changes a struct's layout assumptions (Go's plugin loader
    is layout-tolerant for struct field additions at the end,
    but the conservative ABI rule is: any field addition is a
    bump).
  - Changing the type of an existing field.
  - Removing or renaming any of the exported symbols above.
- Adding new reserved keys in `Input.Hints`, adding new diagnostic
  code constants, and adding wholly new optional sub-interfaces
  (with disjoint method sets) do NOT require an APIVersion bump.

#### Suggested Internals

These are non-binding; the implementer adopts, modifies, or
replaces them based on what they discover while coding.

##### File decomposition

Plan calls for four files (`importer.go`, `registry.go`,
`dispatch.go`, `apply.go`). Two reasonable variants:

- **Variant A (plan as written):** four files, one per concern.
  Pro: matches the plan precisely; mirrors the de-facto layout
  in `pkg/quote` (which has `registry.go`, `fetch.go`, etc.).
  Con: `apply.go` ends up a 30-line file; the `Apply` function
  is small enough to live next to `Dispatch` in `dispatch.go`.
- **Variant B:** three files: `importer.go` (interfaces + types +
  diagnostic constants), `registry.go` (registry), `dispatch.go`
  (both Dispatch and Apply because they form one pipeline).
  Pro: fewer files; Apply and Dispatch share enough context that
  reading them side by side is convenient.
- **Variant C:** one file `importer.go` until a real reason to
  split appears. Pro: the package is small. Con: mixes
  declaration-only ABI types with state-bearing registry code;
  reviewers asked "what's the goplug ABI surface here?" have to
  scan one big file.

Recommended starting point: Variant B. Easy to split into A
later if `apply.go` grows.

##### Test-helper input construction

The test suite needs to build `Input` values from `(path, body)`
pairs. Two options:

- **Internal helper:** a `newTestInput(t *testing.T, path, body string)
  Input` in `importer_test.go` (or `internal/importertest` for
  shared use by 8d/8e tests). Pro: stays out of the ABI; can
  evolve freely. Con: out-of-tree plugin authors writing their
  own tests have to copy it.
- **Exported `NewInput` constructor:**
  `NewInput(path string, body []byte) Input` that fills Sniff
  (up to 4 KiB), sets an Opener that returns `io.NopCloser(bytes.NewReader(body))`,
  leaves MIME and Hints zero. Pro: also useful for 8f's
  fixture-driven tests and for plugin authors. Con: now part of
  the ABI; freezes the helper's signature.

Recommended: ship the internal helper now (`internal/importertest`
or a `testhelper_test.go`); promote to exported `NewInput` if and
when an out-of-tree consumer asks. The Contract is already
permissive enough that constructing an Input directly is a
one-liner.

##### Registry concurrency placement

Reuse the exact `pkg/quote/registry.go` pattern verbatim:
package-level `sync.RWMutex` plus package-level map, no
`type registry struct{}` wrapper. Reason: identical concurrency
guarantees, identical init-time vs. dynamic-load behaviour, and
reviewers reading both packages get a single mental model.

The `globalRegistry` adapter type is unexported, exactly as in
`pkg/quote`. `GlobalRegistry()` returns it.

##### Dispatch implementation hint

Straightforward `for _, name := range reg.Names() { if imp, ok :=
reg.Lookup(name); ok && imp.Identify(ctx, in) { return imp, true,
nil } }`. The ctx-check between iterations is the only
non-obvious bit; placing it at the top of the loop body is
sufficient because Identify itself is documented to be cheap.

##### Test layout

- `importer_test.go` — table-driven tests for Dispatch ordering
  (multiple fakes; assert sorted-by-name first-match).
- `registry_test.go` — port the structure of
  `pkg/quote/registry_test.go` directly: register/lookup/names,
  duplicate-panics, missing lookup, `GlobalRegistry()` round-trip.
- `concurrency_test.go` — goroutine stress: N concurrent
  Lookups while Register is interleaved (use `-race` to catch
  mutex regressions). The plan calls this out and matching it is
  cheap.
- `apply_test.go` — composition tests using a `fakeImporter` that
  has a switchable Identify-result and Extract-result; assert
  diagnostic composition, error propagation, no-match path.
- Fake importer: small struct
  ```go
  type fakeImporter struct {
      name        string
      identifyFn  func(in Input) bool
      extractFn   func(in Input) (Output, error)
  }
  ```
  declared once in a shared test helper file.

#### Alternatives discussed

##### A1. Dispatch ambiguity: first-match vs. probe-all

- **First-match (recommended, locked above).** Walk
  `Names()` in sorted order; return on the first Identify=true.
  - Pro: O(1) Identify calls in the happy path; importer authors
    only need to make Identify fast for one "did I match"
    decision, not for "am I the unique match".
  - Pro: matches beangulp's behaviour; user expectations carry
    over.
  - Con: silently picks an importer when two could plausibly handle
    the input. Mitigation: `--importer NAME` (an explicit override
    flag landing in 8f) lets users force the choice; the CSV
    importer's TOML `match` regex (8d) lets users disambiguate
    via configuration.
- **Probe-all-and-collect.** Walk every name, count matches; if
  >1, emit `importer-ambiguous` and either fail or pick the first
  anyway.
  - Pro: catches collisions early; user-visible.
  - Con: O(N) Identify calls always, including in the no-conflict
    common case. Locking O(N) into the ABI is a real cost: every
    Identify call must remain cheap forever. Importer authors
    writing slow Identify implementations get punished invisibly.
  - Con: forces a "fail or pick first" policy choice into the
    Contract with no obvious right answer.
  - Con: ambiguity is rare in the canonical pipeline (one CSV
    config per shape, regex `match` already disambiguates).
- **Hybrid: probe-all only under a `--strict` mode.** First-match
  by default, probe-all when the caller opts in.
  - Pro: pays the O(N) cost only when asked.
  - Con: bifurcates the Contract — Dispatch's behaviour now
    depends on a flag. Test surface doubles.

##### A2. `Configure` encoding: decode-callback vs. raw bytes vs. typed `cfg any`

- **`Configure(decode func(dest any) error) error` (recommended, locked).**
  - Pro: encoding-agnostic. The framework does not freeze the
    plugin contract to JSON or any other wire format. The CLI
    side picks the decoder for each invocation (TOML now, JSON
    via `goplug.Load` fixtures, an in-memory map in tests).
  - Pro: plugin authors inherit zero encoding dependency. The
    importer writes `var c MyConfig; decode(&c)` and does not
    import `encoding/json` or any TOML library.
  - Pro: future-proof. Swapping the host decoder (e.g. adding
    YAML support to `cmd/beanimport`) requires no plugin-side
    change and no ABI bump.
  - Con: slightly less obvious for plugin authors than
    `json.Unmarshal(raw, &c)`; the callback indirection is one
    extra level of "what does this argument mean?". Cheap to
    document with a single example in the godoc.
- **`Configure(raw json.RawMessage) error`** (originally proposed,
  now rejected).
  - Pro: simplest signature; `encoding/json` is stdlib.
  - Con: freezes the wire format. The CLI must re-encode
    user-facing TOML to JSON purely for the importer hand-off —
    a wasted transcode whose only justification is interface
    convenience. Importer authors gain an unnecessary dependency
    on `encoding/json`. Rejected after review.
- **`Configure(raw []byte, format string) error`** with format =
  `"toml"` | `"json"`.
  - Pro: importer chooses; CLI doesn't have to transcode.
  - Con: every importer now needs both decoders or it advertises
    a format and refuses the other. The framework now has to know
    about TOML on the ABI surface. Rejected.
- **`Configure(cfg any) error`** with the caller passing a
  pre-decoded struct.
  - Pro: zero-marshal cost at the boundary.
  - Con: requires the importer to expose its config struct type
    to the CLI, which then has to know per-importer types — the
    opposite of a plugin model. Rejected.
- **`Configure(cfg *T) error` where T is the importer's config
  struct.**
  - Pro: the most natural Go shape — typed destination, no
    callback.
  - Con: not expressible in a single `Importer` interface because
    T varies per importer; would require generics on the registry
    (`map[string]Importer[?]`), which Go's type system cannot
    represent without erasure. Rejected on feasibility grounds.
- **`ConfigDest() any` + framework decodes into the returned
  pointer.**
  - Pro: terse for the importer.
  - Con: an `any` *return* crossing the goplug boundary is
    pointer-fragile in a way `any` *parameter* in a callback is
    not — the returned pointer outlives the call and the
    framework has to know how to dispatch on it. The framework
    also has to commit to a specific decoder. Rejected.

##### A3. Streaming vs. Extract precedence

- **Apply always uses Extract in ABI v1 (recommended, locked).**
  - Pro: keeps Apply's behaviour predictable; Streaming is opt-in
    for callers that genuinely need it.
  - Con: Streaming importers do extra work for nothing under the
    default CLI.
- **Apply prefers Streaming when available, materialises into a
  slice.**
  - Pro: importers see a single calling pattern.
  - Con: forces Streaming importers to also emit Diagnostics
    through the StreamDiagnostics back-channel even when the
    caller would have happily called Extract. More moving parts
    for no observable gain.
- **Make Streaming the only path; remove Extract.**
  - Pro: one method on Importer.
  - Con: Identify+Extract is beangulp's mental model and is
    simpler for one-shot importers; forcing every importer to
    write an iterator is gratuitous.

##### A4. Diagnostic codes: package constants vs. `pkg/ast` constants

- **Package constants in `pkg/importer` (recommended, locked).**
  - Pro: framework-specific codes live with the framework; new
    codes do not touch `pkg/ast`.
  - Con: callers grepping `pkg/ast` for "all known diagnostic
    codes" miss them.
- **Constants in `pkg/ast/diagnostic.go` (a file that does not yet
  exist; codes currently live as string literals at emit sites).**
  - Pro: single inventory of codes.
  - Con: `pkg/ast` is the AST package; flooding it with
    framework-namespaced codes blurs the boundary. Existing
    `pkg/ext/postproc/apply.go` uses an inline string literal
    `"plugin-not-registered"` rather than a constant, so the
    project has no convention to follow here; defining constants
    in the owning package is consistent with the broader Go style.

##### A5. `Input.Opener` shape: closure vs. `io.ReaderAt` vs. `*os.File`

- **`func() (io.ReadCloser, error)` (recommended, locked).**
  - Pro: works for stdin (a closure that returns a wrapper around
    `os.Stdin` once), for regular files (returns a fresh `os.Open`
    each call), and for in-memory test fixtures
    (`bytes.NewReader` + `io.NopCloser`). Identify can re-open
    cheaply for the rare case it needs to.
  - Con: callers that hold a real file must remember each Opener
    call is a fresh open (cost is negligible for the import use
    case).
- **`io.ReaderAt`.**
  - Pro: explicit "you can seek to any offset" semantic.
  - Con: doesn't work for stdin or pipes. Locks out the common
    `beanimport <(curl …)` invocation pattern the plan mentions.
- **`*os.File`.**
  - Pro: simplest signature.
  - Con: only works for on-disk files; same stdin problem as
    `io.ReaderAt`.

##### A6. `Registry` interface scope: Lookup-only vs. Lookup+Names

- **Lookup + Names (recommended, locked).** Dispatch needs ordered
  iteration; making it accept a `Registry` for testability means
  the interface has to expose iteration. `pkg/quote`'s registry
  exposes only Lookup because pkg/quote's `Fetch` works from a
  caller-supplied request list rather than registry iteration.
- **Lookup-only.** Dispatch would have to accept the package-level
  `Names()` directly, which makes Dispatch un-testable against a
  fake registry without monkey-patching. Rejected.

#### Recommendation

Adopt the Contract as locked in the section above. The four
non-trivial decisions resolved as follows:

1. **Dispatch returns on first match; no probe-all.** O(1)
   Identify-call budget is the right ABI guarantee; users who need
   determinism in collision cases have `--importer NAME` and the
   per-importer config-file disambiguation (TOML `match` regex
   in 8d). `DiagImporterAmbiguous` is reserved in the diagnostic
   namespace so a future opt-in strict mode can use it without an
   ABI bump.

2. **`Configure(decode func(dest any) error) error`, decoder
   chosen at the CLI edge.** The framework is encoding-agnostic:
   the CLI / harness picks the decoder per invocation and the
   plugin author writes a small fixed pattern around a typed
   destination. JSON-via-`json.RawMessage` was originally proposed
   and was reverted after review — locking the wire format gave
   no payoff and added an `encoding/json` dependency to every
   plugin.

3. **`Apply` uses `Extract`, never `StreamExtract`, in ABI v1.**
   Streaming exists for future callers (very large file handling,
   downstream pipelining) and is correctly available as an opt-in
   capability via a type assertion on `Streaming`. Forcing Apply
   to prefer it would make StreamDiagnostics mandatory for every
   Streaming importer with no current consumer benefit.

4. **Registry interface exposes Names.** Dispatch needs ordered
   iteration, and the test path needs a fake Registry. Diverging
   from `pkg/quote`'s Lookup-only Registry is justified by
   Dispatch's iteration requirement.

The Suggested Internals (file decomposition, test-helper shape,
exact internal mutex layout) are advisory. The implementer is
free to pick Variant B or any other arrangement that hits the
Contract above.

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
