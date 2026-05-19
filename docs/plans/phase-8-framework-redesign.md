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
- `bazel build //pkg/importer:importer`
- `bazel test //pkg/importer:importer_test --test_output=errors`
- New unit tests:
  - Parallel factory calls produce independent instances (no shared
    state contamination).
  - Concurrent `Apply` on the same `Importer` from `Dispatch` is
    race-clean (`-race`).
  - Factory error propagation: decode error and validation error.

> **Step-level scope.** This step's verification is intentionally
> narrowed to the `pkg/importer` package itself. Removing the legacy
> `Register` / `Lookup` / `GlobalRegistry` symbols breaks
> `pkg/importer/std/csvimp` and `pkg/importer/hook/...` at compile
> time; those packages are migrated to the new API in Steps α-2
> through α-4. The PR-α-level invariant — `bazel build //...` and
> `bazel test //...` are both green — converges only after Step α-4.
> Intermediate per-step states between α-1 and α-4 are expected to
> leave one or both downstream packages non-buildable; this is
> tracked at the PR-α level, not the step level.

**Quality requirements:** exported symbols documented per project
`CLAUDE.md` (contract-style godoc); concurrency guarantees stated on
`Factory`, `Registry`, `Importer`.

### Detailed Design

#### Contract

All symbols below are exported from `pkg/importer` unless marked unexported.
Godoc shown is the binding external contract; generators must reproduce its
substance.

##### `Importer`

```go
// Importer is a fully-configured import driver for one declared instance
// (e.g. "boa_checking"). An Importer is produced by a Factory; its
// internal state is frozen at that point and Identify/Extract are safe
// for concurrent invocation on the same value.
type Importer interface {
    // Name returns the instance name supplied to the Factory that
    // produced this Importer. The value is stable for the lifetime of
    // the instance and is the key under which a Registry holds it.
    Name() string

    // Identify is a cheap, side-effect-free check. It MUST NOT consume
    // in.Opener unless Path/MIME/Sniff are insufficient; if it does, it
    // MUST close the returned io.ReadCloser before returning. A true
    // result is a non-binding preference; Dispatch picks the first
    // match in Registry.Names() order. Identify reports no error: a
    // failure to identify is simply false.
    Identify(ctx context.Context, in Input) bool

    // Extract returns directives in source-encounter order plus
    // per-record diagnostics. A non-nil error is reserved for
    // system-level failures (I/O, ctx cancellation, structural format
    // corruption); ledger-content problems are Diagnostics, not errors.
    // Context cancellation MUST surface as a non-nil error.
    Extract(ctx context.Context, in Input) (Output, error)
}
```

`Identify` keeps its existing `bool`-only signature (the baseline does not
return error and there is no reason to add one).

`Input` and `Output` are unchanged from PR #83 (`Input` keeps Path, Opener,
Sniff, MIME, Hints; `Output` keeps Directives, Diagnostics).

##### `Factory` and `FactoryFunc`

```go
// Factory produces a single fully-configured Importer instance. The
// New call IS the Configure step: there is no separately exposed
// Configure method on Importer. A non-nil error aborts creation and
// MUST be returned without a partially-constructed Importer leaking
// out; on error the first return MUST be nil.
//
// The decode callback decodes the caller's per-instance configuration
// (the TOML table body, with the reserved keys "kind" and "name"
// stripped) into a destination the factory supplies. It MUST NOT be
// nil; factories that take no configuration may simply ignore it.
//
// Factory.New is called at most once per (name, decode) pair by the
// caller building a Registry. Multiple New calls for distinct
// instances of the same kind MAY run concurrently; a Factory that
// holds shared state across calls is responsible for its own
// synchronisation.
type Factory interface {
    New(name string, decode func(dest any) error) (Importer, error)
}

// FactoryFunc adapts a plain function to Factory, mirroring
// http.Handler / http.HandlerFunc. Stateless factory functions
// register via FactoryFunc(myFactoryFn).
type FactoryFunc func(name string, decode func(dest any) error) (Importer, error)

func (f FactoryFunc) New(name string, decode func(dest any) error) (Importer, error) {
    return f(name, decode)
}
```

