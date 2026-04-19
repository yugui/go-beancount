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
