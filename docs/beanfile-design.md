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
| `Open`, `Close`, `Balance`, `Note`, `Document`, `Transaction` | `Account` | `transactions/{account}/{date}.beancount` |
| `Pad` | `Account` (the *padded* account; `PadAccount` is **not** used for routing but does participate in dedup AST equality) | same as above |
| `Price` | `Commodity` | `quotes/{commodity}/{date}.beancount` |
| `Option`, `Plugin`, `Include`, `Event`, `Query`, `Custom`, `Commodity` | — | not routable |

(`pushtag` / `poptag` are CST-only constructs handled at lowering time —
`pkg/syntax/node_kind.go:13-14` — and never appear as `ast.Directive`
values, so they never reach beanfile.)

Template tokens follow §4.1: `{account}` expands to slash-separated path
segments (`Assets:Foo:Bar:Baz` → `Assets/Foo/Bar/Baz`), `{commodity}` to
the currency name, and `{date}` to the date formatted under the configured
file pattern (`YYYY` / `YYYYmm` / `YYYYmmdd`).

The date is taken from the directive's `DirDate()` (a `time.Time`) and
formatted by reading `.Year()`, `.Month()`, `.Day()` directly — no
timezone conversion is performed. Beancount dates are date-only, so
`DirDate()` carries no meaningful clock; reading the calendar fields
directly side-steps any `time.Local` vs. `time.UTC` ambiguity. The
same rule applies to dedup grouping (when grouping by month, the
`(year, month)` tuple comes from these accessors).

Non-routable directives produce an **error by default**. The
`--pass-through` flag changes this to "emit on stdout, preserving input order".
With multiple input sources (e.g. several positional files), `--pass-through`
emits them in argument order, never interleaving streams.

`Commodity` carries a date and a currency name and could in principle be
routed (for example, to `commodities/<CCY>.beancount`), but no convention
has been agreed in Phase 7.5. It is therefore treated as non-routable for
now; deciding a routing convention is deferred to a follow-up phase.

### Transaction routing override

A `Transaction` touches multiple accounts, so the routing key has to be
chosen. The order of resolution is:

1. The transaction-level metadata `route-account: "Assets:Foo:Bar"` (string) —
   the value is taken as the destination account verbatim.
2. The first posting whose metadata contains `route-account: TRUE` (bool) —
   that posting's account is used. A posting whose `route-account` is
   `FALSE` is treated as if the entry were absent (it does not select
   that posting and does not error). If *every* posting carries
   `FALSE`, no posting matches and resolution falls through to rule 3.
   Other malformed values are covered by Open Question #3.
3. The configured `default_strategy` (`first-posting`, `last-posting`,
   `first-debit`, or `first-credit`).
4. Fallback: the first posting's account.

The metadata key is configurable (`override_meta_key`, default `route-account`).
Whatever key is in effect is **stripped from the emitted directive** on every
output, on both the transaction and every posting — even the entries that
were *not* selected by the resolution chain (e.g. when both a transaction-
level string and a posting-level `TRUE` are present, rule 1 wins for
routing but the posting-level `TRUE` is still stripped on emit). The
original input directive is never mutated; stripping happens on a deep
copy.

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

- **AST equality** via `go-cmp`, with the `Span` field of every directive
  (and of every nested `Posting`) excluded from the comparison. `Span`
  itself wraps two `Position` values (`pkg/ast/ast.go:18-22`), and each
  `Position` carries filename, byte offset, line, and column
  (`pkg/ast/ast.go:10-16`); excluding the `Span` wrapper covers both
  sides at once. The `route-account` metadata entry is also stripped
  from the comparison since it is an internal-only routing hint.
- **Metadata-key equality**: for each entry in the resolved
  `equivalence_meta_keys` list, both directives carry that key with equal
  values. Useful when an upstream importer already stamps a stable
  `import-id` or similar.

Note that the "commented-out" property is *not* a field of any AST type;
it lives on the `CommentedDirective` wrapper in `pkg/distribute/comment`
(§4.3). The wrapper is unwrapped before equality is computed, so the
property never enters the comparison.

The dedup index is also updated as directives are accepted within a single
invocation, so duplicates within the input stream are themselves skipped
(see §7 for the precise semantics).

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
beanfile: transactions/Assets/Cash/202312.beancount: written=0 commented=0 skipped=4
beanfile: total: written=15 commented=1 skipped=6 passthrough=0
```

The skip-only line (`written=0 commented=0 skipped=4`) is shown by
default; whether to suppress such "nothing changed" lines is Open
Question #13.

`passthrough` is **global only** — non-routable directives have no
destination path, so they are reported on the `total:` line only and do
not contribute to any per-path row.

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
   pkg/syntax (CST), pkg/ast, pkg/printer, pkg/format
```

The implementation deliberately bypasses `pkg/loader`: that package always
runs the plugin pipeline plus the `postBuiltins` array
(pad/balance/validations — `pkg/loader/loader.go:79-83`, applied via
`runPipeline` at `loader.go:121-127`), neither of which beanfile needs.
Include resolution alone is provided by `pkg/ast.LoadFile` / `LoadReader`
(`pkg/ast/load.go:16-58`), and that is what beanfile uses.

