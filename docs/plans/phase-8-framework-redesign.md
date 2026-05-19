# Phase 8 Framework Redesign — Multi-Instance Importers and Hooks

## Context

PR #83 merged 8a–8e (importer framework + csvimp + classify) on the
implicit assumption that each registered importer/hook is a
**process-global singleton** identified by a fixed name. Review surfaced
that this assumption is wrong: a real CLI invocation may need multiple
instances of the same kind — e.g. a `csv` importer configured for a BOA
checking account and another configured for an Amex credit card, both
in the same `beanimport` run.

The required model:

- One kind (e.g. `csv`) → many instances (e.g. `boa_checking`,
  `amex_credit`).
- Each instance is created with its own configuration from a `[[importer]]`
  or `[[hook]]` TOML entry.
- Configure→Apply order is enforced by construction: an instance cannot
  exist in an unconfigured state.
- Different instances may be created concurrently (no contention).
- A single instance may receive **concurrent Apply calls** (CLI may
  import multiple files in parallel using the same shape).
- Once an instance is created, its internal state is read-only.

This invalidates the singleton assumption baked into 8a–8e and forces a
framework redesign before 8f (CLI) can be implemented sensibly. The
work is split into two PRs to keep diffs reviewable.

## Goal

Replace the singleton-per-kind registry with a factory-per-kind +
instance-registry model. Fuse Configure into instance creation so the
"unconfigured instance" state is unrepresentable. Drop defensive
mutexes in favour of "constructor-then-frozen" immutability.

## Scope

**In scope:**

- `pkg/importer`: API redesign (factory registration, instance
  registry, fused Configure).
- `pkg/importer/hook`: same redesign mirrored.
- `pkg/importer/std/csvimp`: rewrite to factory; 1 instance = 1 shape;
  TOML schema reshape per B2.
- `pkg/importer/hook/std/classify`: rewrite to factory; drop mutex;
  per-instance rule list.
- `pkg/importer/importerutil`: untouched (pure helpers; no registry
  coupling).
- `cmd/beanimport`: new CLI on the redesigned framework.
- Test coverage: concurrent Apply on same instance (race-tested);
  parallel Configure on different instances; multi-instance TOML
  examples.
- Plan documentation in `docs/plans/phase-8-importer-framework.md`
  (updated to record the new Contracts).

**Out of scope:**

- `Configurable` sub-interface remains removed (configuration fused
  into factory).
- `Streaming` sub-interface: unchanged.
- 8g (goplug fixtures + PLAN.md rewrite).
- Phase 8.1 (ML hooks), Phase 8.2 (XML/OFX/QIF importers).
- Cross-source dedup (still `pkg/distribute`'s concern).

## Architecture

### Factory pattern

The user's adopted shape (論点 A2 with Configure fused, interface +
function-helper pair per `http.Handler` / `http.HandlerFunc`):

```go
// pkg/importer

// Factory creates a configured Importer instance. The factory call IS
// the Configure step — there is no separately exposed Configure on
// Importer. An error return aborts creation; no half-configured
// Importer is ever observable.
type Factory interface {
    New(name string, decode func(dest any) error) (Importer, error)
}

// FactoryFunc adapts a plain function to the Factory interface, mirroring
// the http.Handler / http.HandlerFunc convention. Importers without
// per-factory state (the common case) register via FactoryFunc.
type FactoryFunc func(name string, decode func(dest any) error) (Importer, error)

func (f FactoryFunc) New(name string, decode func(dest any) error) (Importer, error) {
    return f(name, decode)
}

func RegisterFactory(kind string, f Factory)
func LookupFactory(kind string) (Factory, bool)
func KindNames() []string

type Importer interface {
    Name() string                                              // instance name (e.g. "boa_checking")
    Identify(ctx context.Context, in Input) bool
    Extract(ctx context.Context, in Input) (Output, error)
}

// per-run registry of configured instances
type Registry interface {
    Lookup(name string) (Importer, bool)
    Names() []string
}

func Dispatch(ctx context.Context, reg Registry, in Input) (Importer, bool, []ast.Diagnostic)
func Apply(ctx context.Context, reg Registry, in Input) (Output, error)
```

Rationale for the interface + FactoryFunc pair (over a bare function
type): a Factory that holds shared resources (e.g. a connection pool
shared across instances of one kind, or a `prometheus.Counter` for
telemetry) can express itself as a struct that implements the
interface. Plain stateless factories just use `FactoryFunc(myFunc)`
at registration. The mirror of `pkg/importer/hook` follows the same
pattern.

The `Configurable` sub-interface from 8a is **removed**. Static
importers that need no configuration simply ignore the `decode`
parameter inside their factory function.

### Registry split

- **Global kind registry** (factories only): populated at `init()` /
  goplug `InitPlugin`. Holds `Factory` values keyed by kind.
- **Per-run instance registry**: built by the CLI from TOML. Holds
  configured `Importer` values keyed by instance name. This is what
  `Dispatch` walks.

