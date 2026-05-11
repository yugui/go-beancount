package beancompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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
	// TODO(beancompat): check-tier (cost discriminator "cost" 切替、booking 後
	// amount、posting interpolation) は未実装。Plan C で対応。
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
	// Result.Options is intentionally left unset (nil) here. AST 側の options
	// 保持メカニズム整備後、別計画 (Plan A) で options 直列化を導入する。それまで
	// options を要求する fixture (display_precision_by_currency 等) は不一致になる。
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
		data, err := closeDataPayload(v)
		if err != nil {
			return Directive{}, err
		}
		return Directive{
			Type: "close",
			Date: formatDate(v.Date),
			Meta: serializeMeta(v.Meta),
			Data: data,
		}, nil
	case *ast.Commodity:
		data, err := commodityDataPayload(v)
		if err != nil {
			return Directive{}, err
		}
		return Directive{
			Type: "commodity",
			Date: formatDate(v.Date),
			Meta: serializeMeta(v.Meta),
			Data: data,
		}, nil
	case *ast.Balance:
		data, err := balanceDataPayload(v)
		if err != nil {
			return Directive{}, err
		}
		return Directive{
			Type: "balance",
			Date: formatDate(v.Date),
			Meta: serializeMeta(v.Meta),
			Data: data,
		}, nil
	case *ast.Pad:
		data, err := padDataPayload(v)
		if err != nil {
			return Directive{}, err
		}
		return Directive{
			Type: "pad",
			Date: formatDate(v.Date),
			Meta: serializeMeta(v.Meta),
			Data: data,
		}, nil
	case *ast.Note:
		data, err := noteDataPayload(v)
		if err != nil {
			return Directive{}, err
		}
		return Directive{
			Type: "note",
			Date: formatDate(v.Date),
			Meta: serializeMeta(v.Meta),
			Data: data,
		}, nil
	case *ast.Document:
		data, err := documentDataPayload(v)
		if err != nil {
			return Directive{}, err
		}
		return Directive{
			Type: "document",
			Date: formatDate(v.Date),
			Meta: serializeMeta(v.Meta),
			Data: data,
		}, nil
	case *ast.Event:
		return placeholderDirective("event", v.Date, v.Meta), nil
	case *ast.Query:
		return placeholderDirective("query", v.Date, v.Meta), nil
	case *ast.Price:
		data, err := priceDataPayload(v)
		if err != nil {
			return Directive{}, err
		}
		return Directive{
			Type: "price",
			Date: formatDate(v.Date),
			Meta: serializeMeta(v.Meta),
			Data: data,
		}, nil
	case *ast.Transaction:
		data, err := transactionDataPayload(v)
		if err != nil {
			return Directive{}, err
		}
		return Directive{
			Type: "transaction",
			Date: formatDate(v.Date),
			Meta: serializeMeta(v.Meta),
			Data: data,
		}, nil
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

