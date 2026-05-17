package beancompat

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"

	"github.com/yugui/go-beancount/pkg/ast"
)

// cmpJSONRawMessage normalizes json.RawMessage values to their semantic Go
// representation before comparison, so cmp.Diff over Result (whose Meta,
// Data, and Options fields are json.RawMessage) ignores byte-level
// differences such as object-key order or whitespace while still catching
// genuine value-level divergence. The transformer unmarshal step tolerates
// nil/empty raw messages by surfacing them as nil any so JSON null and an
// absent value compare equal at the semantic level.
var cmpJSONRawMessage = cmp.Transformer("normalizeJSON", func(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		// Surface the raw bytes as a string so a malformed payload
		// shows up as a diff instead of being silently swallowed.
		return string(b)
	}
	return v
})

// mustDate parses s as a YYYY-MM-DD calendar date, failing the test on a
// malformed input. Tests use it inline in AST literals where time.Parse's
// error return would just clutter the table. The returned time.Time is
// UTC-anchored (time.Parse with a zone-less layout defaults to UTC), which
// callers comparing against directive Date fields should keep in mind to
// avoid subtle timezone-mismatch diffs.
func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("mustDate(%q): %v", s, err)
	}
	return d
}

// ledgerOf builds a *ast.Ledger from an explicit directive slice using the
// only public construction path the AST package exposes (an empty Ledger
// plus Insert per directive). Inserting directives in input order is
// sufficient for serializer tests because Ledger's canonical ordering is
// stable and deterministic; tests that care about envelope-level ordering
// pick dates/kinds that don't reorder.
func ledgerOf(t *testing.T, directives ...ast.Directive) *ast.Ledger {
	t.Helper()
	l := &ast.Ledger{}
	for _, d := range directives {
		l.Insert(d)
	}
	return l
}

// assertSerializeMatches drives SerializeParsed over ledger and compares
// the result against wantJSON (a literal Result JSON payload) using
// cmpJSONRawMessage so semantically equivalent JSON shapes compare equal.
// On failure it dumps the canonical pretty-printed form of the actual
// result alongside the diff so the author can copy-paste the corrected
// expectation into the test source instead of hand-editing JSON to match
// a textual diff.
//
// Options are compared only when wantJSON includes an "options" field.
// When absent, the actual Options value is adopted into the expectation
// so callers that test directive-level concerns are not forced to enumerate
// the full options envelope.
func assertSerializeMatches(t *testing.T, ledger *ast.Ledger, wantJSON string) {
	t.Helper()
	got, err := SerializeParsed(ledger)
	if err != nil {
		t.Fatalf("SerializeParsed: %v", err)
	}
	var want Result
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if want.Options == nil {
		// Caller did not assert Options; adopt the actual value so the diff
		// focuses on directives and errors.
		want.Options = got.Options
	}
	if diff := cmp.Diff(want, got, cmpJSONRawMessage); diff != "" {
		t.Errorf("SerializeParsed mismatch (-want +got):\n%s", diff)
		if b, err := json.MarshalIndent(got, "", "  "); err == nil {
			t.Logf("got (canonical JSON):\n%s", b)
		}
	}
}

// TestSerializeInfra_EmptyLedger is a smoke test that exercises ledgerOf,
// SerializeParsed, and assertSerializeMatches end-to-end on the trivial
// (empty) ledger so subsequent steps that add per-directive coverage start
// from a verified harness.
func TestSerializeInfra_EmptyLedger(t *testing.T) {
	assertSerializeMatches(t, ledgerOf(t), `{"errors": [], "directives": []}`)
}

// TestCmpJSONRawMessageNormalizesKeyOrder pins down the central guarantee
// of cmpJSONRawMessage: two json.RawMessage values that are byte-different
// but semantically equivalent (here, JSON objects with the same keys in a
// different order) must compare equal under cmp.Diff. Without this, the
// transformer could silently regress to a no-op and downstream tests would
// still pass on byte-identical fixtures, masking the breakage.
func TestCmpJSONRawMessageNormalizesKeyOrder(t *testing.T) {
	a := Result{Options: json.RawMessage(`{"a":1,"b":2}`)}
	b := Result{Options: json.RawMessage(`{"b": 2, "a": 1}`)}
	if diff := cmp.Diff(a, b, cmpJSONRawMessage); diff != "" {
		t.Errorf("cmpJSONRawMessage failed to normalize key order:\n%s", diff)
	}
}

// TestMustDate covers the happy path of mustDate: a well-formed
// YYYY-MM-DD string parses into the expected calendar fields. The error
// path is exercised implicitly by every other test that calls mustDate
// with a literal date — a malformed literal would be caught at first run.
func TestMustDate(t *testing.T) {
	got := mustDate(t, "2024-01-02")
	if y, m, d := got.Date(); y != 2024 || m != time.January || d != 2 {
		t.Errorf("mustDate fields = (%d, %s, %d), want (2024, January, 2)", y, m, d)
	}
}

// mustDecimal parses s as an apd.Decimal literal, failing the test on a
// malformed input. Tests use it inline in MetaValue literals where the
// (*Decimal, Condition, error) tuple from apd.NewFromString would clutter
// the table. apd.NewFromString preserves the source-side Exponent (so
// "1.5600" round-trips with trailing zeros), which is the property
// MetaNumber serialization is expected to preserve.
func mustDecimal(t *testing.T, s string) apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatalf("mustDecimal(%q): %v", s, err)
	}
	return *d
}

// TestSerializeMeta exercises serializeMeta indirectly through an Open
// directive whose envelope is otherwise held constant across subtests,
// so the resulting "meta" field is the only thing that varies. Each
// subtest covers one MetaValueKind (or one filtering rule). Multi-key
// ordering is also asserted at the byte level because cmpJSONRawMessage
// normalizes key order semantically and would hide a regression in the
// alphabetical-sort guarantee.
func TestSerializeMeta(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"description": {Kind: ast.MetaString, String: "primary checking"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {"description": "primary checking"},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})

	t.Run("account", func(t *testing.T) {
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"reference": {Kind: ast.MetaAccount, String: "Assets:Bank"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {"reference": "Assets:Bank"},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})

	t.Run("currency", func(t *testing.T) {
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"unit": {Kind: ast.MetaCurrency, String: "USD"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {"unit": "USD"},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})

	t.Run("tag", func(t *testing.T) {
		// MetaTag's String field carries whatever the AST stored; the
		// serializer emits it verbatim. Tests assert the mechanical
		// pass-through rather than speculating about a "#" prefix
		// convention that lives at a different layer.
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"category": {Kind: ast.MetaTag, String: "trip-paris"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {"category": "trip-paris"},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})

	t.Run("link", func(t *testing.T) {
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"related": {Kind: ast.MetaLink, String: "invoice-2024-001"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {"related": "invoice-2024-001"},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})

	t.Run("date", func(t *testing.T) {
		// A non-trivial month/day (not 01/01) verifies the ISO formatter
		// is actually formatting the field rather than incidentally
		// matching the directive Date.
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"opened_at": {Kind: ast.MetaDate, Date: mustDate(t, "2023-07-15")},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {"opened_at": "2023-07-15"},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})

	t.Run("number", func(t *testing.T) {
		// "1234.5600" exercises trailing-zero precision preservation:
		// apd.Decimal.String() retains the source Exponent, and the
		// serializer must emit the value as a JSON string so the
		// trailing zeros survive (a JSON number token would normalize).
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"rate": {Kind: ast.MetaNumber, Number: mustDecimal(t, "1234.5600")},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {"rate": "1234.5600"},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})

	t.Run("bool_true", func(t *testing.T) {
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"active": {Kind: ast.MetaBool, Bool: true},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {"active": true},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})

	t.Run("bool_false", func(t *testing.T) {
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"active": {Kind: ast.MetaBool, Bool: false},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {"active": false},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})

	t.Run("amount_skipped", func(t *testing.T) {
		// Upstream parity: serialize_meta only emits primitives, Decimal,
		// and date — Amount is silently dropped. Asserting the key is
		// absent (meta == {}) locks down that the Go side matches that
		// behavior rather than synthesizing a {number, currency} object
		// that no fixture would carry.
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"price": {Kind: ast.MetaAmount, Amount: ast.Amount{
					Number:   mustDecimal(t, "10.00"),
					Currency: "USD",
				}},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})

	t.Run("internal_key_filtered", func(t *testing.T) {
		// Keys with the "__" prefix are parser-internal bookkeeping
		// (e.g. __tolerances__, __automatic__) and must never reach the
		// fixture-visible meta object. Pairing the filtered key with an
		// emit-eligible value verifies the filter checks the key name
		// rather than the value Kind.
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"__tolerances__": {Kind: ast.MetaString, String: "internal"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})

	t.Run("internal_key_filtered_with_visible", func(t *testing.T) {
		// A meta map mixing a "__"-prefixed key with a visible,
		// emit-eligible key must drop only the hidden one. A regression
		// where the filter accidentally short-circuited the entire map
		// (returning {} as soon as any "__" key was seen) would pass the
		// internal_key_filtered subtest but fail here.
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"__hidden__": {Kind: ast.MetaString, String: "internal"},
				"visible":    {Kind: ast.MetaString, String: "user-facing"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {"visible": "user-facing"},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})

	t.Run("multiple_keys_sorted", func(t *testing.T) {
		// Three keys spanning three Kinds with deliberately reversed
		// insertion order so the alphabetical-sort guarantee can be
		// observed at the byte level. The semantic assertion via
		// assertSerializeMatches catches missing keys; the byte-level
		// assertion below catches a regression in sort order that the
		// cmp.Transformer would otherwise normalize away.
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"c": {Kind: ast.MetaBool, Bool: true},
				"a": {Kind: ast.MetaString, String: "alpha"},
				"b": {Kind: ast.MetaNumber, Number: mustDecimal(t, "2.50")},
			}},
		}
		ledger := ledgerOf(t, open)
		assertSerializeMatches(t, ledger, `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {"a": "alpha", "b": "2.50", "c": true},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)

		got, err := SerializeParsed(ledger)
		if err != nil {
			t.Fatalf("SerializeParsed: %v", err)
		}
		if len(got.Directives) != 1 {
			t.Fatalf("got %d directives, want 1", len(got.Directives))
		}
		const wantBytes = `{"a":"alpha","b":"2.50","c":true}`
		if string(got.Directives[0].Meta) != wantBytes {
			t.Errorf("meta byte order = %s, want %s",
				string(got.Directives[0].Meta), wantBytes)
		}
	})

	t.Run("empty_props", func(t *testing.T) {
		// Non-nil but empty Props map. encoding/json's default for a
		// map[K]V value distinguishes between nil (emits null) and a
		// non-nil empty map (emits {}); serializeMeta must produce {}
		// for both shapes, so this subtest pins the non-nil branch and
		// nil_map below pins the nil branch.
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta:       ast.Metadata{Props: make(map[string]ast.MetaValue)},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})

	t.Run("nil_map", func(t *testing.T) {
		// nil Props (the case where the parser produced no metadata at
		// all). Paired with empty_props above so both shapes are pinned
		// to {} on the JSON side, defending against a regression that
		// fell through to encoding/json's default and emitted null for
		// the nil branch.
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
			Meta:       ast.Metadata{Props: nil},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {},
				"data": {"account": "Assets:Cash", "currencies": ["USD"], "booking": null}
			}]
		}`)
	})
}

// TestSerializeOpen exercises openData across the dimensions of the open
// directive's data shape: the currencies array (single / empty / multi
// preserving source order), the booking enum (every non-default keyword
// plus the default-emits-null contract), and the meta integration with
// serializeMeta. The open_single fixture only covers one combination of
// these dimensions; this test pins down the rest so a regression in any
// individual axis surfaces here rather than waiting on a future fixture
// to incidentally re-cover it.
//
// Each subtest constructs exactly one Open directive with a fixed date so
// the JSON literal stays focused on the axis under test. The booking
// subtests deliberately fix Currencies to ["USD"] and the currency
// subtests deliberately leave Booking as the zero value, so a single
// failure points unambiguously to the axis it was testing.
func TestSerializeOpen(t *testing.T) {
	t.Run("single_currency", func(t *testing.T) {
		// Mirrors the open_single fixture's canonical shape; serves as
		// the baseline that all other open subtests vary against.
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Bank",
			Currencies: []string{"USD"},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"account": "Assets:Bank",
					"currencies": ["USD"],
					"booking": null
				}
			}]
		}`)
	})

	t.Run("no_currency", func(t *testing.T) {
		// Pin the nil-Currencies case to JSON [] (not null, not omitted).
		// The serializer substitutes an empty non-nil slice; this subtest
		// guards that substitution against a regression that lets the
		// nil leak through as JSON null and breaks containment over the
		// schema-required array shape.
		open := &ast.Open{
			Date:    mustDate(t, "2024-01-01"),
			Account: "Assets:Bank",
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"account": "Assets:Bank",
					"currencies": [],
					"booking": null
				}
			}]
		}`)
	})

	t.Run("multi_currency_preserves_order", func(t *testing.T) {
		// Source order (USD, JPY, EUR) is deliberately not alphabetical
		// so an accidental sort would surface as a diff. The serializer's
		// contract — preserve source order, do not reorder — is the
		// forward-compatible choice for fixtures that distinguish
		// currency lists by position (e.g. a primary-vs-secondary
		// convention encoded in source order).
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Bank",
			Currencies: []string{"USD", "JPY", "EUR"},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"account": "Assets:Bank",
					"currencies": ["USD", "JPY", "EUR"],
					"booking": null
				}
			}]
		}`)
	})

	// Each named BookingMethod renders as its uppercase keyword; the
	// table form makes adding new methods (e.g. AVERAGE_ONLY) trivial.
	for _, tc := range []struct {
		name    string
		booking ast.BookingMethod
		want    string // JSON value for the "booking" field
	}{
		{"booking_strict", ast.BookingStrict, `"STRICT"`},
		{"booking_fifo", ast.BookingFIFO, `"FIFO"`},
		{"booking_lifo", ast.BookingLIFO, `"LIFO"`},
		{"booking_none", ast.BookingNone, `"NONE"`},
		{"booking_average", ast.BookingAverage, `"AVERAGE"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			open := &ast.Open{
				Date:       mustDate(t, "2024-01-01"),
				Account:    "Assets:Bank",
				Currencies: []string{"USD"},
				Booking:    tc.booking,
			}
			wantJSON := fmt.Sprintf(`{
				"errors": [],
				"directives": [{
					"type": "open",
					"date": "2024-01-01",
					"meta": {},
					"data": {
						"account": "Assets:Bank",
						"currencies": ["USD"],
						"booking": %s
					}
				}]
			}`, tc.want)
			assertSerializeMatches(t, ledgerOf(t, open), wantJSON)
		})
	}

	t.Run("booking_default_emits_null", func(t *testing.T) {
		// BookingDefault is the zero value; the schema requires JSON
		// null (not the string "DEFAULT") so adapters can distinguish
		// "no booking keyword present" from any explicit keyword. This
		// is the case the open_single fixture exercises, replicated here
		// to lock the contract against a regression that would silently
		// emit "DEFAULT" or an empty string.
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Bank",
			Currencies: []string{"USD"},
			Booking:    ast.BookingDefault,
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"account": "Assets:Bank",
					"currencies": ["USD"],
					"booking": null
				}
			}]
		}`)
	})

	t.Run("with_metadata", func(t *testing.T) {
		// One string and one number meta value confirms the Open
		// envelope routes Meta through serializeMeta (which itself is
		// exhaustively tested in TestSerializeMeta). Two kinds rather
		// than one ensures the integration handles a multi-key payload
		// rather than incidentally working on a single-entry map.
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Bank",
			Currencies: []string{"USD"},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"description": {Kind: ast.MetaString, String: "primary checking"},
				"limit":       {Kind: ast.MetaNumber, Number: mustDecimal(t, "5000.00")},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, open), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {
					"description": "primary checking",
					"limit": "5000.00"
				},
				"data": {
					"account": "Assets:Bank",
					"currencies": ["USD"],
					"booking": null
				}
			}]
		}`)
	})
}