The same split mirrors `pkg/importer/hook`.

### Concurrency contract (per 論点)

- **Configure** (factory call): runs once per instance, single-threaded
  from the factory's perspective. Multiple instances may be created in
  parallel because each factory call is independent.
- **Apply**: may be called concurrently on the same instance. The
  instance's state after factory return is immutable.

This eliminates the need for mutexes in importers/hooks. csvimp's
`identifyCache` is removed (it was a write-write race under concurrent
Apply); classify's `sync.Mutex` is removed.

### TOML schema (論点 B2)

Flat list of `[[importer]]` and `[[hook]]` entries:

```toml
[[importer]]
kind = "csv"
name = "boa_checking"
# csvimp-specific keys directly here:
delimiter        = ","
skip_lines       = 1
date_col         = "Date"
date_format      = "2006-01-02"
account          = "Assets:BOA:Checking"
narration_cols   = ["Description", "Memo"]
default_currency = "USD"

  [[importer.amount]]
  col    = "Withdrawal"
  negate = true

  [[importer.amount]]
  col    = "Deposit"

[[importer]]
kind = "csv"
name = "amex_credit"
# different csv configuration ...

[[hook]]
kind = "classify"
name = "default"

  [[hook.rule]]
  payee_regex = "(?i)acme"
  account     = "Expenses:Office"
```

- Each entry's body (everything except `kind` and `name`) is parsed by
  the importer/hook factory via its `decode` callback.
- `kind` and `name` are CLI-owned reserved keys; the factory MUST NOT
  read them from its decode (they're stripped before the decode
  closure is invoked).
- Instance order is declaration order in the TOML file; `Dispatch`
  walks instances in that order.

## Steps

This redesign is split into two PRs to keep diffs reviewable. The
step list below is the PR-α decomposition (this orchestration run).
PR-β is a separate orchestration run that follows after PR-α merges.

### Step α-1 — Redesign `pkg/importer` API

**Functional requirements:**

- Define `Factory` interface and `FactoryFunc` helper per `Architecture`.
- Split registry: global kind registry (`RegisterFactory` /
  `LookupFactory` / `KindNames`) and per-run instance registry
  (`Registry` interface + a default in-memory implementation).
- `Dispatch` / `Apply` operate on the instance `Registry` (semantics
  shift: registry now holds `Importer` instances keyed by instance
  name, not factories keyed by kind).
- Remove `Configurable` sub-interface.
- `Importer.Name()` is documented as returning the instance name.

**Modules:** `pkg/importer/importer.go`, `pkg/importer/registry.go`,
`pkg/importer/dispatch.go` (and tests).

**Verification:**

- `bazel run //:gazelle`
- `bazel build //pkg/importer/...`
- `bazel test //pkg/importer/... --test_output=errors`
- New unit tests:
  - Parallel factory calls produce independent instances (no shared
    state contamination).
  - Concurrent `Apply` on the same `Importer` from `Dispatch` is
    race-clean (`-race`).
  - Factory error propagation: decode error and validation error.

**Quality requirements:** exported symbols documented per project
`CLAUDE.md` (contract-style godoc); concurrency guarantees stated on
`Factory`, `Registry`, `Importer`.

### Step α-2 — Mirror redesign in `pkg/importer/hook`

**Functional requirements:**

- Mirror `Factory` / `FactoryFunc`, kind registry, and instance
  `Registry` for hooks.
- `Chain` (or equivalent driver) walks the hook instance registry in
  declaration order.
- Remove the hook `Configurable` sub-interface.

**Modules:** `pkg/importer/hook/hook.go`, `pkg/importer/hook/registry.go`,
`pkg/importer/hook/chain.go` (and tests).

**Verification:**

- `bazel test //pkg/importer/hook/... --test_output=errors`
- Concurrent `Apply` on a hook instance is race-clean.
- Parallel factory calls produce independent hook instances.

**Quality requirements:** same as α-1.

### Step α-3 — Migrate `pkg/importer/std/csvimp` to factory (mechanical)

**Functional requirements:**

- Replace singleton `Importer` with a struct holding a single `shape`
  + `name` field (no `mu`, no `shapes` map, no `identifyCache`).
- `Name()` returns the instance name.
- Move all `Configure` logic into a top-level factory function with
  signature `func(name string, decode func(dest any) error) (importer.Importer, error)`.
- `init()` registers the factory under kind `"csv"`.
- **PR-α schema compatibility:** the factory accepts the existing
  `[shape.<name>]` schema (one TOML entry → one `Importer` per
  `shape.<name>` map entry under the same factory call would break the
  one-instance-per-factory-call invariant; instead, PR-α keeps the
  schema but each `[shape.<name>]` becomes a separate instance
  registered by the CLI in a follow-up). For PR-α the test driver
  builds one instance per shape via the factory; user-facing TOML
  reshape is PR-β's job.
- Concurrent-Apply test added (`-race`).
- Existing tests pass after migration.