Contractual notes the generator MUST honour:

- A `Factory` MUST NOT return `(nil, nil)`. Callers building a Registry
  MUST treat such a return as a programming error and refuse to insert
  the entry (the recommended `MapRegistry` constructor enforces this).
- The `name` parameter is the instance name supplied by the caller
  (typically the `name = "..."` TOML key). The factory MUST set its
  returned Importer's `Name()` to this exact string.

##### Kind registry

```go
// RegisterFactory installs f under the given kind in the package-global
// kind registry. It panics if a Factory has already been registered
// under the same kind, mirroring the pattern in pkg/quote.Register.
// Intended to be called from an init() function (in-tree kinds) or
// from a goplug InitPlugin callback (plugin kinds). Safe for
// concurrent use; reads (LookupFactory, KindNames) MAY run
// concurrently with RegisterFactory, though in practice all
// registrations land before reads begin.
func RegisterFactory(kind string, f Factory)

// LookupFactory returns the Factory registered for kind. The second
// return value is false if no such kind is registered.
func LookupFactory(kind string) (Factory, bool)

// KindNames returns the registered kinds sorted in ascending order so
// that diagnostics and tests have deterministic output.
func KindNames() []string
```

These three symbols **replace** the previous `Register` / `Lookup` /
`Names` / `GlobalRegistry` from `pkg/importer/registry.go`. The old names
are deleted; there is no compatibility shim.

##### Instance registry

```go
// Registry is the per-run lookup of fully-configured Importer
// instances. The CLI builds one Registry per beanimport invocation
// from the [[importer]] entries in TOML and hands it to Dispatch/Apply.
//
// Names returns instance names in the order Dispatch must walk them.
// In ABI v1 this is declaration order (the order the CLI handed the
// instances to the constructor); implementations MUST preserve a
// stable, deterministic order across repeated calls on the same
// Registry value.
//
// All Registry methods are safe for concurrent use. A Registry's
// contents are immutable after construction.
type Registry interface {
    Lookup(name string) (Importer, bool)
    Names() []string
}

// MapRegistry is the default in-memory Registry implementation. Build
// one with NewRegistry; the zero value is not usable.
type MapRegistry struct { /* unexported fields */ }

func (r *MapRegistry) Lookup(name string) (Importer, bool)
func (r *MapRegistry) Names() []string

// NewRegistry returns a Registry populated with the given Importers in
// the order supplied; that order is the Dispatch walk order. NewRegistry
// returns an error if any Importer is nil, if two Importers share the
// same Name(), or if any Name() is the empty string.
func NewRegistry(imps []Importer) (*MapRegistry, error)
```

There is no `GlobalRegistry()` accessor any more. An instance registry
is constructed per run; tests construct their own.

##### `Dispatch` and `Apply`

```go
// Dispatch walks reg.Names() in the registry's declared order and
// returns the first Importer whose Identify returns true. Between
// calls it checks ctx.Err(); on cancellation it returns
// (nil, false, nil) and the caller converts ctx.Err() into an error.
//
// When no instance matches, Dispatch returns (nil, false, diags) where
// diags carries a single Error diagnostic with Code DiagImporterNone
// and Span.Start.Filename = in.Path.
func Dispatch(ctx context.Context, reg Registry, in Input) (Importer, bool, []ast.Diagnostic)

// Apply dispatches in against reg and runs Extract on the chosen
// instance. Diagnostics from Dispatch and Extract are concatenated in
// that order; if both sides produce none, Output.Diagnostics is nil.
// Apply returns (Output{Diagnostics: dispatchDiags}, nil) when no
// instance matches — the absence of a matching importer is a
// ledger-content problem, not a framework error. On ctx cancellation
// Apply returns (Output{}, ctx.Err()).
func Apply(ctx context.Context, reg Registry, in Input) (Output, error)
```

The semantics are byte-identical to the PR #83 implementations except
that the `Registry` they walk now holds `Importer` instances keyed by
instance name. No behaviour change is required in the bodies; only the
type that satisfies `Registry` differs.