// TestSerializeClose covers closeDataPayload across the dimensions the
// close directive can vary along: the account field and meta integration.
// The schema assigns close exactly one data field (account), so a single
// bare-account subtest is sufficient for the data payload itself; the
// metadata subtest pins down that the close envelope routes Meta through
// serializeMeta the same way Open does (TestSerializeMeta exhaustively
// covers serializeMeta's per-Kind behavior, so one or two MetaValue
// entries here is enough to assert the wiring).
func TestSerializeClose(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		// No metadata; verifies the canonical close envelope and the
		// {account} data shape from upstream _parse_helper.py:156-157.
		closeDir := &ast.Close{
			Date:    mustDate(t, "2024-01-01"),
			Account: "Assets:OldChecking",
		}
		assertSerializeMatches(t, ledgerOf(t, closeDir), `{
			"errors": [],
			"directives": [{
				"type": "close",
				"date": "2024-01-01",
				"meta": {},
				"data": {"account": "Assets:OldChecking"}
			}]
		}`)
	})

	t.Run("with_metadata", func(t *testing.T) {
		// Two MetaValue entries spanning two Kinds confirm the close
		// envelope passes Meta through serializeMeta verbatim. A
		// single-entry map could incidentally pass even if the wiring
		// were broken on multi-key payloads.
		closeDir := &ast.Close{
			Date:    mustDate(t, "2024-01-01"),
			Account: "Assets:OldChecking",
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"reason": {Kind: ast.MetaString, String: "closed for redesign"},
				"ticket": {Kind: ast.MetaNumber, Number: mustDecimal(t, "42")},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, closeDir), `{
			"errors": [],
			"directives": [{
				"type": "close",
				"date": "2024-01-01",
				"meta": {
					"reason": "closed for redesign",
					"ticket": "42"
				},
				"data": {"account": "Assets:OldChecking"}
			}]
		}`)
	})
}

// TestSerializeBalance covers balanceDataPayload across the dimensions a
// balance directive can vary along: presence/absence of tolerance,
// source-side decimal precision preservation, and Meta integration. The
// diff_amount slot is always JSON null at the parse tier (the AST has no
// corresponding field; the slot exists for check-tier shape compatibility),
// and every subtest asserts that null explicitly so a future regression
// that elided the key, emitted an empty object, or accidentally populated
// it would surface here.
func TestSerializeBalance(t *testing.T) {
	t.Run("bare_no_tolerance", func(t *testing.T) {
		// Tolerance=nil is the common case: a balance assertion without
		// the "~ N" suffix. The serializer must emit JSON null for the
		// tolerance key (not omit it, not emit an empty string).
		balance := &ast.Balance{
			Date:    mustDate(t, "2024-01-01"),
			Account: "Assets:Cash",
			Amount: ast.Amount{
				Number:   mustDecimal(t, "100.00"),
				Currency: "USD",
			},
		}
		assertSerializeMatches(t, ledgerOf(t, balance), `{
			"errors": [],
			"directives": [{
				"type": "balance",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"account": "Assets:Cash",
					"amount": {"number": "100.00", "currency": "USD"},
					"tolerance": null,
					"diff_amount": null
				}
			}]
		}`)
	})

	t.Run("with_tolerance", func(t *testing.T) {
		// Non-nil Tolerance emits the apd.Decimal.String() form. Using a
		// realistic "0.01" tolerance value mirrors how beancount source
		// syntax expresses tolerance, and the JSON representation is a
		// string (not a number) so source precision survives.
		tol := mustDecimal(t, "0.01")
		balance := &ast.Balance{
			Date:    mustDate(t, "2024-01-01"),
			Account: "Assets:Cash",
			Amount: ast.Amount{
				Number:   mustDecimal(t, "100.00"),
				Currency: "USD",
			},
			Tolerance: &tol,
		}
		assertSerializeMatches(t, ledgerOf(t, balance), `{
			"errors": [],
			"directives": [{
				"type": "balance",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"account": "Assets:Cash",
					"amount": {"number": "100.00", "currency": "USD"},
					"tolerance": "0.01",
					"diff_amount": null
				}
			}]
		}`)
	})

	t.Run("decimal_precision_preserved", func(t *testing.T) {
		// "0.005" tolerance and "100.000" amount both carry trailing
		// significand that apd.Decimal.Exponent encodes; the matchDecimal
		// precision contract from Phase 1 requires this round-trip to
		// preserve those digits. A regression that routed either value
		// through a normalizing path (e.g. .Text('f', N) or float64) would
		// drop the trailing zeros and surface as a diff here.
		tol := mustDecimal(t, "0.005")
		balance := &ast.Balance{
			Date:    mustDate(t, "2024-01-01"),
			Account: "Assets:Cash",
			Amount: ast.Amount{
				Number:   mustDecimal(t, "100.000"),
				Currency: "USD",
			},
			Tolerance: &tol,
		}
		assertSerializeMatches(t, ledgerOf(t, balance), `{
			"errors": [],
			"directives": [{
				"type": "balance",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"account": "Assets:Cash",
					"amount": {"number": "100.000", "currency": "USD"},
					"tolerance": "0.005",
					"diff_amount": null
				}
			}]
		}`)
	})

	t.Run("with_metadata", func(t *testing.T) {
		// One MetaValue confirms the balance envelope routes Meta through
		// serializeMeta. TestSerializeMeta exhaustively covers per-Kind
		// behavior; a single string value here is sufficient to assert
		// the wiring without duplicating that coverage.
		balance := &ast.Balance{
			Date:    mustDate(t, "2024-01-01"),
			Account: "Assets:Cash",
			Amount: ast.Amount{
				Number:   mustDecimal(t, "100.00"),
				Currency: "USD",
			},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"source": {Kind: ast.MetaString, String: "bank statement"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, balance), `{
			"errors": [],
			"directives": [{
				"type": "balance",
				"date": "2024-01-01",
				"meta": {"source": "bank statement"},
				"data": {
					"account": "Assets:Cash",
					"amount": {"number": "100.00", "currency": "USD"},
					"tolerance": null,
					"diff_amount": null
				}
			}]
		}`)
	})
}

// TestSerializeCommodity covers the two dimensions commodity varies along:
// the single-field {currency} data payload and Meta integration through the
// commodity envelope. The "name" meta key in with_metadata mirrors a
// representative real-world shape rather than a synthetic one.
func TestSerializeCommodity(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		// No metadata; verifies the canonical commodity envelope and the
		// {currency} data shape from upstream _parse_helper.py:183-184.
		commodity := &ast.Commodity{
			Date:     mustDate(t, "2024-01-01"),
			Currency: "USD",
		}
		assertSerializeMatches(t, ledgerOf(t, commodity), `{
			"errors": [],
			"directives": [{
				"type": "commodity",
				"date": "2024-01-01",
				"meta": {},
				"data": {"currency": "USD"}
			}]
		}`)
	})

	t.Run("with_metadata", func(t *testing.T) {
		commodity := &ast.Commodity{
			Date:     mustDate(t, "2024-01-01"),
			Currency: "USD",
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"name": {Kind: ast.MetaString, String: "US Dollar"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, commodity), `{
			"errors": [],
			"directives": [{
				"type": "commodity",
				"date": "2024-01-01",
				"meta": {"name": "US Dollar"},
				"data": {"currency": "USD"}
			}]
		}`)
	})
}

