package ast

import "fmt"

// ResolveBookingMethod returns the effective booking method for d, consulting
// opts when d.Booking is BookingDefault (i.e. the Open directive omitted the
// keyword).
//
// Semantics:
//   - d.Booking != BookingDefault → return d.Booking unchanged, no diagnostic.
//   - d.Booking == BookingDefault, opts nil or option unset → BookingStrict via
//     registry default ("STRICT"), no diagnostic.
//   - d.Booking == BookingDefault, option set to "" explicitly → BookingStrict, nil.
//   - d.Booking == BookingDefault, option is a recognized keyword → corresponding method, nil.
//   - d.Booking == BookingDefault, option is an unrecognized value → BookingStrict plus
//     an Error-severity diagnostic with Code "invalid-option" and Span d.Span.
//
// opts may be nil; a nil receiver falls back to the registered default ("STRICT").
// Callers that expect another consumer (typically validations.Apply) to surface
// the returned diagnostics may discard them.
func ResolveBookingMethod(d *Open, opts *OptionValues) (BookingMethod, []Diagnostic) {
	if d.Booking != BookingDefault {
		return d.Booking, nil
	}
	raw := opts.String("booking_method")
	if raw == "" {
		return BookingStrict, nil
	}
	m, err := ParseBookingMethod(raw)
	if err != nil {
		return BookingStrict, []Diagnostic{{
			Code:     invalidOptionCode,
			Severity: Error,
			Span:     d.Span,
			Message:  fmt.Sprintf("invalid booking_method %q; falling back to STRICT", raw),
		}}
	}
	return m, nil
}

// BookingMethod identifies the lot-booking strategy associated with an Open
// directive. The zero value BookingDefault indicates that the directive did
// not specify a booking keyword, in which case consumers should fall back to
// the ledger's configured default (e.g. via the "booking_method" option).
type BookingMethod int

// Booking method constants. These correspond to the uppercase keywords
// accepted after the currency list in an Open directive.
const (
	// BookingDefault represents the absence of an explicit booking keyword
	// on the Open directive. It is the zero value.
	BookingDefault BookingMethod = iota
	// BookingStrict corresponds to the "STRICT" keyword.
	BookingStrict
	// BookingFIFO corresponds to the "FIFO" keyword.
	BookingFIFO
	// BookingLIFO corresponds to the "LIFO" keyword.
	BookingLIFO
	// BookingNone corresponds to the "NONE" keyword.
	BookingNone
	// BookingAverage corresponds to the "AVERAGE" keyword.
	BookingAverage
)

// ParseBookingMethod parses the textual booking keyword used on an Open
// directive. An empty string is treated as an unset field and returns
// BookingDefault with a nil error.
//
// Matching is case-sensitive: beancount upstream requires uppercase
// keywords, so ParseBookingMethod does not accept mixed-case input such as
// "Strict" or "fifo". Unknown values return a zero BookingMethod and a
// descriptive error.
func ParseBookingMethod(s string) (BookingMethod, error) {
	switch s {
	case "":
		return BookingDefault, nil
	case "STRICT":
		return BookingStrict, nil
	case "FIFO":
		return BookingFIFO, nil
	case "LIFO":
		return BookingLIFO, nil
	case "NONE":
		return BookingNone, nil
	case "AVERAGE":
		return BookingAverage, nil
	default:
		return 0, fmt.Errorf("ast: unknown booking method %q", s)
	}
}

// String returns the uppercase keyword corresponding to m, or "DEFAULT" for
// BookingDefault. Unknown values are rendered as "BookingMethod(<int>)" to
// aid debugging.
//
// Note that "DEFAULT" is not a valid ParseBookingMethod input; it is used
// only as a human-readable label for the zero value.
func (m BookingMethod) String() string {
	switch m {
	case BookingDefault:
		return "DEFAULT"
	case BookingStrict:
		return "STRICT"
	case BookingFIFO:
		return "FIFO"
	case BookingLIFO:
		return "LIFO"
	case BookingNone:
		return "NONE"
	case BookingAverage:
		return "AVERAGE"
	default:
		return fmt.Sprintf("BookingMethod(%d)", int(m))
	}
}
