// Package env holds the global BQL function registry and the compile-time
// overload resolver.
//
// Built-in libraries and a future pkg/query/goplug register overloads at
// init time via [Register]; the compiler binds a call site to one overload
// via [Resolve]. The registry is keyed by lowercased function name and
// holds the set of overloads sharing that name. This package depends on
// pkg/query/api (and through it pkg/query/types); api never depends on env,
// keeping the descriptor importable on its own — the same api-vs-runner
// split as pkg/ext/postproc.
package env
