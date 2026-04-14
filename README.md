# go-beancount

A Go library and toolchain for [Beancount](https://beancount.github.io/) plain-text accounting.

> **Status:** Early development. No stable API yet.

## Overview

go-beancount re-implements Beancount's ledger processing in Go, aiming for full compatibility with the Beancount file format and query language. It is designed as a layered system: a low-level parsing library, higher-level semantic libraries, and a suite of end-user tools built on top.

Python compatibility is explicitly out of scope. Custom plugins must be written in Go.

## Components

### Libraries

| Package | Description |
|---|---|
| `pkg/syntax` | Concrete Syntax Tree (CST) parser with error recovery |
| `pkg/ast` | Abstract Syntax Tree (AST) with include resolution |
| `pkg/format` | Canonical formatter |
| `pkg/validation` | Balance checks, account lifecycle, assertions |
| `pkg/inventory` | Lot-based inventory tracking with FIFO/LIFO/STRICT cost basis |
| `pkg/printer` | File generation from CST or AST |
| `pkg/query` | Beancount Query Language (BQL) engine |
| `pkg/quote` | Plugin-extensible commodity price quoter |
| `pkg/plugin` | Go plugin and external-process plugin loader |

### Commands

| Command | Description |
|---|---|
| `beanfmt` | Canonical ledger file formatter |
| `beancount-lsp` | Language Server Protocol server for editor integration |
| `bean-daemon` | Background server: in-memory ledger store, BQL queries, HTTP/JSON API |
| `beansprout` | User-facing CLI: price quoting, transaction importing, and more |

## Building

This project uses [Bazel](https://bazel.build/) via Bazelisk.

```sh
# Build everything
bazel build //...

# Run all tests
bazel test //...

# After adding or modifying .go files
bazel run //:gazelle

# After modifying go.mod
bazel run //:gazelle -- update-repos -from_file=go.mod
```

## Go Module

```
github.com/yugui/go-beancount
```

Requires Go 1.24.2 or later.

## Roadmap

See [PLAN.md](PLAN.md) for the detailed technical design and phased development plan.

## License

Copyright (C) 2007-2022  Martin Blais.  All Rights Reserved.
Copyright (C) 2026  Yugui Sonoda.  All Rights Reserved.

This code is distributed under the terms of the "GNU GPLv2 only".
See COPYING file for details.