## 4. Package details

### 4.1 `pkg/distribute/route`

```go
type Decision struct {
    Path                              string          // routing destination, relative to Config.Root
    Order                             OrderKind       // ascending | descending | append
    StripMetaKeys                     []string        // meta keys removed before printing (route-account)
    EqMetaKeys                        []string        // dedup keys for this destination
    Format                            []format.Option // body-only formatter options
    BlankLinesBetweenDirectives       int             // file-level spacing target
    InsertBlankLinesBetweenDirectives bool            // file-level spacing switch
    PassThrough                       bool            // not routable → CLI errors or emits on stdout
}

type OrderKind int // OrderAscending | OrderDescending | OrderAppend

type Config struct {
    Root   string // populated by CLI; not part of the TOML schema
    Routes Routes
}

type Routes struct {
    Account     AccountSection
    Price       PriceSection
    Transaction TransactionSection
    Format      FormatSection // global format defaults
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
    Template            string    // default "transactions/{account}/{date}.beancount"
    FilePattern         string    // default "YYYYmm"
    Order               string    // default "ascending"
    EquivalenceMetaKeys *[]string // nil = absent; non-nil = explicitly set (empty silences inheritance)
    Format              FormatSection
    Overrides           []AccountOverride // longest prefix wins
}

type PriceSection struct {
    Template            string    // default "quotes/{commodity}/{date}.beancount"
    FilePattern         string
    Order               string
    EquivalenceMetaKeys *[]string
    Format              FormatSection
    Overrides           []CommodityOverride
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
    EquivalenceMetaKeys *[]string
    Format              FormatSection
}

type CommodityOverride struct {
    Commodity           string // exact match
    Template            string
    FilePattern         string
    Order               string
    EquivalenceMetaKeys *[]string
    Format              FormatSection
}

func Decide(d ast.Directive, cfg *Config) (Decision, error)
```

The `Decision.Format` slice carries body-level options only (comma_grouping,
align_amounts, amount_column, east_asian_ambiguous_width, indent_width).
File-level spacing — `blank_lines_between_directives` and
`insert_blank_lines_between_directives` — is exposed as typed fields on
`Decision` so the `merge.Plan` builder can read them without resolving
opaque format closures.

`EquivalenceMetaKeys` is `*[]string` (rather than `[]string`) so callers
can distinguish "not declared" from "declared as empty". On an override,
a non-nil empty slice silences inherited keys; a nil pointer falls back
to the parent scope.

- Account overrides match by **longest account-segment prefix**; ties resolve
  in TOML order. `Assets:JP` matches `Assets:JP:Cash` but not `Assets:JPN`.
- `Order` is declared as `string` in the TOML/`Section` types and as
  `OrderKind` (an int enum) in `Decision`. Conversion happens once at
  config-load time with a case-insensitive match against the literals
  `"ascending"`, `"descending"`, and `"append"`. Any other value (typos,
  abbreviations like `"asc"`, empty strings) is rejected as a config
  error before `Decide` is ever called; there is no fallback to the
  default.
- Template tokens: `{account}` becomes path segments, `{date}` is formatted
  per the resolved file pattern, `{commodity}` is substituted verbatim.
- `Decide` is pure; it does not touch the filesystem.
- `Decision.PassThrough` is purely a function of the directive's kind
  (set when `Decide` cannot pick a destination). It does **not** depend
  on the `--pass-through` CLI flag — that flag is read by the CLI in
  step 5 of §4.5 to decide what to do *when* `Decision.PassThrough` is
  true. `Config` therefore carries no pass-through field.
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

