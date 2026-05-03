# `beanfile`: Directive Distribution CLI (Phase 7.5)

This document specifies `cmd/beanfile` and the supporting `pkg/distribute/*`
libraries. It belongs between Phase 7 (`pkg/quote`) and Phase 8 (`pkg/importer`)
in the overall roadmap (see [PLAN.md](../PLAN.md)).

## 1. Motivation

Phase 7 delivered an end-to-end story for *producing* directives — `pkg/quote`
fetches commodity and FX prices and emits `ast.Price` records, and `cmd/beanprice`
prints them to a deduplicated stream. Phase 8 will deliver `pkg/importer`, which
turns CSV/OFX feeds into `ast.Transaction` records. Neither layer knows where in
a multi-file ledger the directive should land. PLAN.md assigns that
responsibility to `bean-daemon` (Phase 10), but a long-running daemon is heavy
for batch contexts (CI jobs, nightly cron, one-off imports), and Phase 7's
`pricedb` deliberately stops short of writing to disk.

`beanfile` fills this gap as an offline, stateless CLI:

- **Inputs**: a stream of beancount directives on stdin or in files.
- **Output**: edits to one or more ledger source files, plus a status report.
- **Behavior**: each directive is routed to a destination file by convention
  (or configuration), merged into that file in date order while preserving
  surrounding formatting, deduplicated against the existing ledger, and
  optionally written as a commented-out marker when an active equivalent
  already exists elsewhere.

The `pkg/distribute/*` libraries are designed so `bean-daemon`'s eventual
`POST /directives` endpoint can reuse the same routing/merging code in-process.

### Out of scope

- Live services, file watching, locking — owned by `bean-daemon` (Phase 10).
- Multi-file atomic transactions (a crash mid-run may leave some destinations
  written and others not). Each individual destination write is atomic via
  temp file + rename.
- Auto-injecting `include` directives when new destination files are created.
  Users are expected to use a glob include in their root file (or add includes
  manually).

## 2. User-facing specification

### Invocation

```
beanfile [flags] --ledger ROOT.beancount [files...]
```

- `--ledger PATH` is **required**. Used to walk the `include` transitive
  closure and build the dedup index (active + commented-out directives).
- Positional `files...` are read in order. With no positional args, or with a
  single `-`, input comes from stdin.
- Destination root defaults to the directory containing `--ledger`; override
  with `--root`.

### Standard convention

| Directive kind | Routing key | Default destination |
|---|---|---|
| `Open`, `Close`, `Pad`, `Balance`, `Note`, `Document`, `Transaction` | account | `transactions/<account-segments>/<YYYYmm>.beancount` |
| `Price` | commodity | `quotes/<COMMODITY>/<YYYYmm>.beancount` |
| `Option`, `Plugin`, `Include`, `Pushtag`, `Poptag`, `Event`, `Query`, `Custom`, `Commodity` | — | not routable |

`Assets:Foo:Bar:Baz` becomes the path segment `Assets/Foo/Bar/Baz`. The
`<YYYYmm>` token is the directive's date formatted under the configured
file pattern (`YYYY` / `YYYYmm` / `YYYYmmdd`).

Non-routable directives produce an **error by default**. The
`--pass-through` flag changes this to "emit on stdout, preserving input order".

### Transaction routing override

A `Transaction` touches multiple accounts, so the routing key has to be
chosen. The order of resolution is:

1. The transaction-level metadata `route-account: "Assets:Foo:Bar"` (string) —
   the value is taken as the destination account verbatim.
2. The first posting whose metadata contains `route-account: TRUE` (bool) —
   that posting's account is used.
3. The configured `default_strategy` (`first-posting`, `last-posting`,
   `first-debit`, or `first-credit`).
4. Fallback: the first posting's account.

The metadata key is configurable (`override_meta_key`, default `route-account`).
Whatever key is in effect is **stripped from the emitted directive** on every
output, on both the transaction and the postings.

### Dedup: three-way decision per input directive

For each directive `D` whose routing destination is `P`:

1. If `P` already contains an equivalent directive — active **or**
   commented-out — `D` is **skipped**. The skip is counted but the index is
   not modified.
