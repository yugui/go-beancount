// Package api defines the stable interface shared between the
// orchestrator in pkg/quote, source authors writing in-tree quoters,
// and out-of-tree goplug (the mechanism for loading out-of-tree
// quoters from `.so` files at runtime) plugins that contribute
// additional sources.
//
// It contains only declarative types — addressing units (Pair,
// SourceRef, SourceQuery), the request/spec shapes (PriceRequest,
// Spec, Mode), the per-source capability declaration (Capabilities),
// and the Source base interface together with the optional
// LatestSource, AtSource, and RangeSource sub-interfaces. Concrete
// fetching logic, registry management, and orchestration live in
// other packages that import this one.
//
// This package is part of the goplug ABI surface: any incompatible
// change to the types or interfaces declared here requires a
// corresponding goplug APIVersion bump so that older plugins fail
// to load against newer hosts (and vice versa) instead of binding
// against an incompatible interface at runtime.
package api
