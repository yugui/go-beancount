# Enable `options_coverage` parse-tier fixture in beancompat

## Goal

Activate the upstream `parse/options_coverage` containment fixture in
`pkg/compat/beancompat`. The fixture asserts 31 `option` keys with mixed
kinds (strings, bools, decimals, string lists, int, decimal-map,
int-map). Today only 5 keys are registered, the serializer never iterates
`ledger.Options`, and three kinds (int, decimal-map, int-map) are
unsupported in the option registry. After this work the fixture passes
under containment matching, the denylist entry is removed from both Go
and Python mirrors, and behavior-bearing options (`display_precision`,
`inferred_tolerance_default`, `tolerance_multiplier`, `render_commas`,
`booking_method`) get real consumers where feasible — the remaining
options register with upstream defaults and TODO arch-doc entries for
future consumer wiring.

## Scope

**Included:**
- New option kinds: int, decimal-map, int-map.
- `Snapshot()` accessor on `ledger.Options` so the serializer can
  iterate without hand-coding per-key emission.
- Generic option-emission path in `serializeOptions`.
- Spec registration for all 31 expected keys with upstream defaults.
- Real consumers for `display_precision` (formatter), and design
  attempts for `inferred_tolerance_default`, `tolerance_multiplier`,
  `render_commas`, `booking_method` (each falling back to storage-only
  with TODO if design ripples too widely).
- Removing `options_coverage` from `pkg/compat/beancompat/denylist.go`
  and `pkg/compat/beancompat/pyharness/denylist.py`.
- Updating `docs/architecture/display-precision.md` to record what was
  implemented and to consolidate outstanding TODOs.

**Excluded (TODOs after this work):**
- Account-type classification subsystem (consumer for `name_*`).
- Derived-account computation (consumer for `account_previous_*`,
  `account_current_*`, `account_unrealized_gains`).
- Rounding-error plugin (consumer for `account_rounding`).
- Document discovery (consumer for `documents`).
- Conversion logic (consumer for `conversion_currency`).
- Parse-time commodity enumeration (populates `commodities`).
- v2 `plugin` option form (v3 directive form is the supported path).
- Deprecated parser flags (`allow_pipe_separator`,
  `allow_deprecated_none_for_tags_and_links`).
- `long_string_maxlines` parser warning.
- Python-specific `insert_pythonpath`, `pythonpath` (no Go analog).

## Context