// TestSerializePad covers padDataPayload across the dimensions a pad
// directive can vary along: the (account, source_account) pair and Meta
// integration. The schema assigns pad exactly two data fields, both
// required, so the bare subtest is sufficient for the data payload itself;
// the metadata subtest pins down that the pad envelope routes Meta through
// serializeMeta the same way the other directive envelopes do. Both
// subtests use distinct accounts for Account vs PadAccount so a regression
// that swapped the two fields would surface as a diff on both keys rather
// than aliasing into a passing test.
//
// Critically, the bare subtest's wantJSON literal asserts the JSON key is
// "source_account" — not "pad_account" — locking down the AST → JSON
// rename described in padDataPayload's doc comment. Beancompat fixtures
// follow upstream beancount's naming verbatim, so a regression that
// emitted "pad_account" would silently break containment against every
// pad fixture; this assertion catches that regression at the bridge layer
// before any fixture is enabled.
func TestSerializePad(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		pad := &ast.Pad{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			PadAccount: "Equity:Opening-Balances",
		}
		assertSerializeMatches(t, ledgerOf(t, pad), `{
			"errors": [],
			"directives": [{
				"type": "pad",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"account": "Assets:Cash",
					"source_account": "Equity:Opening-Balances"
				}
			}]
		}`)
	})

	t.Run("with_metadata", func(t *testing.T) {
		// One MetaValue confirms the pad envelope routes Meta through
		// serializeMeta. TestSerializeMeta exhaustively covers per-Kind
		// behavior; a single string value here is sufficient to assert
		// the wiring without duplicating that coverage.
		pad := &ast.Pad{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			PadAccount: "Equity:Opening-Balances",
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"reason": {Kind: ast.MetaString, String: "initial seeding"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, pad), `{
			"errors": [],
			"directives": [{
				"type": "pad",
				"date": "2024-01-01",
				"meta": {"reason": "initial seeding"},
				"data": {
					"account": "Assets:Cash",
					"source_account": "Equity:Opening-Balances"
				}
			}]
		}`)
	})
}

// TestSerializeNote covers noteDataPayload across the dimensions a note
// directive can vary along: the {account, comment} data payload, the
// load-bearing schema-omission contract for Tags and Links, and Meta
// integration. The schema assigns note exactly two data fields per upstream
// _parse_helper.py:185-187; the AST additionally carries Tags and Links
// (see pkg/ast/directives.go:170-178), which the canonical shape
// deliberately drops.
//
// The tags_and_links_excluded subtest is the load-bearing assertion that
// proves the schema-omission contract: it populates Tags and Links on the
// AST and asserts the JSON output STILL contains only {account, comment}.
// A regression that "helpfully" added tags/links keys would surface here,
// not in any future fixture (containment tolerates extras, so a fixture
// alone could not catch this).
func TestSerializeNote(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		// No tags, links, or metadata; verifies the canonical note envelope
		// and the {account, comment} data shape from upstream
		// _parse_helper.py:185-187.
		note := &ast.Note{
			Date:    mustDate(t, "2024-01-01"),
			Account: "Assets:Cash",
			Comment: "reconciled by hand",
		}
		assertSerializeMatches(t, ledgerOf(t, note), `{
			"errors": [],
			"directives": [{
				"type": "note",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"account": "Assets:Cash",
					"comment": "reconciled by hand"
				}
			}]
		}`)
	})

	t.Run("tags_and_links_excluded", func(t *testing.T) {
		// Populate Tags and Links on the AST and assert the JSON output
		// contains ONLY {account, comment} — no "tags" or "links" keys.
		// Beancompat fixtures follow upstream _parse_helper.py:185-187
		// verbatim, which intentionally omits these fields. A regression
		// that emitted them would not be caught by any fixture (containment
		// tolerates extra keys), so this subtest is the only place the
		// schema-omission contract is enforced.
		note := &ast.Note{
			Date:    mustDate(t, "2024-01-01"),
			Account: "Assets:Cash",
			Comment: "reconciled by hand",
			Tags:    []string{"trip-2024", "audit"},
			Links:   []string{"invoice-2024-001"},
		}
		assertSerializeMatches(t, ledgerOf(t, note), `{
			"errors": [],
			"directives": [{
				"type": "note",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"account": "Assets:Cash",
					"comment": "reconciled by hand"
				}
			}]
		}`)
	})

	t.Run("with_metadata", func(t *testing.T) {
		// One MetaValue confirms the note envelope routes Meta through
		// serializeMeta. TestSerializeMeta exhaustively covers per-Kind
		// behavior; a single string value here is sufficient to assert
		// the wiring without duplicating that coverage.
		note := &ast.Note{
			Date:    mustDate(t, "2024-01-01"),
			Account: "Assets:Cash",
			Comment: "reconciled by hand",
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"reviewer": {Kind: ast.MetaString, String: "alice"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, note), `{
			"errors": [],
			"directives": [{
				"type": "note",
				"date": "2024-01-01",
				"meta": {"reviewer": "alice"},
				"data": {
					"account": "Assets:Cash",
					"comment": "reconciled by hand"
				}
			}]
		}`)
	})
}

// TestSerializeDocument covers documentDataPayload across the dimensions a
// document directive can vary along: the {account, filename} data payload,
// the load-bearing schema-omission contract for Tags and Links, and Meta
// integration. The schema assigns document exactly two data fields per
// upstream _parse_helper.py:188-190; the AST additionally carries Tags and
// Links (see pkg/ast/directives.go:185-199), which the canonical shape
// deliberately drops (mirroring the same omission applied to Note).
//
// The bare subtest's wantJSON literal pins down two load-bearing schema
// rules at once: the JSON key is "filename" (not "path") — locking down
// the AST Path → JSON filename rename described in documentDataPayload's
// doc comment, the same pattern as Pad's PadAccount → source_account —
// and the absence of any "tags" or "links" keys.
//
// The tags_and_links_excluded subtest is the load-bearing assertion that
// proves the schema-omission contract: it populates Tags and Links on the
// AST and asserts the JSON output STILL contains only {account, filename}.
// A regression that "helpfully" added tags/links keys would surface here,
// not in any future fixture (containment tolerates extras, so a fixture
// alone could not catch this).
func TestSerializeDocument(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		// Pins the Path → filename rename and baseline envelope shape.
		doc := &ast.Document{
			Date:    mustDate(t, "2024-01-01"),
			Account: "Assets:Receipts",
			Path:    "/path/to/receipt.pdf",
		}
		assertSerializeMatches(t, ledgerOf(t, doc), `{
			"errors": [],
			"directives": [{
				"type": "document",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"account": "Assets:Receipts",
					"filename": "/path/to/receipt.pdf"
				}
			}]
		}`)
	})

	t.Run("tags_and_links_excluded", func(t *testing.T) {
		// Populates AST Tags+Links; asserts they don't leak into JSON.
		doc := &ast.Document{
			Date:    mustDate(t, "2024-01-01"),
			Account: "Assets:Receipts",
			Path:    "/path/to/receipt.pdf",
			Tags:    []string{"trip-2024", "audit"},
			Links:   []string{"invoice-2024-001"},
		}
		assertSerializeMatches(t, ledgerOf(t, doc), `{
			"errors": [],
			"directives": [{
				"type": "document",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"account": "Assets:Receipts",
					"filename": "/path/to/receipt.pdf"
				}
			}]
		}`)
	})

	t.Run("with_metadata", func(t *testing.T) {
		// One MetaValue confirms the document envelope routes Meta through
		// serializeMeta. TestSerializeMeta exhaustively covers per-Kind
		// behavior; a single string value here is sufficient to assert
		// the wiring without duplicating that coverage.
		doc := &ast.Document{
			Date:    mustDate(t, "2024-01-01"),
			Account: "Assets:Receipts",
			Path:    "/path/to/receipt.pdf",
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"source": {Kind: ast.MetaString, String: "scanner"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, doc), `{
			"errors": [],
			"directives": [{
				"type": "document",
				"date": "2024-01-01",
				"meta": {"source": "scanner"},
				"data": {
					"account": "Assets:Receipts",
					"filename": "/path/to/receipt.pdf"
				}
			}]
		}`)
	})
}

// TestSerializeEvent covers eventDataPayload across the dimensions an event
// directive can vary along: the {type, description} data payload and Meta
// integration. The schema assigns event exactly two data fields per upstream
// _parse_helper.py:191-193, both of which are renamed from the AST.
//
// The bare subtest's wantJSON literal pins down two load-bearing schema
// rules at once: the JSON keys are "type" and "description" (NOT "name" and
// "value", which match the AST field names) — locking down the AST →
// JSON dual rename described in eventDataPayload's doc comment. Distinct
// values for Name ("location") and Value ("Tokyo") are deliberately chosen
// so a regression that swapped the field-to-key mapping (e.g. emitted
// {type: "Tokyo", description: "location"}) would surface as a diff on both
// keys rather than aliasing into a passing test on identical strings.
func TestSerializeEvent(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		// Distinct Name and Value so the dual rename and the field-to-key
		// mapping are both observable in the diff on regression.
		event := &ast.Event{
			Date:  mustDate(t, "2024-01-01"),
			Name:  "location",
			Value: "Tokyo",
		}
		assertSerializeMatches(t, ledgerOf(t, event), `{
			"errors": [],
			"directives": [{
				"type": "event",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"type": "location",
					"description": "Tokyo"
				}
			}]
		}`)
	})

	t.Run("with_metadata", func(t *testing.T) {
		// One MetaValue confirms the event envelope routes Meta through
		// serializeMeta. TestSerializeMeta exhaustively covers per-Kind
		// behavior; a single string value here is sufficient to assert
		// the wiring without duplicating that coverage.
		event := &ast.Event{
			Date:  mustDate(t, "2024-01-01"),
			Name:  "location",
			Value: "Tokyo",
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"source": {Kind: ast.MetaString, String: "calendar"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, event), `{
			"errors": [],
			"directives": [{
				"type": "event",
				"date": "2024-01-01",
				"meta": {"source": "calendar"},
				"data": {
					"type": "location",
					"description": "Tokyo"
				}
			}]
		}`)
	})
}

// TestSerializeQuery covers queryDataPayload across the dimensions a query
// directive can vary along: the {name, query_string} data payload and Meta
// integration. The schema assigns query exactly two data fields per upstream
// _parse_helper.py:194-196; one of them (query_string) is renamed from the
// AST field BQL.
//
// The bare subtest's wantJSON literal pins down the load-bearing schema
// rule: the JSON key is "query_string" (NOT "bql", which matches the AST
// field name) — locking down the AST BQL → JSON query_string rename
// described in queryDataPayload's doc comment. Distinct values for Name
// ("monthly_cash") and BQL ("SELECT account FROM ...") are deliberately
// chosen so a regression that swapped the field-to-key mapping (e.g.
// emitted {name: "SELECT account FROM ...", query_string: "monthly_cash"})
// would surface as a diff on both keys rather than aliasing into a passing
// test on identical strings.
func TestSerializeQuery(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		// Distinct Name and BQL so the rename and the field-to-key mapping
		// are both observable in the diff on regression.
		query := &ast.Query{
			Date: mustDate(t, "2024-01-01"),
			Name: "monthly_cash",
			BQL:  "SELECT account FROM CLOSE ON 2024-01-01",
		}
		assertSerializeMatches(t, ledgerOf(t, query), `{
			"errors": [],
			"directives": [{
				"type": "query",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"name": "monthly_cash",
					"query_string": "SELECT account FROM CLOSE ON 2024-01-01"
				}
			}]
		}`)
	})

	t.Run("with_metadata", func(t *testing.T) {
		// One MetaValue confirms the query envelope routes Meta through
		// serializeMeta. TestSerializeMeta exhaustively covers per-Kind
		// behavior; a single string value here is sufficient to assert
		// the wiring without duplicating that coverage.
		query := &ast.Query{
			Date: mustDate(t, "2024-01-01"),
			Name: "monthly_cash",
			BQL:  "SELECT account FROM CLOSE ON 2024-01-01",
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"author": {Kind: ast.MetaString, String: "alice"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, query), `{
			"errors": [],
			"directives": [{
				"type": "query",
				"date": "2024-01-01",
				"meta": {"author": "alice"},
				"data": {
					"name": "monthly_cash",
					"query_string": "SELECT account FROM CLOSE ON 2024-01-01"
				}
			}]
		}`)
	})
}