**Modules:** `pkg/importer/std/csvimp/csvimp.go`,
`pkg/importer/std/csvimp/config.go`,
`pkg/importer/std/csvimp/extract.go`,
`pkg/importer/std/csvimp/rowhash.go`,
`pkg/importer/std/csvimp/doc.go` (and tests).

**Verification:**

- `bazel test //pkg/importer/std/csvimp/... --test_output=errors`
- New concurrent-Apply test with `-race`.

**Quality requirements:** exported godoc; the contract that
post-factory state is immutable is documented on the struct.

### Step α-4 — Migrate `pkg/importer/hook/std/classify` to factory (mechanical)

**Functional requirements:**

- Replace singleton `Hook` with a struct holding the per-instance rule
  list (no `mu`).
- `Name()` returns the instance name.
- Move all `Configure` logic into a factory function (signature
  mirrors hook factory shape).
- `init()` registers the factory under kind `"classify"`.
- Concurrent-Apply test added (`-race`).
- Existing tests pass after migration.

**Modules:** `pkg/importer/hook/std/classify/classify.go`,
`pkg/importer/hook/std/classify/config.go`,
`pkg/importer/hook/std/classify/doc.go` (and tests).

**Verification:**

- `bazel test //pkg/importer/hook/std/classify/... --test_output=errors`
- New concurrent-Apply test with `-race`.

**Quality requirements:** exported godoc.

### Step α-5 — Append redesign subsections to `docs/plans/phase-8-importer-framework.md`

**Functional requirements:**

- Append `8a-redesign` and `8b-redesign` subsections to the existing
  plan file. Do **not** delete the original 8a/8b Detailed Designs —
  they remain as historical record.
- Each subsection records the new Contract (factory, instance
  registry, fused Configure, concurrency contract).

**Modules:** `docs/plans/phase-8-importer-framework.md`.

**Verification:** plan file diff is reviewed for completeness.

**Quality requirements:** matches the existing plan document's
style and section nesting.

## Alternatives discussed

- **A1: Configurable sub-interface preserved.** Rejected — leaves
  "unconfigured instance" as a representable state, defeating the
  invariant.
- **A2 with bare `Factory` function type.** Considered. Rejected in
  favour of `Factory` interface + `FactoryFunc` helper so factories
  that hold shared resources (connection pool, telemetry counter) can
  express themselves as structs without a wrapper layer.
- **B1: Nested TOML schema `[importer.csv.boa_checking]`.** Rejected
  in favour of flat `[[importer]]` array (B2) for two reasons: (1)
  TOML array-of-tables gives natural declaration order, which
  `Dispatch` needs as a contract; (2) keeps `kind` and `name` as
  explicit reserved keys rather than positional path segments.

## Recommended approach

Adopt A2-with-interface (Factory interface + FactoryFunc helper) +
B2 (flat array). PR-α lands the framework redesign and mechanical
migration of csvimp and classify, preserving the externally
visible TOML schema. PR-β reshapes the schema and adds
`cmd/beanimport`.

## Concurrency contract reference

- **Configure** (factory call): single-threaded per instance; parallel
  across instances. No mutex needed inside a factory.
- **Apply** (`Identify` / `Extract` / hook `Apply`): may be invoked
  concurrently on the same instance. Instance state is immutable
  post-factory.

This eliminates `csvimp.identifyCache` (write-write race risk) and
`classify.mu` (no longer needed).

## Critical files (paths)

- `pkg/importer/importer.go`
- `pkg/importer/registry.go`
- `pkg/importer/dispatch.go`
- `pkg/importer/hook/hook.go`
- `pkg/importer/hook/registry.go`
- `pkg/importer/hook/chain.go`
- `pkg/importer/std/csvimp/csvimp.go`
- `pkg/importer/std/csvimp/config.go`
- `pkg/importer/std/csvimp/rowhash.go`
- `pkg/importer/std/csvimp/extract.go`
- `pkg/importer/std/csvimp/doc.go`
- `pkg/importer/hook/std/classify/classify.go`
- `pkg/importer/hook/std/classify/config.go`
- `pkg/importer/hook/std/classify/doc.go`
- `docs/plans/phase-8-importer-framework.md`

## Reused existing functions and utilities

- `pkg/importer/importerutil.BalanceWith` and `StampMetadata` —
  untouched; still consumed by classify and csvimp respectively.
- `pkg/ast/clone.go` — directive cloning, used by importerutil.
- `pkg/ext/goplug.Load` — plugin loader for `--plugin` (PR-β only).
- `pkg/printer.Fprint` — stdout output path (PR-β only).
- `pkg/quote/registry.go` — concurrency-pattern reference for the
  kind registry's `sync.RWMutex`.

## Branch and PR plan

- Develop on `claude/phase-8-orchestration-redesign-8PhfV`.
- PR-α opens after all α-N steps converge and commit.
- PR-β (schema reshape + `cmd/beanimport`) is a separate
  orchestration run started after PR-α merges.
