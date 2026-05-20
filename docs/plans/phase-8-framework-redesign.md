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

- `bazel build //pkg/importer/std/csvimp:csvimp`
- `bazel test //pkg/importer/std/csvimp:csvimp_test --test_output=errors`
- New concurrent-Apply test with `-race`.

> **Step-level scope.** After α-3, `pkg/importer/...` builds for
> the importer subtree (α-1 + α-3); only `pkg/importer/hook/std/classify`
> still references the old hook API. The PR-α-level wildcard build
> converges after α-4.

**Quality requirements:** exported godoc; the contract that
post-factory state is immutable is documented on the struct.

### Detailed Design

#### Contract

##### Package surface after migration

The package exports exactly one symbol after this step:

```go
// Importer extracts beancount Transactions from one CSV/TSV shape.
// It is produced by the package's [importer.Factory] (registered under
// kind "csv"); its internal state is frozen at construction and all
// methods are safe for concurrent invocation.
type Importer struct { /* unexported fields only */ }

func (i *Importer) Name() string
func (i *Importer) Identify(ctx context.Context, in importer.Input) bool
func (i *Importer) Extract(ctx context.Context, in importer.Input) (importer.Output, error)
```

Required fields (unexported); the implementer MUST NOT add
`sync.Mutex`, caches, or any field that mutates post-construction:

- instance name (returned by `Name()`)
- one `*shape` value (or its inlined fields) carrying compiled match
  regex, delimiter rune, skip count, column names, default currency,
  default account, amount column descriptors, narration columns and
  separator.

**Deleted:**

- the `Configure` method,
- the `mu sync.Mutex` field,
- the `identifyCache` type and field,
- the `shapes []*shape` slice (or whatever the multi-shape container
  was called),
- the dual-name `importer.Register(...)` calls from `doc.go` (the
  Go-import-path alias is gone).

No compatibility shim is left behind.

The `*shape` type and its internal helpers (`buildColumnIndex`,
`requiredColumns`, `openCSVAtBody`, `processRow`, `sumAmounts`,
`resolveCurrency`, `resolveAccount`, `buildNarration`, `rowHash`,
`rowDiag`) and the diagnostic-code constants (`DiagBadDate`,
`DiagBadAmount`, `DiagAllBlankAmount`, `DiagMissingCurrency`,
`DiagMissingAccount`, `DiagMissingColumn`) keep their PR #83
semantics byte-for-byte. The diagnostic-code constants remain
exported.

##### Factory registration

At init time:

```go
importer.RegisterFactory("csv", importer.FactoryFunc(newImporter))
```

The factory function:

```go
func newImporter(name string, decode func(dest any) error) (importer.Importer, error)
```

Binding requirements:

1. `decode` MUST NOT be nil. A nil `decode` yields
   `fmt.Errorf("csvimp: configure: nil decoder")`.
