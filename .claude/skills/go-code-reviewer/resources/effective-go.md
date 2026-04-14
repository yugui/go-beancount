# Effective Go — Key Rules for Code Review

Source: https://go.dev/doc/effective_go

## Formatting

- Use `gofmt` — do not manually align; tabs for indentation
- Opening braces on the **same line** as control structures (never next line)
- No rigid line length limit; wrap long lines with an extra tab

## Names

- **Packages**: lowercase, single-word, concise (`bytes` not `byte_utils`). Package name is the accessor — exported names can be short (`bufio.Reader` not `bufio.BufReader`)
- **Getters**: `Owner()` not `GetOwner()`; no `Get` prefix unless concept requires it
- **Interfaces**: one-method interfaces named as method + `-er` suffix (`Reader`, `Writer`, `Stringer`)
- **MixedCaps**: always use `MixedCaps` or `mixedCaps` for multi-word names, never underscores (except package-level tests and generated code)

## Commentary

- Doc comments immediately precede the declaration, no blank line between
- Doc comment should begin with the name of the thing declared: `// Request represents...`
- Full sentences; end with period
- All exported top-level identifiers must have doc comments

## Control Structures

- Omit `else` when `if` body ends in `return`/`break`/`continue`/`goto` — keep normal path at minimum indentation
- Prefer `switch` over long `if-else` chains
- Use initialization in `if`/`switch`: `if err := f(); err != nil { ... }`

## Functions

- Multiple return values preferred over in-band error signals (e.g. `-1` or `null`)
- **Error** should be the last return value
- Named result parameters: use when clarifying ambiguous same-type returns or enabling deferred modification; avoid naked returns in medium/large functions
- `defer` for cleanup: place `defer f.Close()` immediately after acquiring the resource

## Data

- `new(T)` — zeroed, returns `*T`; `make` — for slices, maps, channels only
- Design types so **zero value is useful**
- Prefer slices over arrays for most cases; pass slices instead of pointer+length
- Maps: use "comma ok" to distinguish missing from zero value: `v, ok := m[k]`

## Methods — Pointer vs. Value Receivers

- **Pointer receiver** required when: method mutates receiver, receiver contains `sync.Mutex` or similar, receiver is large
- **Value receiver** appropriate for: small read-only structs, basic types, types naturally copyable (like `time.Time`)
- **Do not mix** pointer and value receivers on the same type
- When in doubt, use pointer receiver

## Interfaces

- Interfaces are satisfied implicitly — no `implements` declaration
- One-method interfaces are idiomatic and common
- Define interfaces in the **consuming** package, not the implementing one
- Return concrete types from constructors; let consumers define the interface they need

## Embedding

- Embedded types promote their methods to the outer type
- Prefer composition via embedding over inheritance-like patterns
- Name conflicts: nearest definition wins; same-depth duplicates are usually an error

## Concurrency

- **"Do not communicate by sharing memory; share memory by communicating"**
- Use channels to transfer ownership of data between goroutines
- Goroutines are cheap; launch with `go func()`
- Unbuffered channel: synchronizes sender and receiver
- Buffered channel: sender blocks only when buffer full (use as semaphore)
- Always make goroutine exit conditions clear; document when non-obvious

## Error Handling

- Return `error` as the last return value; `nil` means success
- Include context in errors: `fmt.Errorf("parse %q at line %d: %w", name, line, err)`
- Use `%w` (not `%v`) when wrapping errors that callers may need to inspect with `errors.Is`/`errors.As`
- `panic` only for unrecoverable programmer errors; use `recover` in server goroutines to prevent crash propagation
- **Do not use `panic` for normal error handling**

## Initialization

- Constants with `iota` for enumerated values
- `init()` for setup that can't be expressed as declarations; called after imports initialized
- Multiple `init()` functions per file allowed; executed in declaration order