func BuildIndex(ctx context.Context, ledgerRoot, configRoot string, opts ...Option) (Index, []ast.Diagnostic, error)
```

The `[]ast.Diagnostic` return carries every diagnostic produced during
the ledger walk (parse errors, lower errors, include resolution
problems). The CLI feeds it through the diagnostic policy in §4.5 step
4 — Error severity aborts, Warning severity is logged unless `--quiet`.
The `error` return is reserved for system-level failures
(`ctx.Err()`, I/O on the root file).

`BuildIndex` walks the ledger via `pkg/ast.LoadFile(ledgerRoot)` (which
resolves `include` directives but does not run plugins or validation —
exactly what beanfile needs). For each member source file:

- Records every active directive under its **canonicalized path key**
  (see below).
- Reads the raw bytes once and runs `pkg/distribute/comment.ExtractCommented`
  to recover commented-out directives, lowering each successful parse to AST
  and recording it under the same canonicalized path with `commented = true`.

`ctx` is checked at member-file boundaries — `BuildIndex` calls
`ctx.Err()` before each new file's read+parse+comment-extract pass, so
a cancelled context aborts traversal cleanly. `ast.LoadFile` itself does
not take a context, so the granularity is one file at a time; for typical
ledgers (tens to hundreds of files) this is fine.

**Path key canonicalization.** `ast.LoadFile` records each directive's
origin in `Span.Filename` as an absolute filesystem path
(`pkg/ast/load.go:44-52`). `route.Decide`, on the other hand, returns
destination paths relative to `Config.Root`. To make `InDestination(P, ...)`
queries succeed, `BuildIndex` normalizes every member file's absolute
path into `Config.Root`-relative form using `filepath.Rel(configRoot, abs)`
before using it as the index key. Member files that resolve to outside
`Config.Root` (e.g., the root file includes `../shared/foo.beancount`)
yield a `..`-prefixed key; routing never produces such paths, so those
members are naturally out of scope and never match an `InDestination`
query — which is the intended behaviour (the merger does not write to
files outside the configured root).

The same canonicalization is applied to the `path` argument of
`Index.Add`, `InDestination`, and `InOtherActive` — by contract, callers
pass `Config.Root`-relative paths only.

The conventional setup places `--ledger` inside `--root` (typically at
the root). When `--ledger` is *outside* `--root` (e.g., the user runs
`beanfile --ledger /repo/main.beancount --root /repo/transactions/`),
the root file's canonicalized key is `..`-prefixed and never matches a
routing destination — which is the desired outcome, since the root
file is not a routing target.

Equivalence:

- **AST equality** uses `go-cmp` with the option set returned by the
  `equalityOpts(overrideMetaKey)` helper (definition in §7), which
  ignores every `ast.Span` value (`pkg/ast/ast.go:18-22`) anywhere in
  the AST tree and strips the routing-hint metadata entry from both
  sides.
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
    EndLine    int    // exclusive; reflects the shrunk-to length on tail-shrink success
    Indent     string // ";" + zero or more ASCII whitespace characters
    Body       []byte // the K lines that actually parsed (with prefix stripped),
                     // not the full N-line candidate block — the tail lines that
                     // were dropped during the shrink fallback are not stored here
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
5. **Tail-shrink fallback**: if the full block fails to parse, retry
   with `N-1`, `N-2`, … `1` trailing lines removed. The first prefix
   length that parses to at least one directive defines the commented
   directive; the remaining tail lines are treated as ordinary comments.
   This accommodates the common pattern of a commented directive
   followed by a same-prefixed plain-comment annotation:

   ```
   ; 2024-01-15 * "Coffee" "Espresso bar"
   ;   Expenses:Food:Cafe   3.50 USD
   ;   Assets:Cash         -3.50 USD
   ; receipt was scanned 2024-01-16
   ```

   Dropped tail lines stay in the source file unchanged — the merger
   never edits regions outside its own insertions, so byte-exact
   round-tripping is preserved regardless of which lines `Body` retains.
   The tail lines simply do not participate in dedup.
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
    Path                              string
    Order                             OrderKind // applies to every Insert; one sort order per file
    BlankLinesBetweenDirectives       int       // file-level: target N for spacing rule (§4.4 step 5)
    InsertBlankLinesBetweenDirectives bool      // file-level: B switch (§4.4 step 5)
    Inserts                           []Insert  // input order preserved
}

type Insert struct {
    Directive     ast.Directive
    Commented     bool             // true → emit via comment.EmitCommented
    Prefix        string           // commented-only, default "; "
    StripMetaKeys []string
    Format        []format.Option  // body-only options: comma_grouping, align_amounts,
                                   // amount_column, east_asian_ambiguous_width, indent_width.
                                   // File-level spacing lives on Plan.
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

`Order` belongs to the `Plan`, not to individual inserts: a single file
has one canonical sort order, and mixing orders within one file would be
nonsensical. The CLI groups inserts by destination path *and* by the
`Order` resolved for that path; if a future scenario produces conflicting
orders for the same path, the CLI must surface that as a config error
before constructing the `Plan`.

The split between `Plan` and `Insert` mirrors the scope of each option:
the two spacing options (`blank_lines_between_directives`,
`insert_blank_lines_between_directives`) describe the file's layout and
must be uniform across all directives in that file, so they live on
`Plan` as typed fields rather than as opaque `format.Option` functions.
The five body-printing options (`comma_grouping`, `align_amounts`,
`amount_column`, `east_asian_ambiguous_width`, `indent_width`) describe
how an individual directive is rendered and stay on `Insert.Format` —
this allows a cross-posting commented insert to be rendered with a
different style if a future config requires it.

Because `format.Option` is `func(*formatopt.Options)` — opaque at call
sites — the merger composes the two scopes when emitting an insert:

1. Resolve `Insert.Format` against `formatopt.Default()` to get an
   effective `formatopt.Options` value carrying body-level fields.
2. Overlay the spacing fields with `Plan.BlankLinesBetweenDirectives`
   and `Plan.InsertBlankLinesBetweenDirectives`.
3. Use the resulting options for both `printer.Fprint` (which only
   reads the body fields when printing one directive) and for the
   merger's own spacing logic (which reads only the spacing fields).

Producers of `Insert.Format` (currently only `route.Decide`) emit a
body-only slice; the spacing overlay is therefore a no-op composition
rather than a fix-up of an ill-formed input.

Behaviour:

1. **New file path**: create parent directories, sort the inserts (using a
   **stable** sort, so same-date inserts retain their input/FIFO order)
   according to `Plan.Order`, print each one with `printer.Fprint`, and
   place `Plan.BlankLinesBetweenDirectives` blank lines between
   consecutive inserts (the same `B`/`N` rule from step 5 below applies,
   with no "existing side" since the file is new). The first insert
   starts at byte 0 with no leading blank lines; the file ends with
   exactly one trailing newline. Write atomically (temp file in the
   same directory + `fsync` + rename).
2. **Existing file**: read the file's bytes once into memory (the merger
   needs them both for `syntax.ParseReader` and as the canvas for patches
   in step 4), parse with `pkg/syntax.ParseReader`, and walk the top-level
   children. Each directive node's date is extracted via
   `Node.FindToken(syntax.DATE)`. `FindToken` is shallow-only
   (`pkg/syntax/node.go:54`), but every dated-directive parser in
   `pkg/syntax/parser.go` (`parseTransaction`, `parseOpen`, `parseClose`,
   `parseCommodity`, `parseBalance`, `parsePad`, `parseNote`,
   `parseDocument`, `parseEvent`, `parseQuery`, `parsePrice`,
   `parseCustom`) attaches the DATE token directly on the directive
   node, so the call always succeeds for the kinds beanfile cares about.
   Undated directives (file headers) act as fixed anchors and are
   excluded from the date search.
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
   of the bytes immediately after.

   `N` is the *target* count of blank lines between consecutive directives.
   `B` controls whether the merger creates blank lines where none currently
   exist. Note that the merger **deliberately departs** from
   `internal/formatopt/options.go:12-16`'s documented `B = false`
   semantics ("existing blank lines are normalized to
   `BlankLinesBetweenDirectives` but no new blank lines are created"):
   the merger cannot normalize *existing* blank lines without editing
   bytes outside its own insertions, which would violate the
   relative-position invariant. Normalizing pre-existing whitespace is
   left to a follow-up `beanfmt` pass.

   Let `X` be the count of blank lines already present on the existing
   side immediately adjacent to the insertion offset.
   - **`B = true`**: the merger contributes `max(0, N - X)` blank lines on
     that side, aiming for `N` total. If the file already has more than `N`
     blank lines there (`X > N`), the merger does not reduce them — it
     never edits bytes outside its own insertions. Whole-file beanfmt
     would then trim them; the merger leaves that to a later format pass.
   - **`B = false`**: the merger contributes zero blank lines and only the
     single newline needed to terminate its insertion. Whatever blank
     lines already existed on the existing side stay as-is.
   - **File start** (insertion before the first existing directive):
     no leading blank lines are added — the new directive starts at byte
     0 (or right after a leading byte-order mark / shebang if one exists,
     though beancount source typically has neither). The trailing side
     follows the normal `B`/`N` rule against whatever comes after.
   - **File end** (insertion after the last existing directive, or
     `append` Order, or empty file): no trailing blank lines are added,
     but the merger ensures the file ends with exactly one newline (POSIX
     text-file convention). The leading side follows the normal `B`/`N`
     rule against the previous existing directive.
6. **Atomic write**: temp file in the same directory, `fsync`, rename.

Invariant: the bytes the merger *itself* added (the new directive's body
plus the *padding* it chose to inject on the new side) are canonically
formatted under the resolved options. This is a per-insertion-side
invariant, not a whole-file one: when the existing side already had more
blank lines than `N`, the surrounding region as a whole does not satisfy
`pkg/format` semantics, but the merger declines to touch existing bytes.
Whole-file `beanfmt` (with the same options) would tidy such pre-existing
oddities; the merger leaves that to a later format pass and accepts that
its output is canonical only modulo whatever the existing file already
contained.

### 4.5 `cmd/beanfile`

```
beanfile [flags] --ledger ROOT.beancount [files...]

  --ledger PATH                 ledger root file (REQUIRED)
  --config PATH                 TOML config (default: ./beanfile.toml if present)
  --root PATH                   destination root for routing AND the base for
                                dedup index path canonicalization (§4.2;
                                default: dir of --ledger)
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
   defaults). A pre-scan of `args` locates `--config` so the TOML can be
   parsed before the FlagSet runs; each `--order` / `--file-pattern` /
   `--txn-strategy` / `--override-meta-key` / `--format-*` flag is then a
   `flag.Func` (or `flag.BoolFunc`) callback that mutates the loaded
   `route.Config` in place. Each flag's effect lives in one place — its
   callback — so the overlay is implicit in `flag.Parse`.