2. The decode target is a single `shapeConfig` struct (the type
   already used as the value side of PR #83's `config.Shapes`).
   The factory calls `decode(&sc)` where `sc` is a fresh
   `shapeConfig`.
3. Validation reuses PR #83's per-shape validation
   (`validateShape`), producing a `*shape`. The `name` parameter
   from the factory call becomes the shape's `name` field — the
   TOML body no longer carries the shape name (callers wrap the
   body so the factory sees the body only).
4. On any error the factory returns `(nil, err)` and the error
   message is prefixed `"csvimp: configure: "` (preserves PR #83
   error-text shape — config_test.go relied on this prefix).
5. On success the factory returns a fully-initialised `*Importer`
   whose `Name()` is the `name` argument verbatim (NOT `"csv"`).
6. The factory MUST NOT register the resulting `*Importer` in any
   global state.

Only one kind is registered — `"csv"`. The Go-import-path alias is
dropped.

##### Name() returns the instance name

`Name()` returns the string supplied to the factory. PR #83's
`return "csv"` becomes `return i.name`. The kind name `"csv"` is
visible only at the `RegisterFactory` call site.

##### Identify and Extract semantics

The `importer.Importer` interface contract from α-1 is binding.
Behavioural deltas vs PR #83:

- `Identify` consults the single configured shape only —
  extension/MIME gate, optional match-regex against `in.Path`, then
  header read + required-columns check. It MUST NOT mutate any
  field of `*Importer` (the `identifyCache` write disappears).
- `Extract` likewise dispatches against the single shape. On match
  failure it returns
  `fmt.Errorf("csvimp: no shape matched for %q", in.Path)`
  preserving PR #83's surface text.
- The "not configured" framework error path disappears: an
  unconfigured `*Importer` is unrepresentable.

##### Behaviour preservation (binding)

For any input that PR #83's `csvimp` accepted with a single
`[shape.<name>]` configuration, the migrated package MUST emit:

- the same directives in the same order;
- identical `csvimp-rowhash` bytes on each directive. The rowhash
  canonical form is unchanged: the shape-name passed to `rowHash`
  is `i.name`, which equals the PR #83 shape-table key under the
  fixtures' instance-name choices (`"simple"`, `"debitcredit"`);
  the project's golden files remain valid;
- the same diagnostic codes, severities, messages, and span
  filenames / line numbers (skip_lines offset preserved).

PR-β changes the canonical rowhash form per the plan's PR-β
section; PR-α does not.

##### Multi-shape semantics live in the Registry

PR #83's lex-sorted, single-importer-walks-many-shapes selection is
gone. Multi-shape scenarios are realised by constructing one
`*Importer` per shape via the factory and walking them with
`importer.Dispatch` / `importer.Apply` against an
`*importer.MapRegistry`. Walk order is the registry's declaration
order (α-1 contract). Tests that asserted lex-first shape
selection must declare instances in the equivalent order
explicitly; lex sorting no longer happens inside csvimp.

##### Concurrency contract per symbol

| Symbol               | Contract |
| -------------------- | -------- |
| `Importer.Name`      | Safe for concurrent use; pure read of immutable field. |
| `Importer.Identify`  | Safe for concurrent invocation on the same value; no state mutation. |
| `Importer.Extract`   | Safe for concurrent invocation on the same value; no state mutation. `in.Opener` MAY be called more than once across goroutines, so the caller's Opener must return a fresh reader per call (already the documented `importer.Input` contract). |
| `newImporter` factory | Caller invokes at most once per instance; distinct instances MAY be constructed concurrently. |

#### Suggested Internals

**File layout.** Recommend:

- `csvimp.go` keeps `type Importer struct`, `Name`, `Identify`,
  `Extract`, and the small matcher/header helpers (with `Identify`
  non-mutating).
- `config.go` keeps `shapeConfig`, `amountColumn`, `shape`, and
  `validateShape`. `buildShapes` and the multi-shape `config`
  struct are deleted. The factory function (`newImporter`) lives
  here next to the validation logic it drives.
- `extract.go`, `rowhash.go`, `diag.go` unchanged in body.
- `doc.go` rewritten: package overview reflects single-shape model;
  `init()` calls
  `importer.RegisterFactory("csv", importer.FactoryFunc(newImporter))`.

  Alternative: keep `init()` in `factory.go` or `csvimp.go` — equivalent.

**rowHash shape-name argument.** Recommend `i.name` (instance
name). Removes the vestigial `shape.name` field; bytes identical to
PR #83 under the fixtures' instance-name choices. Alternative
(keep `s.name` seeded from factory `name`) is functionally
identical; recommended choice is cleaner.

**Shape struct retention.** Keep `*shape` to minimise diff and reuse
the extract helpers verbatim. The `*Importer` then has `name
string` and `s *shape`. Alternative (inline) is a fine internals
refactor for later.

**`selectShape` collapse.** With one shape, the old loop becomes a
straight-line check. Inline into `Identify` or keep as a small
helper — either is acceptable.

**Extract's match path.** No cache to consult. Extract simply runs
the same matcher Identify uses. Match-failure error path collapses
to one `fmt.Errorf`.

**Test file layout.**

- `csvimp_test.go`: drop `TestImporterRegisteredUnderBothNames`.
  Replace `TestName_Constant` with `TestName_ReturnsInstanceName`.
  Rewrite `newConfigured` helper to call
  `newImporter("test", permissiveDecoder(src))`.
  `TestIdentify_MatchRegexGatesShapeSelection` and
  `TestConfigure_LexicographicShapeOrder` rewritten as
  multi-instance scenarios via `MapRegistry` + `Dispatch`.
  `TestExtract_NotConfigured` deleted.
- `config_test.go`: TOML fixtures changed from `[shape.<name>]`
  form to bare body form. `Configure` calls replaced by
  `newImporter`. `TestConfigure_Reconfigure` deleted (each factory
  call yields a fresh instance — property is now structural).
  Other tests renamed `TestConfigure_*` → `TestFactory_*` and
  drop the rollback assertions.
- `rowhash_test.go`: unchanged (still reaches `rowHash` directly
  under the CLAUDE.md test-exception clause).
- `idempotency_test.go`: rewrite `runOnce` to use `newImporter`
  with a permissive decoder built from
  `loadFixtureConfig(t, shape)`. Fixture TOML files
  (`testdata/{simple,debitcredit}/config.toml`) reshape from
  `[shape.<name>]` form to bare body form (drop the table header
  line, dedent fields, change `[[shape.<name>.amount]]` to
  `[[amount]]`). Instance name passed to `newImporter` is the
  shape directory name (`"simple"` / `"debitcredit"`) — keeps the
  rowhash bytes identical to the golden files.

  Alternative: keep fixture TOMLs unchanged and have `runOnce`
  decode `cfg.Shapes[shape]` before passing to user dest.
  Rejected — bakes the old schema into the test driver and
  obscures the migration's intent.

- New `concurrency_test.go`: a test that builds one `*Importer`
  via the factory and runs `Identify` + `Extract` from N
  goroutines against the same `Input` whose `Opener` is
  re-entrant; runs under `-race`.

#### Alternatives

- **Inline `shape` fields into `*Importer` vs keep `*shape`.**
  Recommend keeping `*shape` for minimum surgery.
- **Where to put `newImporter`.** `config.go` next to validation.
- **Decode target shape (bare body vs preserved table).** Bare body
  — the plan's mechanical framing. Multi-shape callers build
  per-shape decoders.
- **`rowHash` argument source.** `i.name` over a retained
  `s.name`. Identical bytes under fixture instance-name choices.
- **Dual-name registration retention.** Drop. The Go-import-path
  alias served no concrete user; α-1's `KindNames` would otherwise
  list a misleading duplicate.
- **Replacement for `TestExtract_NotConfigured`.** None — the state
  is structurally unreachable.

#### Recommendation

Adopt the contract above: `*Importer` with `name string` and
`*shape`, no mutex / cache / shapes slice; `newImporter(name,
decode)` factory in `config.go`; `init()` in `doc.go` registering
the factory under kind `"csv"` only; `Identify` and `Extract`
consult the single shape without state mutation; rowhash bytes
preserved under fixture instance-name choices. Tests migrate
multi-shape scenarios into multi-instance scenarios via
`importer.NewRegistry` + `importer.Dispatch`; testdata TOML
fixtures flatten to bare bodies. A concurrent-Apply test under
`-race` pins the frozen-state contract.

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

- `bazel build //pkg/importer/hook/std/classify:classify`
- `bazel test //pkg/importer/hook/std/classify:classify_test --test_output=errors`
- New concurrent-Apply test with `-race`.
- **PR-α convergence:** after α-4, run the wildcard build/test —
  `bazel build //...` and `bazel test //... --test_output=errors`
  must both succeed. This is the PR-α-level invariant that's been
  deferred since α-1.

**Quality requirements:** exported godoc.

### Detailed Design

#### Contract

##### Package surface after migration

The package exports exactly one symbol after this step:

```go
// Hook is the classify hook for one declared instance. It is produced
// by the package's [hook.Factory] (registered under kind "classify");
// its internal state is frozen at construction and Apply is safe for
// concurrent invocation on the same value.
type Hook struct { /* unexported fields only */ }

func (h *Hook) Name() string
func (h *Hook) Apply(ctx context.Context, in hook.HookInput) (hook.HookResult, error)
```

Required fields (unexported); MUST NOT add `sync.Mutex` or any field
that mutates post-construction:

- instance name (returned by `Name()`)
- rule list (the `[]rule` already used in PR #83), populated once at
  factory time.

**Deleted:**

- the `Configure` method,
- the dual-name `hook.Register(...)` calls (the Go-import-path alias
  is gone),
- the singleton-construction idiom (`h := &Hook{}` from `init()` and
  from tests).

`DiagNoRule` exported constant keeps its value and severity
(`ast.Warning`). The unexported helpers (`isSingleLeg`, `applyRules`,
`buildRules`, `validateRule`, `ruleConfig`, `rule`, `config`) keep
their PR #83 semantics byte-for-byte.

##### Factory registration

At init time:

```go
hook.RegisterFactory("classify", hook.FactoryFunc(newHook))
```

Only one kind is registered. The PR #83 Go-import-path alias is
dropped without replacement.

##### Factory function

```go
func newHook(name string, decode func(dest any) error) (hook.Hook, error)
```

Binding requirements:

1. `decode` MUST NOT be nil. A nil `decode` yields
   `fmt.Errorf("classify: configure: nil decoder")`.
2. The decode target is the existing `config` struct (with
   `Rules []ruleConfig` tagged `toml:"rule"`). The factory calls
   `decode(&cfg)` where `cfg` is a fresh `config`. This preserves
   the existing `[[rule]]` TOML schema verbatim.
3. Validation reuses PR #83's `buildRules` / `validateRule`
   unchanged, producing a `[]rule`.
4. On any error the factory returns `(nil, err)` and the error
   message is prefixed `"classify: configure: "`.
5. On success the factory returns a fully-initialised `*Hook` whose
   `Name()` is the `name` argument verbatim (NOT `"classify"`).
6. The factory MUST NOT register the resulting `*Hook` in any
   global state.
7. An empty `[[rule]]` list is accepted (matches PR #83's
   `TestConfigure_EmptyRules`); such a `Hook` emits `DiagNoRule` for
   every single-leg transaction.

##### Name() returns the instance name

`Name()` returns the string supplied to the factory. PR #83's
`return "classify"` becomes `return h.name`. The kind name
`"classify"` is visible only at the `RegisterFactory` call site.

##### Apply semantics — behaviour preservation (binding)

For any input that PR #83's classify accepted after a successful
`Configure`, the migrated package MUST produce byte-identical
output:

- Non-Transaction directives pass through aliased.
- Transactions with zero or 2+ postings pass through aliased.
- The "no single-leg in input" fast path returns
  `HookResult{Directives: in.Directives}` aliasing the input slice
  (no allocation), Diagnostics nil.
- When at least one single-leg transaction is present:
  - A fresh `out := make([]ast.Directive, len(in.Directives))` is
    allocated.
  - Each non-single-leg directive is copied aliased into `out`.
  - Each single-leg transaction is matched against the rule list in
    declaration order; the first matching rule produces a
    `importerutil.BalanceWith(tx, r.account, r.currency)` directive.
  - On no-match: the original transaction is aliased into `out` and
    a `DiagNoRule` Warning diagnostic is appended (Code =
    `DiagNoRule`, Span = `tx.Span`, Severity = `ast.Warning`,
    Message format unchanged).
- Rule matching semantics unchanged: AND between `payeeRegex` and
  `narrationRegex` when both set; a nil regex skips that selector.
- `ctx.Err()` checked at entry; mid-loop checks at `i%64==0`
  produce partial output `out[:i]` and accumulated diagnostics on
  cancellation.
- Hints / Options on `HookInput` are NOT consulted.
- Input directives are NOT mutated; counterpart postings come from
  `importerutil.BalanceWith` which clones.

`Apply`'s body is preserved verbatim from PR #83 except that
`h.rules` is now an immutable factory-time field.

##### Concurrency contract per symbol

| Symbol       | Contract |
| ------------ | -------- |
| `Hook.Name`  | Safe for concurrent use; pure read of an immutable field. |
| `Hook.Apply` | Safe for concurrent invocation on the same value; no state mutation. The `[]rule` slice and its regex pointers are read-only after factory return. |
| `newHook`    | Caller invokes at most once per instance; distinct instances MAY be constructed concurrently. |

#### Suggested Internals

**File layout.**

- `classify.go` keeps `type Hook struct`, `Name`, `Apply`, the
  `DiagNoRule` constant, and the `isSingleLeg` / `applyRules`
  helpers.
- `config.go` keeps `config`, `ruleConfig`, `rule`, `buildRules`,
  `validateRule`. `Configure` deleted; `newHook(name, decode)` added
  here next to the validation logic.
- `doc.go` rewritten: package overview reflects the per-instance
  model; `init()` calls
  `hook.RegisterFactory("classify", hook.FactoryFunc(newHook))`.

  Alternative: place `newHook` and `init()` in a new `factory.go`.
  Equivalent.

**Rule compilation: factory-time vs lazy.** Factory-time
(unchanged from PR #83). Compiling regexes at construction catches
malformed regex during CLI startup. Lazy compilation would
re-introduce a write-after-construction path and break the
frozen-state contract.

**Hook struct shape.**

```go
type Hook struct {
    name  string
    rules []rule
}
```

Two unexported fields, both set in `newHook` and never written
again.

**Test file layout.**

- `classify_test.go`: rewrite the `newHook` helper to drive through
  `hook.LookupFactory("classify")`. `newHook(t, tomlSrc)` becomes
  `f, _ := hook.LookupFactory("classify"); h, err := f.New("test", permissiveDecode(tomlSrc))`.
  Replace bare-`&classify.Hook{}` constructions in
  `TestApply_NoRuleDiagSpan`, `TestApply_NoConfigurePath` (rename
  to `TestApply_EmptyRules`), `TestApply_NonTransactionPassThrough`,
  `TestApply_BalancedTxnPassThrough`,
  `TestApply_AliasOnNoSingleLeg`, `TestApply_CancelledContext` with
  a factory call decoding an empty TOML body. The "no Configure"
  scenario is structurally unrepresentable; the renamed test
  preserves the assertion that every single-leg txn yields
  `DiagNoRule`.
- `config_test.go`: rename `TestConfigure_*` → `TestFactory_*`.
  `TestConfigure_PriorRulesUntouched` **deleted** — re-Configure
  rollback is no longer a concept. Other tests are mechanical
  rewrites: replace `h := &classify.Hook{}; err := h.Configure(...)`
  with `_, err := f.New("test", ...)`. Error prefix assertions
  unchanged.
- `register_test.go`: **deleted**. Both tests assert dual-name
  registration / singleton lookup that no longer exists. The
  factory-side analogue is covered by the new `factory_test.go`.
- New `factory_test.go`:
  - `TestFactory_NameReturnsInstanceName`
  - `TestFactory_RegisteredUnderKindClassify`
  - `TestFactory_NoGoPathAlias` (pins the deletion)
  - `TestFactory_DecoderError`
  - `TestFactory_ValidationError`
  - `TestFactory_NilDecoder` (asserts exact message).
- New `concurrency_test.go`: build one `*Hook` via the factory with
  a non-trivial rule list; run `Apply` from N goroutines (≥8)
  against shared `HookInput` values whose `Directives` are
  read-only. Race-clean.

**Multi-instance test scenarios.** The classify hook is naturally
single-instance per CLI run. No PR #83 test asserts multi-instance
dispatch to migrate. Skip speculatively adding multi-instance
scenarios.

#### Alternatives

- **Where to put `newHook`.** `config.go` next to validation.
- **Inline `rules` directly on `Hook` vs keep the unexported `rule`
  type.** Keep `rule`.
- **Compile regex eagerly vs lazily.** Eagerly (mirrors PR #83).
- **Drop the Go-import-path alias retention.** Drop.
- **Empty-rule-list acceptance.** Keep accepting (matches PR #83).
- **Driving tests through `hook.LookupFactory` vs an exported
  `NewHook`.** Lookup-driven, mirroring α-3. Exporting the factory
  function would widen the package's published surface for no test
  benefit.

#### Recommendation

Adopt the contract above. After α-4 the wildcard `bazel build //...`
and `bazel test //... --test_output=errors` both succeed — PR-α
converges and is ready for review.

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

## Post-PR-α API refinement

The following changes were applied on top of the merged PR-α commits in
response to a post-review API tweak request. They are recorded here as a
supplement to the α-1 and α-2 Detailed Design sections, which remain
unchanged as historical record of what shipped at those steps.

### Step α-1 (pkg/importer): LookupFactory demoted; New added

- `LookupFactory` renamed to `lookupFactory` (unexported). The only
  callers were in test code; the two-step lookup-then-call pattern had
  no production use.
- `New(kind, name string, decode func(dest any) error) (Importer, error)`
  added as a package-level function. It is the one-shot form of
  `lookupFactory + Factory.New` and is documented as the recommended
  path for CLIs and tests to build an Importer instance. On unknown
  kind it returns `(nil, fmt.Errorf("importer: unknown kind %q", kind))`.
  On factory error it returns the factory's error verbatim.
- `RegisterFactory`'s godoc updated to reference `New` (replacing the
  old reference to `LookupFactory`).

### Step α-2 (pkg/importer/hook): LookupFactory demoted; New added

- Mirrors the α-1 change: `LookupFactory` → `lookupFactory`.
- `New(kind, name string, decode func(dest any) error) (Hook, error)`
  added. On unknown kind returns `(nil, fmt.Errorf("hook: unknown kind %q", kind))`.
- `pkg/importer/hook/std/classify` tests updated: `factory_test.go`
  replaces the two `hook.LookupFactory` call sites with
  `slices.Contains(hook.KindNames(), ...)` checks; all `factory(t).New(...)`
  call sites replaced with `hook.New("classify", ...)` directly;
  `config_test.go` likewise inlines `hook.New` at each call site.

### Rationale

The lookup-then-call pattern was the dominant — and only — use of
`LookupFactory` in the entire codebase. Collapsing it into a single
`New` operation is a strict simplification: it removes a public API
path that provided no value beyond test setup, reduces boilerplate at
every call site, and makes the recommended usage unambiguous.

## PR-β: `cmd/beanimport` CLI, end-to-end fixtures, and docs polish

PR-α landed the factory + instance-registry framework and migrated
the two in-tree implementations (`csvimp`, `classify`). PR-β ships
the user-visible piece of the redesign: a `cmd/beanimport` binary
that consumes the flat `[[importer]]` / `[[hook]]` TOML schema PR-α
already implies, exercises the multi-instance dispatch the redesign
exists to enable, and surfaces goplug-loaded importers through
`--plugin`.

### Goal

Ship `cmd/beanimport` on the PR-α framework: a CLI that loads a flat
TOML config of `[[importer]]` / `[[hook]]` entries, optionally loads
goplug `.so`s, dispatches a single input through the right instance,
runs the hook chain, and prints directives + diagnostics. Prove the
multi-instance model end-to-end with integration tests, and polish
the surrounding docs.

### Scope

**In scope:**

- β-1: doc-only adjustments to csvimp's `rowhash` godoc; addition of
  `in.Hints["account"]` override plumbing in csvimp's `Extract`;
  confirmation of the flat `[[importer]]` / `[[hook]]` TOML schema
  PR-α already implies; the new `cmd/beanimport` binary (flags,
  TOML loader, pipeline, unit tests).
- β-2: end-to-end integration fixtures under `cmd/beanimport/testdata/`
  plus a goplug fixture importer modelled on beanprice's staticquoter.
- β-3: update `PLAN.md` (project root) and the `## Out of scope`
  section of this document to reflect the new in-scope/completed
  list.

**Out of scope (explicitly deferred):**

- The larger PLAN.md historical rewrite (PR-β.1).
- Phase 8.1 (ML hooks) and Phase 8.2 (XML/OFX/QIF importers).
- Cross-source dedup (remains `pkg/distribute`'s concern).
- Any change to the PR-α framework APIs (`pkg/importer`,
  `pkg/importer/hook`); β never touches their exported surface.
- Multi-file batch import on a single invocation — single positional
  input is the only β-1 mode.

### Step β-1 — Schema confirmation, `--account` Hint, and `cmd/beanimport` CLI core

**Functional requirements:**

- The user-facing TOML is confirmed as the flat `[[importer]]` /
  `[[hook]]` arrays already implied by PR-α; no parser change.
- csvimp's `rowhash`-related godoc (and any design-doc fragment)
  refers to `Name()` (instance name), not "shape-name". The
  implementation already passes `i.name`; this is purely doc cleanup.
- csvimp reads `in.Hints["account"]` during **Extract** (not
  Identify). When the Hint is set and non-empty, it overrides the
  shape's `account` field for that single Extract call. Shape
  `account` remains mandatory at factory time.
- New binary `cmd/beanimport` exposes flags: `--config PATH`
  (required), `--hook NAME[,NAME...]` (comma- or repeat-list;
  selects which configured hooks form the chain, in order),
  `--importer NAME` (at-most-once; opt out of Dispatch),
  `--account NAME` (at-most-once; written to `in.Hints["account"]`),
  `--plugin PATH.so` (repeatable), `--strict`. Exactly one positional
  input file argument.
- TOML loader extracts top-level `[[importer]]` and `[[hook]]` arrays;
  the body of each entry minus the reserved `kind` / `name` keys
  becomes the `decode func(dest any) error` closure passed to
  `importer.New` / `hook.New`. Unknown top-level keys are an error.
- Pipeline in `run()`: load plugins → decode TOML → build instance
  registries → open and sniff input → dispatch (or use `--importer`)
  → `Extract` → `hook.Chain` over the `--hook`-selected subset (or
  all declared hooks, in declaration order, if `--hook` is absent)
  → `printer.Fprint` to stdout → diagnostics to stderr formatted via
  the same `<file>:<line>:<col>: <sev>: <msg>` shape `cmd/beanprice`
  uses.
- Exit codes: 0 clean; 1 if any Error diagnostic OR (Warning present
  AND `--strict`); 2 for CLI/config/plugin failures.
- `--importer NAME` referencing an unknown instance is exit 2. A
  known instance whose `Identify` returns false is a Warning
  diagnostic, then `Extract` runs anyway (user is explicitly
  overriding Dispatch).

**Modules:**

- `pkg/importer/std/csvimp/extract.go` (or wherever `resolveAccount`
  lives) — read `in.Hints["account"]`.
- `pkg/importer/std/csvimp/csvimp.go` / `rowhash.go` godoc — wording
  fixes.
- `cmd/beanimport/main.go` (new) — flag set, `run` entry point,
  plugin loading.
- `cmd/beanimport/config.go` (new) — TOML loader producing
  `[]importer.Importer` + `[]hook.Hook`.
- `cmd/beanimport/pipeline.go` (new) — sniff + dispatch + extract +
  chain + print.
- `cmd/beanimport/diag.go` (new) — diagnostic formatter.
- `cmd/beanimport/BUILD.bazel` (Gazelle-generated).

**Verification:**

- `bazel run //:gazelle`.
- `bazel build //...` and `bazel test //... --test_output=errors`.
- New unit tests in `cmd/beanimport/`: flag parser; TOML loader
  (happy path, unknown reserved-key collision, missing
  `kind`/`name`, factory error propagation); `run()` smoke
  (single-instance config against a tiny in-memory fixture);
  `--account` Hint round-trip into csvimp output.
- New unit test in `pkg/importer/std/csvimp/`: extract with
  `in.Hints["account"]` set overrides shape's account; Hint
  empty/missing falls back to shape value; verified against existing
  extract fixture.

**Quality requirements:**

- All new exported symbols documented per project `CLAUDE.md`
  (contract-style godoc, no implementation narration).
- Tests target the package's exported surface only (`run()` is the
  CLI's externally observable entry); per-helper tests only where
  they reduce total test surface.
- `cmd/beanimport`'s `package` doc carries the flag table,
  exit-code table, and a minimal worked TOML example — same layout
  as `cmd/beanprice`.

#### Detailed Design

##### Scope note

The code-survey conducted during Phase 4 design discovered that two
items the high-level β-1 plan listed are **already shipped** in PR-α:

1. **csvimp `--account` Hint plumbing.** `resolveAccount` at
   `pkg/importer/std/csvimp/extract.go:226` already reads
   `hints["account"]` and overrides `s.account` when non-empty;
   `TestExtract_HintsAccountOverridesShape` (extract_test.go:184)
   and `TestExtract_DiagMissingAccount` (extract_test.go:211)
   already cover the behaviour. `Input.Hints` godoc at
   `pkg/importer/importer.go:111-116` already documents `"account"`
   as a framework-reserved key set by `cmd/beanimport --account`.
2. **rowhash godoc terminology.** `pkg/importer/std/csvimp/rowhash.go:14-20`
   already says "instance name", not "shape-name". The only
   residual "shape-name" usage is in `rowhash_test.go` comments —
   intentionally not polished.

Per user decision, β-1's scope narrows to **`cmd/beanimport`
implementation only**. The high-level β-1 functional-requirements
list above is preserved as historical record; the Contract below
binds what β-1 actually delivers.

A secondary survey finding: `pkg/distribute/route/routeconfig/routeconfig.go`
does **not** use `toml.Primitive` — it does single-pass `Decode`
plus `meta.Undecoded()` rejection of unknown keys. The Contract
below introduces `toml.Primitive` afresh for the per-entry deferred
decode that the factory call requires, and cites `routeconfig` only
as precedent for the `meta.Undecoded()` strict-key pattern.

##### Package shape

`cmd/beanimport` is `package main`. It exports no library API. The
"test surface" — the set of internally-bound symbols `main_test.go`
pins — is fixed below and MUST remain reachable from same-package
tests at the listed names and signatures.

```go
// run is the testable entry point. args is os.Args[1:]. stdout and
// stderr receive the binary's two output streams. The return value
// is the process exit code (see the exit-code table). run does not
// call os.Exit; main does.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int

// loadConfig reads one flat-schema TOML document from r and returns
// the [[importer]] and [[hook]] entries in declaration order, each
// already constructed via importer.New / hook.New. path is the
// display name used in error messages.
//
// Errors are wrapped as: "beanimport: config %s: %v" (path, cause).
// Specific cause prefixes (which tests grep for):
//   - "decode: <toml error>"        — toml syntax / type mismatch
//   - "unknown top-level key %q"    — anything other than importer/hook
//   - "[[importer]] #%d: missing %q"  — kind or name absent
//   - "[[hook]] #%d: missing %q"      — kind or name absent
//   - "[[importer]] #%d (%q): %v"   — factory error verbatim
//   - "[[hook]] #%d (%q): %v"
//   - "unknown body key %q"         — Undecoded() leftovers per entry
//   - "no [[importer]] entries"     — empty config
func loadConfig(r io.Reader, path string) (
    importers []importer.Importer,
    hooks []hook.Hook,
    err error,
)

// selectHooks returns the subset of all whose Name() appears in
// names, in the order names lists them. When names is nil or empty
// it returns all unchanged (declaration order). It returns an error
// of the form "unknown hook %q" naming the first unknown entry.
func selectHooks(all []hook.Hook, names []string) ([]hook.Hook, error)

// printDiagnostics writes one line per diagnostic to w using
// ast.Diagnostic.String(). It is a no-op when diags is empty.
func printDiagnostics(w io.Writer, diags []ast.Diagnostic)
```

The runtime flag bundle is the package-private `runOptions` struct
(name not pinned; tests reach it only via `run`). Field set is
fixed by the flag table below.

##### Flag table (binding)

stdlib `flag.FlagSet` with `ContinueOnError`; single-dash form
matching `cmd/beanprice` (`-strict`, not `--strict`). Stdlib `flag`
also accepts `--name` for any registered flag, so the documentation
shows the single-dash form but both work.

| Flag         | Type                  | Default | Multiplicity        |
|--------------|-----------------------|---------|---------------------|
| `-config`    | string (path)         | `""`    | at-most-once; required |
| `-hook`      | stringSlice           | nil     | repeatable AND comma-separated inside each value |
| `-importer`  | string                | `""`    | at-most-once (stdlib "last wins" is accepted; doc says at-most-once) |
| `-account`   | string                | `""`    | at-most-once         |
| `-plugin`    | stringSlice (path.so) | nil     | repeatable (comma NOT split — paths may contain commas in pathological filesystems) |
| `-strict`    | bool                  | `false` | at-most-once         |

Positional: **exactly one** input file path. Zero or 2+ → exit 2
with message `"beanimport: exactly one input file required"`.

`-hook` canonical form: both repeat AND comma are accepted, and
the two compose. Pinned by `TestRun_HookFlag_CommaAndRepeatCompose`.
Empty segments produced by a leading/trailing/double comma are
silently discarded (rejecting them would be a footgun in shell
quoting). `-plugin` is repeat-only because `.so` paths legitimately
may contain commas; pinned by `TestRun_PluginFlag_CommaInPathIsLiteral`.

`-config ""` (flag absent or explicitly empty) → exit 2 with
`"beanimport: -config is required"`.

##### Exit code mapping (binding)

| Condition | Exit |
|---|---|
| Pipeline completed; no Error diagnostic; (no Warning OR `-strict` false) | 0 |
| ≥1 Error diagnostic in the composed stream OR (`-strict` AND ≥1 Warning) OR Extract/Chain returned a non-nil error promoted to an Error diagnostic | 1 |
| Flag parse failure, missing `-config`, config load failure (any `loadConfig` error), plugin load failure, unknown `-hook` name, unknown `-importer` name, positional-argument-count mismatch, input-file open failure, instance-registry construction failure (`importer.NewRegistry` / `hook.NewRegistry` error) | 2 |

Context cancellation (`ctx.Err() != nil` propagated up by Apply or
Chain) is reported as a single Error diagnostic with Code
`"beanimport-cancelled"`, Message `ctx.Err().Error()`, empty Span,
and produces exit 1. Pinned by `TestRun_CancelledContext`.

##### TOML schema (binding)

The wire format `loadConfig` accepts:

```toml
[[importer]]
kind = "csv"
name = "boa_checking"
# ... arbitrary csvimp body keys ...

[[hook]]
kind = "classify"
name = "default"
# ... arbitrary classify body keys ...
```

Rules:

1. The only permitted top-level keys are `importer` and `hook`,
   both as TOML array-of-tables. Any other top-level key (including
   misspellings like `importers`) is rejected via
   `meta.Undecoded()` with `"unknown top-level key %q"`.
2. Each entry MUST carry string-valued `kind` and `name`. Missing
   or wrong-typed → error per the prefix table above.
3. Every remaining key in the entry's table forms the **body**.
   The body is handed to the factory through a `decode` closure
   that invokes `meta.PrimitiveDecode(prim, dest)` exactly once.
4. After the factory returns, `loadConfig` re-checks
   `meta.Undecoded()` restricted to that entry's body and rejects
   any keys neither the loader nor the factory consumed
   (`"unknown body key %q"`). This gives each factory
   `routeconfig`-style strict-key behaviour without per-factory
   boilerplate.
5. Factories MUST NOT define TOML-tagged struct fields named `kind`
   or `name`; those keys are consumed at the top-level decode and
   are not present in the body the factory sees. (csvimp and
   classify already satisfy this.)
6. A config with **zero** `[[importer]]` entries is an error
   (`"no [[importer]] entries"`, exit 2). Zero `[[hook]]` is fine.
7. Instance order is TOML declaration order. `NewRegistry` walks
   that order; `Dispatch` walks `NewRegistry`'s `Names()`.

##### Pipeline (binding)

`run`'s sequence and per-step failure mapping:

1. **Parse flags.** Flag parse error → write usage via the
   `FlagSet`'s own handler (already printed); return 2.
   `flag.ErrHelp` → return 0. `-config` empty after parse → write
   `"beanimport: -config is required"` to stderr; return 2.
   Positional count ≠ 1 → return 2.
2. **Load plugins.** For each `-plugin` in order, call
   `goplug.Load(path)`. First error → `"beanimport: plugin %q: %v"`;
   return 2. (Abort on first failure; do not attempt the remainder.)
3. **Load config.** `os.Open(-config)` + `loadConfig(f, -config)`.
   Any error → write to stderr verbatim; return 2.
4. **Build registries.** `importer.NewRegistry(importers)` and
   `hook.NewRegistry(hooks)`. Either error → return 2.
5. **Filter hooks.** `selected, err := selectHooks(hooks, -hook)`;
   on error return 2. Then
   `filteredReg, _ := hook.NewRegistry(selected)` (cannot fail
   because the entries came from a valid registry).
6. **Open input.** `os.Open(positional)`; on error return 2.
   Build `importer.Input` with:
   - `Path` = positional argument verbatim.
   - `Opener` = a closure that calls `os.Open(positional)` and
     returns the result (a **fresh** `*os.File` per call so
     concurrent Extract is safe; the once-opened handle from this
     step is closed immediately after constructing the Input).
   - `Hints` = `map[string]string{"account": -account}` only when
     `-account` is non-empty; otherwise `nil`.
   - `Sniff` / `MIME` left zero-valued in β-1.
7. **Dispatch.**
   - If `-importer` set: `imp, ok := impReg.Lookup(-importer)`.
     `!ok` → `"beanimport: unknown importer %q"`; return 2.
     If `imp.Identify(ctx, in)` is false: append a Warning
     diagnostic — Code `"beanimport-identify-forced"`, Severity
     `ast.Warning`, Message
     `fmt.Sprintf("importer %q: Identify returned false; extracting anyway because -importer was set", name)`,
     Span filename = `in.Path` — then call `imp.Extract(ctx, in)`.
   - Otherwise: `out, err := importer.Apply(ctx, impReg, in)`.

   In either branch, capture `out.Directives`, `out.Diagnostics`,
   and `err`. On `err != nil` and `ctx.Err() == nil`, promote to a
   single Error diagnostic Code `"beanimport-extract"` Message
   `err.Error()` Span filename = `in.Path`.
8. **Chain hooks.**
   `result, herr := hook.Chain(ctx, filteredReg, hook.HookInput{Directives: out.Directives, Hints: in.Hints})`.
   On `herr != nil` and `ctx.Err() == nil`, promote to a single
   Error diagnostic Code `"beanimport-hook"` Message `herr.Error()`
   Span filename = `in.Path`.
9. **Print directives.** `printer.Fprint(stdout, result.Directives)`.
   On error → `"beanimport: writing stdout: %v"` to stderr,
   return 1.
10. **Print diagnostics.** Concatenate in this order: dispatch
    diagnostics already inside `out.Diagnostics`, the
    Identify-forced Warning from step 7 (if any), Chain
    diagnostics in `result.Diagnostics`, any promoted Error from
    step 7 or 8, and the cancellation diagnostic if
    `ctx.Err() != nil`. Call `printDiagnostics(stderr, composed)`.
11. **Exit code.** Apply the table above against the composed
    diagnostic stream and `-strict`.

##### Package doc (binding)

`main.go` opens with two paragraphs:

1. One-sentence overview: "Command beanimport drives the
   `pkg/importer` + `pkg/importer/hook` pipeline against a single
   input file using a flat `[[importer]]` / `[[hook]]` TOML
   config." Second sentence asserts diagnostic-format conformance:
   "Diagnostics are written to stderr in the canonical
   `<path>:<line>:<col>: <severity>: <message>` form that
   `cmd/beanprice` and `cmd/beancheck` emit, via
   `ast.Diagnostic.String`."
2. Worked TOML example, 10-15 lines, mirroring the Architecture
   section of this document: one `[[importer]]` kind=csv with a
   half-dozen csvimp body keys plus nested `[[importer.amount]]`,
   one `[[hook]]` kind=classify with a `[[hook.rule]]` block.
   Followed by the canonical invocation
   `beanimport -config config.toml statement.csv`.

The `-h` output additionally enumerates the flag table, the
exit-code table, and a one-line "EXAMPLES" block — mirroring
`cmd/beanprice/main.go`'s `printUsage` shape.

##### Test set (binding)

All tests live in `cmd/beanimport/main_test.go` (or sibling
`_test.go` files in the same package, at the implementer's
discretion). Each MUST exist with the listed name and pin the
listed property.

End-to-end tests using the in-tree `csv` kind (no fake
registration needed because csvimp's `init()` registers it):

- `TestRun_SingleInstanceSmoke` — minimal config + tiny CSV
  fixture written into `t.TempDir()`; assert exit 0 and non-empty
  stdout.
- `TestRun_MissingConfig` — no `-config`; exit 2; stderr contains
  `"-config is required"`.
- `TestRun_ZeroPositional` / `TestRun_TwoPositionals` — exit 2.
- `TestRun_UnknownImporter` — `-importer foo` with a config that
  declares only `bar`; exit 2; stderr contains
  `"unknown importer"`.
- `TestRun_UnknownHook` — `-hook foo` with a config that declares
  only `bar`; exit 2; stderr contains `"unknown hook"`.
- `TestRun_ImporterIdentifyFalseWithFlag` — config with one csv
  shape whose `match` regex does NOT match the input file's name;
  `-importer NAME` forces it. Assert exit 0 (Extract still
  succeeds), and stderr contains both the Warning code
  `beanimport-identify-forced` and the substring "extracting
  anyway".
- `TestRun_StrictPromotesWarningToExit1` — same fixture run
  twice: without `-strict` → exit 0; with `-strict` → exit 1.
  Stderr text is identical between the two runs (only the exit
  code differs).
- `TestRun_AccountHintPlumbed` — csv shape with `account =
  "Assets:FromShape"`; invocation passes `-account Assets:FromCLI`;
  parse stdout and assert at least one Posting carries
  `Assets:FromCLI`. (Pins the end-to-end Hint > shape precedence
  PR-α landed in csvimp.)
- `TestRun_HookFlag_CommaAndRepeatCompose` — config declares
  hooks A, B, C; invocation passes `-hook B,A -hook C`; assert
  Chain ran them in the order B, A, C (verified by reading the
  emitted directives or a sentinel diagnostic; the implementer
  picks the cheapest observable).
- `TestRun_PluginFlag_CommaInPathIsLiteral` — pass `-plugin
  no,such.so`; assert exit 2 with stderr naming `"no,such.so"`
  literally (proves comma is not split).
- `TestRun_CancelledContext` — pass an already-cancelled `ctx`;
  exit 1; stderr contains code `beanimport-cancelled`.

Loader-focused tests (drive `loadConfig` directly because the
exhaustive error-message grid would otherwise require dozens of
end-to-end fixtures — falls under the CLAUDE.md test exception
for package-internal building blocks with independent contract
value):

- `TestLoadConfig_HappyPath` — two `[[importer]]` + one
  `[[hook]]` (using the real `csv` and `classify` kinds) →
  expected counts and declaration-order `Name()` sequence.
- `TestLoadConfig_UnknownTopLevelKey` — toplevel `importers = []`
  → error containing `"unknown top-level key"` and the key name.
- `TestLoadConfig_UnknownBodyKey` — csvimp entry with a key
  csvimp does not consume (e.g. `bogus = "x"`) → error containing
  `"unknown body key"` and the key name.
- `TestLoadConfig_MissingKind` / `TestLoadConfig_MissingName` —
  each entry-shape error reported with the offending entry index.
- `TestLoadConfig_FactoryError` — register a same-package fake
  factory whose decode callback returns an error; assert the
  error is wrapped with the `[[importer]] #<idx> (<name>): `
  prefix.
- `TestLoadConfig_NoImporterEntries` — `[[hook]]`-only config →
  error `"no [[importer]] entries"`.

Helper tests:

- `TestSelectHooks_Subset` — declaration order is preserved when
  `names` is a contiguous prefix.
- `TestSelectHooks_ReorderFromArgs` — passing names in reverse
  order returns them in reverse order (pins user controls
  execution order, not declaration order).
- `TestSelectHooks_Unknown` — returns error of form
  `"unknown hook %q"`.
- `TestSelectHooks_NilAndEmpty` — both nil and `[]string{}`
  return the input slice unchanged.
- `TestPrintDiagnostics_Format` — one diagnostic of each severity;
  assert the emitted bytes equal `diag.String() + "\n"` per line.
  (Pins the contract that we delegate to `ast.Diagnostic.String`
  and add nothing.)

In-process fake factories used by `TestLoadConfig_FactoryError`
and by `TestRun_HookFlag_CommaAndRepeatCompose` are registered
in `init()` of an `_test.go` file under fresh kind names (e.g.
`"_beanimport_fake_imp"`, `"_beanimport_fake_hook"`) to avoid
colliding with later test runs.

#### Suggested Internals

These are recommendations. The implementer may adopt, modify, or
replace them based on what they discover while coding; nothing in
this section is binding because none of it leaks across the
binary's externally observable surface.

**File layout.** Recommend four files: `main.go` (package doc,
`main`, `run`, `runOptions`, flag parsing, `printUsage`),
`config.go` (`loadConfig` and TOML-typed helper structs),
`pipeline.go` (`selectHooks`, dispatch/extract/chain sequence
factored out of `run` if `run` gets long), `diag.go`
(`printDiagnostics` and shared diagnostic-code constants
`codeIdentifyForced`, `codeCancelled`, `codeExtract`,
`codeHook`).

**stringSlice helper.** Copy `cmd/beanprice/main.go:51-54`'s
`stringSlice` verbatim for `-plugin` (no comma split). For
`-hook`, a second type `commaSlice` whose `Set` splits on `,`
and appends non-empty segments. Alternative: one type with a
boolean flag — uglier, no real saving.

**Input.Opener.** Construct the closure as
`func() (io.ReadCloser, error) { return os.Open(path) }`. The
pre-opened handle from pipeline step 6 is closed immediately
because csvimp calls Opener itself; keeping it around would
leak.

**`loadConfig` shape with `toml.Primitive`.** The
`BurntSushi/toml` library makes per-entry deferred decode
awkward because `Primitive` is opaque. The recommended path:
decode the entire document into a struct whose `Importer` /
`Hook` fields are `[]toml.Primitive`, then for each entry call
`PrimitiveDecode` twice — once into a `{Kind,Name string}`
shim, once into the factory's dest via a closure. **Either
internal shape satisfies the Contract above** — the Contract
specifies what `loadConfig` returns and the errors it produces,
not how it walks the TOML tree.

**Hook subset realisation.** Construct a fresh
`*hook.MapRegistry` via `hook.NewRegistry(selected)` rather
than a filtering wrapper that proxies a parent registry.
`NewRegistry` already enforces the no-duplicates, no-empty-name
invariants and already returns names in the supplied order.

**Cancellation diagnostic placement.** Construct the
cancellation diagnostic at the single end-of-pipeline point
that checks `ctx.Err()`, not at each pipeline step.

**Identify-forced Warning span.** `Span{Start:
ast.Position{Filename: in.Path}}` — no line/column because the
diagnostic refers to the file as a whole. Pins the
`<path>: warning: ...` form rather than `<path>:0:0:`.

#### Alternatives

- **Flag library: stdlib `flag` vs `spf13/cobra` vs `pflag`.**
  stdlib, for consistency with `cmd/beanprice`, `cmd/beancheck`,
  and `cmd/beanfile`. Cobra would import a large dependency for
  a six-flag CLI; pflag would introduce a second flag idiom in
  the repo. Rejected both.

- **TOML strategy: `toml.Primitive` vs two-pass `map[string]any`
  vs per-entry text slice.** Primitive. Preserves numeric type
  fidelity, carries `Undecoded()` through to per-entry
  strict-key checks, and gives each factory the structured-decode
  shape PR-α already defined. `map[string]any` forces every
  factory to re-implement type coercion; text-slice needs raw
  byte ranges that `BurntSushi/toml` does not expose. Cite
  `pkg/distribute/route/routeconfig/routeconfig.go:38-56` for
  `meta.Undecoded()` precedent only — `routeconfig` does a
  single-pass full decode and does not use Primitive itself; the
  per-entry deferred-decode pattern is a fresh introduction
  justified by the factory-dest opacity.

- **`-hook` parsing: comma-only vs repeat-only vs both.** Both.
  Comma-only loses on shell quoting; repeat-only is awkward when
  users have a hook list in an env var. Accepting both costs one
  extra unit test and matches the `kubectl --selector` pattern.

- **`-plugin` parsing: same or different.** Different.
  `.so` paths legitimately may contain commas. Comma-splitting
  paths would be a silent footgun. Repeat-only matches beanprice's
  `-plugin` exactly.

- **Hook subset realisation: filtered Registry wrapper vs fresh
  NewRegistry.** Fresh NewRegistry. The wrapper would duplicate
  the validation NewRegistry already does.

- **`-importer NAME` + Identify=false: Warning+continue vs
  Error+halt vs CLI-error.** Warning+continue. Passing
  `-importer` is itself the override; treating Identify-false as
  fatal contradicts the user's stated intent. Silently extracting
  without any signal would hide the mismatch; the Warning leaves
  an audit trail. Unknown-name stays exit 2 (CLI failure).

- **`-strict` semantics: rewrite-at-emission vs
  exit-code-mapping.** Exit-code mapping, copying
  `cmd/beanprice/main.go:362-382`'s `report()` shape. Preserves
  the Diagnostic record verbatim for downstream tooling.

- **Diagnostic formatter: copy beanprice's `formatDiagnostic` or
  call `ast.Diagnostic.String()`.** Call `String()`.
  `ast.Diagnostic.String()` (`pkg/ast/ast.go:83-96`) already
  produces the identical greppable shape; the doc comment marks
  the format as part of the diagnostic contract.

- **Empty config (zero importers, zero hooks): error or proceed.**
  Error at config-load (`"no [[importer]] entries"`, exit 2). A
  config with no importers cannot produce any directive; running
  the pipeline would print nothing and exit 0 — indistinguishable
  from success on real work.

- **Plugin load failures: continue with remaining or abort on
  first.** Abort on first failure, exit 2. Matches beanprice's
  posture; a partially-loaded plugin set produces a fragile
  registry whose downstream errors would be confusing.

- **Positional input must exist: open-then-error vs
  stat-then-error.** Open. One syscall; the OS error message
  already names the path. Stat-then-open introduces a TOCTOU race
  for no benefit.

#### Recommendation

Adopt the contract above: a `package main` binary structured
after `cmd/beanprice` with `run(ctx, args, stdout, stderr) int`
as the testable entry; stdlib `flag` with the six-flag table; a
`toml.Primitive`-based `loadConfig` that hands each entry's body
to `importer.New` / `hook.New` via a `meta.PrimitiveDecode`
closure and enforces strict per-entry keys via the post-decode
`meta.Undecoded()` re-check; a fresh `*hook.MapRegistry`
constructed from `selectHooks` output for `-hook` subsetting;
the `-importer NAME` override emits a Warning when Identify
fails and proceeds to Extract; `-strict` translates only at
exit-code mapping; diagnostics are rendered via
`ast.Diagnostic.String()`; exit codes follow the 0/1/2 table
verbatim. csvimp's `-account` Hint plumbing and the rowhash
godoc are out of scope per the survey.

### Step β-2 — Integration fixtures and goplug fixture importer

**Functional requirements:**

- `cmd/beanimport/testdata/` gains directories, each holding a
  `config.toml`, an input CSV (or fixture), and a golden
  stdout/stderr pair, covering:
  - `multi_instance/`: two `[[importer]]` kind=csv entries with
    overlapping `match` regexes — declaration-first selection
    asserted on stdout, and a disjoint-regex variant asserted
    separately. This is THE PR-β behavioural test.
  - `single_instance/`: minimal smoke fixture; one importer, no
    hooks.
  - `with_classify/`: one importer + one `[[hook]]` kind=classify
    carrying a `[[rule]]` list.
  - `account_hint/`: single shape whose `account` is overridden by
    `--account` on the CLI.
  - `strict_warning/`: a fixture whose Extract produces a Warning;
    verifies bare run is exit 0 and `--strict` flips to exit 1, with
    identical stderr.
  - `plugin/`: invokes a fixture importer registered by a goplug
    `.so`.
- `cmd/beanimport/testdata/staticimporter/` (new package, modelled on
  `cmd/beanprice/testdata/staticquoter/`): a buildable goplug plugin
  that registers a new importer kind, used by the `plugin/` fixture.
- Diagnostic stderr lines exactly match the `ast.Diagnostic.String()`
  / beanprice format (one grep matches both tools' output).
- Multi-instance fixture's `[[importer]]` ordering exercises non-lex
  order (e.g. declare `zzz` before `aaa`) to pin declaration-order
  semantics, not lexicographic accident.

**Modules:**

- `cmd/beanimport/main_test.go` (new) — table-driven over
  `testdata/` subdirectories, modelled on
  `cmd/beanprice/main_test.go`.
- `cmd/beanimport/testdata/...` (golden files + configs + inputs).
- `cmd/beanimport/testdata/staticimporter/{doc.go,plugin.go,BUILD.bazel}`
  (new).

**Verification:**

- `bazel test //cmd/beanimport:beanimport_test --test_output=errors`.
- `bazel test //... --test_output=errors` must remain green.
- Plugin fixture compiled and loaded via `go_binary` with
  `linkmode = "plugin"` (mirror staticquoter's BUILD layout).

**Quality requirements:**

- Each fixture directory contains a short `README.md` (one
  paragraph) stating what property it pins, so a future reader can
  prune duplicates safely.

#### Detailed Design

##### Scope decision (binding — narrower than high-level β-2 plan)

After β-1 shipped, the in-memory `cmd/beanimport/main_test.go`
already pins these via fakes:
TestRun_SingleInstanceSmoke, TestRun_AccountHintPlumbed,
TestRun_StrictPromotesWarningToExit1,
TestRun_ImporterIdentifyFalseWithFlag,
TestRun_HookFlag_CommaAndRepeatCompose, TestRun_FailFast (6
exit-2 paths), and TestRun_CancelledContext.

β-2 drops the redundant 4 fixtures from the high-level plan
(`single_instance/`, `account_hint/`, `with_classify/`,
`strict_warning/`) and ships ONLY:

1. `cmd/beanimport/testdata/multi_instance/` — the headline PR-β
   behavioural assertion. Two `[[importer]] kind="csv"` shapes
   with overlapping `match` regexes and non-lex declared names,
   pins that Dispatch picks the first-declared instance.
2. `cmd/beanimport/testdata/plugin/` — proves
   `goplug.Load → InitPlugin → importer.RegisterFactory`
   end-to-end against a real `.so`. Cannot be tested in-memory.
3. `cmd/beanimport/testdata/staticimporter/` — the buildable
   goplug plugin package backing fixture #2, mirroring
   `cmd/beanprice/testdata/staticquoter/` shape.

**Assertion mechanism**: `strings.Contains` on stdout/stderr +
exit-code checks. NO golden files (matches `cmd/beanprice`
precedent; avoids `-update` mechanism + printer-output churn).

**β-1 in-memory tests**: ALL stay. β-2 only adds tests.

##### Files added

```
cmd/beanimport/testdata/multi_instance/config.toml
cmd/beanimport/testdata/multi_instance/statement.csv
cmd/beanimport/testdata/multi_instance/README.md
cmd/beanimport/testdata/plugin/config.toml
cmd/beanimport/testdata/plugin/statement.csv
cmd/beanimport/testdata/plugin/README.md
cmd/beanimport/testdata/staticimporter/doc.go
cmd/beanimport/testdata/staticimporter/plugin.go
cmd/beanimport/testdata/staticimporter/BUILD.bazel
cmd/beanimport/fixture_test.go
```

`cmd/beanimport/BUILD.bazel` is amended (see §BUILD wiring). No
β-1 file is modified or deleted.

##### Fixture #1 — `testdata/multi_instance/`

`config.toml` declares exactly two `[[importer]]` entries, both
`kind = "csv"`, in this order (the non-lex ordering is part of
the Contract):

- entry #1: `name = "zzz_first"`, `match = ".*\\.csv$"`,
  `account = "Assets:First"`
- entry #2: `name = "aaa_second"`, `match = ".*statement.*"`,
  `account = "Assets:Second"`

Lexicographic sort would put `aaa_second` first; declaration
order MUST place `zzz_first` first. Both regexes fire against
the input `statement.csv` so both shapes' `Identify` returns
true; Dispatch picks the first by declaration order.

Both entries set the minimum csvimp keys:
`kind = "csv"`, `date_col = "Date"`,
`date_format = "2006-01-02"`, `default_currency = "USD"`, and
one `[[importer.amount]]` sub-table with `col = "Amount"`.

`statement.csv` is a single clean row producing zero csvimp
diagnostics:

```
Date,Amount
2024-01-15,-4.50
```

`README.md` is one paragraph: "Pins that `importer.Apply`
(Dispatch) picks the first-declared `[[importer]]` instance when
multiple shapes' `match` regexes all fire, regardless of name
lexicographic order. The instance names are deliberately non-lex
(`zzz_first` before `aaa_second`) so that any future regression
toward lexicographic sort fails this test."

##### Fixture #2 — `testdata/plugin/`

`config.toml`:

```toml
[[importer]]
kind = "static"
name = "fixture"
```

No body keys. The plugin factory consumes the body via
`decode(&struct{}{})` so loadConfig's strict-key check does not
fire on an unused entry body.

`statement.csv`: one-row placeholder (static importer ignores
it):

```
Date,Amount
2024-01-15,0.00
```

`README.md` is one paragraph: "Pins that `-plugin PATH.so` loads
a goplug plugin whose `InitPlugin()` registers a new importer
kind, which is then usable in the same run via the normal
`[[importer]]` declaration path."

##### `cmd/beanimport/testdata/staticimporter/` — plugin package

Layout: `doc.go`, `plugin.go`, `BUILD.bazel` (3 files).

`doc.go` carries the package doc and nothing else, modelled
verbatim on `cmd/beanprice/testdata/staticquoter/doc.go` (goplug
fixture for `cmd/beanimport`'s integration test; registers
importer kind `"static"`; not a production importer).

`plugin.go` is `package main` and exports:

- `const pluginName = "staticimporter"` — Manifest-facing name;
  the registered kind is `"static"` (separate string).
- `var Manifest = goplug.Manifest{
      APIVersion: goplug.APIVersion,
      Name:       pluginName,
      Version:    "v0.0.0-fixture",
  }`
- `func InitPlugin() error` — calls
  `importer.RegisterFactory("static", importer.FactoryFunc(newStatic))`,
  returns nil. Registration happens here (not in `init()`) so
  goplug's APIVersion check can veto an incompatible plugin
  before any side effect.
- `func main() {}` — required by `-buildmode=plugin`, never
  called.
- `func newStatic(name string, decode func(dest any) error)
  (importer.Importer, error)` — decodes the empty body
  (`decode(&struct{}{})`), returns `&staticImp{name: name}, nil`.
- `type staticImp struct { name string }` implementing
  `importer.Importer`:
  - `Name() string` → `s.name`
  - `Identify(ctx, in) bool` → `true` unconditionally (sole
    importer in the fixture; Dispatch picks it regardless)
  - `Extract(ctx, in) (importer.Output, error)` returns Output
    with exactly one `*ast.Transaction`:
    - `Date`: `time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)`
    - `Flag`: `'*'`
    - `Payee`: `"static-fixture"`
    - `Postings`: two `Posting`:
      - `Account = "Assets:Static"`, Amount with
        `Number = apd.New(1, 0)`, `Currency = "USD"`
      - `Account = "Equity:Other"`, `Amount = nil`
        (auto-balanced)
    - Empty `Diagnostics`.

**Pinned sentinel strings** the test asserts on:
`"Assets:Static"` and `"static-fixture"`. Implementers MUST NOT
rename these without updating `fixture_test.go` in the same
change.

##### `testdata/staticimporter/BUILD.bazel`

Mirrors staticquoter file-for-file with name substitutions:

```python
load("@rules_go//go:def.bzl", "go_binary")
load("//pkg/ext/goplug:fixture_constraints.bzl", "PLUGIN_COMPATIBLE_PLATFORMS")

# gazelle:ignore

go_binary(
    name = "staticimporter",
    srcs = [
        "doc.go",
        "plugin.go",
    ],
    cgo = True,
    gotags = ["testhelpers"],
    linkmode = "plugin",
    out = "staticimporter.so",
    target_compatible_with = PLUGIN_COMPATIBLE_PLATFORMS,
    testonly = True,
    visibility = ["//cmd/beanimport:__pkg__"],
    deps = [
        "//pkg/ast",
        "//pkg/ext/goplug",
        "//pkg/importer",
        "@com_github_cockroachdb_apd_v3//:apd",
    ],
)
```

The `load(...)` line and the `gazelle:ignore` directive are
load-bearing. Implementer MUST verify staticquoter's
`PLUGIN_COMPATIBLE_PLATFORMS` load path; if it differs in the
repo, use the same path staticquoter uses.

##### `cmd/beanimport/BUILD.bazel` amendment

`go_test` gains a `data` attribute that bundles the testdata
tree, with a platform `select` that adds the built `.so` only on
plugin-capable platforms:

```python
go_test(
    name = "beanimport_test",
    srcs = [
        "fixture_test.go",
        "main_test.go",
    ],
    data = glob(
        ["testdata/**"],
        exclude = ["testdata/staticimporter/**"],
    ) + select({
        "@platforms//os:linux":   ["//cmd/beanimport/testdata/staticimporter"],
        "@platforms//os:macos":   ["//cmd/beanimport/testdata/staticimporter"],
        "@platforms//os:freebsd": ["//cmd/beanimport/testdata/staticimporter"],
        "//conditions:default":   [],
    }),
    embed = [":beanimport_lib"],
    deps = [
        "//pkg/ast",
        "//pkg/importer",
        "//pkg/importer/hook",
    ],
)
```

The `exclude` excises the plugin's source files from the
runfiles glob; the `select(...)` adds the built `.so` as a data
dep on platforms that can produce it. Implementer MUST verify
cmd/beanprice's BUILD.bazel uses an equivalent pattern; if it
uses a different platform-constraint idiom, prefer that for
consistency.

##### Test surface — `cmd/beanimport/fixture_test.go`

`package main`. Two test functions pinned by name and behaviour:

```go
func TestFixture_MultiInstance(t *testing.T)
func TestFixture_Plugin(t *testing.T)
```

Both invoke `run(ctx, args, &stdout, &stderr)` (the β-1 entry
point; unchanged). Both use `context.Background()`.

`TestFixture_MultiInstance` MUST:

1. Resolve the fixture dir via runfiles env
   (`TEST_SRCDIR`, `TEST_WORKSPACE`); skip with message naming
   `bazel test //cmd/beanimport:beanimport_test` when either
   unset.
2. Build args `["-config", <dir>/config.toml,
   <dir>/statement.csv]`.
3. Assert `run() == 0`. On failure, log both stdout and stderr.
4. Assert `strings.Contains(stdout, "Assets:First")`.
5. Assert `!strings.Contains(stdout, "Assets:Second")` — second
   shape MUST never run because Dispatch picked the first.

`TestFixture_Plugin` MUST:

1. Skip when `runtime.GOOS` not in {linux, freebsd, darwin}.
2. Skip when `TEST_SRCDIR`/`TEST_WORKSPACE` unset.
3. Skip when `staticimporter.so` does not exist at the resolved
   path (defensive — covers `bazel test` on an unsupported
   platform that slipped past the GOOS guard).
4. Build args `["-config", <dir>/config.toml, "-plugin",
   <soPath>, <dir>/statement.csv]`.
5. Assert `run() == 0`.
6. Assert `strings.Contains(stdout, "Assets:Static")` AND
   `strings.Contains(stdout, "static-fixture")`.

Skip messages MUST name `bazel test
//cmd/beanimport:beanimport_test` verbatim so a developer running
`go test` immediately knows the invocation that satisfies the
test.

Plugin runfiles path:
`filepath.Join(srcDir, workspace, "cmd", "beanimport",
"testdata", "staticimporter", "staticimporter.so")` — identical
pattern to `cmd/beanprice/main_test.go:339`.

##### Verification

- `bazel build
  //cmd/beanimport/testdata/staticimporter:staticimporter`
  produces `staticimporter.so` on linux/macos/freebsd.
- `bazel test //cmd/beanimport:beanimport_test
  --test_output=errors` passes;
  `TestFixture_MultiInstance` and `TestFixture_Plugin` both
  green (the latter skipped on unsupported GOOS is fine).
- `bazel test //... --test_output=errors` remains green; no β-1
  test is touched.
- Each fixture directory contains a `README.md`.

#### Suggested Internals

- **Test file split.** `fixture_test.go` separate from
  `main_test.go`. Different cognitive loads (testdata-driven vs
  in-memory fakes); separation enables
  `grep -r TestFixture_ cmd/beanimport` to find runfiles-based
  tests.
- **Two helpers vs one.** `fixtureDir(t, name)` and
  `staticImporterPath(t)`. Plugin path also needs GOOS guard +
  file-exists check; one polymorphic helper would take a boolean
  flag, uglier than duplication.
- **Sentinel string choice.** `"Assets:Static"` + payee
  `"static-fixture"` are obviously synthetic; `git blame` reader
  will not confuse them with a real importer artefact.
- **Canned-directive content.** Minimum that `printer.Fprint`
  renders to non-empty stdout: one `Transaction` with payee + two
  postings (one with amount, one auto-balanced).
- **Regex form in `multi_instance/`.**
  `match = ".*\\.csv$"` on entry #1 and
  `match = ".*statement.*"` on entry #2. Both fire for
  `statement.csv`; both regexes are realistic config shapes.
- **Static importer's Identify body.** Return `true`
  unconditionally. Sole importer in the fixture; hardcoded `true`
  keeps the plugin trivially small.
- **`InitPlugin` factory wiring.** Register inside `InitPlugin`
  (not in `init()`). goplug ABI guarantees `InitPlugin` is called
  exactly once after Manifest validation; using `init()` would
  register before goplug's version check could veto an
  incompatible plugin.
- **TOML body shape in `plugin/config.toml`.** Empty body, factory
  decodes into `&struct{}{}`. Alternative noop key would be
  pointless surface.

#### Alternatives

- **Golden-file assertions vs `strings.Contains`.** Contains-style
  (per user scope decision; matches `cmd/beanprice` precedent;
  avoids `-update` mechanism and printer-whitespace churn).
- **Real-csv `multi_instance/` vs in-memory fakes.** Real csv.
  In-memory `TestRun_SingleInstanceSmoke` proves "one csv importer
  works" but NOT the overlap-selection rule. Fakes-only test of
  overlap would not exercise real csvimp Identify path.
- **Standalone `staticimporter` vs reuse `staticquoter`.**
  Standalone. A quoter `api.Source` cannot satisfy
  `importer.Importer` (different methods/return types). Reuse
  structurally impossible.
- **Skip vs fail when runfiles env unset.** Skip with message
  naming the bazel command. `go test` users do not have
  `TEST_SRCDIR`/`TEST_WORKSPACE`; failing would force everyone
  onto `bazel test`.
- **Manifest field values.** Pin
  `APIVersion: goplug.APIVersion`, `Name: "staticimporter"`,
  `Version: "v0.0.0-fixture"` — same convention staticquoter
  uses.
- **Both `multi_instance/` shapes have non-empty `match`.**
  Without `match`, csvimp's `Identify` returns true after the
  column-presence check; both shapes pass identically and the
  test would still pass but would not document the regex-overlap
  property the README claims.
- **Static importer's Identify returning true unconditionally.**
  In a single-importer config, Dispatch picks the sole instance
  regardless. Returning `false` would mask future bugs where
  someone adds a second importer to the plugin fixture and forgets
  to update Identify.
- **Empty TOML body for plugin fixture + `decode(&struct{}{})`.**
  Defensive against someone adding a body key later without
  updating the factory; `meta.Undecoded()` would catch the new
  key as `"unknown body key"`.
- **Whether to retire any β-1 in-memory test.** Keep all (per
  user scope decision). β-1 tests cover failure paths fixtures
  cannot reach (cancelled context, forced Identify, fake-hook
  composition order).
- **Test file naming: `fixture_test.go` vs `golden_test.go`.** No
  golden files; `golden_test.go` would mislead future readers.

#### Recommendation

Ship exactly two fixtures (`multi_instance/`, `plugin/`) plus the
`staticimporter` plugin package. `multi_instance/` is the only
end-to-end demonstration of Phase 8's headline behavioural
property (declaration-order Dispatch over overlapping regex
matches); `plugin/` is the only path that can exercise
`goplug.Load` against a real `.so`. Both omit golden files in
favour of small `strings.Contains` checks, matching
`cmd/beanprice` precedent. β-1 in-memory tests stay because they
cover failure modes — cancelled context, forced Identify,
fake-hook composition order — that no fixture can reach. The
static importer is deliberately single-purpose (one canned
Transaction, two pinned sentinel strings) so the assertion
surface is small and the fixture cannot silently become a
parallel test framework over time.

### Step β-3 — Docs polish

**Functional requirements:**

- `PLAN.md` (project root): replace the PR #83-era Phase 8 sketch
  with a brief pointer to this document (final state) and a short
  paragraph documenting the new `cmd/beanimport` CLI surface (flags,
  exit codes, one-line example).
- This document's `## Out of scope` rewritten: move "PR-β CLI",
  "schema reshape", "goplug plugin fixture", "--account Hint",
  "rowhash godoc fix" from out-of-scope/PR-β-deferred into the
  in-scope/completed list, leaving Phase 8.1, Phase 8.2, the
  PLAN.md historical rewrite, and cross-source dedup as
  out-of-scope.

**Modules:**

- `PLAN.md`.
- `docs/plans/phase-8-framework-redesign.md` (`## Out of scope` and
  any directly contradictory wording near the PR-β section).

**Verification:** plan diff reviewed for completeness; no
compile/test surface.

**Quality requirements:** matches the style of the existing plan
documents (heading nesting, factual tone, no implementation
narration).

### PR-β alternatives discussed

- **TOML decode strategy.** Three options were considered:
  - (a) single-pass with `toml.Primitive` so each `[[importer]]` body
    becomes a deferred-decode handle that the CLI wraps in a
    `func(dest any) error` closure;
  - (b) two-pass — decode to `map[string]any` then re-marshal each
    entry body for the factory's structured decode;
  - (c) per-entry text-slice + `toml.Unmarshal`.

  (a) preserves `meta.Undecoded()` semantics for unknown-key
  rejection and lets each factory enforce its own strict-key policy
  via the same decode call PR-α already accepts; it is the pattern
  `pkg/distribute/route/routeconfig` already establishes. (b) loses
  type fidelity (numbers become `int64`/`float64` ambiguously) and
  forces every factory to tolerate `any`. (c) needs the raw byte
  range of each entry, which `BurntSushi/toml` does not expose
  cleanly. **Choose (a)**, with the codebase precedent as a
  load-bearing argument.

- **`--account` Hint key naming.** Options: a bare `"account"`
  string key, `"default_account"`, or a typed enum constant in
  `pkg/importer`. The Hints map is `map[string]any` and PR-α did not
  introduce a typed-key API; introducing one for a single key would
  be over-design. `"default_account"` overstates the semantics — the
  Hint is an override, not a default. **Choose `"account"`**,
  documented as a reserved key on `importer.Input.Hints` so future
  hint keys are namespaced deliberately rather than ad hoc.

- **`--importer NAME` semantics when the named instance fails
  Identify.** Options: (i) force-extract regardless and emit a
  Warning diagnostic; (ii) refuse with an Error diagnostic, exit 1;
  (iii) refuse with a CLI-level error, exit 2. By passing
  `--importer`, the user is explicitly overriding Dispatch;
  treating Identify-false as fatal contradicts that intent. But
  silently extracting when the importer self-reports "I cannot
  handle this" hides a real problem. **Choose (i)**: emit a Warning
  diagnostic ("Identify returned false; extracting anyway because
  --importer was set"), run Extract, and let Extract's own
  structural errors surface naturally. An Identify-false plus a
  successful Extract is then auditable in stderr. The unknown-name
  case stays exit 2 (CLI failure).

- **`--strict` semantics.** Options: rewrite each Warning to Error
  at the moment of emission, or keep severities intact and translate
  at exit-code mapping. The latter preserves the Diagnostic record
  for downstream tooling and matches the pattern already in
  `cmd/beanprice` and `cmd/beancheck`. **Choose the latter**, copy
  beanprice's `report()` shape.

- **Where csvimp reads `in.Hints["account"]`.** Options: Identify,
  Extract, or both. Identify's job is shape-recognition (extension +
  match regex + required columns); whether the user named an account
  doesn't change recognition. Extract is where the account is
  written into directives. Reading the Hint in Identify would couple
  a recognition signal to a substitution value for no benefit.
  **Choose Extract only.** Document the precedence: Hint > shape
  `account`; empty Hint falls through to shape.

- **Single-instance vs multi-instance CLI fixture.** Multi-instance
  is THE behaviour PR-β exists to prove; without an integration test
  for it, the framework redesign's value isn't demonstrated
  end-to-end. A single-instance smoke test documents the simpler
  usage pattern most users actually hit. **Keep both**:
  `single_instance/` as the smoke / documentation fixture,
  `multi_instance/` as the property-pinning fixture with both
  overlapping and disjoint `match` variants. Cost is negligible;
  coverage is asymmetric — losing either weakens a distinct
  guarantee.

### PR-β recommendation

Adopt the path above: `Primitive`-based single-pass TOML loader
(precedent: `routeconfig`); bare `"account"` Hint key read only in
csvimp's Extract; `--importer NAME` overrides Dispatch with a Warning
when Identify says false; `--strict` translates at exit-code mapping
only; two CLI fixtures (single + multi), with the multi-instance
fixture exercising both overlapping and disjoint `match` regexes in
non-lex declaration order; goplug fixture importer mirroring
beanprice's staticquoter layout. β-1 keeps the CLI structurally
close to `cmd/beanprice` so the two binaries share idiom and
diagnostic format; β-2 pins the behaviours that justify PR-α's
existence; β-3 finalises the paper trail without rewriting history.
No PR-α API is touched.
