package beancompat

import (
	"errors"

	"github.com/yugui/go-beancount/pkg/ast"
)

// SerializeParsed lowers a parse-tier ledger into the beancompat Result
// JSON shape. Step 4 of the integration plan implements the body; until
// then it returns an informative error so callers reaching this path
// receive a clear signal rather than silent empty data. The allowlist is
// empty in Step 2, so no test invokes this stub.
func SerializeParsed(ledger *ast.Ledger) (Result, error) {
	return Result{}, errors.New("beancompat: SerializeParsed not yet implemented")
}

// SerializeChecked lowers a check-tier ledger (post plugin pipeline) into
// the beancompat Result JSON shape. See SerializeParsed for the Step 2
// disposition.
func SerializeChecked(ledger *ast.Ledger) (Result, error) {
	return Result{}, errors.New("beancompat: SerializeChecked not yet implemented")
}
