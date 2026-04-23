// Package api defines the stable interface for beancount postprocessors.
//
// A postprocessor transforms a beancount ledger in response to a plugin
// directive. The [Plugin] interface, along with [Input], [Result], and
// [Error], form the contract between postprocessor implementations and the
// runner in the parent pkg/ext/postproc package. This package depends only on
// pkg/ast so that future loader backends (.so, external process) can
// compile against it without pulling in the runner.
//
// Postprocessor names follow Go fully-qualified package path convention
// (e.g. "github.com/yugui/go-beancount/plugins/auto_accounts") to avoid
// collisions across independently developed postprocessors. When multiple
// instances of the same type are registered, prefix with the package
// path and append a distinguishing suffix.
package api