2. `dedup.BuildIndex(ctx, --ledger, --root)` builds the active+commented
   index over the entire transitive include closure (using
   `pkg/ast.LoadFile`, which resolves includes without running validation
   or plugins). Returns `(Index, []ast.Diagnostic, error)`; the
   diagnostic slice is fed through the policy in step 4 below before any
   input is read.
3. Read input: stdin when no positional args (or a single `-`), otherwise
   each positional file in turn.
4. Parse each input source with `pkg/ast.LoadReader` (stdin) or
   `pkg/ast.LoadFile` (positional files). This bypasses the plugin
   pipeline entirely and yields a flat AST directive stream alongside an
   `*ast.Ledger` whose `Diagnostics` slice may be non-empty (parse
   errors, lowering errors, include resolution failures).

   **Diagnostic policy** (applies to both the ledger build in step 2 and
   the input parse here):
   - Any `Severity == Error` diagnostic is fatal: print all collected
     diagnostics to stderr, then exit non-zero. No destination files are
     touched.
   - `Severity == Warning` diagnostics are printed to stderr unless
     `--quiet` is set, but processing continues.

   **Include directives in input are not resolved.** The CLI passes
   `ast.WithBaseDir("")` to *both* `LoadReader` and `LoadFile` so that
   relative includes uniformly emit an Error-severity diagnostic
   (`pkg/ast/load.go:123-135`) and abort the run. This avoids surprise
   when a positional input file accidentally `include`s another
   beancount file from the same directory — without the override,
   `LoadFile` would silently set `WithBaseDir(filepath.Dir(absPath))`
   (`pkg/ast/load.go:49-52`) and pull external files into the input
   stream. Absolute-path includes still resolve in either case; this
   asymmetry is accepted as the Phase 7.5 contract. If a use case
   emerges for resolving relative includes from inputs (e.g., a
   hand-curated import-file batch), a future flag like
   `--inputs-resolve-includes` can opt back in.