2. Else, if any **active** equivalent of `D` exists at any path other than
   `P` (i.e., the transaction has already been filed somewhere else in the
   ledger), `D` is written to `P` as a **commented-out** directive. This
   handles the common practice of leaving a `; ...` marker on accounts that
   participate in a transaction but are not the canonical home of it.
3. Otherwise, `D` is written to `P` as a normal active directive.

Equivalence is OR-combined from two rules:

- **AST equality** via `go-cmp`, with `Span`/line/column information, the
  origin filename, the `route-account` metadata, and the commented-out flag
  excluded from the comparison.
- **Metadata-key equality**: for each entry in the resolved
  `equivalence_meta_keys` list, both directives carry that key with equal
  values. Useful when an upstream importer already stamps a stable
  `import-id` or similar.

The dedup index is also updated as directives are accepted within a single
invocation, so duplicates within the input stream are themselves skipped.

### Merge semantics

- Files that don't exist are created, including parent directories.
- Existing files round-trip via the CST: every byte not covered by a new
  insertion is written back unchanged. Independent comment blocks, blank
  lines, and undated header directives keep their byte-exact relative
  position to surrounding directives.
- Insertion position is determined by binary search on the existing dated
  directives according to the requested order (see §9). The library does
  *not* try to detect the file's implied order; if the user requests
  `ascending` and the file is in fact descending, the search returns *some*
  syntactically valid offset and the new directive lands there. The
  surrounding existing content is never reordered.
- Same-day, same-destination input directives keep their input order.
- Each destination write is atomic: temp file + `fsync` + rename.
- Spacing around new directives honors the resolved
  `blank_lines_between_directives` and `insert_blank_lines_between_directives`
  format options (see §4.4 / §6).

### Stats

On exit, unless `--quiet` is given, each destination file gets one stderr line:

```
beanfile: transactions/Assets/Bank/202401.beancount: written=3 commented=1 skipped=0
beanfile: quotes/JPY/202401.beancount:               written=12 commented=0 skipped=2
beanfile: total: written=15 commented=1 skipped=2 passthrough=0
```

## 3. High-level architecture

```
┌──────────────────────────────────────────┐
│ cmd/beanfile (CLI)                         │
│  flags + config + stdin/file reader        │
│  orchestrator (3-way dedup decision)       │
├──────────────────────────────────────────┤
│ pkg/distribute/route                       │  Directive + Config → Decision
│  - standard convention resolution           │
│  - account-tree / commodity overrides       │
│  - txn strategy + meta override             │
├──────────────────────────────────────────┤
│ pkg/distribute/dedup                       │  Ledger-wide equivalence index
│  - active + commented-out collection        │
│  - InDestination / InOtherActive queries    │
├──────────────────────────────────────────┤
│ pkg/distribute/comment                     │  Commented-out directive parser
│  - "^;[ \t]*\d{4}-\d{2}-\d{2}" recognizer  │
│  - tail-shrinking parse fallback            │
│  - "; "-prefixed emitter                    │
├──────────────────────────────────────────┤
│ pkg/distribute/merge                       │  Plan → File
│  - CST round-trip insertion                 │
│  - order-driven binary search position      │
│  - same-day FIFO + relative-position safety │
│  - atomic write                             │
└──────────────────────────────────────────┘
                 ↓ uses
   pkg/syntax (CST), pkg/ast, pkg/printer, pkg/format, pkg/loader
```

## 4. Package details

### 4.1 `pkg/distribute/route`