// TestSerializeCustom covers customDataPayload across the dimensions a
// custom directive can vary along: the {type, values} data payload, the
// per-MetaValueKind stringification matrix, the empty-Values default, and
// Meta integration. The schema assigns custom exactly two data fields per
// upstream _parse_helper.py:200-208; the load-bearing complexity lives in
// the values list, which beancount Python serializes via str(v.value) for
// each MetaValue and which Go must replicate verbatim for cross-
// implementation parity.
//
// The bool_capitalization subtest is the load-bearing assertion that
// proves the Python-parity rule: Go's default str(bool) renders "true"/
// "false" but Python renders "True"/"False". Beancompat fixtures originate
// from Python beancount and assert the capitalized form; lowercasing
// would break containment against every fixture that carries a custom
// bool value. This subtest is the only place that contract is enforced
// at the bridge layer before any fixture exercises it.
func TestSerializeCustom(t *testing.T) {
	t.Run("bare_empty_values", func(t *testing.T) {
		// Nil Values must serialize as "values": [] (a concrete empty
		// array, never null and never omitted) per Python's [] default.
		// The schema requires the key to always be present, and a
		// regression that emitted null would surface as a type mismatch
		// in the diagnostic JSON dump even though containment itself
		// might tolerate it.
		custom := &ast.Custom{
			Date:     mustDate(t, "2024-01-01"),
			TypeName: "budget",
		}
		assertSerializeMatches(t, ledgerOf(t, custom), `{
			"errors": [],
			"directives": [{
				"type": "custom",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"type": "budget",
					"values": []
				}
			}]
		}`)
	})

	t.Run("mixed_value_kinds", func(t *testing.T) {
		// Four MetaValue kinds in a single Values list assert the
		// stringification matrix routes each kind through the correct
		// arm of stringifyMetaValue. The order of the values is the
		// AST source order — beancount preserves Custom value order
		// (the Python list is positional) and so must the Go side.
		custom := &ast.Custom{
			Date:     mustDate(t, "2024-01-01"),
			TypeName: "budget",
			Values: []ast.MetaValue{
				{Kind: ast.MetaString, String: "Income"},
				{Kind: ast.MetaNumber, Number: mustDecimal(t, "1000.00")},
				{Kind: ast.MetaCurrency, String: "USD"},
				{Kind: ast.MetaBool, Bool: true},
			},
		}
		assertSerializeMatches(t, ledgerOf(t, custom), `{
			"errors": [],
			"directives": [{
				"type": "custom",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"type": "budget",
					"values": ["Income", "1000.00", "USD", "True"]
				}
			}]
		}`)
	})

	t.Run("bool_capitalization", func(t *testing.T) {
		// Pin the Python-parity rule: str(True)/str(False) emit
		// "True"/"False" (capitalized), not Go's default "true"/"false".
		// Both polarities in one list rule out a regression that
		// happened to capitalize only one branch of the if.
		custom := &ast.Custom{
			Date:     mustDate(t, "2024-01-01"),
			TypeName: "flags",
			Values: []ast.MetaValue{
				{Kind: ast.MetaBool, Bool: true},
				{Kind: ast.MetaBool, Bool: false},
			},
		}
		assertSerializeMatches(t, ledgerOf(t, custom), `{
			"errors": [],
			"directives": [{
				"type": "custom",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"type": "flags",
					"values": ["True", "False"]
				}
			}]
		}`)
	})

	t.Run("date_value", func(t *testing.T) {
		// MetaDate uses the v.Date.Format(isoDate) code path, which is
		// distinct from the v.String passthrough used by other kinds.
		// A regression in this arm would not be caught by any other
		// Custom subtest because no other subtest constructs a MetaDate.
		custom := &ast.Custom{
			Date:     mustDate(t, "2024-01-01"),
			TypeName: "anniversary",
			Values: []ast.MetaValue{
				{Kind: ast.MetaDate, Date: mustDate(t, "2024-06-15")},
			},
		}
		assertSerializeMatches(t, ledgerOf(t, custom), `{
			"errors": [],
			"directives": [{
				"type": "custom",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"type": "anniversary",
					"values": ["2024-06-15"]
				}
			}]
		}`)
	})

	t.Run("amount_value", func(t *testing.T) {
		// MetaAmount stringifies as "{number} {currency}" matching
		// Python beancount Amount.__str__. The number routes through
		// apd.Decimal.String() so source-side precision (e.g. "50.00"
		// trailing zeros) survives.
		custom := &ast.Custom{
			Date:     mustDate(t, "2024-01-01"),
			TypeName: "limit",
			Values: []ast.MetaValue{
				{Kind: ast.MetaAmount, Amount: ast.Amount{
					Number:   mustDecimal(t, "50.00"),
					Currency: "EUR",
				}},
			},
		}
		assertSerializeMatches(t, ledgerOf(t, custom), `{
			"errors": [],
			"directives": [{
				"type": "custom",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"type": "limit",
					"values": ["50.00 EUR"]
				}
			}]
		}`)
	})

	t.Run("with_metadata", func(t *testing.T) {
		// One MetaValue confirms the custom envelope routes Meta through
		// serializeMeta. TestSerializeMeta exhaustively covers per-Kind
		// behavior; a single string value here is sufficient to assert
		// the wiring without duplicating that coverage.
		custom := &ast.Custom{
			Date:     mustDate(t, "2024-01-01"),
			TypeName: "budget",
			Values: []ast.MetaValue{
				{Kind: ast.MetaString, String: "Income"},
			},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"author": {Kind: ast.MetaString, String: "alice"},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, custom), `{
			"errors": [],
			"directives": [{
				"type": "custom",
				"date": "2024-01-01",
				"meta": {"author": "alice"},
				"data": {
					"type": "budget",
					"values": ["Income"]
				}
			}]
		}`)
	})
}

// TestSerializePrice covers priceDataPayload across the dimensions a price
// directive can vary along: the base/quote currency-pair orientation, the
// source-side decimal precision contract on the rate, and Meta integration.
// The schema assigns price exactly two data fields per upstream
// _parse_helper.py:197-199: a top-level "currency" carrying the BASE
// commodity and an embedded "amount" object carrying the QUOTE currency
// alongside the rate.
//
// The bare subtest's wantJSON literal pins down the load-bearing schema
// rule: the JSON top-level "currency" key carries the AST Commodity field
// (the BASE) and "amount.currency" carries the embedded Amount.Currency
// (the QUOTE). Distinct values for Commodity ("EUR") and Amount.Currency
// ("USD") are deliberately chosen so a regression that swapped the two
// (e.g. emitted {currency: "USD", amount: {currency: "EUR"}}) would surface
// as a diff on both keys rather than aliasing into a passing test on
// identical strings. This base-vs-quote orientation is load-bearing
// because beancount price semantics are directional — "1 EUR = 1.10 USD"
// and "1 USD = 1.10 EUR" describe different markets — and a regression
// would silently invert every downstream price lookup.
//
// The TestParseFixtures/price fixture exercises one currency-pair shape;
// the per-axis subtests below anchor base-vs-quote orientation, decimal
// precision preservation, and meta integration as separate concerns so a
// future bridge change can't silently regress one axis.
func TestSerializePrice(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		// Distinct base (EUR) and quote (USD) so the field-to-key mapping
		// is observable in the diff on regression. Mirrors the canonical
		// "1 EUR is worth 1.10 USD" shape from _parse_helper.py:197-199.
		price := &ast.Price{
			Date:      mustDate(t, "2024-01-01"),
			Commodity: "EUR",
			Amount: ast.Amount{
				Number:   mustDecimal(t, "1.10"),
				Currency: "USD",
			},
		}
		assertSerializeMatches(t, ledgerOf(t, price), `{
			"errors": [],
			"directives": [{
				"type": "price",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"currency": "EUR",
					"amount": {"number": "1.10", "currency": "USD"}
				}
			}]
		}`)
	})

	t.Run("decimal_precision_preserved", func(t *testing.T) {
		// "1.10000" exercises trailing-zero precision preservation on the
		// rate: apd.Decimal.String() retains the source Exponent, and the
		// serializer must emit the value as a JSON string so the trailing
		// zeros survive (a JSON number token would normalize them away).
		// This matches the matchDecimal precision contract from Phase 1 —
		// a regression that routed the rate through .Text('f', N) or
		// float64 would drop the trailing zeros and surface as a diff
		// here.
		price := &ast.Price{
			Date:      mustDate(t, "2024-01-01"),
			Commodity: "EUR",
			Amount: ast.Amount{
				Number:   mustDecimal(t, "1.10000"),
				Currency: "USD",
			},
		}
		assertSerializeMatches(t, ledgerOf(t, price), `{
			"errors": [],
			"directives": [{
				"type": "price",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"currency": "EUR",
					"amount": {"number": "1.10000", "currency": "USD"}
				}
			}]
		}`)
	})

	t.Run("with_metadata", func(t *testing.T) {
		// Two MetaValue entries spanning two Kinds confirm the price
		// envelope passes Meta through serializeMeta verbatim.
		// TestSerializeMeta exhaustively covers per-Kind behavior; a
		// multi-key payload here is sufficient to assert the wiring
		// without duplicating that coverage, and a single-entry map could
		// incidentally pass even if multi-key wiring were broken.
		price := &ast.Price{
			Date:      mustDate(t, "2024-01-01"),
			Commodity: "EUR",
			Amount: ast.Amount{
				Number:   mustDecimal(t, "1.10"),
				Currency: "USD",
			},
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"source":     {Kind: ast.MetaString, String: "ECB"},
				"confidence": {Kind: ast.MetaNumber, Number: mustDecimal(t, "0.95")},
			}},
		}
		assertSerializeMatches(t, ledgerOf(t, price), `{
			"errors": [],
			"directives": [{
				"type": "price",
				"date": "2024-01-01",
				"meta": {
					"confidence": "0.95",
					"source": "ECB"
				},
				"data": {
					"currency": "EUR",
					"amount": {"number": "1.10", "currency": "USD"}
				}
			}]
		}`)
	})
}

// txnWithCost constructs a minimal *ast.Transaction whose only purpose is to
// carry one Posting with a CostSpec attached through SerializeParsed.
// Date, Flag, Narration, and the posting's Account / Amount are fixed across
// subtests so each subtest's wantJSON only has to vary the cost object —
// keeping the per-subtest JSON literal focused on what is actually under test
// (the cost_spec encoding) rather than padding every literal with directive
// boilerplate.
func txnWithCost(t *testing.T, cost ast.CostHolder) *ast.Transaction {
	t.Helper()
	return &ast.Transaction{
		Date:      mustDate(t, "2024-01-15"),
		Flag:      '*',
		Narration: "buy lot",
		Postings: []ast.Posting{
			{
				Account: "Assets:Investments",
				Amount: &ast.Amount{
					Number:   mustDecimal(t, "10"),
					Currency: "HOOL",
				},
				Cost: cost,
			},
		},
	}
}

// txnCostWantJSON formats the canonical Transaction envelope around a cost
// JSON fragment. The data payload is fixed (matches txnWithCost) so each
// subtest only needs to specify the expected cost body, which is the quantity
// actually under test.
func txnCostWantJSON(costJSON string) string {
	return fmt.Sprintf(`{
		"errors": [],
		"directives": [{
			"type": "transaction",
			"date": "2024-01-15",
			"meta": {},
			"data": {
				"flag": "*",
				"payee": null,
				"narration": "buy lot",
				"tags": [],
				"links": [],
				"postings": [{
					"account": "Assets:Investments",
					"units": {"number": "10", "currency": "HOOL"},
					"cost": %s,
					"price": null,
					"flag": null,
					"meta": {}
				}]
			}
		}]
	}`, costJSON)
}