##### Diagnostic codes

`DiagImporterNone` and `DiagImporterAmbiguous` remain.
`DiagImporterNotRegistered` is **kept** as an exported constant — it is
part of the published surface even though `Apply` itself does not emit
it in ABI v1 (the CLI may emit it when the user `--force`s an unknown
instance name). The godoc updates its description to refer to instance
names rather than registered importers.

##### `Configurable` removal

`Configurable` is deleted. The generator MUST:

- Remove the interface declaration from `importer.go`.
- Remove all `configurableImporter` fixtures and the
  `TestApply_DoesNotCallConfigure` /
  `TestOptionalInterface_ConfigurableAssertion` tests from `apply_test.go`
  and `fake_test.go`. (These two tests vanish; there is no replacement,
  because the property they checked — "no Configure call leaks" — is
  now true by construction.)

`Streaming` and `StreamDiagnoser` are unchanged.

##### Concurrency contract (per symbol)

| Symbol                 | Concurrency contract                                                          |
| ---------------------- | ----------------------------------------------------------------------------- |
| `RegisterFactory`      | Safe for concurrent use; intended init-time. Panics on duplicate kind.        |
| `LookupFactory`, `KindNames` | Safe for concurrent read; may race with RegisterFactory cleanly.        |
| `Factory.New`          | Caller calls once per instance. Distinct New calls (different instances) MAY run concurrently. A Factory implementation that shares state across calls owns its synchronisation. |
| `Registry.Lookup`, `Registry.Names` | Safe for concurrent read; contents immutable post-construction.    |
| `Importer.Identify`, `Importer.Extract` | Safe for concurrent invocation on the same value. State is frozen at factory return. |

##### Test adaptation (binding)

The generator MUST update the existing tests as follows:

- `registry_test.go` → `kind_registry_test.go` (rename). Tests cover
  `RegisterFactory` / `LookupFactory` / `KindNames`:
  round-trip, duplicate panic, missing lookup, sorted-order. The
  `GlobalRegistry` test is deleted (no such symbol any more).
- `dispatch_test.go` keeps every test; `fakeRegistry` continues to
  satisfy the new `Registry` interface unchanged.
- `apply_test.go` keeps every test except `TestApply_DoesNotCallConfigure`
  and `TestOptionalInterface_ConfigurableAssertion`, which are deleted.
- `fake_test.go`: the `configurableImporter` fixture and the
  `withCleanRegistry` helper are deleted. `withCleanRegistry` is no
  longer needed because tests now build local `MapRegistry` values.
- `concurrency_test.go` → splits into two:
  - One concurrent test on the **kind registry** (parallel
    `RegisterFactory` / `LookupFactory` / `KindNames`), mirroring the
    existing structure.
  - One new test that builds a single `Importer` via a factory and
    invokes `Identify` / `Extract` from N goroutines under `-race` to
    pin the frozen-state contract.
- Two new factory-focused tests (new file, e.g. `factory_test.go`):
  - Parallel factory calls with different `name`s produce independent
    `Importer` instances.
  - Factory error propagation: a factory whose `decode` callback errors,
    and a factory whose own validation errors, both surface to the
    caller of `NewRegistry`.

#### Suggested Internals

These are recommendations. The implementer may adopt, modify, or
replace them based on what they discover while coding.

**Kind registry storage.** Recommend mirroring `pkg/quote/registry.go`
verbatim: package-level `var kindMu sync.RWMutex; var kinds = map[string]Factory{}`
guarded by `kindMu`. Same lock discipline (write lock for register,
read lock for lookup/names). Alternative (unguarded map relying on
init-time-only writes) rejected for consistency with `pkg/quote` and
to accommodate goplug `InitPlugin` callbacks that may fire after
`init()`.