```go
type Decision struct {
    Path          string          // routing destination, relative to Config.Root
    Order         OrderKind       // ascending | descending | append
    StripMetaKeys []string        // meta keys removed before printing (route-account)
    EqMetaKeys    []string        // dedup keys for this destination
    Format        []format.Option // resolved formatter options
    PassThrough   bool            // not routable → CLI errors or emits on stdout
}

type OrderKind int // OrderAscending | OrderDescending | OrderAppend

type Config struct {
    Root               string
    Account            AccountSection
    Price              PriceSection
    Transaction        TransactionSection
    Format             FormatSection // global format defaults
    AccountOverrides   []AccountOverride   // longest prefix wins
    CommodityOverrides []CommodityOverride
    PassThrough        bool                // default false
}

type FormatSection struct { // every field optional
    CommaGrouping                     *bool
    AlignAmounts                      *bool
    AmountColumn                      *int
    EastAsianAmbiguousWidth           *int
    IndentWidth                       *int
    BlankLinesBetweenDirectives       *int
    InsertBlankLinesBetweenDirectives *bool
}

type AccountSection struct {
    Template            string   // default "transactions/{account}/{date}.beancount"
    FilePattern         string   // default "YYYYmm"
    Order               string   // default "ascending"
    EquivalenceMetaKeys []string
    Format              FormatSection
}

type PriceSection struct {
    Template            string   // default "quotes/{commodity}/{date}.beancount"
    FilePattern         string
    Order               string
    EquivalenceMetaKeys []string
    Format              FormatSection
}

type TransactionSection struct {
    DefaultStrategy string // first-posting | last-posting | first-debit | first-credit
    OverrideMetaKey string // default "route-account"
}

type AccountOverride struct {
    Prefix              string // "Assets:JP" matches Assets:JP and Assets:JP:*
    Template            string
    FilePattern         string
    Order               string
    TxnStrategy         string
    EquivalenceMetaKeys []string
    Format              FormatSection
}

type CommodityOverride struct {
    Commodity           string // exact match
    Template            string
    FilePattern         string
    Order               string
    EquivalenceMetaKeys []string
    Format              FormatSection
}

func Decide(d ast.Directive, cfg *Config) (Decision, error)
```

- Account overrides match by **longest account-segment prefix**; ties resolve
  in TOML order. `Assets:JP` matches `Assets:JP:Cash` but not `Assets:JPN`.
- Template tokens: `{account}` becomes path segments, `{date}` is formatted
  per the resolved file pattern, `{commodity}` is substituted verbatim.
- `Decide` is pure; it does not touch the filesystem.
- `Format` resolution is field-wise: each `*T` field falls back to its parent
  scope when nil. Resolution order, low to high precedence:
  1. `pkg/format` built-in defaults
  2. `[routes.format]` (TOML global)
  3. `[routes.account.format]` / `[routes.price.format]` (section)
  4. `[[routes.account.override]] [.format]` / `[[routes.price.override]] [.format]`
  5. CLI `--format-*` flags

### 4.2 `pkg/distribute/dedup`

```go
type Index interface {
    // True if path P contains an equivalent directive (active or commented).
    InDestination(path string, d ast.Directive, eqKeys []string) (matched bool, kind MatchKind)
    // True if any path other than P contains an equivalent active directive.
    InOtherActive(path string, d ast.Directive, eqKeys []string) (matched bool, kind MatchKind)
    // Record a directive as "now present at path" so subsequent dedup queries
    // see it. Used between pre- and post-write.
    Add(path string, d ast.Directive, commented bool)
}

type MatchKind int // MatchAST | MatchMeta | MatchNone

func BuildIndex(ctx context.Context, ledgerRoot string, opts ...Option) (Index, error)
```

`BuildIndex` walks the ledger via `pkg/loader.LoadFile`, then for each
member source file:

- Records every active directive under its origin path.
- Reads the raw bytes and runs `pkg/distribute/comment.ExtractCommented` to
  recover commented-out directives, lowering each successful parse to AST and
  recording it under the same path with `commented = true`.

Equivalence:

- **AST equality** uses `go-cmp` with a fixed `cmp.Option` set (`equalityOpts`)
  that ignores `Span`, `Pos`, line/column, the origin filename, the
  `route-account` meta entry, and the `commented` flag.
- **Meta equality** triggers when both directives carry one of the keys in
  `eqKeys` and the values compare equal.
- The two rules are OR-combined; the first match wins and `MatchKind` records
  which rule fired.

Scopes:

- `InDestination` matches against active+commented entries at the same path.
- `InOtherActive` matches active entries at paths other than the given one.
  Commented-out entries elsewhere in the ledger are *not* triggers — they're
  notes, not the canonical record.

Stream-internal dedup falls out naturally: after each accepted write the CLI
calls `Add`, so a subsequent input directive that matches will be skipped.