// TestSerializeTransaction exercises transactionDataPayload across the
// dimensions a transaction can vary along. This step (Step 13 of the
// Phase 1.5 plan) covers the cost_spec discriminator on Posting.Cost and the
// nil-Cost baseline; later steps add price-annotation and other
// transaction-level subtests (payee, tags, links, multi-posting, elided
// amounts).
//
// The cost_spec subtests pin down the load-bearing schema rule that optional
// sub-fields are OMITTED (not emitted as JSON null) when the AST has no
// value, mirroring upstream beancount's _parse_helper.serialize_cost
// insertion-conditional pattern. Containment matching tolerates extras on
// the actual side, but emitting an explicit null where upstream omits the
// key would surface as a type mismatch in any cross-implementation
// diagnostic dump and make parity reads noisier.
func TestSerializeTransaction(t *testing.T) {
	t.Run("cost_spec_per_unit", func(t *testing.T) {
		// {X CUR}: PerUnit-only cost. Asserts that number_per is emitted,
		// number_total is OMITTED (not null), and currency is read from
		// CostSpec.Currency.
		perUnit := mustDecimal(t, "150.00")
		txn := txnWithCost(t, &ast.CostSpec{
			PerUnit:  &perUnit,
			Currency: "USD",
		})
		assertSerializeMatches(
			t,
			ledgerOf(t, txn),
			txnCostWantJSON(`{"kind": "cost_spec", "currency": "USD", "number_per": "150.00"}`),
		)
	})

	t.Run("cost_spec_total", func(t *testing.T) {
		// {{X CUR}}: Total-only cost. Asserts that number_total is emitted,
		// number_per is OMITTED, and currency reads from CostSpec.Currency.
		total := mustDecimal(t, "1500.00")
		txn := txnWithCost(t, &ast.CostSpec{
			Total:    &total,
			Currency: "USD",
		})
		assertSerializeMatches(
			t,
			ledgerOf(t, txn),
			txnCostWantJSON(`{"kind": "cost_spec", "currency": "USD", "number_total": "1500.00"}`),
		)
	})

	t.Run("cost_spec_combined", func(t *testing.T) {
		// {X # Y CUR}: combined per-unit + total form. Both number_per
		// and number_total must be emitted under the shared currency.
		perUnit := mustDecimal(t, "150.00")
		total := mustDecimal(t, "9.95")
		txn := txnWithCost(t, &ast.CostSpec{
			PerUnit:  &perUnit,
			Total:    &total,
			Currency: "USD",
		})
		assertSerializeMatches(
			t,
			ledgerOf(t, txn),
			txnCostWantJSON(`{"kind": "cost_spec", "currency": "USD", "number_per": "150.00", "number_total": "9.95"}`),
		)
	})

	t.Run("cost_spec_empty", func(t *testing.T) {
		// {}: all sub-fields absent. Output must be exactly
		// {"kind": "cost_spec"} — no currency, number_per, number_total,
		// date, or label keys. Reachable from valid input via the bare
		// {} cost-spec syntax.
		txn := txnWithCost(t, &ast.CostSpec{})
		assertSerializeMatches(
			t,
			ledgerOf(t, txn),
			txnCostWantJSON(`{"kind": "cost_spec"}`),
		)
	})

	t.Run("cost_spec_with_date_and_label", func(t *testing.T) {
		// {X CUR, YYYY-MM-DD, "label"}: pins down the date and label
		// emission paths. Date is formatted as ISO YYYY-MM-DD (distinct
		// from the directive Date so a regression that aliased the two
		// would surface), and the label passes through verbatim.
		acquired := mustDate(t, "2023-06-15")
		perUnit := mustDecimal(t, "150.00")
		txn := txnWithCost(t, &ast.CostSpec{
			PerUnit:  &perUnit,
			Currency: "USD",
			Date:     &acquired,
			Label:    "tax-lot-A",
		})
		assertSerializeMatches(
			t,
			ledgerOf(t, txn),
			txnCostWantJSON(`{"kind": "cost_spec", "currency": "USD", "number_per": "150.00", "date": "2023-06-15", "label": "tax-lot-A"}`),
		)
	})

	t.Run("cost_spec_empty_label_omitted", func(t *testing.T) {
		// Label="" must NOT emit a "label" key. The empty-string sentinel
		// is the AST's "absent label" representation, and the
		// insertion-conditional schema rule maps it to key omission rather
		// than an empty-string value. Containment tolerates a real absent
		// key on the EXPECTED side, but emitting "label": "" on the
		// ACTUAL side would surface as a value mismatch against any
		// fixture that intentionally omits the label field.
		perUnit := mustDecimal(t, "150.00")
		txn := txnWithCost(t, &ast.CostSpec{
			PerUnit:  &perUnit,
			Currency: "USD",
			Label:    "",
		})
		assertSerializeMatches(
			t,
			ledgerOf(t, txn),
			txnCostWantJSON(`{"kind": "cost_spec", "currency": "USD", "number_per": "150.00"}`),
		)
	})

	t.Run("posting_no_cost", func(t *testing.T) {
		// p.Cost == nil baseline: posting "cost" key MUST be JSON null
		// (not omitted, not a "kind: cost_spec" envelope with all sub-
		// fields absent). The schema requires the key to always be present
		// at the posting level, so the discriminated cost_spec only
		// appears when the AST actually carries a CostSpec. This subtest
		// guards the conditional in postingPayload against a regression
		// that always called costSpecPayload on a nil Cost (which would
		// NPE) or that emitted an empty cost_spec envelope where the
		// fixture asserts null.
		txn := txnWithCost(t, nil)
		assertSerializeMatches(t, ledgerOf(t, txn), txnCostWantJSON(`null`))
	})

	t.Run("price_per_unit", func(t *testing.T) {
		// Per-unit `@` price: PriceAnnotation with IsTotal=false. The
		// emitted "price" object must be the {number, currency} shape
		// upstream's serialize_amount(p.price) produces — a regression
		// that wrapped it in a discriminated envelope (e.g.
		// "kind": "price") would diff here.
		txn := txnWithPrice(t, &ast.PriceAnnotation{
			Amount: ast.Amount{
				Number:   mustDecimal(t, "1.10"),
				Currency: "USD",
			},
			IsTotal: false,
		})
		assertSerializeMatches(
			t,
			ledgerOf(t, txn),
			txnPriceWantJSON(`{"number": "1.10", "currency": "USD"}`),
		)
	})

	t.Run("price_total_istotal_dropped", func(t *testing.T) {
		// Total `@@` price: PriceAnnotation with IsTotal=true. The
		// emitted JSON MUST be byte-equivalent to the IsTotal=false case
		// (same number, same currency, no extra discriminator). This
		// subtest is the load-bearing assertion that IsTotal is
		// intentionally dropped — see priceAnnotationPayload's doc
		// comment. A regression that started emitting a "total" key (or
		// silently swapped to a per-unit normalization) would surface
		// here. Upstream beancount's parser similarly normalizes `@@`
		// internally; the JSON cannot distinguish `@` from `@@`.
		txn := txnWithPrice(t, &ast.PriceAnnotation{
			Amount: ast.Amount{
				Number:   mustDecimal(t, "1.10"),
				Currency: "USD",
			},
			IsTotal: true,
		})
		assertSerializeMatches(
			t,
			ledgerOf(t, txn),
			txnPriceWantJSON(`{"number": "1.10", "currency": "USD"}`),
		)
	})

	t.Run("posting_no_price", func(t *testing.T) {
		// p.Price == nil baseline: posting "price" key MUST be JSON null
		// (not omitted, not an empty {number, currency} object). The
		// schema requires the key to always be present at the posting
		// level, so the price object only appears when the AST actually
		// carries a PriceAnnotation. This subtest guards the conditional
		// in postingPayload against a regression that always called
		// priceAnnotationPayload on a nil Price (which would NPE on the
		// embedded Amount access).
		txn := txnWithPrice(t, nil)
		assertSerializeMatches(t, ledgerOf(t, txn), txnPriceWantJSON(`null`))
	})

	t.Run("decimal_precision_preserved_in_price", func(t *testing.T) {
		// Trailing-zero rate: the apd.Decimal's source-side Exponent
		// must survive serialization so "1.10000" stays "1.10000". A
		// regression that routed Number through %f, %g, .Float64(),
		// or any normalization path would silently strip the trailing
		// zeros and diff here. Same precision-preservation contract
		// matchDecimal enforces on the comparison side.
		txn := txnWithPrice(t, &ast.PriceAnnotation{
			Amount: ast.Amount{
				Number:   mustDecimal(t, "1.10000"),
				Currency: "USD",
			},
		})
		assertSerializeMatches(
			t,
			ledgerOf(t, txn),
			txnPriceWantJSON(`{"number": "1.10000", "currency": "USD"}`),
		)
	})

	// ---------------------------------------------------------------------
	// Envelope subtests (Step 15 of the Phase 1.5 plan).
	//
	// These pin down the directive-level fields of transactionDataPayload
	// (flag, payee, narration, tags, links, postings list) as separate axes
	// so a future bridge change cannot silently regress one of them under
	// cover of the others. The transaction_balanced fixture exercises one
	// envelope shape (payee + narration + 2 postings, no tags/links/cost/
	// price); the per-axis subtests below break that down so each schema
	// rule has its own failing case.
	//
	// Unlike the cost_spec / price subtests above, these construct the
	// *ast.Transaction inline rather than through a fixed helper because
	// the envelope tests need to vary multiple Transaction fields at once
	// (flag, payee, tags, links, postings count); a per-axis helper would
	// have to thread every axis as a parameter and would obscure what each
	// subtest is actually asserting.
	t.Run("minimal", func(t *testing.T) {
		// Bare transaction: flag + narration + one posting, no payee, no
		// tags, no links. Pins (1) the empty-but-present array contract
		// for tags and links — they MUST be JSON [] not null and not
		// omitted; (2) the null-payee contract — empty AST Payee MUST emit
		// JSON null via stringOrNil; (3) that the single-posting shape
		// round-trips with units, cost: null, price: null, flag: null,
		// meta: {} all present.
		txn := &ast.Transaction{
			Date:      mustDate(t, "2024-01-15"),
			Flag:      '*',
			Narration: "lunch",
			Postings: []ast.Posting{
				{
					Account: "Expenses:Food",
					Amount: &ast.Amount{
						Number:   mustDecimal(t, "12.00"),
						Currency: "USD",
					},
				},
			},
		}
		assertSerializeMatches(t, ledgerOf(t, txn), `{
			"errors": [],
			"directives": [{
				"type": "transaction",
				"date": "2024-01-15",
				"meta": {},
				"data": {
					"flag": "*",
					"payee": null,
					"narration": "lunch",
					"tags": [],
					"links": [],
					"postings": [{
						"account": "Expenses:Food",
						"units": {"number": "12.00", "currency": "USD"},
						"cost": null,
						"price": null,
						"flag": null,
						"meta": {}
					}]
				}
			}]
		}`)
	})

	t.Run("with_payee", func(t *testing.T) {
		// Same as minimal but with a non-empty Payee. Pins the non-nil
		// branch of stringOrNil: a real payee string MUST round-trip
		// verbatim into JSON. Distinct from minimal because a regression
		// that always nilled Payee (or always emitted "") would diff here.
		txn := &ast.Transaction{
			Date:      mustDate(t, "2024-01-15"),
			Flag:      '*',
			Payee:     "Grocery Store",
			Narration: "lunch",
			Postings: []ast.Posting{
				{
					Account: "Expenses:Food",
					Amount: &ast.Amount{
						Number:   mustDecimal(t, "12.00"),
						Currency: "USD",
					},
				},
			},
		}
		assertSerializeMatches(t, ledgerOf(t, txn), `{
			"errors": [],
			"directives": [{
				"type": "transaction",
				"date": "2024-01-15",
				"meta": {},
				"data": {
					"flag": "*",
					"payee": "Grocery Store",
					"narration": "lunch",
					"tags": [],
					"links": [],
					"postings": [{
						"account": "Expenses:Food",
						"units": {"number": "12.00", "currency": "USD"},
						"cost": null,
						"price": null,
						"flag": null,
						"meta": {}
					}]
				}
			}]
		}`)
	})

	t.Run("with_tags_and_links", func(t *testing.T) {
		// Tags and Links populated. Pins that Transaction-level tags and
		// links ARE part of the schema (per upstream
		// _parse_helper.py:162-163), in deliberate contrast to Note and
		// Document where Tags/Links are intentionally omitted from the
		// data payload. A regression that started filtering them out at
		// the transaction level (or vice versa) would diff here.
		// Multi-element Tags array also pins source order is preserved
		// (no incidental sorting).
		txn := &ast.Transaction{
			Date:      mustDate(t, "2024-01-15"),
			Flag:      '*',
			Narration: "lunch",
			Tags:      []string{"trip-2024", "audit"},
			Links:     []string{"invoice-2024-001"},
			Postings: []ast.Posting{
				{
					Account: "Expenses:Food",
					Amount: &ast.Amount{
						Number:   mustDecimal(t, "12.00"),
						Currency: "USD",
					},
				},
			},
		}
		assertSerializeMatches(t, ledgerOf(t, txn), `{
			"errors": [],
			"directives": [{
				"type": "transaction",
				"date": "2024-01-15",
				"meta": {},
				"data": {
					"flag": "*",
					"payee": null,
					"narration": "lunch",
					"tags": ["trip-2024", "audit"],
					"links": ["invoice-2024-001"],
					"postings": [{
						"account": "Expenses:Food",
						"units": {"number": "12.00", "currency": "USD"},
						"cost": null,
						"price": null,
						"flag": null,
						"meta": {}
					}]
				}
			}]
		}`)
	})

	t.Run("multi_posting_balanced", func(t *testing.T) {
		// Two postings (the canonical balanced-transaction shape). Pins
		// that the postings array preserves AST source order: the
		// Expenses:Food leg comes first, the Assets:Cash leg second,
		// because that is the order they appear in t.Postings. A
		// regression that sorted postings (e.g. by account name) would
		// reorder these and diff here.
		txn := &ast.Transaction{
			Date:      mustDate(t, "2024-01-15"),
			Flag:      '*',
			Narration: "lunch",
			Postings: []ast.Posting{
				{
					Account: "Expenses:Food",
					Amount: &ast.Amount{
						Number:   mustDecimal(t, "100.00"),
						Currency: "USD",
					},
				},
				{
					Account: "Assets:Cash",
					Amount: &ast.Amount{
						Number:   mustDecimal(t, "-100.00"),
						Currency: "USD",
					},
				},
			},
		}
		assertSerializeMatches(t, ledgerOf(t, txn), `{
			"errors": [],
			"directives": [{
				"type": "transaction",
				"date": "2024-01-15",
				"meta": {},
				"data": {
					"flag": "*",
					"payee": null,
					"narration": "lunch",
					"tags": [],
					"links": [],
					"postings": [
						{
							"account": "Expenses:Food",
							"units": {"number": "100.00", "currency": "USD"},
							"cost": null,
							"price": null,
							"flag": null,
							"meta": {}
						},
						{
							"account": "Assets:Cash",
							"units": {"number": "-100.00", "currency": "USD"},
							"cost": null,
							"price": null,
							"flag": null,
							"meta": {}
						}
					]
				}
			}]
		}`)
	})

	t.Run("elided_posting_amount", func(t *testing.T) {
		// One posting with Amount=nil (the auto-balanced posting case the
		// parser emits for a posting without an explicit amount). Pins
		// that postingPayload's nil-Amount branch produces "units": null
		// (the *amountData pointer marshals as JSON null). The
		// missing_sentinel fixture upstream tolerates either key omission
		// or explicit null on the actual side; our serializer emits
		// explicit null, and this subtest pins that contract so a future
		// switch to an empty {number: "", currency: ""} object (or to key
		// omission via omitempty) would surface here.
		txn := &ast.Transaction{
			Date:      mustDate(t, "2024-01-15"),
			Flag:      '*',
			Narration: "auto-balanced",
			Postings: []ast.Posting{
				{
					Account: "Assets:Cash",
					Amount: &ast.Amount{
						Number:   mustDecimal(t, "100.00"),
						Currency: "USD",
					},
				},
				{
					Account: "Expenses:Food",
					Amount:  nil,
				},
			},
		}
		assertSerializeMatches(t, ledgerOf(t, txn), `{
			"errors": [],
			"directives": [{
				"type": "transaction",
				"date": "2024-01-15",
				"meta": {},
				"data": {
					"flag": "*",
					"payee": null,
					"narration": "auto-balanced",
					"tags": [],
					"links": [],
					"postings": [
						{
							"account": "Assets:Cash",
							"units": {"number": "100.00", "currency": "USD"},
							"cost": null,
							"price": null,
							"flag": null,
							"meta": {}
						},
						{
							"account": "Expenses:Food",
							"units": null,
							"cost": null,
							"price": null,
							"flag": null,
							"meta": {}
						}
					]
				}
			}]
		}`)
	})

	t.Run("flag_bang", func(t *testing.T) {
		// Flag '!' (the alternative to '*'). Pins that flagString passes
		// the byte through verbatim rather than hard-coding "*". A
		// regression that always emitted "*" — easy to introduce when
		// special-casing the cleared-transaction path — would diff here.
		txn := &ast.Transaction{
			Date:      mustDate(t, "2024-01-15"),
			Flag:      '!',
			Narration: "needs review",
			Postings: []ast.Posting{
				{
					Account: "Expenses:Food",
					Amount: &ast.Amount{
						Number:   mustDecimal(t, "12.00"),
						Currency: "USD",
					},
				},
			},
		}
		assertSerializeMatches(t, ledgerOf(t, txn), `{
			"errors": [],
			"directives": [{
				"type": "transaction",
				"date": "2024-01-15",
				"meta": {},
				"data": {
					"flag": "!",
					"payee": null,
					"narration": "needs review",
					"tags": [],
					"links": [],
					"postings": [{
						"account": "Expenses:Food",
						"units": {"number": "12.00", "currency": "USD"},
						"cost": null,
						"price": null,
						"flag": null,
						"meta": {}
					}]
				}
			}]
		}`)
	})

	t.Run("posting_with_flag", func(t *testing.T) {
		// Per-posting Flag set to '*'. The transaction-level flag and the
		// posting-level flag are independent in beancount's grammar
		// (postings can carry their own flag to override the transaction
		// flag for booking purposes). This subtest pins that distinction:
		// the transaction's flag goes through flagString (always present)
		// and the posting's flag goes through flagPtr (nil when zero,
		// pointer-to-string otherwise). A regression that conflated the
		// two — e.g. always emitting null at the posting level, or
		// inheriting the transaction flag onto every posting — would diff
		// here.
		txn := &ast.Transaction{
			Date:      mustDate(t, "2024-01-15"),
			Flag:      '!',
			Narration: "mixed flags",
			Postings: []ast.Posting{
				{
					Flag:    '*',
					Account: "Expenses:Food",
					Amount: &ast.Amount{
						Number:   mustDecimal(t, "12.00"),
						Currency: "USD",
					},
				},
			},
		}
		assertSerializeMatches(t, ledgerOf(t, txn), `{
			"errors": [],
			"directives": [{
				"type": "transaction",
				"date": "2024-01-15",
				"meta": {},
				"data": {
					"flag": "!",
					"payee": null,
					"narration": "mixed flags",
					"tags": [],
					"links": [],
					"postings": [{
						"account": "Expenses:Food",
						"units": {"number": "12.00", "currency": "USD"},
						"cost": null,
						"price": null,
						"flag": "*",
						"meta": {}
					}]
				}
			}]
		}`)
	})

	t.Run("empty_payee_emits_null", func(t *testing.T) {
		// Payee="" must emit JSON null, not the empty string. The AST's
		// empty-string sentinel for "absent payee" is collapsed by
		// stringOrNil into a nil *string. Beancount's syntax cannot
		// produce a literal empty-string payee distinct from an absent
		// one, so the collapse is information-preserving for parser-
		// emitted directives. This subtest is the explicit assertion of
		// that contract — a regression that started emitting "payee": ""
		// would diff here even though the with_payee subtest passes
		// (with_payee uses a non-empty string and so cannot catch the
		// empty case).
		txn := &ast.Transaction{
			Date:      mustDate(t, "2024-01-15"),
			Flag:      '*',
			Payee:     "",
			Narration: "lunch",
			Postings: []ast.Posting{
				{
					Account: "Expenses:Food",
					Amount: &ast.Amount{
						Number:   mustDecimal(t, "12.00"),
						Currency: "USD",
					},
				},
			},
		}
		assertSerializeMatches(t, ledgerOf(t, txn), `{
			"errors": [],
			"directives": [{
				"type": "transaction",
				"date": "2024-01-15",
				"meta": {},
				"data": {
					"flag": "*",
					"payee": null,
					"narration": "lunch",
					"tags": [],
					"links": [],
					"postings": [{
						"account": "Expenses:Food",
						"units": {"number": "12.00", "currency": "USD"},
						"cost": null,
						"price": null,
						"flag": null,
						"meta": {}
					}]
				}
			}]
		}`)
	})
}

