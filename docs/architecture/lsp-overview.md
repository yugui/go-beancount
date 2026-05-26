# LSP Server Architecture

## Overview

`cmd/beancount-lsp` is a Language Server Protocol 3.17 server that surfaces
beancount diagnostics, completion, hover, definition, document symbols, and
formatting to compatible editors. It is layered atop the same loader/Session
abstractions used by other tooling.

## Layering

```
LSP wire (JSON-RPC over stdio)
        ↓
cmd/beancount-lsp handlers
        ↓
pkg/session.Session (overlay + Snapshot + Subscribe)
        ↓
pkg/loader.LoadFile (overlay-aware loader)
        ↓
pkg/ast loader + plugin pipeline
```

## Key design decisions

### Overlay model

LSP clients keep the authoritative view of unsaved buffers. The server uses
`pkg/loader.WithOverlay` to inject those buffers into the loader's source
provider seam — see `pkg/ast.WithOverlay` for the contract. didOpen/didChange
set overlays; didSave clears them so disk content becomes authoritative on save,
preventing divergence from external modifications made to the file between edits.

### UTF-16 ↔ byte conversion

LSP positions are 0-based with UTF-16 code-unit character offsets. The ast
uses 1-based positions with rune-count columns. Conversion is local to the
LSP package (`position.go`) and uses a hybrid line-offset table (precomputed
per snapshot) + on-demand per-line walk. Conversion lives in the LSP layer
because the rest of the codebase should not know about UTF-16.

### Reload strategy

Per-document `didChange` is debounced (100ms default; `WithDebounce(0)` for
tests). On fire, the latest content is pushed to the session via SetOverlay,
which invalidates the cache; the next Snapshot/Reload performs the actual
parse. The subscriber goroutine receives the new ledger via Subscribe and
publishes diagnostics. Per-request hot-path operations (hover, completion,
definition, formatting, documentSymbol) call Snapshot directly.

`didOpen` also triggers a Snapshot after setting the overlay so that
diagnostics are published immediately on file open, not only on the first
subsequent change.

### Why no fsnotify

File watching is delegated to the LSP client via
`workspace/didChangeWatchedFiles`. This avoids server/client double-watching,
is the LSP-canonical mechanism, and keeps the server portable. Phase 10
(bean-daemon) is the layer that adds an fsnotify adapter on top of the same
SessionAPI (`SetOverlay`/`ClearOverlay`/`Reload`).

### Diagnostics publish policy

On each reload the subscriber goroutine publishes:

1. Diagnostics for every file that has errors.
2. An empty-array notification for every currently-open file that has no
   errors (so editors clear stale underlines).
3. An empty-array notification for every file that had diagnostics in the
   previous cycle but does not appear in the current one (resolved errors).

This ensures editors always see a fresh view without needing a prior error
notification to trigger clearing.

### SymbolKind mapping

| Directive | SymbolKind | Notes |
|---|---|---|
| Open / Close | Class | Account name as label |
| Commodity | Constant | Currency code |
| Transaction | Event | Narration, then payee, then "(transaction)" |
| Balance | Operator | "balance " + account |
| Pad | Operator | "pad " + account |
| Price | Operator | commodity → amount.currency |
| Include | File | Path as label |
| Option | Property | Key as label |
| Plugin | Package | Plugin name |
| Event | Event | Event name |
| Note | String | Account as label |
| Document | File | Filename literal |
| Custom | Variable | Custom type name |
| Query | Function | Query name |
| Transaction postings | Field (children) | Account names |

Pushtag / Poptag / Pushmeta / Popmeta are consumed during AST lowering (they
mutate the active tag / meta state); they do not appear as directives in
`File.Directives` and thus produce no DocumentSymbol.

## Files

- `main.go` — entry point + stdio wiring
- `server.go` — Server struct, options, SessionAPI interface, Run
- `handlers.go` — initialize / initialized / shutdown / exit + root resolution
- `recover.go` — panic-recovery dispatch wrapper
- `docsync.go` — didOpen / didChange / didClose / didSave + docStore + debouncer
- `debounce.go` — per-document debounce timers
- `diagnostics.go` — Subscribe loop + publishDiagnostics + UTF-16 conversion
- `position.go` — lineOffsets + byte ↔ LSP Position conversion
- `formatting.go` — textDocument/formatting + textDocument/rangeFormatting
- `symbol.go` — textDocument/documentSymbol
- `definition.go` + `locate.go` — textDocument/definition + cursor → AST helper
- `hover.go` — textDocument/hover (account + commodity context-date)
- `completion.go` + `completion_context.go` — textDocument/completion + ContextKind classification
- `watch.go` — workspace/didChangeWatchedFiles + dynamic capability registration
- `smoke_test.go` — end-to-end integration test (real session, real protocol roundtrip)

## See also

- `pkg/loader` — overlay-aware loader public API
- `pkg/session` — long-lived loader view with Snapshot/Subscribe
- `pkg/ast` — directive AST and source-provider seam
- `pkg/syntax` — CST parser
