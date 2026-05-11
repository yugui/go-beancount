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