// txnWithPrice constructs a minimal *ast.Transaction whose only purpose is
// to carry one Posting with a PriceAnnotation attached through
// SerializeParsed. It mirrors txnWithCost's shape so the price-focused
// subtests' wantJSON literals only differ in the "price" slot — keeping
// each per-subtest JSON literal focused on what is actually under test
// (the price encoding) rather than padding every literal with directive
// boilerplate. Kept separate from txnWithCost rather than unified because
// the two helpers exercise opposite sides of the (cost, price) envelope:
// folding them together would require every subtest to thread a
// "which slot are you testing?" parameter, which adds noise without
// removing duplication.
func txnWithPrice(t *testing.T, price *ast.PriceAnnotation) *ast.Transaction {
	t.Helper()
	return &ast.Transaction{
		Date:      mustDate(t, "2024-01-15"),
		Flag:      '*',
		Narration: "buy lot",
		Postings: []ast.Posting{
			{
				Account: "Assets:Investments",
				Amount: &ast.Amount{
					Number:   mustDecimal(t, "10"),
					Currency: "HOOL",
				},
				Price: price,
			},
		},
	}
}

// txnPriceWantJSON formats the canonical Transaction envelope around a
// price JSON fragment. The data payload is fixed (matches txnWithPrice)
// so each subtest only needs to specify the expected price body, which is
// the quantity actually under test. The "cost" slot is hard-coded to null
// because txnWithPrice attaches no CostSpec.
func txnPriceWantJSON(priceJSON string) string {
	return fmt.Sprintf(`{
		"errors": [],
		"directives": [{
			"type": "transaction",
			"date": "2024-01-15",
			"meta": {},
			"data": {
				"flag": "*",
				"payee": null,
				"narration": "buy lot",
				"tags": [],
				"links": [],
				"postings": [{
					"account": "Assets:Investments",
					"units": {"number": "10", "currency": "HOOL"},
					"cost": null,
					"price": %s,
					"flag": null,
					"meta": {}
				}]
			}
		}]
	}`, priceJSON)
}

