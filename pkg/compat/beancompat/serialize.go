package beancompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
)

// isoDate is the canonical YYYY-MM-DD layout beancompat fixtures use. Centralizing
// the layout string here keeps every directive serializer on one definition.
const isoDate = "2006-01-02"

// SerializeParsed lowers a parse-tier ledger into the beancompat Result
// JSON shape.
//
// Containment matching (see match.go) means a directive may legitimately
// expose additional keys beyond what a fixture asserts; the serializer's
// contract is therefore "emit every key beancompat's schema requires for
// each directive type" rather than "emit only what some fixture happens
// to assert". Each tier-specific data payload (open, close, ...) is
// emitted by a dedicated helper so additional directive types can be
// wired in one at a time without re-touching dispatch.
//
// Only diagnostics with [ast.Error] severity become Result.Errors;
// warnings are excluded because beancompat's "errors" field is reserved
// for fatal-tier reports across implementations, and surfacing warnings
// there would manufacture spurious divergences.
func SerializeParsed(ledger *ast.Ledger) (Result, error) {
	if ledger == nil {
		return Result{}, errors.New("beancompat: SerializeParsed: nil ledger")
	}
	return serialize(ledger)
}

// SerializeChecked lowers a check-tier ledger (post plugin pipeline) into
// the beancompat Result JSON shape. The full check-tier surface (cost
// booking, posting interpolation, display precision) is not yet
// implemented; this entry point returns an informative error so any
// check-tier fixture that is mistakenly added to the allowlist fails
// loudly rather than masquerading as a pass.
func SerializeChecked(ledger *ast.Ledger) (Result, error) {
	return Result{}, errors.New("beancompat: SerializeChecked not yet implemented")
}

// serialize lowers a parsed ledger into the beancompat Result shape.
// Directive types other than open emit placeholder Data (JSON null) so
// future fixtures surface clear MissingKey diagnostics when promoted to
// the allowlist, identifying exactly which directive case still needs a
// real payload.
func serialize(ledger *ast.Ledger) (Result, error) {
	out := Result{
		// Initialize Errors as a non-nil empty slice so the JSON form is
		// "errors": [] rather than "errors": null. Beancompat fixtures always
		// supply a concrete array, and a null on the actual side would render
		// as a spurious type mismatch in the diagnostic JSON dump even though
		// containment itself does not care.
		Errors:     []string{},
		Directives: make([]Directive, 0, ledger.Len()),
	}
	for _, diag := range ledger.Diagnostics {
		if diag.Severity == ast.Error {
			out.Errors = append(out.Errors, diag.Message)
		}
	}
	for _, d := range ledger.All() {
		dir, err := serializeDirective(d)
		if err != nil {
			return Result{}, err
		}
		// Header directives (option, plugin, include) carry no date and are
		// not part of beancompat's directive stream; they surface elsewhere
		// (errors, options). Skip them rather than emitting a synthetic
		// 0001-01-01 date that would break date-format containment.
		if dir.Type == "" {
			continue
		}
		out.Directives = append(out.Directives, dir)
	}
	return out, nil
}