### 4.3 `pkg/distribute/comment`

```go
type CommentedDirective struct {
    SourcePath string
    StartLine  int
    EndLine    int    // exclusive
    Indent     string // ";" + zero or more ASCII whitespace characters
    Body       []byte // candidate block with the prefix stripped
    Directive  ast.Directive // non-nil iff the body parsed cleanly
}

func ExtractCommented(src []byte, path string) []CommentedDirective
func EmitCommented(w io.Writer, d ast.Directive, prefix string) error
```

Detection rules:

1. A line starts a candidate block when it begins with `;`, optionally
   followed by ASCII whitespace, followed by a `YYYY-MM-DD`-shaped date.
2. The shared *prefix* is the literal `;` plus the run of ASCII whitespace.
3. Subsequent lines belong to the candidate block while they share the same
   prefix. Empty lines, shorter prefixes, or EOF terminate the block.
4. The block's lines (with prefix stripped) are concatenated and parsed via
   `pkg/syntax.Parse`.
5. **Tail-shrink fallback**: if the full block fails to parse, retry with
   `N-1`, `N-2`, … `1` trailing lines removed. The first prefix length that
   parses to at least one directive defines the commented directive; the
   remaining tail lines are treated as ordinary comments. This accommodates
   the common pattern of a commented directive followed by a same-prefixed
   plain-comment annotation:

   ```
   ; 2024-01-15 * "Coffee" "Espresso bar"
   ;   Expenses:Food:Cafe   3.50 USD
   ;   Assets:Cash         -3.50 USD
   ; receipt was scanned 2024-01-16
   ```
6. If no shrink length parses, the entire block is treated as a plain
   comment and contributes nothing to the dedup index.
7. Newlines: both `\n` and `\r\n` are accepted.

`EmitCommented` formats the directive via `pkg/printer`, then prepends every
output line with `prefix` (default `"; "`).

Performance: the worst-case fallback is `O(N)` parses for a single block,
but `N` is the line count of one directive — typically a handful of lines —
and the scan only runs once per ledger build.

### 4.4 `pkg/distribute/merge`

```go
type Plan struct {
    Path    string
    Inserts []Insert // input order preserved
}

type Insert struct {
    Directive     ast.Directive
    Order         OrderKind
    Commented     bool   // true → emit via comment.EmitCommented
    Prefix        string // commented-only, default "; "
    StripMetaKeys []string
    Format        []format.Option
}

type Options struct {
    // Reserved for future per-call overrides.
}

type Stats struct {
    Path      string
    Written   int
    Commented int
    Skipped   int // typically 0; the CLI dedups before calling
}

func Merge(plan Plan, opts Options) (Stats, error)
```

Behaviour:

1. **New file path**: create parent directories, sort the inserts according
   to their `Order`, print them with `printer.Fprint`, and write atomically
   (temp file in the same directory + `fsync` + rename).
2. **Existing file**: read the file, parse it with `pkg/syntax.ParseReader`,
   and walk the top-level children. Each directive node's date is extracted
   via `Node.FindToken(syntax.DATE)`. Undated directives (file headers) act
   as fixed anchors and are excluded from the date search.
3. **Insertion offset** per insert is decided by binary search; see §9.
   Same-date inserts to the same path collapse onto the same offset and are
   emitted in input order.
4. **Patch composition**: rather than mutate the CST, the implementation
   builds a list of `(byte_offset, text)` patches against the original file
   bytes, then writes the file by interleaving the original bytes with the
   patches in offset order. This guarantees byte-exact preservation of every
   region not covered by an insertion — independent comment blocks, blank
   lines, undated headers, and any unusual formatting all stay put.
5. **Spacing around new directives** is governed by the resolved
   `BlankLinesBetweenDirectives` (`N`) and `InsertBlankLinesBetweenDirectives`
   (`B`) format options. The merger inspects the trailing newline run of the
   bytes immediately before the insertion offset and the leading newline run
   of the bytes immediately after:
   - **`B = true`**: ensure exactly `N` blank lines (i.e. `N+1` newlines) on
     each side, padding the *new* side. The existing surrounding bytes are
     never modified.
   - **`B = false`**: keep the existing whitespace as-is; only ensure that at
     least one newline separates new from neighbour. Excess blank lines on
     the existing side are preserved (no normalization).
   - File start and end lack one of the two sides; behave accordingly.