5. For each directive in input order:
   - If `route.Decide` returns `PassThrough = true`:
     - With `--pass-through`: emit on stdout; bump the global `passthrough`
       counter (counts directives, not bytes or lines).
     - Without: error out and stop.
   - Otherwise apply the three-way dedup decision (§2). The path `P` is the
     `Decision.Path` returned by `route.Decide` — this is the destination
     the directive *would* be written to, and per-path stats are bucketed
     under it regardless of which branch is taken below. The three
     branches are tried in order and the first match short-circuits:
     - **Rule 1** — `InDestination(P, d)` matches → skip; bump
       `Skipped[P]`; do not call `Add`. This is the **only** branch that
       contributes to `Skipped[P]`; rules 2 and 3 always write something.
     - **Rule 2** — Rule 1 did not match, and `InOtherActive(P, d)`
       matches → record `Insert{Commented: true}` into the per-path
       plan; bump `Commented[P]`; call `Add(P, d, commented=true)`.
     - **Rule 3** — neither matched → record `Insert{Commented: false}`;
       bump `Written[P]`; call `Add(P, d, commented=false)`.
6. Group inserts by path, preserving input order within each group, and
   call `merge.Merge(plan, merge.Options{})` per path. The `Options`
   struct is empty in Phase 7.5 (reserved for future per-call overrides);
   pass a zero value.
