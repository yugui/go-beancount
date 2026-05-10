# go-beancount

A Go library and toolchain for [Beancount](https://beancount.github.io/) plain-text accounting.

## Project status

**This project is in early development.** No stable API yet; expect breaking changes without notice.

The component listings below distinguish between **implemented** packages and commands (which exist and build today) and **planned** ones (which are designed in [PLAN.md](PLAN.md) but not yet written). Do not assume that a name appearing in this README corresponds to working code unless it is in an "Implemented" section.

## Overview

go-beancount re-implements Beancount's ledger processing in Go, aiming for full compatibility with the Beancount file format and query language. It is designed as a layered system: a low-level parsing library, higher-level semantic libraries, and a suite of end-user tools built on top.

Python compatibility is explicitly out of scope. Custom plugins must be written in Go.

## Components

### Implemented packages

| Package | Description |
|---|---|
| `pkg/syntax` | Concrete Syntax Tree (CST) parser with error recovery |
| `pkg/ast` | Abstract Syntax Tree (AST) with include resolution |
| `pkg/format` | Canonical formatter |
| `pkg/printer` | File generation from CST or AST |
| `pkg/inventory` | Lot-based inventory tracking with FIFO/LIFO/STRICT cost basis |
| `pkg/validation` | Balance checks, account lifecycle, padding, document validation (subpackages: `balance`, `document`, `pad`, `validations`) |
| `pkg/ext` | Plugin system: Go `.so` loader (`pkg/ext/goplug`) and post-parse / pre-validation runner with bundled std plugins (`pkg/ext/postproc`) |
| `pkg/loader` | High-level entry point that loads Beancount source through the full pipeline (parse → AST → plugins → validation); mirrors upstream `loader.py`. Exposes `Load`, `LoadReader`, `LoadFile`. |
| `pkg/quote` | Plugin-extensible commodity price quoter (subpackages: `api`, `meta`, `pricedb`, `sourceutil`, `std/ecb`) |
| `pkg/distribute` | Stateless directive-distribution library backing `beanfile`: route directives to files (`route`), detect duplicates across active and commented-out entries (`dedup`), recognize/emit commented directives (`comment`), and perform CST-preserving atomic writes (`merge`) |

### Planned packages

| Package | Description |
|---|---|
| `pkg/query` | Beancount Query Language (BQL) engine (PLAN.md Phase 9) |

### Implemented commands

| Command | Description |
|---|---|
| `beanfmt` | Canonical ledger file formatter |
| `beancheck` | Loads a ledger through the full pipeline (pad, balance, validations, plugins) and reports diagnostics; supports `-strict` |
| `beanprice` | Commodity price fetcher built on `pkg/quote` |
| `beanfile` | Stateless directive distributor for multi-file ledgers: reads a directive stream and files each directive into the appropriate destination with a three-way write / comment / skip decision |

### Planned commands

| Command | Description |
|---|---|
| `beancount-lsp` | Language Server Protocol server for editor integration (PLAN.md Phase 11) |
| `bean-daemon` | Background server: in-memory ledger store, BQL queries, HTTP/JSON API (PLAN.md Phase 10) |
| `beansprout` | User-facing CLI: price quoting, transaction importing, and more (PLAN.md Phase 12) |

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

Requires Go 1.25 or later.

## Roadmap

See [PLAN.md](PLAN.md) for the detailed technical design and phased development plan.

## License

Copyright (C) 2007-2022  Martin Blais.  All Rights Reserved.
Copyright (C) 2026  Yugui Sonoda.  All Rights Reserved.

This code is distributed under the terms of the "GNU GPLv2 only".
See COPYING file for details.