6. **Atomic write**: temp file in the same directory, `fsync`, rename.

Invariant: applying `pkg/format` (with the same options) to the merger's
output should be a no-op for the bytes the merger produced. The merger does
not reformat untouched existing regions.

### 4.5 `cmd/beanfile`

```
beanfile [flags] --ledger ROOT.beancount [files...]

  --ledger PATH                 ledger root file (REQUIRED)
  --config PATH                 TOML config (default: ./beanfile.toml if present)
  --root PATH                   destination root (default: dir of --ledger)
  --dry-run                     print proposed patches instead of writing
  --pass-through                emit non-routable directives on stdout
                                (default: error out)
  --order STR                   ascending | descending | append
  --file-pattern STR            YYYY | YYYYmm | YYYYmmdd
  --txn-strategy STR            first-posting | last-posting |
                                first-debit  | first-credit
  --override-meta-key STR       metadata key (default: route-account)
  --format-comma-grouping BOOL
  --format-align-amounts BOOL
  --format-amount-column INT
  --format-east-asian-ambiguous-width INT
  --format-indent-width INT
  --format-blank-lines-between-directives INT
  --format-insert-blank-lines-between-directives BOOL
  --quiet                       suppress stderr stats
```

Orchestration:

1. Load the config (explicit `--config` > `./beanfile.toml` > built-in
   defaults). CLI flags overlay the result.
2. `dedup.BuildIndex(ctx, --ledger)` builds the active+commented index over
   the entire transitive include closure.
3. Read input: stdin when no positional args (or a single `-`), otherwise
   each file in turn.
4. Lower the input via `pkg/loader.LoadReader` (validation off, no include
   resolution — the input is treated as a flat directive stream).
5. For each directive:
   - If `route.Decide` returns `PassThrough = true`:
     - With `--pass-through`: emit on stdout; bump `passthrough`.
     - Without: error out and stop.
   - Otherwise apply the three-way dedup decision (§2):
     - **InDestination match** → skip; bump `Skipped[P]`; do not call `Add`.
     - **InOtherActive match** → record `Insert{Commented: true}` into the
       per-path plan; bump `Commented[P]`; call `Add(P, d, commented=true)`.
     - **No match** → record `Insert{Commented: false}`; bump `Written[P]`;
       call `Add(P, d, commented=false)`.
6. Group inserts by path, preserving input order within each group, and
   call `merge.Merge` per path.
7. With `--dry-run`, print the proposed patches with `+` / `;+` line
   prefixes instead of writing.
8. Print per-path and total stats on stderr unless `--quiet`.

## 5. Configuration file

The TOML schema:

```toml
[routes.account]
template              = "transactions/{account}/{date}.beancount"
file_pattern          = "YYYYmm"
order                 = "ascending"
equivalence_meta_keys = ["import-id"]

[routes.price]
template              = "quotes/{commodity}/{date}.beancount"
file_pattern          = "YYYYmm"
order                 = "ascending"
equivalence_meta_keys = []

[routes.transaction]
default_strategy  = "first-posting"
override_meta_key = "route-account"

# Global format defaults. All seven pkg/format options are settable.
[routes.format]
comma_grouping                        = false
align_amounts                         = true
amount_column                         = 50
east_asian_ambiguous_width            = 1
indent_width                          = 2
blank_lines_between_directives        = 1
insert_blank_lines_between_directives = true

# Section-level format overrides
[routes.account.format]
indent_width = 4

[routes.price.format]
amount_column = 30

# Per-account-tree override (longest prefix wins)
[[routes.account.override]]
prefix       = "Assets:JP"
file_pattern = "YYYY"

[routes.account.override.format]
east_asian_ambiguous_width = 2

[[routes.account.override]]
prefix                = "Expenses:Food"
template              = "transactions/expenses-food/{date}.beancount"
order                 = "descending"
equivalence_meta_keys = ["receipt-id"]

# Per-commodity override (exact match)
[[routes.price.override]]
commodity    = "JPY"
file_pattern = "YYYY"

[routes.price.override.format]
amount_column = 24
```