// TestSerializeLedger exercises the Ledger-to-Result envelope behavior
// in serialize.go that is not specific to any one directive type:
// diagnostic-severity filtering into Errors, header-directive (option,
// plugin, include) skipping, canonical directive ordering as exposed by
// Ledger.All(), the empty-ledger shape, and the explicit nil-ledger guard
// in SerializeParsed. Per-directive shape concerns are covered by the
// TestSerialize<Type> functions; this test pins down the surrounding
// envelope so a regression in the framing logic surfaces here rather than
// being detected only indirectly through fixture failures.
func TestSerializeLedger(t *testing.T) {
	t.Run("empty_ledger", func(t *testing.T) {
		// Empty ledger must produce the canonical envelope with both
		// arrays present and empty (not null), so containment matchers
		// that distinguish "[]" from "null" do not see a spurious type
		// mismatch. TestSerializeInfra_EmptyLedger covers the same case
		// from the perspective of validating the test helpers themselves;
		// this subtest keeps the envelope axis self-contained inside
		// TestSerializeLedger so future changes to the empty-ledger
		// contract are reviewed alongside the rest of the envelope rules.
		assertSerializeMatches(t, ledgerOf(t), `{"errors": [], "directives": []}`)
	})

	t.Run("error_severity_in_errors", func(t *testing.T) {
		// A Diagnostic with Severity=Error must appear in Result.Errors
		// verbatim. ledgerOf does not surface diagnostics, so the
		// Diagnostics field is set inline; the directives slot stays empty
		// to keep this subtest focused on the errors axis only.
		ledger := &ast.Ledger{
			Diagnostics: []ast.Diagnostic{
				{Message: "balance assertion failed", Severity: ast.Error},
			},
		}
		assertSerializeMatches(t, ledger, `{
			"errors": ["balance assertion failed"],
			"directives": []
		}`)
	})

	t.Run("warning_severity_excluded", func(t *testing.T) {
		// Diagnostics with Severity=Warning are intentionally excluded
		// from Result.Errors; beancompat reserves "errors" for fatal
		// reports across implementations, and surfacing warnings would
		// manufacture spurious divergences. The expected envelope is
		// indistinguishable from a ledger with no diagnostics at all.
		ledger := &ast.Ledger{
			Diagnostics: []ast.Diagnostic{
				{Message: "unused option foo", Severity: ast.Warning},
			},
		}
		assertSerializeMatches(t, ledger, `{
			"errors": [],
			"directives": []
		}`)
	})

	t.Run("mixed_severities", func(t *testing.T) {
		// With one Error and one Warning, Result.Errors must contain
		// only the Error message. A regression that emitted both would
		// surface here as an extra entry; one that emitted neither would
		// surface as a missing entry. Two distinct Diagnostics let the
		// filter exercise its predicate on every input, not just the
		// single-element case.
		ledger := &ast.Ledger{
			Diagnostics: []ast.Diagnostic{
				{Message: "fatal: bad reference", Severity: ast.Error},
				{Message: "advisory: deprecated keyword", Severity: ast.Warning},
			},
		}
		assertSerializeMatches(t, ledger, `{
			"errors": ["fatal: bad reference"],
			"directives": []
		}`)
	})

	t.Run("header_directives_filtered", func(t *testing.T) {
		// All three header directive types (option, plugin, include) carry
		// no date and must NOT appear in Result.Directives; the
		// dir.Type == "" guard in serialize() drops them. Including all
		// three plus an Open guards against a future regression that
		// special-cased the filter on directive type rather than relying
		// on the kind-agnostic empty-Type sentinel. The Open is included
		// to confirm the filter does not also drop dated body directives
		// — a regression that bailed out of the loop on the first header
		// would also drop the Open.
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
		}
		opt := &ast.Option{Key: "title", Value: "My Ledger"}
		plugin := &ast.Plugin{Name: "beancount.plugins.auto"}
		include := &ast.Include{Path: "./other.beancount"}
		assertSerializeMatches(t, ledgerOf(t, open, opt, plugin, include), `{
			"errors": [],
			"directives": [{
				"type": "open",
				"date": "2024-01-01",
				"meta": {},
				"data": {
					"account": "Assets:Cash",
					"currencies": ["USD"],
					"booking": null
				}
			}]
		}`)
	})

	t.Run("directive_ordering_preserved", func(t *testing.T) {
		// Distinct dates choose a deterministic canonical order regardless
		// of DirectiveKind tiebreakers: Open(2024-01-01) <
		// Transaction(2024-01-02) < Close(2024-01-03). Insertion order is
		// deliberately scrambled (Close, Open, Transaction) to prove the
		// serializer iterates Ledger.All() — which respects canonical
		// ordering — rather than insertion order. A regression that
		// accidentally iterated a separately-recorded source order would
		// surface here as an out-of-date sequence.
		open := &ast.Open{
			Date:       mustDate(t, "2024-01-01"),
			Account:    "Assets:Cash",
			Currencies: []string{"USD"},
		}
		txn := &ast.Transaction{
			Date:      mustDate(t, "2024-01-02"),
			Flag:      '*',
			Narration: "midday",
			Postings: []ast.Posting{
				{
					Account: "Assets:Cash",
					Amount: &ast.Amount{
						Number:   mustDecimal(t, "10.00"),
						Currency: "USD",
					},
				},
			},
		}
		closeDir := &ast.Close{
			Date:    mustDate(t, "2024-01-03"),
			Account: "Assets:Cash",
		}
		assertSerializeMatches(t, ledgerOf(t, closeDir, open, txn), `{
			"errors": [],
			"directives": [
				{
					"type": "open",
					"date": "2024-01-01",
					"meta": {},
					"data": {
						"account": "Assets:Cash",
						"currencies": ["USD"],
						"booking": null
					}
				},
				{
					"type": "transaction",
					"date": "2024-01-02",
					"meta": {},
					"data": {
						"flag": "*",
						"payee": null,
						"narration": "midday",
						"tags": [],
						"links": [],
						"postings": [{
							"account": "Assets:Cash",
							"units": {"number": "10.00", "currency": "USD"},
							"cost": null,
							"price": null,
							"flag": null,
							"meta": {}
						}]
					}
				},
				{
					"type": "close",
					"date": "2024-01-03",
					"meta": {},
					"data": {"account": "Assets:Cash"}
				}
			]
		}`)
	})

	t.Run("nil_ledger_returns_error", func(t *testing.T) {
		// SerializeParsed has an explicit nil-guard so callers that hand
		// it a zero *ast.Ledger get an actionable error instead of a nil
		// dereference inside the serializer. The exact error message is
		// an implementation detail; this test only pins the contract that
		// SOME error is returned. assertSerializeMatches would itself
		// fatal on the SerializeParsed call here, so this subtest invokes
		// SerializeParsed directly.
		if _, err := SerializeParsed(nil); err == nil {
			t.Errorf("SerializeParsed(nil) error = nil, want non-nil")
		}
		// SerializeChecked has the same nil-guard contract — both
		// entry points should reject a zero *ast.Ledger before
		// touching internal serializer state.
		if _, err := SerializeChecked(nil); err == nil {
			t.Errorf("SerializeChecked(nil) error = nil, want non-nil")
		}
	})
}

// ledgerWithPrecision returns a Ledger whose PrecisionProfile is seeded with
// (currency, precision) pairs; each precision is an integer fractional-digit count.
func ledgerWithPrecision(t *testing.T, observations ...any) *ast.Ledger {
	t.Helper()
	if len(observations)%2 != 0 {
		t.Fatalf("ledgerWithPrecision: odd argument count; pass (currency, precision) pairs")
	}
	pp := ast.NewPrecisionProfile()
	for i := 0; i < len(observations); i += 2 {
		ccy, ok := observations[i].(string)
		if !ok {
			t.Fatalf("ledgerWithPrecision: arg %d must be string (currency), got %T", i, observations[i])
		}
		prec, ok := observations[i+1].(int)
		if !ok {
			t.Fatalf("ledgerWithPrecision: arg %d must be int (precision), got %T", i+1, observations[i+1])
		}
		// "1." + prec zeros gives the desired exponent.
		var s string
		if prec == 0 {
			s = "1"
		} else {
			s = "1."
			for j := 0; j < prec; j++ {
				s += "0"
			}
		}
		d, _, err := apd.NewFromString(s)
		if err != nil {
			t.Fatalf("ledgerWithPrecision: apd.NewFromString(%q): %v", s, err)
		}
		pp.Update(d, ccy)
	}
	return &ast.Ledger{PrecisionProfile: pp}
}