**Default instance registry shape.** Recommend a concrete exported
`*MapRegistry` returned by `NewRegistry([]Importer) (*MapRegistry, error)`,
holding a `map[string]Importer` and a `[]string` of names in
declaration order. Both fields filled once at construction and never
mutated, so no mutex is needed.

  Alternative A: a builder pattern (`r.Add(imp)`). Rejected — it
  re-introduces a mutable phase and tempts a "register after Dispatch
  starts" race. The slice-in constructor makes the
  immutable-after-construction contract structural.

  Alternative B: have `NewRegistry` return a `Registry` interface
  instead of `*MapRegistry`. Acceptable; pick whichever reads cleaner.

  Alternative C: closure-based implementation of `Registry`. Rejected
  on debuggability.

**Dispatch / Apply sharing.** Keep the current shape: `Apply` calls
`Dispatch` then `Extract`, concatenating diagnostics. PR #83's body
is already minimal; do not extract a helper.

**Where the no-match Diagnostic is built.** Keep inline in `Dispatch`.

**Empty-slice vs nil for `Diagnostics`.** Preserve the PR #83
invariant: `Apply` returns `nil` when both Dispatch and Extract
diagnostics are empty. `TestApply_EmptyOutputHasNilDiagnostics` is
kept and continues to pin this.

**Where `FactoryFunc` lives.** Same file as `Factory` (importer.go is
fine; or a new `factory.go` if the file is getting large).

#### Alternatives

- **Duplicate-kind registration: panic vs return error.** Recommend
  **panic**, matching `pkg/quote/registry.go`. Registration is
  init-time and cannot meaningfully recover; an error return would be
  silently ignored at every realistic call site.

- **Registry constructor name: `NewRegistry` vs `NewMapRegistry`.**
  Recommend `NewRegistry` returning `*MapRegistry`. `NewMapRegistry`
  reads as if there were other registry constructors to disambiguate
  from; there aren't.

- **`MapRegistry` exported vs unexported.** Recommend exported.
  Exporting the concrete type lets tests assert on it and lets
  advanced callers reach for it directly. The interface is still the
  recommended consumer contract.

- **Keep `DiagImporterNotRegistered` or delete it.** Recommend keep.
  PR-β's CLI emits it when `--force <name>` references a missing
  instance; deleting and re-adding across PRs churns the ABI for no
  reason.

- **Should `Identify` gain an `error` return?** No — the plan and the
  existing code both keep `bool` only.

#### Recommendation

Adopt the contract above verbatim: `Factory` interface +
`FactoryFunc` adapter; kind registry (`RegisterFactory`,
`LookupFactory`, `KindNames`) with `sync.RWMutex` mirroring
`pkg/quote`; per-run instance `Registry` interface plus a concrete
`*MapRegistry` built by `NewRegistry([]Importer)`; `Dispatch` and
`Apply` unchanged in body, only retyped over the new `Registry`.
Delete `Configurable` and the two tests that exercised it; rename
`registry_test.go` to `kind_registry_test.go`; split
`concurrency_test.go` into a kind-registry concurrency test and a
new frozen-instance concurrent-Apply test; add a factory-focused
test file covering parallel construction and error propagation.

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

- `bazel build //pkg/importer/hook:hook`
- `bazel test //pkg/importer/hook:hook_test --test_output=errors`
- Concurrent `Apply` on a hook instance is race-clean.
- Parallel factory calls produce independent hook instances.

> **Step-level scope.** Mirrors α-1's step-scope rule. After α-2,
> `pkg/importer/hook/std/classify` will still reference the old
> hook API and fail to build; classify is migrated in Step α-4.
> The PR-α-level wildcard `bazel build //...` invariant converges
> after α-4.

**Quality requirements:** same as α-1.

### Detailed Design

#### Contract

All symbols below are exported from `pkg/importer/hook` unless marked
unexported. The contract mirrors `pkg/importer` (Step α-1) verbatim
except where hook-specific behaviour diverges; divergences are called
out inline.

##### `Hook`

