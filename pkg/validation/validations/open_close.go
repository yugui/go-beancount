package validations

import (
	"fmt"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation"
	"github.com/yugui/go-beancount/pkg/validation/internal/accountstate"
)

// openClose surfaces Open/Close accounting errors that the plugin's
// initial Build pass already detected. It does not inspect per-entry
// state itself; all heavy lifting lives in accountstate.Build, which
// records duplicate-open directives while walking the ledger to
// construct the canonical per-account lifecycle map.
type openClose struct {
	result accountstate.BuildResult
}

// newOpenClose constructs an openClose validator that reports on the
// duplicate-open diagnostics collected during accountstate.Build.
func newOpenClose(r accountstate.BuildResult) *openClose {
	return &openClose{result: r}
}

// Name identifies this validator for diagnostic and debugging purposes.
func (*openClose) Name() string { return "open_close" }

// ProcessEntry is a no-op: the duplicate-open determination was already
// made by accountstate.Build, so there is no per-directive work here.
func (*openClose) ProcessEntry(ast.Directive) []api.Error { return nil }

// Finish emits one CodeDuplicateOpen diagnostic per duplicate-open
// directive recorded in the BuildResult. The message text matches the
// legacy checker's visitOpen path verbatim for byte-for-byte parity.
func (v *openClose) Finish() []api.Error {
	if len(v.result.DuplicateOpens) == 0 {
		return nil
	}
	errs := make([]api.Error, 0, len(v.result.DuplicateOpens))
	for _, op := range v.result.DuplicateOpens {
		errs = append(errs, api.Error{
			Code:    string(validation.CodeDuplicateOpen),
			Span:    op.Span,
			Message: fmt.Sprintf("account %q already opened", op.Account),
		})
	}
	return errs
}