// serializeDirective dispatches on the directive's concrete Go type and
// returns the beancompat envelope (type/date/meta/data). Directive types
// whose data payload is not yet implemented produce an envelope with
// data set to JSON null; containment matching against a fixture that
// requires payload keys will then emit a precise MissingKey diagnostic,
// which is the correct signal that the corresponding case needs to be
// filled in.
func serializeDirective(d ast.Directive) (Directive, error) {
	switch v := d.(type) {
	case *ast.Open:
		data, err := openDataPayload(v)
		if err != nil {
			return Directive{}, err
		}
		return Directive{
			Type: "open",
			Date: formatDate(v.Date),
			Meta: serializeMeta(v.Meta),
			Data: data,
		}, nil
	case *ast.Close:
		return placeholderDirective("close", v.Date, v.Meta), nil
	case *ast.Commodity:
		return placeholderDirective("commodity", v.Date, v.Meta), nil
	case *ast.Balance:
		return placeholderDirective("balance", v.Date, v.Meta), nil
	case *ast.Pad:
		return placeholderDirective("pad", v.Date, v.Meta), nil
	case *ast.Note:
		return placeholderDirective("note", v.Date, v.Meta), nil
	case *ast.Document:
		return placeholderDirective("document", v.Date, v.Meta), nil
	case *ast.Event:
		return placeholderDirective("event", v.Date, v.Meta), nil
	case *ast.Query:
		return placeholderDirective("query", v.Date, v.Meta), nil
	case *ast.Price:
		return placeholderDirective("price", v.Date, v.Meta), nil
	case *ast.Transaction:
		return placeholderDirective("transaction", v.Date, v.Meta), nil
	case *ast.Custom:
		return placeholderDirective("custom", v.Date, v.Meta), nil
	case *ast.Option, *ast.Plugin, *ast.Include:
		// Header directives are intentionally dropped; see the caller's
		// dir.Type == "" guard.
		return Directive{}, nil
	default:
		return Directive{}, fmt.Errorf("beancompat: unsupported directive type %T", d)
	}
}

// placeholderDirective builds an envelope for a directive whose data
// payload is not yet implemented. Data is JSON null, so a fixture that
// requires payload keys for this type will produce a clear MissingKey
// diagnostic identifying exactly which directive case still needs a
// real payload.
func placeholderDirective(typ string, date time.Time, meta ast.Metadata) Directive {
	return Directive{
		Type: typ,
		Date: formatDate(date),
		Meta: serializeMeta(meta),
		Data: json.RawMessage("null"),
	}
}

// formatDate renders t in beancompat's canonical YYYY-MM-DD form. The
// zero time would produce "0001-01-01"; callers must skip header
// directives before reaching this function.
func formatDate(t time.Time) string {
	return t.Format(isoDate)
}

// serializeMeta encodes a directive's user-defined metadata as a JSON
// object. Per-key MetaValue encoding is not yet implemented; the stub
// always emits an empty object so fixtures that assert "meta": {} via
// containment are satisfied without the field appearing absent.
func serializeMeta(_ ast.Metadata) json.RawMessage {
	return json.RawMessage("{}")
}

// openDataPayload renders the data payload of an open directive per the schema:
//
//	{"account": string, "currencies": [string...], "booking": string | null}
//
// Currencies are emitted in source order because that is what beancount v3
// (the schema reference implementation) does; alphabetical reordering is a
// known divergence for adapters that sort. Preserving source order is the
// only forward-compatible choice for multi-currency fixtures, even though
// the single-currency open_single fixture cannot distinguish the two.
//
// Booking serializes as a JSON null for [ast.BookingDefault] (the source
// directive carried no booking keyword) and as the canonical uppercase
// keyword otherwise. The matcher requires literal JSON null for the
// "unspecified" case; emitting an empty string would surface as a
// ValueMismatch.
func openDataPayload(o *ast.Open) (json.RawMessage, error) {
	currencies := o.Currencies
	if currencies == nil {
		// Beancompat asserts "currencies" as an array (possibly empty),
		// never null; force the JSON form to "[]" by substituting an
		// empty non-nil slice.
		currencies = []string{}
	}
	payload := struct {
		Account    string   `json:"account"`
		Currencies []string `json:"currencies"`
		Booking    *string  `json:"booking"`
	}{
		Account:    string(o.Account),
		Currencies: currencies,
		Booking:    bookingJSON(o.Booking),
	}
	return json.Marshal(payload)
}

// bookingJSON returns the JSON representation of a BookingMethod: nil
// (rendered as JSON null) for [ast.BookingDefault] and a pointer to the
// canonical keyword string otherwise. Using *string rather than a bare
// string is what makes "null" emerge from json.Marshal — a plain ""
// would round-trip as "" and fail containment.
func bookingJSON(m ast.BookingMethod) *string {
	if m == ast.BookingDefault {
		return nil
	}
	s := m.String()
	return &s
}