```go
// Hook transforms a directive list produced by an importer or a prior
// rung of Chain. A Hook is produced by a Factory; its internal state
// is frozen at that point and Apply is safe for concurrent invocation
// on the same value.
type Hook interface {
    // Name returns the instance name supplied to the Factory that
    // produced this Hook. The value is stable for the lifetime of the
    // instance and is the key under which a Registry holds it.
    Name() string

    // Apply transforms in.Directives and returns the new list plus any
    // per-directive Diagnostics. A non-nil error is reserved for
    // system-level failures (ctx cancellation, I/O, programmer error);
    // ledger-content problems are Diagnostics. Apply MUST NOT mutate
    // in.Directives, in.Hints, or in.Options. Context cancellation
    // MUST surface as a non-nil error.
    Apply(ctx context.Context, in HookInput) (HookResult, error)
}
```

`HookInput` and `HookResult` are unchanged from PR #83.

##### `Factory` and `FactoryFunc`

```go
// Factory produces a single fully-configured Hook instance. The New
// call IS the Configure step; there is no separately exposed Configure
// method on Hook. A non-nil error aborts creation and MUST be returned
// without a partially-constructed Hook leaking out; on error the first
// return MUST be nil.
//
// The decode callback decodes the caller's per-instance configuration
// (the TOML table body, with reserved keys "kind" and "name" stripped)
// into a destination the factory supplies. It MUST NOT be nil;
// factories that take no configuration may ignore it.
//
// Factory.New is called at most once per (name, decode) pair by the
// caller building a Registry. Multiple New calls for distinct
// instances of the same kind MAY run concurrently; a Factory holding
// shared state across calls is responsible for its own synchronisation.
type Factory interface {
    New(name string, decode func(dest any) error) (Hook, error)
}

// FactoryFunc adapts a function to the Factory interface, analogous to
// http.HandlerFunc.
type FactoryFunc func(name string, decode func(dest any) error) (Hook, error)

func (f FactoryFunc) New(name string, decode func(dest any) error) (Hook, error) {
    return f(name, decode)
}
```

Contractual notes the generator MUST honour:

- A `Factory` MUST NOT return `(nil, nil)`. Callers building a Registry
  MUST treat such a return as a programming error and refuse to insert
  the entry (the `MapRegistry` constructor enforces this).
- The `name` parameter is the instance name supplied by the caller
  (typically the `name = "..."` TOML key). The factory MUST set its
  returned Hook's `Name()` to this exact string.

##### Kind registry

```go
// RegisterFactory installs f under the given kind in the package-global
// kind registry. Panics if a Factory has already been registered under
// the same kind. Intended to be called from an init() function or a
// goplug InitPlugin callback. Safe for concurrent use; reads
// (LookupFactory, KindNames) MAY run concurrently with RegisterFactory.
func RegisterFactory(kind string, f Factory)

// LookupFactory returns the Factory registered for kind. The second
// return value is false if no such kind is registered.
func LookupFactory(kind string) (Factory, bool)

// KindNames returns the registered kinds sorted in ascending order so
// that diagnostics and tests have deterministic output.
func KindNames() []string
```

These symbols **replace** `Register` / `Lookup` / `Names` /
`GlobalRegistry` in `pkg/importer/hook/registry.go`. The old names are
deleted; there is no compatibility shim.

##### Instance registry

```go
// Registry is the per-run lookup of fully-configured Hook instances.
// The CLI builds one Registry per beanimport invocation from the
// [[hook]] entries in TOML and hands it to Chain.
//
// Names returns instance names in the order Chain walks them: the
// order they were supplied to NewRegistry (declaration order in
// TOML). Implementations MUST preserve a stable, deterministic order
// across repeated calls on the same Registry value.
//
// All Registry methods are safe for concurrent use. A Registry's
// contents are immutable after construction.
type Registry interface {
    Lookup(name string) (Hook, bool)
    Names() []string
}

// MapRegistry is the default in-memory Registry implementation. Build
// one with NewRegistry; the zero value has a nil map and must not be used.
type MapRegistry struct { /* unexported fields */ }

func (r *MapRegistry) Lookup(name string) (Hook, bool)
func (r *MapRegistry) Names() []string

// NewRegistry returns a Registry populated with the given Hooks in the
// order supplied; that order is the Chain walk order. NewRegistry
// returns an error if any Hook is nil, if two Hooks share the same
// Name(), or if any Name() is the empty string.
func NewRegistry(hooks []Hook) (*MapRegistry, error)
```