`pkg/compat/beancompat/denylist.go` skips the upstream `options_coverage`
parse-tier fixture (reason: "go-beancount fix pending: parse-tier serializer
does not yet emit the options envelope (~30 BeancountOptions keys expected)").
The mirror Python denylist
(`pkg/compat/beancompat/pyharness/denylist.py`) carries the same entry.

The serializer at `pkg/compat/beancompat/serialize.go:109-132` currently
emits only the derived `display_precision_by_currency` view from
`PrecisionProfile`; it never iterates `ledger.Options`. Only 5 of the
31 expected options are registered today
(`pkg/ast/optvalues.go:202-237`).

`docs/architecture/display-precision.md:299-329` records
`options_coverage.json` as future scope and stipulates the
option-vs-derived-view split that must be preserved.

Matching is **containment**, so emitting a defaulted value for every
expected key is sufficient for the fixture itself. For several options
the original "storage-only" categorization was challenged on the
grounds that upstream semantics were not investigated first. The next
section captures that investigation; the per-option strategy then drives
the commit plan.

## Phase 0 — Upstream investigation findings

Source: beancount master, `beancount/parser/options.py`.

| Key | Upstream default | Upstream consumer (cited) | Go consumer today | Strategy |
|---|---|---|---|---|
| `title` | `""` | None (informational) | None | **Already registered**. No change. |
| `operating_currency` | `[]` | Validation / reports (Fava) | None | **Already registered**. No change. |
| `inferred_tolerance_multiplier` | `D("0.5")` | DEPRECATED upstream — aliased to `tolerance_multiplier` | `pkg/validation/internal/tolerance` | **Already registered & consumed**. Hold semantics; alias considered in commit 6. |
| `infer_tolerance_from_cost` | `False` | `pkg/validation/internal/tolerance` | Consumed | **Already registered & consumed**. No change. |
| `plugin_processing_mode` | `"default"` | Plugin runner | `pkg/loader/loader.go:138` | **Already registered & consumed**. No change. |
| `name_assets` … `name_expenses` (5) | `"Assets" … "Expenses"` | `get_account_types()` | None | **Storage-only**. Account-type classification subsystem not present. TODO. |
| `account_previous_balances` / `_earnings` / `_conversions` | constants | `get_previous_accounts()` | None | **Storage-only**. No derived-account computation. TODO. |
| `account_current_earnings` / `_conversions` | constants | `get_current_accounts()` | None | **Storage-only**. Same. TODO. |
| `account_unrealized_gains` | `"Earnings:Unrealized"` | `get_unrealized_account()` | None | **Storage-only**. No unrealized-gains plugin. TODO. |
| `account_rounding` | `None` | Rounding-error plugin | None | **Storage-only** (default `""` in Go). TODO. |
| `booking_method` | `Booking.STRICT` (enum) | Open-directive default booking | `pkg/ast` resolves booking per Open directive; no global default | **Design attempt** — wire as fallback in lower-pass when Open omits `booking`. Storage-only fallback if it ripples. |
| `conversion_currency` | `"NOTHING"` | Conversion-currency / zero-rate logic | None | **Storage-only**. TODO. |
| `inferred_tolerance_default` | `{}` | Per-currency tolerance default in validation | None | **Design + implement** — extend `pkg/validation/internal/tolerance`. |
| `display_precision` | `{}` | `dcontext` / formatter | None | **Design + implement** — wrap `PrecisionProfile` with override layer, plumb through formatter's `DisplayContext`. |
| `tolerance_multiplier` | `D("0.5")` | Aliased to `inferred_tolerance_multiplier` | Aliased name not present | **Design attempt** — alias semantics in `set()`. Storage-only fallback. |
| `render_commas` | `False` | Number printer thousands-separator | None | **Design attempt** — thread into `pkg/format` / printer. Storage-only fallback. |
| `commodities` | `set()` (OUTPUT — computed) | Auto-populated by parser | None | **Storage-only emit `[]`**. Parse-time scan deferred. TODO. |
| `plugin` | `[]` (OUTPUT — v2 captured form) | Plugin runner | v3 directive is supported path | **Storage-only emit `[]`**. TODO. |
| `documents` | `[]` | Document-discovery search paths | None | **Storage-only**. TODO. |
| `pythonpath` | NOT in v3 options.py — Fava key only | n/a | n/a | **Storage-only emit `[]`**. Fava-compat. |
| `insert_pythonpath` | `False` | Python sys.path manipulation | n/a (Go) | **Storage-only**. Python-specific. |
| `long_string_maxlines` | `64` | Parser warning threshold | None | **Storage-only**. TODO. |
| `allow_deprecated_none_for_tags_and_links` | `False` | Parser accepts `None` for tags/links | n/a (Go parser never accepted) | **Storage-only**. |
| `allow_pipe_separator` | `False` | Parser accepts `|` separator | n/a (Go parser uses comma) | **Storage-only**. |

### Calibration

For containment matching, the fixture only checks emission. The
storage-only rows are deliberate: each has either no Go analog, or its
consumer would require a substantial new subsystem. Documenting them
in `docs/architecture/display-precision.md` preserves design intent
without bloating this work. The "design + implement" / "design
attempt" rows are where real behavior change happens.

## Steps (one commit each)

### Step 1 — New kinds + `Snapshot` iteration

**Files:** `pkg/ast/optvalues.go`, `pkg/ast/optvalues_test.go`.

Functional requirements:
- Add `kindInt`, `kindDecimalMap`, `kindIntMap` to the registry.
- Parsers: `parseIntOption` (base-10), `parseDecimalMapEntry`
  ("KEY:value" → `(string, *apd.Decimal)`), `parseIntMapEntry`
  ("KEY:value" → `(string, int)`).
- Accessors: `Int(key) int`, `DecimalMap(key) map[string]*apd.Decimal`
  (fresh shallow copy, decimals cloned), `IntMap(key) map[string]int`
  (fresh copy).
- `set()` accumulates map entries across multiple `option` directives;
  last-write-wins per sub-key.
- `Snapshot() []OptionEntry` returns entries sorted by key.
  `OptionEntry` exposes the discriminator via methods; the internal
  `kind` enum stays unexported.

Verification: `bazel test //pkg/ast/...` — per-kind parse success and
error tests, accumulation tests, clone-isolation, `Snapshot()` order
and per-kind values for set and unset keys.

Quality: brief godoc on each exported symbol per project Go style;
unexported helpers documented only when non-obvious.

### Detailed Design

#### Contract

**New kinds (exported enum).** `kind` is renamed and exported because step 2's serializer must switch on it:

```go
// OptionKind classifies how an option's raw value is parsed, stored,
// and serialized. Callers (in particular beancompat) switch on
// OptionKind to dispatch formatting.
type OptionKind int

const (
    KindString OptionKind = iota
    KindBool
    KindDecimal
    KindStringList
    KindInt
    KindDecimalMap
    KindIntMap
)
```

The existing unexported `kind` identifier and its constants (`kindString`, `kindBool`, `kindDecimal`, `kindStringList`) are renamed to the exported form throughout `pkg/ast`. No other public surface changes.

**Accessor methods on `*OptionValues`.** Two new accessors are added; their contract mirrors the existing four (nil-safe, panic on unregistered key, return registered default when unset, return fresh copies for non-scalar kinds):

```go
// Int returns the integer value for key.
func (v *OptionValues) Int(key string) int

// DecimalMap returns a fresh map keyed by the option's sub-key. The
// returned map and every *apd.Decimal value are fresh copies; callers
// may mutate them without affecting stored state. Returns an empty
// (non-nil) map when nothing has been set and the registered default
// is empty.
func (v *OptionValues) DecimalMap(key string) map[string]*apd.Decimal

// IntMap returns a fresh map keyed by the option's sub-key. The
// returned map is a fresh copy; callers may mutate it without
// affecting stored state. Returns an empty (non-nil) map when nothing
// has been set and the registered default is empty.
func (v *OptionValues) IntMap(key string) map[string]int
```

`Int` returns `0` when neither a value nor a non-nil default is registered. `DecimalMap` and `IntMap` always return a non-nil map (possibly empty) so the serializer can render `{}` without a nil check.

**`OptionEntry` and `Snapshot()`.** This is the surface step 2 binds to.

```go
// OptionEntry is one option's snapshot at the time Snapshot was called.
// The Kind field tells the caller which typed accessor method returns
// a meaningful value; all other accessors return the zero value for
// their type.
//
// Map and slice accessors return fresh copies; mutating them does not
// affect the OptionValues the entry came from.
type OptionEntry struct {
    Key  string
    Kind OptionKind
    // unexported value storage
}

func (e OptionEntry) String() string
func (e OptionEntry) Bool() bool
func (e OptionEntry) Decimal() *apd.Decimal
func (e OptionEntry) StringList() []string
func (e OptionEntry) Int() int
func (e OptionEntry) DecimalMap() map[string]*apd.Decimal
func (e OptionEntry) IntMap() map[string]int

// Snapshot returns one OptionEntry per registered key, in ascending
// key order. Keys that were never set are included with their
// registered default. Map and slice values inside each entry are
// fresh copies. Snapshot on a nil *OptionValues returns the defaults
// for every key in the default registry.
func (v *OptionValues) Snapshot() []OptionEntry
```

Notes that are part of the contract:

- Every registered key appears in `Snapshot()` exactly once, whether set or not. This is what makes containment matching work for the upstream fixture: step 2 emits a defaulted value for every expected key.
- Ordering is ascending Unicode code-point order on `Key` (`sort.Strings` semantics).
- The `String()` method on `OptionEntry` does **not** implement `fmt.Stringer`'s convention of returning a human display form — it returns the **stored string-kind value**, or `""` when `Kind != KindString`. This collision with the conventional `String() string` method is a documented wart. The method is godoc'd to that effect, and `OptionEntry` is not passed to `fmt`-family formatters in normal use. If it becomes a problem, the method can be renamed to `StringValue()` in a future change without breaking the `Snapshot()` shape.
- Accessors that don't match the entry's kind return the zero value of their return type without panicking.

**Parsers.** Three new package-level parser functions, signatures matching the existing parsers (`func(raw string) (any, error)`):

```go
// parseIntOption parses raw as a base-10 signed integer with
// surrounding whitespace trimmed. Returns int.
func parseIntOption(raw string) (any, error)

// parseDecimalMapEntry parses raw as "KEY:value" where value is an
// apd.Decimal. Returns an unexported map-entry helper consumed by
// set(). Errors when the separator is missing, KEY is empty, or
// value fails decimal parsing.
func parseDecimalMapEntry(raw string) (any, error)

// parseIntMapEntry parses raw as "KEY:value" where value is a base-10
// integer. Same error conditions as parseDecimalMapEntry plus integer
// parse failure.
func parseIntMapEntry(raw string) (any, error)
```

**`set()` behavior for the new kinds.**

- `KindInt`: parses, last-write-wins (same as scalar kinds).
- `KindDecimalMap` / `KindIntMap`: parses one entry from `"KEY:value"`, merges into the stored map. Same sub-key set twice across directives: second wins. Across the ledger, the final map is the union of all sub-keys, each holding its last-written value. Empty sub-key or missing separator surfaces as a parse error (`ParseOptions` records as diagnostic; map unchanged for that directive).
- The existing contract for unknown top-level keys (silently ignored) is preserved.

**Separator choice.** `:` for both map kinds. Consistent with step 4's `"USD:0.01"` form and with beancount's amount-and-currency surface.

#### Suggested Internals

The implementer is free to deviate.

- **Storage.** Keep the existing `values map[string]any` shape and store `int` directly, `map[string]*apd.Decimal` and `map[string]int` as their concrete typed maps.
- **Map merge in `set()`.** Mirror the `KindStringList` branch: cast the per-directive parsed entry to an internal `mapEntry` struct, fetch (or lazily create) the typed map from `v.values[key]`, write the sub-key.
- **`OptionEntry` value storage.** A single `value any` field plus the `Kind` discriminator; each accessor type-asserts; wrong-kind cases return the zero value. `Snapshot` is not hot.
- **`Snapshot` implementation.** `make([]string, 0, len(v.reg.specs))`, append all spec keys, `sort.Strings(keys)`, then for each key call the appropriate accessor to populate the entry. Reuses existing clone-on-read semantics.
- **Nil-`*OptionValues` Snapshot.** Route through `defaultRegistry` the same way `lookupSpec` does.
- **Separator helper.** A tiny `splitMapEntry(raw string) (key, value string, err error)` shared by both map parsers, then dispatch value half to `parseDecimalOption` / `parseIntOption`.
- **Test layout.** Extend `testRegistry` with one spec per new kind. Mirror existing test shapes (defaults, parse success, parse error, accumulation, clone-isolation). Add `TestSnapshotOrderAndKinds` covering: every registered key present, ascending order, `Kind` matches spec, wrong-kind accessors return zero, returned-map mutation does not affect next snapshot.

#### Alternatives discussed

1. **`OptionEntry` shape.** Visitor interface (rejected: ceremony exceeds the safety win for one call site). `Value() any` + `Kind()` (rejected: pushes type switch to every caller, no compile-time check). **Adopted: flat struct + `Kind OptionKind` + typed accessors** — cleanest `switch e.Kind` dispatch Go offers; cost is `String()` method shadowing `fmt.Stringer`, mitigated by docs.
2. **`OptionKind` exported vs unexported.** Unexported with `IsX()` methods (rejected: ugly if-else dispatch, same churn to add kinds). **Adopted: exported enum + exported constants.**
3. **Map accessor return: copy vs read-only view.** Read-only wrapper or `iter.Seq2` (rejected: callers want `m["USD"]` and `len(m)`). **Adopted: fresh shallow copy with cloned decimals** (matches existing `StringList`/`Decimal` convention).
4. **Sort eagerness.** Registry-side maintained order (rejected: zero amortized win for a one-shot snapshot consumer). **Adopted: sort inside `Snapshot()`.**
5. **Separator.** `=` (no beancount precedent), whitespace (collides with multi-word values). **Adopted: `:`** — matches step 4's documented form, matches beancount currency syntax.

#### Recommendation + rationale

Flat-struct `OptionEntry` with exported `OptionKind` enum gives step 2's serializer a single clean `switch e.Kind` dispatch with no `any` in user code. The `String()` Stringer-collision is a documented mild wart, not a real defect. Internal storage, parser return-value packaging, and the `mapEntry` helper layout are left in the suggestion layer.

### Step 2 — Rewrite `serializeOptions` to iterate via `Snapshot`

**Files:** `pkg/compat/beancompat/serialize.go`,
`pkg/compat/beancompat/serialize_test.go`.

Functional requirements:
- Iterate `ledger.Options.Snapshot()`; format each entry via a new
  internal `formatOptionValue` helper.
- Per-kind formatting: strings → JSON string; bools → JSON bool; ints
  → bare integer; decimals → `apd.Decimal.String()` as JSON string;
  string lists → JSON array (nil → `[]`); decimal maps → sorted JSON
  object with string values; int maps → sorted JSON object with
  bare-int values. Use `marshalSortedObject` for stable byte output.
- Continue to emit `display_precision_by_currency` from
  `PrecisionProfile` only when the profile has observations; the
  envelope itself becomes unconditional.

Verification: `bazel test //pkg/compat/beancompat/...` — per-kind
formatting tests, both `display_precision_by_currency` branches,
empty-map renders as `{}`; existing fixtures stay green under
containment.

Quality: keep serialize_test.go organized by kind; resist coupling
serializer logic to particular keys.

### Step 3 — Storage-only spec registrations (batch)

**Files:** `pkg/ast/optvalues.go`, `pkg/ast/optvalues_test.go`.

Functional requirements: register the storage-only options listed
below with their upstream defaults. Each spec carries a one-line godoc
citing upstream and noting the deferral rationale.

Options registered:
- Account names: `name_assets`, `name_liabilities`, `name_equity`,
  `name_income`, `name_expenses`.
- Derived-account references: `account_previous_balances`,
  `account_previous_earnings`, `account_previous_conversions`,
  `account_current_earnings`, `account_current_conversions`,
  `account_unrealized_gains`, `account_rounding`.
- Python-only / Fava: `pythonpath`, `insert_pythonpath`.
- Deprecated parser flags: `allow_pipe_separator`,
  `allow_deprecated_none_for_tags_and_links`.
- Other deferred: `conversion_currency`, `commodities`, `plugin`
  (option form), `documents`, `long_string_maxlines`.

Verification: per-spec default-value subtest in
`TestDefaultRegistryKeys`. `bazel test //...` clean;
`options_coverage` still SKIPped.

Quality: defaults cite upstream `beancount/parser/options.py`.

### Step 4 — `display_precision` option + `DisplayContext` wrapper

**Files:** `pkg/ast/optvalues.go`, `pkg/ast/precision_profile.go` (or
sibling), `pkg/format/...`, related tests.

Implements `docs/architecture/display-precision.md` §"Option-driven
override".

Functional requirements:
- Register `display_precision` as `kindIntMap`, default empty.
  Parser `parseDisplayPrecisionEntry`: `"CCY:DECIMAL"` (e.g.
  `"USD:0.01"`); the integer stored is the fractional-digit count
  derived from the example decimal's exponent.
- Exported wrapper type that decorates a `*PrecisionProfile` with
  overrides from `OptionValues.IntMap("display_precision")`.
  `Precision(currency) (int, bool)` returns the override when set and
  delegates otherwise.
- Wire the wrapper at the single formatter construction site for
  `formatopt.Options.DisplayContext`.
- Serializer requires no change: `display_precision` emits from the
  registry IntMap path; `display_precision_by_currency` still emits
  from raw `PrecisionProfile`.

Verification: parser digit derivation (`"0.01"` → 2, `"1"` → 0,
`"0.0005"` → 4); wrapper precision returns override vs delegation;
formatter golden showing override changes USD rendering.

Quality: respect the option-vs-derived-view split documented in the
arch-doc. No collapse of the two surfaces into one.

### Detailed Design

#### Contract

**Option registration** (in `pkg/ast/optvalues.go::defaultRegistry`):
- Key: `"display_precision"`. Kind: `KindIntMap`. Parser: `parseDisplayPrecisionEntry`. Default: `map[string]int(nil)`.

**Parser `parseDisplayPrecisionEntry`** signature `func(raw string) (any, error)`:
1. Split on first `:` via `splitMapEntry`. Missing separator or empty sub-key → error.
2. Parse value half with `apd.BaseContext.NewFromString` after `TrimSpace`. On apd error → wrap.
3. Reject NaN, Infinity, negative, **zero** values with a clear message.
4. Compute digit count as `max(0, -int(d.Exponent))`.

Locked edge cases:

| Input | Result |
|---|---|
| `"USD:0.01"` | `("USD", 2)` |
| `"USD:1"` | `("USD", 0)` |
| `"USD:0.0005"` | `("USD", 4)` |
| `"USD:1.5"` | `("USD", 1)` |
| `"USD:1E-3"` | `("USD", 3)` |
| `"USD:1.50"` | `("USD", 2)` |
| `"USD:0"` | error (degenerate) |
| `"USD:-0.01"` | error |
| `"USD:NaN"` / `"USD:Inf"` | error |
| `"USD"` / `":0.01"` | error |
| `"  USD : 0.01  "` | `("USD", 2)` (trimmed) |

**Wrapper type** in `pkg/ast/precision_profile.go`:
```go
// DisplayPrecisionContext combines an inferred *PrecisionProfile with
// per-currency overrides from option "display_precision".
type DisplayPrecisionContext struct {
    Profile   *PrecisionProfile
    Overrides map[string]int
}

func (c *DisplayPrecisionContext) Precision(currency string) (int, bool)
```

`Precision` returns the override entry when present, otherwise delegates to `Profile.Precision`. Nil-safe on a nil receiver. The zero value is usable.

**Constructor** in `pkg/ast/precision_profile.go`:
```go
// LedgerDisplayContext returns a DisplayContext combining the ledger's
// observed PrecisionProfile with overrides from option "display_precision".
// When overrides are empty, returns ledger.PrecisionProfile typed as the
// interface (no wrapper allocation; byte-identical formatter behavior in
// the no-option case). Nil ledger returns nil.
func LedgerDisplayContext(ledger *Ledger) interface {
    Precision(currency string) (int, bool)
}
```

Locked behavior:
- `LedgerDisplayContext(nil)` → nil interface.
- Empty overrides → returns `ledger.PrecisionProfile` as the interface; no wrapper allocation.
- Non-empty overrides → `*DisplayPrecisionContext{Profile: ledger.PrecisionProfile, Overrides: ledger.Options.IntMap("display_precision")}`. The `IntMap` returns a fresh copy, safe to retain.

**Serializer** in `pkg/compat/beancompat`: NO change. Step 2's generic `KindIntMap` dispatch already handles it. `display_precision_by_currency` continues to flow from `PrecisionProfile` independently.

**Wiring location**: There are no production callers of `format.WithDisplayContext` today (only test files). Step 4 does NOT modify existing call sites. `LedgerDisplayContext` is the canonical entry point for any future internal caller (CLI, etc.). The arch-doc records this as the implemented "Option-driven override" wiring.

#### Suggested Internals

- **Parser decomposition**: a small helper `digitsFromDecimal(d *apd.Decimal) (int, error)` for the NaN/Inf/negative/zero rejection plus the `max(0, -int(d.Exponent))` clamp. Inline if it feels like overkill for one site.
- **Wrapper internal layout**: flat struct with exported fields documented as "read-only after construction." Alternative (unexported fields + explicit constructor) closes mutation but adds ceremony without real safety win.
- **Identity short-circuit**: in `LedgerDisplayContext`, check `len(overrides) == 0` after `IntMap()`; if zero, return bare profile typed as interface. Preserves identity-equality for the no-option path.
- **Wrapper location**: `pkg/ast/precision_profile.go` keeps override + bookkeeping bookended in one file.
- **Tests**:
  - `optvalues_test.go`: parser edge-case table (all 12 rows above), plus accumulation test (two directives, two currencies).
  - `precision_profile_test.go`: `TestDisplayPrecisionContext` covering override-hit, miss-delegates, nil-profile, nil-receiver, empty-overrides-returns-bare-profile.
  - `pkg/format/format_displaycontext_test.go`: end-to-end golden with `option "display_precision" "USD:0.01"`.
  - `pkg/printer/printer_displaycontext_test.go`: parallel test for AST printer.
- **Documentation**: update `docs/architecture/display-precision.md` §"Option-driven override" to record this as implemented and name `DisplayPrecisionContext` / `LedgerDisplayContext`.

#### Alternatives discussed

1. **Parser semantics**: regex on textual form (rejected: duplicates decimal parsing, mishandles scientific notation), accept zero as 0 (rejected: zero is not a precision *example*; better to surface as typo).
2. **Wrapper shape**: constructor returning interface (rejected: hides type unnecessarily, tests can't reach fields), inline glue without exported type (rejected: future callers re-implement). **Adopted: exported struct + helper constructor.**
3. **Wiring location**: per-callsite wrap (rejected: silent future bypass), centralize in `ast.Ledger.DisplayContext()` (rejected: inverts package dependency — formatter would need to import `ast`). **Adopted: `LedgerDisplayContext` helper in `pkg/ast`** — data and constructor co-located, interface-only coupling preserved.
4. **Plan's "single construction site" phrasing**: that site does not exist in production code; this step provides the canonical helper instead of modifying nonexistent wiring. The arch-doc is updated accordingly.

#### Recommendation + rationale

Register `display_precision` as `KindIntMap` with `parseDisplayPrecisionEntry`; export `DisplayPrecisionContext` struct in `pkg/ast/precision_profile.go`; provide `LedgerDisplayContext` as the canonical wiring helper. The identity short-circuit preserves byte-identical output for current consumers (no re-golden needed). The option-vs-derived-view split is honored at every surface. The parser's zero-rejection is the only contentious call; it costs the user nothing (write `"USD:1"` to mean zero fractional digits) and catches a class of typo.

### Step 5 — `inferred_tolerance_default` + validation integration

**Files:** `pkg/ast/optvalues.go`, `pkg/validation/internal/tolerance/...`,
related tests.

Functional requirements:
- Register `inferred_tolerance_default` as `kindDecimalMap`, default
  empty.
- Extend the tolerance package to consult the per-currency default
  when no posting-level tolerance is inferred. Precedence:
  posting-level inference > per-currency `inferred_tolerance_default`
  > registered fallback.

Verification: balance assertion with `option
"inferred_tolerance_default" "USD:0.005"` accepts a 0.005 imbalance
that otherwise fails.

Quality: keep the precedence chain explicit and testable.

### Detailed Design

#### Contract

**No exported API changes in `pkg/validation/internal/tolerance`.** The four existing functions (`Infer`, `ForAmount`, `ForBalanceAssertion`, `Within`) keep their current signatures and continue to take `*ast.OptionValues`. The new map is read internally via `opts.DecimalMap("inferred_tolerance_default")`. External callers (`pkg/validation/validations/transaction_balances.go`, `pkg/validation/balance/plugin.go`, the loader) require no edits.

**Precedence chain (locked) for each residual currency `cur`:**

1. **Posting-level inference present** → use that (posting/amount exponent × `inferred_tolerance_multiplier`, optionally maxed with cost-side via `infer_tolerance_from_cost`). Per-currency default NOT consulted, regardless of magnitude.
2. **Posting-level inference absent** → consult `inferred_tolerance_default[cur]`. If entry exists and value is **positive**, use it.
3. **Zero or negative entry** → treated as absent; fall through.
4. **Map miss** → existing fallback (zero `*apd.Decimal`), unchanged.

**Per-function application:**
- `Infer`: per-currency default applies for residual currencies without a contributing posting (where the existing path emits `new(apd.Decimal)`).
- `ForAmount` / `ForBalanceAssertion`: behavior unchanged. Posting-level inference is always present (the amount itself yields an exponent), so per-currency default is unreachable for these.

**Deliberate divergence from upstream beancount.** Upstream's `get_balance_tolerance` consults the per-currency default for balance assertions whose asserted amount has zero fractional digits, even though posting-level inference (`2 × 0.5 × 1 = 1`) is computable. Step 5 honors the plan's precedence chain ("posting-level > per-currency default") uniformly across all three computation functions and does NOT replicate upstream's integer-assertion nuance. The divergence is documented in `pkg/validation/internal/tolerance/doc.go`. The fixture this step services (`parse/options_coverage`) only checks emission, not this behavioral nuance; the divergence is reopenable in a follow-up if a real consumer needs it.

#### Suggested Internals

- **Splice site**: inside `Infer`'s per-currency loop (currently around `tolerance.go:91-98`). Replace the `new(apd.Decimal)` zero-emission branch with a small helper that consults the per-currency default first.
- **Helper shape**: hoist `opts.DecimalMap(...)` once before the loop (it clones), then `lookupDefault(defaults, cur)` per currency — a small gate enforcing "non-nil, non-zero, non-negative". Use `(*apd.Decimal).IsZero()` and `(*apd.Decimal).Sign() < 0` for consistency with existing checks.
- **Helper placement**: unexported, adjacent to `Infer`. No new file.
- **No change to `ForAmount` / `ForBalanceAssertion`** per Contract.
- **Doc.go update**: a paragraph documenting the precedence chain, zero/negative→absent semantics, and the upstream divergence.
- **Tests** (`pkg/validation/internal/tolerance/tolerance_test.go`):
  1. Per-currency default applies when no posting for that currency.
  2. Posting-level inference wins over per-currency default when both exist.
  3. Zero entry ignored (falls through).
  4. Negative entry ignored.
  5. Missing currency falls through.
  6. (No new tests for `ForAmount` / `ForBalanceAssertion` — behavior unchanged.)
- **Integration test**: one loader-level test (in `pkg/validation/balance/plugin_test.go` or a sibling) exercising the option-directive → diagnostic path. If awkward to set up, a transaction-level test using `asttest.MustOptions` is sufficient.
- **Out-of-scope guard**: do NOT pre-emptively consult `tolerance_multiplier` (Step 6's territory).

#### Alternatives discussed

1. **How the option flows in**: (a) status-quo `*ast.OptionValues` plumbing, (b) introduce a `Config` struct precomputed per ledger, (c) pass full `*ast.Ledger`. **Adopted (a)** — zero caller churn, symmetric with how the other two tolerance knobs are read today; one map clone per `Infer` is amortized over all currencies.
2. **Precedence semantics**: plan's chain vs upstream-faithful (integer-assertion case consults per-currency default even when posting-level inference is computable). **Adopted plan's chain** — simple, monotonic, predictable; upstream nuance is a documented divergence reopenable later if a real consumer reports it.
3. **Zero/negative entry handling**: treat as literal zero ("exact" tolerance) vs treat as absent (fall through). **Adopted absent** — avoids typo trapdoors; literal "0" is observationally identical to "unset" under the existing fallback; consistent with `digitsFromDecimal`'s zero-rejection.

#### Recommendation + rationale

A 4-line change inside `Infer`'s per-currency loop plus a small helper, a `doc.go` paragraph, and 5-6 unit tests + one integration test. The public surface is unchanged; the new map is consumed exactly where the existing zero-fallback fires; the chain is honored verbatim. The integer-assertion divergence from upstream is documented as a deliberate Step-5 trade. Zero/negative entries are absent to keep "set" / "unset" semantics monotonic.

### Step 6 — `tolerance_multiplier` alias semantics (design attempt)

**Files:** `pkg/ast/optvalues.go`, optionally
`pkg/validation/internal/tolerance/...`, related tests.

Upstream defines `inferred_tolerance_multiplier` as DEPRECATED, aliased
to `tolerance_multiplier`. Both keys appear in the options dict
independently.

Design attempt:
- Register `tolerance_multiplier` as `kindDecimal`, default
  `apd.New(5, -1)` ("0.5").
- In `set()`, when the user sets either `tolerance_multiplier` or
  `inferred_tolerance_multiplier`, the value propagates to both keys
  via a small alias map at the registry layer. Validation continues
  reading `inferred_tolerance_multiplier`; behavior is unchanged
  unless the user sets `tolerance_multiplier`, in which case the
  alias propagates.
- Fallback: register `tolerance_multiplier` as storage-only with
  arch-doc TODO and a `known_divergences` entry.

Verification: setting `tolerance_multiplier` changes the validation
tolerance the same way setting `inferred_tolerance_multiplier` does;
both keys appear in the serialized envelope with the same value.

Quality: the alias mechanism must be small and easy to extend if
upstream adds more deprecated aliases.

### Step 7 — `booking_method` (open-directive default)

**Files:** `pkg/ast/optvalues.go`, `pkg/ast/lower.go`, related tests.

Functional requirements:
- Register `booking_method` as `kindString`, default `"STRICT"`.
- In Open-directive lowering: when source omits the booking keyword,
  apply `OptionValues.String("booking_method")` instead of the
  hardcoded default.
- Fall back to storage-only if reducer / validation tests reveal
  cross-cutting changes; document the deferral.

Verification: an Open directive without explicit booking adopts the
configured default; setting `booking_method` to `"NONE"` propagates.

Quality: lower-pass change should be local; if it isn't, fall back.

### Step 8 — `render_commas` formatter integration (design attempt)

**Files:** `pkg/ast/optvalues.go`, `pkg/format/...`, related tests.

Functional requirements:
- Register `render_commas` as `kindBool`, default `false`.
- Thread the value through `pkg/format` / printer surface (add a
  `RenderCommas bool` field on `formatopt.Options` or equivalent and
  consult it in the number-formatting path).
- Fall back to storage-only with arch-doc TODO if the printer surface
  is too entangled.

Verification: printer golden showing `1,000.00` vs `1000.00` toggled
by the option.

Quality: if the printer's number formatter is shared across many
paths, prefer the storage-only fallback over a broad refactor.

### Step 9 — Denylist removal + arch-doc update

**Files:** `pkg/compat/beancompat/denylist.go`,
`pkg/compat/beancompat/pyharness/denylist.py`,
`docs/architecture/display-precision.md`.

Functional requirements:
- Remove `options_coverage` from both denylists.
- Update arch-doc: mark `options_coverage` implemented; mark
  `DisplayContext` override implemented; consolidate the
  storage-only TODOs into a clearly-labeled section for future work.

Verification: `bazel test
//pkg/compat/beancompat:parse_fixtures_test
--test_filter=TestParseFixtures/options_coverage` PASSES; full
`bazel test //...` clean.

Quality: arch-doc reads as a current snapshot of what is implemented
vs deferred; readers can pick up TODOs incrementally.

## Alternatives discussed

- **Bulk storage-only**: register all 31 keys as inert defaults and
  flip the denylist immediately. Rejected — it short-changes options
  with viable Go-side consumers (display_precision,
  inferred_tolerance_default) and creates technical debt the
  arch-doc already calls out.
- **Implement every consumer**: real semantic wiring for all 31 keys.
  Rejected — several keys would require entirely new subsystems
  (account classification, derived-accounts, conversion logic,
  document discovery) that are far out of scope for fixture
  enablement.
- **Per-key denylist instead of fixture-level**: maintain a finer
  denylist that lets us land partial coverage. Rejected — adds
  denylist surface area and doesn't materially shorten the path to
  the same end state.

## Recommended approach

The nine-commit structure above. Mechanical foundations land first
(kinds, generic serializer, storage-only batch); behavior-bearing
options follow one per commit so reviews stay narrow and any
design-attempt fallback to storage-only is visible in the commit
history. The denylist removal and arch-doc update land last as a
single self-contained commit.

## Critical files

- `pkg/ast/optvalues.go` — kinds, parsers, accessors, `Snapshot`,
  all spec registrations.
- `pkg/ast/optvalues_test.go` — tests across steps 1, 3, 4–8.
- `pkg/ast/precision_profile.go` — `DisplayContext` wrapper (step 4).
- `pkg/compat/beancompat/serialize.go` — `serializeOptions` rewrite
  (step 2; lines 109-132).
- `pkg/compat/beancompat/serialize_test.go` — per-kind envelope tests.
- `pkg/compat/beancompat/denylist.go` — drop entry (step 9).
- `pkg/compat/beancompat/pyharness/denylist.py` — drop entry (step 9).
- `pkg/format/...` — formatter integration (steps 4 and 8).
- `pkg/validation/internal/tolerance/{tolerance.go,doc.go}` —
  per-currency tolerance (step 5) and alias semantics (step 6).
- `pkg/ast/lower.go` — `booking_method` fallback (step 7).
- `docs/architecture/display-precision.md` — running TODO log; final
  update step 9.

## Verification (cumulative)

1. Step 1: `bazel test //pkg/ast/...` — new kind tests pass.
2. Step 2: `bazel test //pkg/compat/beancompat/...` — per-kind
   serializer tests pass; other fixtures still green.
3. Step 3: `bazel test //...` clean; `options_coverage` still SKIPped.
4. Steps 4–8: targeted test in the affected package + `bazel test
   //...`. `options_coverage` still SKIPped.
5. Step 9: targeted fixture filter passes; full `bazel test //...`
   clean.

After Gazelle-sensitive changes, run `bazel run //:gazelle` before
`bazel build` / `bazel test`.

All work happens on branch `claude/enable-options-coverage-nTayc`.
