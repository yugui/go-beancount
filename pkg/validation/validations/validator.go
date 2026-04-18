package validations

import (
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/postproc/api"
)

// entryValidator is the contract implemented by each of the per-check
// validators in this package. Apply constructs one instance per run,
// passes every ledger entry through ProcessEntry in canonical order,
// then calls Finish once to flush deferred diagnostics (e.g. end-of-run
// reports). All methods must tolerate concurrent reads of the shared
// *accountstate.State but are never called concurrently on the same
// instance.
type entryValidator interface {
	// Name identifies the validator for diagnostic and debugging
	// purposes. It is not user-facing and need not be stable across
	// releases.
	Name() string

	// ProcessEntry is invoked once per directive in canonical order.
	// Implementations return any diagnostics produced by this
	// directive; the returned slice may be nil when there are no
	// findings.
	ProcessEntry(d ast.Directive) []api.Error

	// Finish is invoked after every directive has been processed,
	// exactly once per Apply call. It returns any deferred diagnostics
	// (e.g. unresolved-pad reports) that require a full-ledger view.
	Finish() []api.Error
}
