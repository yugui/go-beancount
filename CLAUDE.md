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