7. With `--dry-run`, print the proposed patches to stdout instead of
   writing files. **MVP format** (locked in for the first sub-phase that
   ships `--dry-run`; Open Question #8 may refine it later): one block
   per destination, headed by `--- <relative path> ---`, then each
   inserted line prefixed with `+ ` for active inserts and `;+ ` for
   commented inserts. No surrounding context is shown; this is a
   one-way preview, not a reversible diff.
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
# These match internal/formatopt.Default() exactly; an empty
# [routes.format] section (or omitting it entirely) yields identical
# behaviour. Shown here for explicitness — change any field to override.
comma_grouping                        = false
align_amounts                         = true
amount_column                         = 52
east_asian_ambiguous_width            = 2
indent_width                          = 2
blank_lines_between_directives        = 1
insert_blank_lines_between_directives = false

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

### Non-routable directives

Without `--pass-through`, encountering an `Option`, `Plugin`, `Include`,
`Event`, `Query`, `Custom`, or `Commodity` directive in the input is a
hard error; the implementation does not currently know where to put them.
With `--pass-through`, they are written verbatim to stdout in input order
across each input source, with sources processed in the order given on
the command line (no interleaving).

`Commodity` is technically dated (`pkg/ast/directives.go:119-129`) and is
*not* a file-header directive in the way `Option` / `Plugin` / `Include`
are — it could plausibly be routed to a per-currency file. Phase 7.5
nonetheless treats it as non-routable because no convention has been
agreed; a later phase may set one.

## 7. Dedup and equivalence

### Equivalence rules (OR)

1. **AST equality** — `go-cmp` invoked with the option set returned by
   `equalityOpts(overrideMetaKey)`:

   ```go
   import (
       "github.com/cockroachdb/apd/v3"
       "github.com/google/go-cmp/cmp"
       "github.com/google/go-cmp/cmp/cmpopts"
       "github.com/yugui/go-beancount/pkg/ast"
   )

   // overrideMetaKey is the resolved value of TransactionSection.OverrideMetaKey
   // (default "route-account").
   func equalityOpts(overrideMetaKey string) cmp.Options {
       return cmp.Options{
           // Ignore every ast.Span anywhere in the AST tree. ast.Span wraps
           // two ast.Position values (filename, offset, line, column —
           // pkg/ast/ast.go:10-22); a single IgnoreTypes for the wrapper
           // covers every directive type and every nested Posting, and any
           // new directive type that embeds Span is automatically covered.
           cmpopts.IgnoreTypes(ast.Span{}),
           // Compare ast.Metadata via a dedicated Comparer rather than
           // cmpopts.IgnoreMapEntries on the inner Props map. Filtering
           // {overrideMetaKey: X} down to zero entries with IgnoreMapEntries
           // yields an empty (non-nil) map, which go-cmp does not treat as
           // equal to a nil map — so a directive that gained a route-account
           // hint would not compare equal to one that never had any
           // metadata. The Comparer strips the override key and then walks
           // the remaining entries, equating nil and empty maps.
           cmp.Comparer(func(a, b ast.Metadata) bool {
               return metadataEqual(a, b, overrideMetaKey)
           }),
           // Compare apd.Decimal by numeric value: its underlying big.Int
           // carries unexported fields that cmp cannot reflect into.
           cmp.Comparer(func(a, b apd.Decimal) bool {
               return a.Cmp(&b) == 0
           }),
           // Canonicalize posting order. []ast.Posting appears in the AST
           // only as Transaction.Postings, so this type-keyed Transformer
           // makes "same postings, different order" compare equal without
           // affecting any other slice. The ordering key is built from
           // every field that participates in equality (flag, account,
           // amount, cost, price, and per-key posting metadata
           // including normalized MetaString values), so two
           // multiset-equal posting lists land on the same canonical
           // order and cmp walks them pairwise after sort.
           sortPostings,
           // Normalize free-text fields before comparison so that the
           // same transaction emitted by different importers — possibly
           // with NFC vs NFD accents, full-width vs half-width
           // characters, or stray Unicode whitespace — still
           // deduplicates. Scope is intentionally narrow:
           // Transaction.Narration, Transaction.Payee, Note.Comment,
           // and MetaValue values whose Kind == MetaString. Everything
           // else (account paths, currency codes, tag/link names,
           // metadata keys, file paths, plugin/query/custom names,
           // option keys/values, MetaValue of other kinds) stays
           // byte-exact: those strings are identifiers, and silently
           // collapsing identifier variants would mask routing
           // mistakes.
           freeTextCmp,
       }
   }
   ```

   "Commented-out" status is *not* an AST property; it lives on the
   `CommentedDirective` wrapper from `pkg/distribute/comment` (§4.3) and
   is unwrapped before the comparison runs.

   The free-text normalizer applies `golang.org/x/text/unicode/norm`
   `NFKC` and then strips every rune for which `unicode.IsSpace`
   returns true (covers ASCII whitespace plus U+0085, U+00A0, U+1680,
   U+2000–U+200A, U+2028, U+2029, U+202F, U+205F, U+3000). NFKC
   intentionally folds compatibility variants — full-width "ＡＢＣ"
   compares equal to "ABC", precomposed "café" compares equal to
   decomposed "café" — because cross-source dedup is the whole
   point of the rule.

   AST equality does **not** bridge auto-posting (amount-elided
   posting) differences. If importer A emits all postings explicitly
   while importer B emits one posting with no amount and lets
   beancount auto-balance, the two transactions still compare unequal
   under rule 1 because their `[]Posting` lists have different shape.
   Cross-source dedup involving auto-postings should rely on rule 2
   below — see Open Question #17 for the future option of resolving
   auto-postings via `pkg/inventory` before comparison.

2. **Metadata equality** — the resolved `EqMetaKeys` for the directive's
   account-tree (transactions) or commodity (prices) yield at least one key
   present in both directives with equal values. `MetaString` values use
   the same NFKC + whitespace-strip normalization as rule 1, so the AST
   path and the meta-key path agree on what counts as the same human
   string. Identifier-bearing kinds (`MetaAccount`, `MetaCurrency`,
   `MetaTag`, `MetaLink`) compare byte-exact, and typed scalars
   (`MetaDate`, `MetaNumber`, `MetaAmount`, `MetaBool`) compare
   structurally — neither group is normalized.

### Scopes

| Query             | Active in same path | Commented in same path | Active elsewhere | Commented elsewhere |
|-------------------|:---:|:---:|:---:|:---:|
| `InDestination`   |  ✓  |  ✓  |     |     |
| `InOtherActive`   |     |     |  ✓  |     |

Commented-out directives in *other* paths do not trigger the cross-posting
rule. They are notes, not a canonical record.

### Stream-internal dedup

After each accepted insert (active *or* commented), the CLI calls
`Add(path, d, commented)`. After a *skip*, no `Add` is made (the matched
existing directive is already in the index). A later input directive that
matches will then fire `InDestination` against either the pre-existing
ledger entry or the just-added one and be skipped.

Concrete example: the input contains the same directive `D` twice, and
neither already exists in the ledger.

| Step | Decision | `Written[P]` | `Skipped[P]` | Index after |
|---|---|---|---|---|
| `D` (1st) | no match → write | `+1` | `0` | gains active `D@P` |
| `D` (2nd) | `InDestination` match → skip | `+0` | `+1` | unchanged |

Total stats for that path: `written=1, skipped=1`. The same logic catches
accidental duplicates from a stream replayed through a pipe, or from an
upstream importer emitting redundant records.

A second example covers cross-posting cascade: input `D` matches an
active equivalent at some other path `Q`, so it is recorded at `P` as
commented-out (rule 2). A later input `D'` (equivalent to `D`) with the
same destination `P` also matches the same active equivalent at `Q`
*and* now matches the commented entry just added at `P` — `InDestination`
fires (it covers both active and commented in same path per the §7
scope table) and `D'` is skipped:

| Step | Decision | `Commented[P]` | `Skipped[P]` | Index after |
|---|---|---|---|---|
| `D` (1st) | active at `Q` ≠ `P` → write commented at `P` | `+1` | `0` | gains commented `D@P` |
| `D'` (2nd) | `InDestination` (commented at `P`) → skip | `+0` | `+1` | unchanged |

So duplicate cross-posting markers do not accumulate — they are
deduplicated on the second occurrence.

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
  - `ast.LoadFile(path, opts...)` and `ast.LoadReader(r, opts...)`
    (`pkg/ast/load.go:16-58`) — parse with include resolution but no
    plugin pipeline. These are the entry points for both the input stream
    and the ledger index. Note: this means beanfile depends on the
    `pkg/ast` loader entry points but **not** on `pkg/loader`,
    `pkg/validation`, or `pkg/ext` (Phases 4 and 6 are not required).
- `pkg/printer`:
  - `Fprint(w, node, opts...)` (`pkg/printer/printer.go:20`). Accepts
    `*ast.File`, `ast.File`, `*ast.Ledger`, `ast.Ledger`,
    `[]ast.Directive`, `*ast.Amount`, `ast.Amount`, or a single
    `ast.Directive`. The merger feeds it one directive at a time.
- `pkg/format`: `Option` plus the seven `With*` constructors
  (`pkg/format/option.go`).
- `internal/formatopt`: not directly imported (private), but its
  documented semantics for `InsertBlankLinesBetweenDirectives`
  (`internal/formatopt/options.go:12-16`) anchor the merger's spacing
  rules in §4.4.
- `github.com/BurntSushi/toml`: already a transitive dependency, promoted
  to direct in 7.5f.
- `github.com/google/go-cmp`: already direct, used for AST equality.

## 11. Integration with bean-daemon (Phase 10)

PLAN.md describes Phase 10's `POST /directives` as appending to "a
designated target file" *and* notes that the implementation reuses
`pkg/distribute/{route,merge,dedup,comment}`. Those two statements
describe different shapes:

- The endpoint signature implies a **single-file** write — the caller
  picks the destination, the server appends.
- The reuse note implies **multi-file routing** — `pkg/distribute/route`
  picks the destination per directive.

The conflict is intentional left to Phase 10 to resolve. Two paths exist:

1. Reshape `POST /directives` to accept a routing-aware request body
   (omit the target file, let the server route via `pkg/distribute/route`).
   The endpoint then becomes the live-service mirror of `beanfile`.
2. Keep the single-file signature and reuse only `pkg/distribute/merge`
   (CST round-trip insertion + spacing) and `pkg/distribute/dedup` (for
   idempotent retries), leaving routing to the caller.

This document does not commit Phase 10 to either; the `pkg/distribute/*`
libraries are designed to be useful in both shapes, and Phase 7.5 makes
no API stability promises that Phase 10 must honour. PLAN.md should be
revisited at Phase 10 design time to disambiguate.

## 12. Open questions

The following are deferred to the sub-phase that touches them. Each entry
will be resolved in discussion with the user before that sub-phase's
implementation begins.

| # | Topic | Sub-phase |
|---|---|---|
| 1 | The `printer.Fprint` signature accepts many node kinds. The merger calls it once per directive — confirm there is no preferable batch path (e.g. passing `[]ast.Directive`) and decide which form to use. | 7.5b |
| 2 | Should `Decision` expose the resolved account / commodity used for routing, in addition to `Path` and `Format`? Useful for CLI logging but not strictly required, since `Format` already encodes the resolved style. | 7.5a / 7.5f |
| 3 | When a `route-account` metadata value is present but malformed (string that is not a valid `Account`, an unsupported type, an empty string), what is the behaviour: silent ignore, warn, error? | 7.5g |
| 4 | ~~The exact construction of `equalityOpts` — per-type `cmpopts.IgnoreFields(...)` enumerated for every directive type, vs. a generic `Span`-suppressing transformer that walks via reflection.~~ **Resolved in 7.5e**: the generic `cmpopts.IgnoreTypes(ast.Span{})` form was chosen so any new directive type that embeds `Span` is covered automatically. See §7 for the full option set, including the dedicated `ast.Metadata` and `apd.Decimal` comparers required for nil-map and unexported-field handling. | 7.5e |
| 5 | Promoting `github.com/BurntSushi/toml` from indirect to direct requires running `bazel run //:gazelle -- update-repos -from_file=go.mod` per CLAUDE.md. Confirm the workflow and capture it in the 7.5f sub-phase plan. | 7.5f |
| 6 | `EmitCommented(prefix string)` documents a default of `"; "` but `prefix` is a required argument. Either drop the "default" wording or make `prefix` a functional option / use a wrapper that supplies the default. | 7.5c |
| 7 | Inter-insert spacing rule when multiple patches collide at the same byte offset. **Default for 7.5b unless changed**: apply the same `B`/`N` rule between every consecutive pair of new inserts, then between the last insert and the existing-after side. Subsumed by #11 below; resolve together. | 7.5b |
| 8 | Concrete `--dry-run` output format. Candidate forms: unified-diff per destination, `+` / `;+` per-line prefix list (the MVP form locked in §4.5 step 7), or a structured JSON dump. Refine when the merger's patch list type is concrete. | 7.5i |
| 9 | Should destinations with `written=0 commented=0 skipped=N` (only-skipped) appear in the per-file stats output, or only the total line? Skip-only is common for re-runs where nothing new was added. | 7.5i |
| 10 | Behaviour of `append` Order against EOF trivia (trailing comments and blank lines). Insert before or after such trivia? "After the last dated directive" and "at EOF" diverge when trivia is present. | 7.5h |
| 11 | When several inserts collide at the same byte offset and mix `Commented` and active forms, what is the emit order — input order, active-first, commented-first? **Default for 7.5b unless changed**: input order (matches the same-day FIFO promise in §2 / §8). Together with #7 this fully specifies same-offset collision behaviour for 7.5b implementation. | 7.5b |
| 16 | Round-trip stability of comment prefix variants. The recognizer accepts `;` followed by zero or more whitespace characters (so `;2024-01-15 ...` and `;  2024-01-15 ...` are both valid), but the emitter always uses `"; "`. A re-emit therefore canonicalizes existing prefixes — decide whether that is desired or whether the emitter should preserve the input's prefix when known. | 7.5c |
| 17 | Auto-posting bridging in cross-source AST equality. Today rule 1 in §7 compares `[]Posting` structurally, so two transactions that differ only in whether one posting is amount-elided (and beancount auto-balances it) compare unequal even though they describe the same event. The 7.5 workaround is rule 2 (`equivalence_meta_keys`) — importers should attach a stable id. A future option is to resolve the auto-posting via `pkg/inventory` on a deep-copied directive before comparison; this needs a decision on how to handle cases where the elided amount cannot be uniquely inferred (multiple commodities, ambiguous cost basis). | future |

## 13. Implementation workflow

Phase 7.5 deliberately separates **design and orchestration** (top-level
agent) from **implementation** (subagents). Each sub-phase from §9
follows the same loop. The intent is to keep design intent close to the
user, who reviews each step plan before any code is written.

For sub-phase **S<sub>k</sub>**:

1. **Plan the step.** The top-level agent expands the row in §9 into a
   detailed step plan: which packages and files are touched, which
   functions are added, which open questions from §12 are in scope.
   Cross-checks the plan against this design document for consistency
   and against the existing codebase for API drift.

2. **Resolve unknowns with the user.** Any open question, ambiguity, or
   discovered divergence between the plan and the codebase is discussed
   with the user *before* implementation. The agent does not start
   implementing on its own initiative; it waits for the user's explicit
   approval of the step plan.

3. **Delegate implementation.** Once the user approves the step plan, the
   agent launches an **implementation subagent** with:
   - The relevant section(s) of this design document, copied into the
     prompt (subagents do not inherit the parent's transcript).
   - The approved step plan.
   - A clear task scope and the requirement that `bazel build //...` and
     `bazel test //...` must pass before completion.

4. **Verify against the spec.** When the implementation subagent reports
   completion, the agent launches a *separate* **verification subagent**
   that reviews the implementation against the design document and the
   step plan. The verification subagent looks for missing behaviour,
   contract violations, and test coverage gaps. Its output is a list of
   findings.

   If findings are non-empty, the agent dispatches them to a (possibly
   new) implementation subagent for fix-up, then re-runs verification.
   Loop until verification reports no findings.

5. **Run go-code-reviewer.** Once verification is clean, the agent
   invokes the `go-code-reviewer` skill to audit the code against
   *Effective Go*, *Go Code Review Comments*, and *Test Comments*. Any
   findings round-trip through an implementation subagent the same way.
   Loop until the reviewer has no comments.

6. **Commit and report.** When all loops have converged, the agent
   commits using a CLAUDE.md-compliant message and reports back to the
   user. Per CLAUDE.md's *Clean Commit History* policy, every fix-up
   produced by the verification or `go-code-reviewer` loops is folded
   into the sub-phase's original commit via `git commit --amend` (if the
   sub-phase is still on a single in-flight commit) or `git rebase -i`
   with `fixup` / `squash`. **Each sub-phase lands as exactly one commit
   on the branch** — review-loop noise never appears in history. The
   user decides whether to advance to S<sub>k+1</sub>.

### Roles in this loop

| Actor | Writes code? | Writes plans / docs? |
|---|:---:|:---:|
| Top-level agent | **No** | Yes |
| Implementation subagent | Yes | No |
| Verification subagent | No | No (produces a findings list) |
| `go-code-reviewer` skill | No | No (produces a findings list) |
| User | — | Approves step plans, resolves open questions |

The top-level agent's `Edit`/`Write` access during a sub-phase is limited
to design-level artefacts (this document, plan files, PLAN.md updates).
Implementation belongs to subagents.

### Why this loop

- Subagents do not see the parent's transcript, so each invocation is a
  blank slate. The verification subagent therefore reviews against the
  written design, not the agent's mental model — catching cases where
  the implementation drifted from the spec.
- `go-code-reviewer` after verification means style fixes never mask
  behavioural gaps.
- The user signs off on each step plan, so any divergence between intent
  and implementation surfaces in design discussion rather than during
  late-stage code review.