// serializeMeta encodes a directive's (or posting's) user-defined metadata
// as a JSON object, mirroring upstream beancount's _parse_helper.serialize_meta.
// It filters keys with the "__" prefix (parser-internal bookkeeping the
// canonical fixture shape never asserts), silently skips MetaAmount values
// to match upstream's primitives-only emission policy, and sorts the
// remaining keys alphabetically so byte-level output is deterministic
// regardless of Go's randomized map iteration.
func serializeMeta(m ast.Metadata) json.RawMessage {
	if len(m.Props) == 0 {
		return json.RawMessage("{}")
	}
	// Populate the emission-key list and value map in a single pass so a
	// key only appears in `keys` when it has a corresponding entry in
	// `out`. Filtered ("__"-prefixed) and skipped (MetaAmount, unknown
	// kinds) keys never enter either collection.
	keys := make([]string, 0, len(m.Props))
	out := make(map[string]any, len(m.Props))
	for k, v := range m.Props {
		if strings.HasPrefix(k, "__") {
			continue
		}
		switch v.Kind {
		case ast.MetaString, ast.MetaAccount, ast.MetaCurrency, ast.MetaTag, ast.MetaLink:
			out[k] = v.String
		case ast.MetaDate:
			out[k] = v.Date.Format(isoDate)
		case ast.MetaNumber:
			// Emit as a JSON string so source-side precision (e.g.
			// "1.5600" with trailing zeros) survives — a JSON number
			// token would normalize.
			out[k] = v.Number.String()
		case ast.MetaBool:
			out[k] = v.Bool
		default:
			// MetaAmount and any future kind: skip silently. Continue
			// without appending k so the emission-key list stays
			// consistent with the value map.
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return json.RawMessage("{}")
	}
	sort.Strings(keys)
	// Marshal in caller-controlled key order. Go's encoding/json happens
	// to sort map[string]any keys today, but pinning the contract at this
	// layer avoids silent regressions if that stdlib detail ever changes.
	b, err := marshalSortedObject(keys, out)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

// marshalSortedObject emits a JSON object whose keys appear in the given
// order. Callers must ensure every key in keys has a corresponding entry
// in values; a missing key is treated as a programmer error and surfaces
// as an explicit error rather than being silently skipped, which would
// hide a serializer bug. Go's encoding/json happens to sort map[string]any
// keys today, but explicitly emitting in caller-controlled order pins the
// contract at this layer rather than relying on a library implementation
// detail.
//
// strings.Builder.Write is documented to never return an error, so the
// (int, error) returns are intentionally discarded with blank assignments.
func marshalSortedObject(keys []string, values map[string]any) (json.RawMessage, error) {
	var buf strings.Builder
	buf.WriteByte('{')
	for i, k := range keys {
		v, ok := values[k]
		if !ok {
			return nil, fmt.Errorf("beancompat: marshalSortedObject: key %q missing from values map", k)
		}
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		_, _ = buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		_, _ = buf.Write(vb)
	}
	buf.WriteByte('}')
	return json.RawMessage(buf.String()), nil
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

// closeDataPayload renders the data payload of a close directive per the
// schema: {"account": string}. Per upstream beancount's _parse_helper.py
// (lines 156-157), close carries no fields beyond the account being closed.
func closeDataPayload(c *ast.Close) (json.RawMessage, error) {
	payload := struct {
		Account string `json:"account"`
	}{
		Account: string(c.Account),
	}
	return json.Marshal(payload)
}

// commodityDataPayload renders the data payload of a commodity directive
// per the schema: {"currency": string}. Per upstream beancount's
// _parse_helper.py (lines 183-184), commodity carries no fields beyond
// the currency it declares.
func commodityDataPayload(c *ast.Commodity) (json.RawMessage, error) {
	payload := struct {
		Currency string `json:"currency"`
	}{
		Currency: c.Currency,
	}
	return json.Marshal(payload)
}

// balanceDataPayload renders the data payload of a balance directive per
// the schema (upstream _parse_helper.py:175-179):
//
//	{
//	  "account":     string,
//	  "amount":      {"number": string, "currency": string},
//	  "tolerance":   string | null,
//	  "diff_amount": null
//	}
//
// Tolerance is emitted as the apd.Decimal.String() of the source value when
// AST holds a non-nil pointer, preserving source-side precision (e.g.
// "0.005" stays "0.005"); nil tolerance becomes JSON null.
//
// diff_amount is always JSON null at the parse tier. The schema includes
// the key so the check tier (which computes booking diffs after asserting
// balances) can populate it without changing shape; the AST has no
// DiffAmount field at parse time, so there is nothing to emit. This is
// intentional, not a bug — the slot exists to keep parse and check tiers
// shape-compatible.
func balanceDataPayload(b *ast.Balance) (json.RawMessage, error) {
	var tolerance *string
	if b.Tolerance != nil {
		s := b.Tolerance.String()
		tolerance = &s
	}
	payload := struct {
		Account    string     `json:"account"`
		Amount     amountData `json:"amount"`
		Tolerance  *string    `json:"tolerance"`
		DiffAmount *string    `json:"diff_amount"`
	}{
		Account: string(b.Account),
		Amount: amountData{
			Number:   b.Amount.Number.String(),
			Currency: b.Amount.Currency,
		},
		Tolerance:  tolerance,
		DiffAmount: nil,
	}
	return json.Marshal(payload)
}

// padDataPayload renders the data payload of a pad directive per the
// schema (upstream _parse_helper.py:180-182):
//
//	{"account": string, "source_account": string}
//
// The JSON key "source_account" intentionally does not match the AST field
// name PadAccount: upstream beancount names the funding account
// "source_account" in its serialized form, and beancompat fixtures follow
// upstream naming verbatim. The rename is load-bearing — emitting
// "pad_account" would silently break containment against every pad
// fixture — so flag it here for any future reader who might mistake the
// mapping for a typo.
func padDataPayload(p *ast.Pad) (json.RawMessage, error) {
	payload := struct {
		Account       string `json:"account"`
		SourceAccount string `json:"source_account"`
	}{
		Account:       string(p.Account),
		SourceAccount: string(p.PadAccount),
	}
	return json.Marshal(payload)
}

// noteDataPayload renders the data payload of a note directive per the
// schema (upstream _parse_helper.py:185-187):
//
//	{"account": string, "comment": string}
//
// The schema deliberately emits only account and comment. AST also carries
// Tags and Links on Note (see pkg/ast/directives.go:170-178), but the
// canonical beancompat shape does not include them — this is upstream's
// intentional design, not a Go-side oversight. Do not add tags/links keys
// here without first checking _parse_helper.py: containment over a fixture
// asserting only {account, comment} would still pass with extras, but the
// shape would diverge from upstream and confuse cross-implementation
// comparisons. The TestSerializeNote/tags_and_links_excluded subtest
// enforces this contract.
func noteDataPayload(n *ast.Note) (json.RawMessage, error) {
	payload := struct {
		Account string `json:"account"`
		Comment string `json:"comment"`
	}{
		Account: string(n.Account),
		Comment: n.Comment,
	}
	return json.Marshal(payload)
}

// documentDataPayload renders the data payload of a document directive per
// the schema (upstream _parse_helper.py:188-190):
//
//	{"account": string, "filename": string}
//
// Two load-bearing schema rules govern this mapping:
//
//  1. The JSON key "filename" intentionally does not match the AST field
//     name Path: upstream beancount names the document path "filename" in
//     its serialized form, and beancompat fixtures follow upstream naming
//     verbatim. The rename is load-bearing — emitting "path" would
//     silently break containment against every document fixture — so
//     flag it here for any future reader who might mistake the mapping
//     for a typo. (Same pattern as Pad's PadAccount → source_account.)
//
//  2. AST also carries Tags and Links on Document (see
//     pkg/ast/directives.go:185-199), but the canonical beancompat shape
//     does not include them — this is upstream's intentional design,
//     mirroring the same omission applied to Note. Do not add tags/links
//     keys here without first checking _parse_helper.py: containment over
//     a fixture asserting only {account, filename} would still pass with
//     extras, but the shape would diverge from upstream and confuse
//     cross-implementation comparisons. The
//     TestSerializeDocument/tags_and_links_excluded subtest enforces
//     this contract.
func documentDataPayload(d *ast.Document) (json.RawMessage, error) {
	payload := struct {
		Account  string `json:"account"`
		Filename string `json:"filename"`
	}{
		Account:  string(d.Account),
		Filename: d.Path,
	}
	return json.Marshal(payload)
}

// amountData mirrors the {number, currency} shape beancompat assigns to
// posting units, transaction amounts, and prices. Number is a string so
// that the source-side precision (e.g. "50.00" with two trailing zeros)
// survives JSON round-trip; routing it through apd.Decimal.String()
// preserves the original Exponent, which matchDecimal compares.
type amountData struct {
	Number   string `json:"number"`
	Currency string `json:"currency"`
}

// transactionDataPayload renders the data payload of a transaction
// directive per the schema:
//
//	{
//	  "flag": string,        // single-char flag, "*" or "!"
//	  "payee": string|null,  // null when no payee
//	  "narration": string,   // possibly empty
//	  "tags": [string...],   // [] not null when none
//	  "links": [string...],  // [] not null when none
//	  "postings": [postingData...]
//	}
//
// Payee uses *string so that "no payee specified" emits JSON null, which
// is how beancompat distinguishes an absent payee from an empty-string
// payee. The AST stores Payee as a bare string with empty == absent;
// since beancount's syntax forbids a literal empty-string payee without
// a quoted form, mapping ""→nil is correct for parser-emitted
// transactions.
//
// Tags and Links are forced to non-nil empty slices so JSON renders "[]"
// rather than "null"; the fixture always supplies a concrete array.
func transactionDataPayload(t *ast.Transaction) (json.RawMessage, error) {
	postings := make([]postingData, 0, len(t.Postings))
	for i := range t.Postings {
		postings = append(postings, postingPayload(&t.Postings[i]))
	}
	tags := t.Tags
	if tags == nil {
		tags = []string{}
	}
	links := t.Links
	if links == nil {
		links = []string{}
	}
	payload := struct {
		Flag      string        `json:"flag"`
		Payee     *string       `json:"payee"`
		Narration string        `json:"narration"`
		Tags      []string      `json:"tags"`
		Links     []string      `json:"links"`
		Postings  []postingData `json:"postings"`
	}{
		Flag:      flagString(t.Flag),
		Payee:     stringOrNil(t.Payee),
		Narration: t.Narration,
		Tags:      tags,
		Links:     links,
		Postings:  postings,
	}
	return json.Marshal(payload)
}

// postingData mirrors beancompat's per-posting envelope. Cost and Price
// are intentionally typed as json.RawMessage placeholders rendered as
// JSON null: the parse tier does not synthesize cost or price values
// (that work belongs to the check tier's cost-spec interpolation), but
// the keys must still appear so containment over a fixture asserting
// "cost": null / "price": null is satisfied. Flag uses *string so the
// zero-rune ("no posting flag") emits JSON null rather than an empty
// string.
type postingData struct {
	Account string          `json:"account"`
	Units   *amountData     `json:"units"`
	Cost    json.RawMessage `json:"cost"`
	Price   json.RawMessage `json:"price"`
	Flag    *string         `json:"flag"`
	Meta    json.RawMessage `json:"meta"`
}

// postingPayload renders one Posting into the postingData envelope. The
// Amount→units conversion uses apd.Decimal.String() so the source-side
// precision survives — fmt.Sprintf("%s", ...) or .Text('f', N) would
// silently normalize trailing zeros and break matchDecimal's precision
// check.
func postingPayload(p *ast.Posting) postingData {
	var units *amountData
	if p.Amount != nil {
		units = &amountData{
			Number:   p.Amount.Number.String(),
			Currency: p.Amount.Currency,
		}
	}
	return postingData{
		Account: string(p.Account),
		Units:   units,
		// TODO(beancompat): Plan D — __missing__ sentinel emission unsupported.
		// AST nil → JSON null at parse tier; the matcher tolerates this via
		// containment.
		Cost:  json.RawMessage("null"),
		Price: json.RawMessage("null"),
		Flag:  flagPtr(p.Flag),
		Meta:  serializeMeta(p.Meta),
	}
}

// priceDataPayload renders the data payload of a price directive per the
// schema:
//
//	{"currency": string, "amount": {"number": string, "currency": string}}
//
// The base commodity lives in Price.Commodity in the AST but is named
// "currency" in the fixture schema; the quote currency lives inside the
// embedded Amount. apd.Decimal.String() preserves the source-side
// precision of the rate (e.g. "1.10" stays "1.10").
func priceDataPayload(p *ast.Price) (json.RawMessage, error) {
	payload := struct {
		Currency string     `json:"currency"`
		Amount   amountData `json:"amount"`
	}{
		Currency: p.Commodity,
		Amount: amountData{
			Number:   p.Amount.Number.String(),
			Currency: p.Amount.Currency,
		},
	}
	return json.Marshal(payload)
}

// flagString renders a transaction-level flag byte as a single-character
// string. The AST guarantees Transaction.Flag is non-zero for
// parser-emitted transactions (either '*' or '!'), so the zero-byte case
// is treated as a programmer-detectable defect and falls through to an
// empty string rather than a JSON null.
func flagString(f byte) string {
	if f == 0 {
		return ""
	}
	return string([]byte{f})
}

// flagPtr renders a posting-level flag byte: zero → nil (JSON null), any
// other byte → pointer to its single-character string. Posting flags are
// genuinely optional in beancount's grammar, which is why this variant
// returns a pointer rather than a bare string.
func flagPtr(f byte) *string {
	if f == 0 {
		return nil
	}
	s := string([]byte{f})
	return &s
}

// stringOrNil maps the AST's empty-string "absent" sentinel to a nil
// *string so json.Marshal emits JSON null. Beancount's syntax cannot
// produce a literal empty-string payee distinct from an absent one, so
// the collapse is information-preserving for parser-emitted directives.
func stringOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