No `GlobalRegistry()` accessor exists. An instance registry is
constructed per run; tests construct their own.

**Divergence from PR #83:** the old `Registry.Names()` returned names
in **sorted** order and the chain runner ignored it (Chain used the
caller-supplied `names []string` for order). The new `Registry.Names()`
returns names in **declaration order** because Chain now derives walk
order from the Registry itself. The "sorted" contract from the prior
godoc is removed.

##### `Chain`

```go
// Chain runs every Hook in reg.Names() order against in and returns
// the composed HookResult.
//
// Empty registry (reg.Names() returns empty) returns
// HookResult{Directives: in.Directives, Diagnostics: nil} with zero
// allocations; the returned Directives shares the same backing array
// as in.Directives.
//
// Before each rung, Chain checks ctx.Err(). On cancellation it returns
// the composed-so-far HookResult together with ctx.Err().
//
// If a name returned by reg.Names() is not present in reg.Lookup
// (a registry that violates its own invariant — should not happen for
// well-formed *MapRegistry, but Chain accepts arbitrary Registry
// implementations), Chain halts and returns the composed-so-far
// HookResult augmented with a [DiagHookNotRegistered] Error diagnostic
// and a nil error.
//
// If a hook's Apply returns a non-nil error, Chain halts: it returns
// the previous rung's Directives (the failing hook's Directives are
// discarded), the composed Diagnostics (including any the failing
// hook emitted), and the error.
//
// Diagnostics from successive rungs concatenate in chain order. When
// no rung emits any diagnostic, the returned Diagnostics is nil (not
// an empty slice). Chain MUST NOT defensively copy Directives between
// rungs.
func Chain(ctx context.Context, reg Registry, in HookInput) (HookResult, error)
```

**Signature change vs PR #83:** the `names []string` parameter is
removed. The instance Registry IS the ordered list of hooks to run —
that was the architectural point of moving from a kind-keyed global to
a per-run declaration-ordered instance registry. The CLI no longer
needs to maintain a separate chain-name list parallel to the registry.

**Behavioural deltas relative to PR #83 Chain:** every Chain
behavioural property is preserved (zero-alloc empty-input path,
declaration-order traversal, halt + diag on unknown rung, halt +
prior-Directives on Apply error, ctx check between rungs, nil
Diagnostics when none emitted, no defensive copy). The only change
is where the declaration order comes from: `reg.Names()` instead of
the caller's `names []string`.

`DiagHookNotRegistered` is **kept** as an exported constant. Its
description is updated to refer to a Registry that yields a name its
own `Lookup` does not resolve.

##### `Configurable` removal

