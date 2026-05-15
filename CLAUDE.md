# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Go library for beancount (plain-text accounting). Early stage — no Go source files yet.

## Build System

This project uses **Bazel** (via Bazelisk, version 9.0.1) with `rules_go` and Gazelle.

### Common Commands

- **Build all:** `bazel build //...`
- **Test all:** `bazel test //...`
- **Run a single test:** `bazel test //path/to:target --test_filter=TestName`
- **Update BUILD files after adding/changing Go files:** `bazel run //:gazelle`
- **Update BUILD files after changing go.mod:** `bazel run //:gazelle -- update-repos -from_file=go.mod`

### Workflow

After adding or modifying `.go` files or dependencies, always run Gazelle to regenerate BUILD files before building/testing.

## Go Module

`github.com/yugui/go-beancount` — Go 1.24.2

## Go Code Style

These conventions apply to all `.go` source in this repository and
are binding on both hand-written code and code produced by subagents
(`generator`, `go-code-reviewer`, etc.). They sit on top of upstream
Go style (Effective Go, Code Review Comments, Google Go Style); where
upstream is silent or permissive, prefer the more minimal form below.

### Doc comments on exported symbols

Required. State the **external contract** concisely:

- For functions and methods: behavior callers can rely on, error
  semantics where they affect callers, preconditions / postconditions.
- For types: role and lifecycle — what the value represents, when it
  becomes valid, when it must be released, what concurrency guarantees
  apply.
- For variables and constants: the invariant the value carries.

Do not narrate the implementation. Exception: when the narration is
itself part of the contract (a complexity bound, an ordering
guarantee, a goroutine-safety claim, a documented allocation
behavior), it stays — but kept tight. Brevity is a feature, not a
side effect; a two-line contract is better than a paragraph that
restates the code.

### Doc comments on unexported symbols

Not required by default. Omit when the name and signature already
make the purpose obvious. Add a doc comment only when:

- the symbol is a non-obvious helper, or carries a subtle invariant a
  reader could plausibly violate; or
- it is a package-internal building block designed for reuse, in
  which case treat it like an exported symbol and document its
  contract with the same brevity.

### Inline comments

Default: none. Code structure, type names, and function names should
carry the meaning. When a comment is genuinely needed, prefer the
shortest form that conveys it — typically 1–3 words that name the
non-obvious property or reference a term defined in the surrounding
godoc. Examples:

    // unreachable
    // avoid aliasing
    // pass 1
    // invariant: sorted

Reserve longer comments for genuinely non-obvious workarounds (cite
the issue or bug). Before writing a long comment, ask whether a
rename, a small extraction, or a clearer type would make the comment
unnecessary; if so, prefer the code change.

Never narrate what the code already says.

### Tests target observable behavior

Tests exercise the package's **exported surface**. Exported symbols
are the externally observable behavior; unexported symbols are
implementation that must remain free to be reorganized without
rewriting tests.

Exceptions where a direct test on an unexported symbol is justified:

- The symbol is a package-internal building block intentionally
  designed for reuse across multiple call sites, where its contract
  has independent value.
- The code path's coverage via the exported API would require
  disproportionately many tests or fragile fixtures, such that
  testing the helper directly reduces total test surface.

When you take an exception, note the rationale briefly (in a test
comment or in the implementation report) so a reviewer can see why
direct testing was chosen.

## Git Workflow

### Commit Message Style

Commit messages and PR descriptions must convey **why** the change was made, what
**behavior or feature** it realizes, and any **design intent, tradeoffs, or
alternatives considered**. Do not narrate internal implementation details.
Mention implementation only when a significant design decision was made — briefly
describe the overall design choice, not the mechanics.

Structure:
- **Subject line**: concise statement of purpose or effect (imperative mood)
- **Body** (when needed): motivation, realized behavior, design rationale,
  rejected alternatives

Do NOT write:
- Descriptions of what the code does mechanically ("add a loop over X")
- References to internal variable names, function names, or file structure
  unless they represent a key design decision

### Clean Commit History

When revising code in response to code review feedback, **amend or fixup the
original commit** rather than adding a new standalone commit. Use
`git commit --amend` for the most recent commit, or `git rebase -i` with
`fixup`/`squash` to fold fixes into the appropriate earlier commit. Only create
a new commit for review feedback if it represents a genuinely independent change.