// TestFormatOptionValue exercises formatOptionValue directly for each OptionKind.
func TestFormatOptionValue(t *testing.T) {
	// makeEntry builds an OptionEntry by constructing a ledger with a single
	// option directive set and calling Snapshot to get the typed entry.
	makeEntry := func(t *testing.T, key, raw string) ast.OptionEntry {
		t.Helper()
		l := &ast.Ledger{}
		l.Insert(&ast.Option{Key: key, Value: raw})
		opts, diags := ast.ParseOptions(l)
		if len(diags) > 0 {
			t.Fatalf("ParseOptions diagnostics: %v", diags)
		}
		for _, e := range opts.Snapshot() {
			if e.Key == key {
				return e
			}
		}
		t.Fatalf("key %q not found in Snapshot", key)
		return ast.OptionEntry{}
	}

	t.Run("KindString_plain", func(t *testing.T) {
		e := makeEntry(t, "title", "My Ledger")
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != `"My Ledger"` {
			t.Errorf("got %s, want %q", raw, `"My Ledger"`)
		}
	})

	t.Run("KindString_with_quotes", func(t *testing.T) {
		// Strings containing special characters must be JSON-escaped.
		e := makeEntry(t, "title", `say "hello"`)
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		var got string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got != `say "hello"` {
			t.Errorf("round-trip = %q, want %q", got, `say "hello"`)
		}
	})

	t.Run("KindBool_true", func(t *testing.T) {
		e := makeEntry(t, "infer_tolerance_from_cost", "TRUE")
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != "true" {
			t.Errorf("got %s, want true", raw)
		}
	})

	t.Run("KindBool_false", func(t *testing.T) {
		e := makeEntry(t, "infer_tolerance_from_cost", "FALSE")
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != "false" {
			t.Errorf("got %s, want false", raw)
		}
	})

	t.Run("KindDecimal_typical", func(t *testing.T) {
		e := makeEntry(t, "inferred_tolerance_multiplier", "0.5")
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != `"0.5"` {
			t.Errorf("got %s, want %q", raw, `"0.5"`)
		}
	})

	t.Run("KindDecimal_nil", func(t *testing.T) {
		// Defensive branch: no registered KindDecimal has a nil default, so this
		// path is unreachable via Snapshot today. Tested via struct literal to
		// ensure formatOptionValue does not panic and emits "null".
		e := ast.OptionEntry{Kind: ast.KindDecimal}
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != "null" {
			t.Errorf("got %s, want null", raw)
		}
	})

	t.Run("KindStringList_nonempty", func(t *testing.T) {
		l := &ast.Ledger{}
		l.Insert(&ast.Option{Key: "operating_currency", Value: "USD"})
		l.Insert(&ast.Option{Key: "operating_currency", Value: "EUR"})
		opts, diags := ast.ParseOptions(l)
		if len(diags) > 0 {
			t.Fatalf("ParseOptions diagnostics: %v", diags)
		}
		var e ast.OptionEntry
		for _, en := range opts.Snapshot() {
			if en.Key == "operating_currency" {
				e = en
				break
			}
		}
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		var got []string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		want := []string{"USD", "EUR"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("formatOptionValue(KindStringList) mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("KindStringList_nil_emits_empty_array", func(t *testing.T) {
		// Default operating_currency is nil; must serialize as [].
		var e ast.OptionEntry
		opts := ast.NewOptionValues()
		for _, en := range opts.Snapshot() {
			if en.Key == "operating_currency" {
				e = en
				break
			}
		}
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != "[]" {
			t.Errorf("got %s, want []", raw)
		}
	})

	t.Run("KindInt_zero", func(t *testing.T) {
		e := makeEntry(t, "long_string_maxlines", "0")
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != "0" {
			t.Errorf("got %s, want 0", raw)
		}
	})

	t.Run("KindInt_positive", func(t *testing.T) {
		e := makeEntry(t, "long_string_maxlines", "42")
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != "42" {
			t.Errorf("got %s, want 42", raw)
		}
	})

	t.Run("KindInt_negative", func(t *testing.T) {
		e := makeEntry(t, "long_string_maxlines", "-7")
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != "-7" {
			t.Errorf("got %s, want -7", raw)
		}
	})

	t.Run("KindDecimalMap_empty", func(t *testing.T) {
		// inferred_tolerance_default is registered with an empty default; Step 5 lands consumer.
		opts := ast.NewOptionValues()
		var e ast.OptionEntry
		for _, en := range opts.Snapshot() {
			if en.Key == "inferred_tolerance_default" {
				e = en
				break
			}
		}
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != "{}" {
			t.Errorf("got %s, want {}", raw)
		}
	})

	t.Run("KindDecimalMap_one_entry", func(t *testing.T) {
		e := makeEntry(t, "inferred_tolerance_default", "USD:0.01")
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != `{"USD":"0.01"}` {
			t.Errorf("got %s, want %q", raw, `{"USD":"0.01"}`)
		}
	})

	t.Run("KindDecimalMap_multiple_sorted", func(t *testing.T) {
		// EUR and USD inserted in reverse alphabetical order; output must be sorted.
		l := &ast.Ledger{}
		l.Insert(&ast.Option{Key: "inferred_tolerance_default", Value: "USD:0.01"})
		l.Insert(&ast.Option{Key: "inferred_tolerance_default", Value: "EUR:0.001"})
		opts, diags := ast.ParseOptions(l)
		if len(diags) > 0 {
			t.Fatalf("ParseOptions diagnostics: %v", diags)
		}
		var e ast.OptionEntry
		for _, en := range opts.Snapshot() {
			if en.Key == "inferred_tolerance_default" {
				e = en
				break
			}
		}
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != `{"EUR":"0.001","USD":"0.01"}` {
			t.Errorf("got %s, want %q", raw, `{"EUR":"0.001","USD":"0.01"}`)
		}
	})

	t.Run("KindIntMap_empty", func(t *testing.T) {
		// no KindIntMap key registered yet; Step 4 lands display_precision.
		e := ast.OptionEntry{Kind: ast.KindIntMap}
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != "{}" {
			t.Errorf("got %s, want {}", raw)
		}
	})

	// TODO(step-4): re-add KindIntMap_one_entry, KindIntMap_multiple_sorted, and
	// KindIntMap_negative_value via display_precision once its parser exists.
	// Step 4's parser takes decimal form ("USD:0.01") and derives the digit count
	// via apd.Decimal.Exponent, so parseIntMapEntry is not suitable.

	t.Run("KindDecimal_negative", func(t *testing.T) {
		e := makeEntry(t, "inferred_tolerance_multiplier", "-0.5")
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != `"-0.5"` {
			t.Errorf("got %s, want %q", raw, `"-0.5"`)
		}
	})

	t.Run("KindDecimal_exponent", func(t *testing.T) {
		// apd normalizes 1E-3 to 0.001; the JSON form preserves that representation.
		e := makeEntry(t, "inferred_tolerance_multiplier", "1E-3")
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		if string(raw) != `"0.001"` {
			t.Errorf("got %s, want %q", raw, `"0.001"`)
		}
	})

	t.Run("KindString_with_newline", func(t *testing.T) {
		e := makeEntry(t, "title", "line1\nline2")
		raw, err := formatOptionValue(e)
		if err != nil {
			t.Fatalf("formatOptionValue: %v", err)
		}
		var got string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got != "line1\nline2" {
			t.Errorf("round-trip = %q, want %q", got, "line1\nline2")
		}
	})
}

// TestSerializeOptions verifies the options envelope emitted by SerializeParsed.
func TestSerializeOptions(t *testing.T) {
	t.Run("default_options_envelope", func(t *testing.T) {
		// Golden test: pins the exact JSON for an empty ledger so drift in
		// default-options serialization surfaces immediately. When a new option
		// is added to the registry, update this snapshot.
		got, err := SerializeParsed(&ast.Ledger{})
		if err != nil {
			t.Fatalf("SerializeParsed: %v", err)
		}
		want := `{"infer_tolerance_from_cost":false,"inferred_tolerance_default":{},"inferred_tolerance_multiplier":"0.5","long_string_maxlines":64,"operating_currency":[],"plugin_processing_mode":"","title":""}`
		if string(got.Options) != want {
			t.Errorf("default Options envelope mismatch:\ngot  %s\nwant %s", got.Options, want)
		}
	})

	t.Run("nil_precision_profile_emits_registry_defaults", func(t *testing.T) {
		// Even with no PrecisionProfile, the envelope is emitted with all
		// registered option defaults.
		ledger := &ast.Ledger{PrecisionProfile: nil}
		got, err := SerializeParsed(ledger)
		if err != nil {
			t.Fatalf("SerializeParsed: %v", err)
		}
		if got.Options == nil {
			t.Fatalf("Options = nil, want non-nil envelope")
		}
		var gotMap map[string]any
		if err := json.Unmarshal(got.Options, &gotMap); err != nil {
			t.Fatalf("unmarshal Options: %v", err)
		}
		// display_precision_by_currency must be absent with nil profile.
		if _, ok := gotMap["display_precision_by_currency"]; ok {
			t.Errorf("display_precision_by_currency present, want absent")
		}
		// Registered defaults must be present.
		if _, ok := gotMap["infer_tolerance_from_cost"]; !ok {
			t.Errorf("infer_tolerance_from_cost missing from envelope")
		}
	})

	t.Run("empty_precision_profile_omits_dpbc", func(t *testing.T) {
		// Empty PrecisionProfile (no observations): display_precision_by_currency absent.
		ledger := &ast.Ledger{PrecisionProfile: ast.NewPrecisionProfile()}
		got, err := SerializeParsed(ledger)
		if err != nil {
			t.Fatalf("SerializeParsed: %v", err)
		}
		if got.Options == nil {
			t.Fatalf("Options = nil, want non-nil envelope")
		}
		var gotMap map[string]any
		if err := json.Unmarshal(got.Options, &gotMap); err != nil {
			t.Fatalf("unmarshal Options: %v", err)
		}
		if _, ok := gotMap["display_precision_by_currency"]; ok {
			t.Errorf("display_precision_by_currency present for empty profile, want absent")
		}
	})

	t.Run("single_currency", func(t *testing.T) {
		ledger := ledgerWithPrecision(t, "USD", 2)
		got, err := SerializeParsed(ledger)
		if err != nil {
			t.Fatalf("SerializeParsed: %v", err)
		}
		var gotMap map[string]any
		if err := json.Unmarshal(got.Options, &gotMap); err != nil {
			t.Fatalf("unmarshal Options: %v", err)
		}
		dpbc, ok := gotMap["display_precision_by_currency"]
		if !ok {
			t.Fatalf("display_precision_by_currency missing")
		}
		inner, ok := dpbc.(map[string]any)
		if !ok {
			t.Fatalf("display_precision_by_currency = %T, want map", dpbc)
		}
		if v, ok := inner["USD"]; !ok || v != float64(2) {
			t.Errorf("USD precision = %v, want 2", v)
		}
	})

	t.Run("multiple_currencies_sorted", func(t *testing.T) {
		// USD and JPY inserted out of alphabetical order; output must be alphabetical.
		ledger := ledgerWithPrecision(t, "USD", 2, "JPY", 0)
		got, err := SerializeParsed(ledger)
		if err != nil {
			t.Fatalf("SerializeParsed: %v", err)
		}
		// Extract raw display_precision_by_currency to verify key order.
		want := `{"JPY":0,"USD":2}`
		var gotMap map[string]json.RawMessage
		if err := json.Unmarshal(got.Options, &gotMap); err != nil {
			t.Fatalf("unmarshal Options: %v", err)
		}
		dpbcRaw, ok := gotMap["display_precision_by_currency"]
		if !ok {
			t.Fatalf("display_precision_by_currency missing")
		}
		if string(dpbcRaw) != want {
			t.Errorf("display_precision_by_currency = %s, want %s", dpbcRaw, want)
		}
	})

	t.Run("full_envelope_with_options_and_dpbc", func(t *testing.T) {
		// Ledger with a few options set plus a populated PrecisionProfile: both
		// the registered options and display_precision_by_currency must appear.
		ledger := ledgerWithPrecision(t, "USD", 2)
		synthLedger := &ast.Ledger{}
		synthLedger.Insert(&ast.Option{Key: "title", Value: "Test Ledger"})
		synthLedger.Insert(&ast.Option{Key: "infer_tolerance_from_cost", Value: "TRUE"})
		opts, diags := ast.ParseOptions(synthLedger)
		if len(diags) > 0 {
			t.Fatalf("ParseOptions: %v", diags)
		}
		ledger.Options = opts
		got, err := SerializeParsed(ledger)
		if err != nil {
			t.Fatalf("SerializeParsed: %v", err)
		}
		var gotMap map[string]any
		if err := json.Unmarshal(got.Options, &gotMap); err != nil {
			t.Fatalf("unmarshal Options: %v", err)
		}
		if gotMap["title"] != "Test Ledger" {
			t.Errorf("title = %v, want Test Ledger", gotMap["title"])
		}
		if gotMap["infer_tolerance_from_cost"] != true {
			t.Errorf("infer_tolerance_from_cost = %v, want true", gotMap["infer_tolerance_from_cost"])
		}
		if _, ok := gotMap["display_precision_by_currency"]; !ok {
			t.Errorf("display_precision_by_currency missing")
		}
	})

	t.Run("dpbc_absent_when_no_observations", func(t *testing.T) {
		// Confirm display_precision_by_currency is absent for both nil and
		// empty PrecisionProfile while the rest of the envelope is present.
		cases := []struct {
			name    string
			profile *ast.PrecisionProfile
		}{
			{"nil_profile", nil},
			{"empty_profile", ast.NewPrecisionProfile()},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ledger := &ast.Ledger{PrecisionProfile: tc.profile}
				got, err := SerializeParsed(ledger)
				if err != nil {
					t.Fatalf("SerializeParsed: %v", err)
				}
				var gotMap map[string]any
				if err := json.Unmarshal(got.Options, &gotMap); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if _, ok := gotMap["display_precision_by_currency"]; ok {
					t.Errorf("display_precision_by_currency present, want absent")
				}
				// At least one registered key must be present.
				if _, ok := gotMap["title"]; !ok {
					t.Errorf("title key missing from envelope")
				}
			})
		}
	})

	t.Run("determinism", func(t *testing.T) {
		// Byte-equal output across two calls guards against map iteration nondeterminism.
		ledger := ledgerWithPrecision(t, "USD", 2, "JPY", 0)
		got1, err := SerializeParsed(ledger)
		if err != nil {
			t.Fatalf("first SerializeParsed: %v", err)
		}
		got2, err := SerializeParsed(ledger)
		if err != nil {
			t.Fatalf("second SerializeParsed: %v", err)
		}
		if string(got1.Options) != string(got2.Options) {
			t.Errorf("non-deterministic: first=%s second=%s", got1.Options, got2.Options)
		}
	})

	t.Run("serialize_checked_same_output", func(t *testing.T) {
		ledger := ledgerWithPrecision(t, "USD", 2, "JPY", 0)
		checked, err := SerializeChecked(ledger)
		if err != nil {
			t.Fatalf("SerializeChecked: %v", err)
		}
		var gotMap map[string]json.RawMessage
		if err := json.Unmarshal(checked.Options, &gotMap); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		want := `{"JPY":0,"USD":2}`
		if string(gotMap["display_precision_by_currency"]) != want {
			t.Errorf("SerializeChecked display_precision_by_currency = %s, want %s",
				gotMap["display_precision_by_currency"], want)
		}
	})
}

// TestCostPayload pins the check-tier "kind":"cost" envelope shape
// emitted for a booked *ast.Cost. The transaction_with_cost fixture
// covers the happy path end-to-end through the loader; this focused
// test exercises the schema directly so a regression that affects
// only the optional Label branch or a derived-from-total Number is
// caught even when the fixture file is stale or unavailable.
func TestCostPayload(t *testing.T) {
	date := mustDate(t, "2024-01-15")
	cases := []struct {
		name string
		in   *ast.Cost
		want string
	}{
		{
			name: "per-unit only, no label",
			in: &ast.Cost{
				Number:   mustDecimal(t, "150.00"),
				Currency: "USD",
				Date:     date,
			},
			want: `{"kind":"cost","number":"150.00","currency":"USD","date":"2024-01-15"}`,
		},
		{
			name: "per-unit with label",
			in: &ast.Cost{
				Number:   mustDecimal(t, "100.00"),
				Currency: "USD",
				Date:     date,
				Label:    "lot-A",
			},
			want: `{"kind":"cost","number":"100.00","currency":"USD","date":"2024-01-15","label":"lot-A"}`,
		},
		{
			name: "derived-from-total preserves Number precision",
			in: &ast.Cost{
				// 4.2 / 4.1 at decimal128 precision; .String() preserves the exponent.
				Number:   mustDecimal(t, "1.024390243902439024390243902439024"),
				Currency: "JPY",
				Date:     date,
				Total:    &ast.Amount{Number: mustDecimal(t, "4.2"), Currency: "JPY"},
			},
			want: `{"kind":"cost","number":"1.024390243902439024390243902439024","currency":"JPY","date":"2024-01-15"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := costPayload(tc.in)
			if err != nil {
				t.Fatalf("costPayload: %v", err)
			}
			if diff := cmp.Diff(json.RawMessage(tc.want), got, cmpJSONRawMessage); diff != "" {
				t.Errorf("costPayload mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
