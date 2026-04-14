# Google Go Style Decisions — Key Rules

Source: https://google.github.io/styleguide/go/decisions

## Naming

### Underscores
Names should **not** contain underscores, except:
- Package names imported only by generated code
- Test/Benchmark/Example function names in `*_test.go`
- Low-level cgo/OS interop

### Package Names
Concise, lowercase, letters and numbers only. Multi-word names stay unbroken (`tabwriter` not `tab_writer`). Avoid `util`, `common`, `api`, etc.

### Receiver Names
Short (1-2 letters), abbreviation of the type, consistently applied. Not underscores; omit if unused.

### Constant Names
Use `MixedCaps`, never `SCREAMING_SNAKE_CASE` or `kPrefixStyle`. Name by role, not value.

### Initialisms
Consistent casing throughout. `URL`/`url`, never `Url`. Mixed: `xmlHTTPRequest` or `XMLHTTPRequest`. For initialisms with lowercase letters (`gRPC`): exported → `GRPC`, unexported → `gRPC`.

### Getters
No `Get` prefix unless the underlying concept uses that word. Prefer `Counts()` over `GetCounts()`. Use `Compute`/`Fetch` for expensive operations.

### Variable Names
Length proportional to scope:
- Small scope (1-7 lines): single letter acceptable
- Large scope (15+ lines): descriptive name required
- Omit type-like words: `users` not `userSlice`, `count` not `numUsers`

### Repetition — Avoid These Patterns
- Package + exported symbol: use `New` not `NewPackageName` for single-type packages
- Variable + type: compiler knows the type; only clarify when ambiguous
- External context + local names: package/method names already qualify identifiers

## Commentary

### Doc Comments
All top-level exported names must have doc comments. Begin with the declared name. Use full sentences.

### Named Result Parameters
Name results only when: returning multiple same-type params, or action taken is important to convey. Example: `func WithTimeout(...) (ctx Context, cancel func())`. Don't name to avoid local variable declarations.

### Package Comments
Immediately above the package clause, no blank line. One file per package should have it.

## Imports

### Grouping Order
1. Standard library
2. Other project and vendored packages
3. Protocol Buffer imports
4. Side-effect imports (`import _ "..."`)

### Renaming
Required for name collisions only. Follow package naming rules (no underscores, no capitals). Prefer renaming the most local/project-specific import. For collisions with common variable names: use `pkg` suffix (e.g. `urlpkg`).

### Blank Imports
Only in `package main` or tests. Not in library packages. Exception: `embed` with `//go:embed`.

### Dot Imports
Do not use `import .` in production code.

## Errors

### Returning Errors
Exported functions return `error` type, not concrete error types (avoids nil pointer bugs).

### Error Strings
Not capitalized (unless starting with exported name, proper noun, or acronym); no trailing punctuation.

### Handling Errors
Must handle errors deliberately: handle immediately, return to caller, or call `log.Fatal`/`panic`. Discard only when documented safe (e.g. `(*bytes.Buffer).Write`).

### Indent Error Flow
Handle errors first, return/continue early. Normal code flows straight down, not in `else`:

```go
// Good
if err != nil {
    return err
}
// normal code

// Bad
if err != nil {
    return err
} else {
    // normal code
}
```

## Language

### Literal Formatting
Include field names in composite literals for types from other packages. Closing brace aligns with opening. Omit zero-value fields when clarity isn't lost.

### Nil Slices
Use `var s []T` (nil slice) in local variables. Don't create APIs requiring distinction between nil and empty. Check emptiness with `len(s) == 0`, not `s == nil`.

### Copying
Synchronization objects (`sync.Mutex`) must not be copied. `bytes.Buffer` slice may alias array in copies. Don't copy `T` if methods are on `*T`.

### Don't Panic
Use `error` and multiple returns for normal error handling. In `main`/init, use `log.Exit` for terminal errors. "Impossible" bugs may return errors or call `log.Fatal`.

### Goroutine Lifetimes
Make exit conditions obvious. Use `context.Context` for cancellation. Synchronize with `sync.WaitGroup`. Goroutines blocked on channels are not garbage collected.

### Interfaces
- No pre-emptive interfaces — don't create before genuine need exists
- Don't wrap RPC clients in interfaces just for abstraction
- Keep interfaces small; consumer defines them
- Functions take interfaces as arguments, return concrete types
- Exception: interfaces for encapsulation (like `error`)

### Pass Values
Do not pass pointers just to save bytes. Exceptions: large structs, protocol buffer messages.

### Receiver Type
- Value receiver: read-only small structs, basic types, maps/functions/channels
- Pointer receiver: mutation required, non-copyable fields, sync objects, large structs
- Be consistent across all methods of a type

### Synchronous Functions
Prefer synchronous (callers add concurrency via goroutines; you can't remove unnecessary concurrency).

### Use `%q`
Prefer `%q` for strings in format functions (adds double quotes automatically).

### Use `any`
Prefer `any` over `interface{}` in new code (alias available since Go 1.18).

## Common Libraries

### Flags
Flag names use `snake_case`; variables holding flag values use `MixedCaps`. Define flags only in `package main`.

### Context
Always first parameter. Exceptions: HTTP handlers (`req.Context()`), streaming RPC methods, test helpers (`t.Context()`), entrypoints (`context.Background()`). Never in struct fields.

### crypto/rand
Never `math/rand` for keys. Use `crypto/rand.Reader` or `crypto/rand.Text`.

## Useful Test Failures

### Format
Include function name, inputs, actual, expected:
```
YourFunc(%v) = %v, want %v
```

**Got before want** — never invert order.

### Assertion Libraries
Do not create assertion libraries. Use `cmp.Equal` / `cmp.Diff` for comparisons. Avoid domain-specific testing languages.

### Keep Going
Prefer `t.Error` over `t.Fatal` when subsequent checks still make sense. Use `t.Fatal` when later failures would be meaningless.

### Full Structure Comparisons
Compare entire structs with deep comparison rather than field-by-field. Use `cmp.Diff` with `protocmp.Transform` for proto types.