The PR #83 `Configurable` sub-interface (`Hook` + `Configure(decode
func(dest any) error) error`) is **deleted**. The generator MUST:

- Remove the interface declaration from `hook.go`.
- Remove the `fakeConfigurableHook` fixture from `fake_test.go`.
- Remove `TestOptionalInterface_ConfigurableAssertion` from
  `hook_test.go`. If no tests remain in `hook_test.go`, delete the
  file.

##### Concurrency contract (per symbol)

| Symbol                                | Concurrency contract                                                   |
| ------------------------------------- | ---------------------------------------------------------------------- |
| `RegisterFactory`                     | Safe for concurrent use; intended init-time. Panics on duplicate kind. |
| `LookupFactory`, `KindNames`          | Safe for concurrent read; may race with RegisterFactory cleanly.       |
| `Factory.New`                         | Caller calls once per instance. Distinct New calls (different instances) MAY run concurrently. |
| `Registry.Lookup`, `Registry.Names`   | Safe for concurrent read; contents immutable post-construction.        |
| `Hook.Apply`                          | Safe for concurrent invocation on the same value. State is frozen at factory return. |
| `Chain`                               | Safe for concurrent invocation on the same Registry.                   |

##### Test adaptation (binding)

- `hook.go`: remove `Configurable`; add `Factory` and `FactoryFunc`.
- `registry.go`: full rewrite as described above. Old global
  registry symbols deleted.
- `chain.go`: drop `names []string` parameter; iterate
  `reg.Names()`.
- `registry_test.go` → `kind_registry_test.go` (rename). Cover
  `RegisterFactory` / `LookupFactory` / `KindNames`: round-trip,
  duplicate panic, missing lookup, sorted-order. `TestGlobalRegistry`
  deleted.
- New `instance_registry_test.go` covering `NewRegistry` happy path
  (declaration order, copy semantics on Names) and error cases.
- `hook_test.go`: delete `TestOptionalInterface_ConfigurableAssertion`.
- `chain_test.go`: update call sites
  (`Chain(ctx, reg, names, in)` → `Chain(ctx, reg, in)`). Use
  declaration-disagreeing-with-lex fixtures (e.g. `zzz/bbb/aaa`
  declared in that order) in at least one test to pin
  declaration-order semantics. `TestChain_MissingRungHaltsWithDiag`
  exercises the defensive branch via a `fakeRegistry` whose
  `Names()` yields a name `Lookup` does not resolve.
- `fake_test.go`: delete `fakeConfigurableHook` and any
  `withCleanRegistry` helper for the old instance-registry path.
  Keep `fakeHook`. Add `withCleanKindRegistry` if needed for the
  kind-registry concurrency test, with the same "must not be used
  with t.Parallel()" godoc note as α-1.
- `concurrency_test.go` → splits:
  - `kind_registry_concurrency_test.go`: parallel
    `RegisterFactory` / `LookupFactory` / `KindNames`.
  - `apply_concurrency_test.go` (new): concurrent `Apply` on the
    same frozen `Hook` instance under `-race`.
- `factory_test.go` (new):
  - Parallel factory calls with different `name`s produce
    independent `Hook` instances.
  - Factory error propagation: decode error and validation error
    both surface to the caller.
  - `NewRegistry` error cases: nil Hook, duplicate `Name()`, empty
    `Name()`.

#### Suggested Internals

**Kind registry storage.** Mirror α-1: `var kindMu sync.RWMutex; var
kinds = map[string]Factory{}`.

**Default instance registry shape.** Concrete exported `*MapRegistry`
returned by `NewRegistry([]Hook)`. Map + declaration-order slice;
no mutex. `Names()` returns a fresh copy (mirroring α-1's final
state after fix-cycle).

**Where `FactoryFunc` lives.** Same file as `Factory` (`hook.go` or
a new `factory.go`).

**Chain implementation hint.** Snapshot `reg.Names()` once at entry
into a local variable, then iterate. Cheaper than calling per
iteration, and means any `Registry` impl whose `Names()` recomputes
isn't penalised.

#### Alternatives

- **Keep `names []string` on Chain.** Rejected — re-creates a
  duplicate-source-of-truth problem the redesign exists to
  eliminate, and forces the CLI to maintain a parallel list.

- **`Registry.Names()` returns sorted vs declaration order.** Sorted
  was useful in PR #83 only because chain order came from a
  separate parameter; without that parameter, sorted actively
  breaks the contract that hook declaration order in TOML governs
  execution order. Declaration order is the only consistent choice.
  (`KindNames()` retains ascending sort — kinds have no declaration
  order.)

- **Drop the unknown-rung diagnostic branch.** Kept — `Registry` is
  an interface, plugin authors will write their own implementations,
  and the defensive diagnostic is cheap insurance.

- **Duplicate-kind registration: panic vs error.** Panic, mirroring
  α-1.

- **Registry constructor name: `NewRegistry` vs `NewMapRegistry`.**
  `NewRegistry`, mirroring α-1.

#### Recommendation

Adopt the contract above verbatim. The design is structurally
isomorphic to α-1 with two principled divergences justified by the
hook-side semantics: Chain's signature loses `names` (because the
instance Registry now encodes both membership and order), and
`Registry.Names()` returns declaration order rather than sorted
order (because Chain consults it for execution order, not just for
listing).

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