Resolution rules:

- An override's missing fields inherit from its parent section.
- Account override prefixes match on account-segment boundaries.
- `equivalence_meta_keys` inherits by **replacement**, not concatenation.
  Use `equivalence_meta_keys = []` in an override to silence inherited keys.
- `format` inherits **field-wise**: setting just `amount_column` in an
  override leaves all other format fields at their inherited values.
- Transaction printing uses the format resolved for the transaction's
  *routing destination* account. For commented-out cross-postings the
  format is the one resolved for the destination of the comment.

## 6. Routing and printing details

### Transaction routing override

The resolution order, repeated here for completeness:

1. Transaction-level `route-account: "Assets:Foo:Bar"` (string).
2. First posting whose metadata contains `route-account: TRUE` (bool).
3. The configured `default_strategy`.
4. The first posting's account.

If multiple postings carry `route-account: TRUE`, the first occurrence wins
silently. The original input directive is never mutated — `route-account`
stripping happens on a deep copy used for emission.

### New directive printing

`pkg/printer.Fprint(w, d, decision.Format...)` produces the new directive's
text. All seven `pkg/format` options are honoured.
`blank_lines_between_directives` and `insert_blank_lines_between_directives`
additionally drive the merger's spacing logic (§4.4) — they govern the gap
between the new directive and its existing neighbours.

Reproducing the indent/column style of an existing file by *measurement*
(rather than by configuration) is a future extension; today, format options
are taken from CLI/TOML or the `pkg/format` defaults.

### Commented-out emission

Multi-line directives have the prefix (default `"; "`) prepended to every
line, including continuation lines belonging to postings or metadata. A
trailing newline ensures the next directive starts cleanly.

### File-header directives

Without `--pass-through`, encountering an `Option`, `Plugin`, `Include`,
`Pushtag`, `Poptag`, `Event`, `Query`, `Custom`, or `Commodity` directive in
the input is a hard error; the implementation does not currently know where
to put them. With `--pass-through`, they are written verbatim to stdout in
input order.

## 7. Dedup and equivalence

### Equivalence rules (OR)

1. **AST equality** — `go-cmp` with `equalityOpts` excluding:
   - `Span`, `Pos`, line/column.
   - Origin filename.
   - The `route-account` metadata entry on transactions and postings.
   - The `commented` flag on stored directives.
2. **Metadata equality** — the resolved `EqMetaKeys` for the directive's
   account-tree (transactions) or commodity (prices) yield at least one key
   present in both directives with equal values.

### Scopes

| Query             | Active in same path | Commented in same path | Active elsewhere | Commented elsewhere |
|-------------------|:---:|:---:|:---:|:---:|
| `InDestination`   |  ✓  |  ✓  |     |     |
| `InOtherActive`   |     |     |  ✓  |     |

Commented-out directives in *other* paths do not trigger the cross-posting
rule. They are notes, not a canonical record.

### Stream-internal dedup

After each accepted insert, the CLI calls `Add(path, d, commented)`. A later
input directive that hashes-equal to that one will fire `InDestination` and
be skipped. This prevents accidental duplicates when the same input file is
piped twice or when an upstream importer emits redundant records.

### Future: fuzzy matching

A separate matching mode under consideration is *posting-equality plus
narration similarity*: two transactions match when their routing posting is
AST-equal and their narration strings have high textual similarity. The
`Index` interface deliberately keeps `MatchKind` as a typed enum so a
future `MatchFuzzy` value can slot in without changing call sites.

## 8. Insertion position (binary search)

The merger does not analyze the existing file's implied order. Instead, for
each insert it runs a binary search against the existing dated-directive
list using the requested `Order` and accepts whatever offset the search
returns.

| Order | Search predicate | Insertion |
|---|---|---|
| ascending  | largest `i` with `existing[i].date <= input.date` | just after `i` |
| descending | largest `i` with `existing[i].date >= input.date` | just after `i` |
| append     | — | end of file |

Boundary cases: if no element matches, the insertion goes just before the
first dated directive; if there are no dated directives, the insertion goes
at end of file.

