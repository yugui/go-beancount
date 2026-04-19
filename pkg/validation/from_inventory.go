package validation

import (
	"fmt"

	"github.com/yugui/go-beancount/pkg/inventory"
)

// FromInventoryError converts an inventory.Error into a validation.Error so
// that a unified diagnostics layer can present inventory and validation
// errors through one channel. Codes without a direct equivalent in the
// validation layer map to CodeInternalError. The span and message are
// preserved; the account name, if any, is folded into the message so it
// survives the lossy conversion.
//
// FromInventoryError replaces the former inventory.Error.AsValidationError
// method. The direction of the adapter was inverted so that pkg/inventory no
// longer imports pkg/validation, breaking a dependency cycle between the two
// packages.
func FromInventoryError(e inventory.Error) Error {
	var vc Code
	switch e.Code {
	case inventory.CodeInvalidBookingMethod:
		vc = CodeInvalidBookingMethod
	case inventory.CodeMultipleAutoPostings:
		vc = CodeMultipleAutoPostings
	default:
		vc = CodeInternalError
	}
	msg := e.Message
	if e.Account != "" {
		msg = fmt.Sprintf("%s: %s", e.Account, msg)
	}
	return Error{
		Code:    vc,
		Span:    e.Span,
		Message: msg,
	}
}