When the file *is* in the requested order, this matches the spec literally:

- **Ascending file + ascending insert**: the search lands on the last
  same-date directive, so the new one goes right after the same-date block,
  before the next-day block.
- **Descending file + descending insert**: the search lands on the last
  element that is `>= input.date` — in a descending file that is the *last*
  entry of the same-date block in file order, i.e. the position immediately
  before the same-date block in date order, which is exactly the spec.

When the file disagrees with the requested order, the binary search runs
against a non-monotonic sequence and may return an offset that does not
respect the requested order. **This is allowed.** The only invariant is
that no existing directive, comment, or blank line ever changes its
relative position in the file.

Same-date FIFO falls out of the patch model: every same-date insert from
the input gets the same byte offset, and patches at the same offset are
emitted in input order, so the output is `[existing same-date block]
[input₁] [input₂] …` for ascending or `[input₁] [input₂] … [existing
same-date block]` for descending.

## 9. Sub-phase plan

Each row is intended to land as an independent PR.

| Sub | Focus | Deliverable |
|-----|---|---|
| 7.5a | `pkg/distribute/route` MVP | Standard convention resolution, `PassThrough` detection, unit tests of `Decide`. No overrides, no config. |
| 7.5b | `pkg/distribute/merge` MVP | CST round-trip insertion (ascending, `YYYYmm`), new-file creation, atomic write, tests for comment/blank-line preservation. |
| 7.5c | `pkg/distribute/comment` | Recognizer with tail-shrink fallback, emitter, table tests. |
| 7.5d | `cmd/beanfile` MVP | stdin/file input, `--ledger` required, `--pass-through` default off, route → merge wiring. No dedup yet. |
| 7.5e | `pkg/distribute/dedup` + integration | `BuildIndex` covering active and commented entries, three-way decision wired into the CLI, per-file stats. |
| 7.5f | TOML config + overrides | Account-tree and commodity overrides, `./beanfile.toml` discovery, `equivalence_meta_keys`, full `format` hierarchy, CLI `--format-*` overrides. |
| 7.5g | Transaction routing override | `route-account` metadata at txn and posting level, strip on emit. |
| 7.5h | Order and pattern expansion | `descending`, `append`, `YYYY` and `YYYYmmdd` patterns; binary-search variants. |
| 7.5i | Polish | `--dry-run` patch printing, stderr stats formatting, regression tests covering "messy existing file" cases. |

`route`, `merge`, and `comment` can advance in parallel; `dedup` and the CLI
integration depend on all three.

## 10. Existing assets reused

- `pkg/syntax`:
  - `Parse(src string) *File` (`pkg/syntax/parser.go:10`)
  - `ParseReader(io.Reader) (*File, error)` (`pkg/syntax/parser.go:22`)
  - `Node.FullText()` for byte-exact CST reconstruction (`pkg/syntax/node.go:84-98`).
  - `Node.FindToken(syntax.DATE)` for date extraction (`pkg/syntax/node.go:54`).
- `pkg/ast`:
  - All directive types, `ast.Directive` interface, `DirDate()`, `DirKind()`.
  - `ast.Lower` (`pkg/ast/lower.go:15`) lowers a single CST file to AST.
- `pkg/printer`:
  - `Fprint(w, node, opts...)` (`pkg/printer/printer.go:20`) — accepts a
    single directive.
- `pkg/loader`:
  - `LoadFile(ctx, path, opts...)` (`pkg/loader/loader.go:107`) — used to
    walk the include closure when building the dedup index.
- `pkg/format`: `Option` plus the seven `With*` constructors
  (`pkg/format/option.go`).
- `github.com/BurntSushi/toml`: already a transitive dependency, promoted
  to direct in 7.5f.
- `github.com/google/go-cmp`: already direct, used for AST equality.

## 11. Integration with bean-daemon (Phase 10)

Phase 10's `POST /directives` endpoint is the live-service equivalent of
`beanfile`. When that phase lands, it should use `pkg/distribute/route` and
`pkg/distribute/merge` (and `pkg/distribute/dedup` for idempotency on
retries) rather than reimplementing the logic.
