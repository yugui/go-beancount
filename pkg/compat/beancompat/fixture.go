// Package beancompat consumes the upstream beancompat JSON fixture suite to
// measure go-beancount's behavioral conformance against other beancount
// implementations.
//
// Fixtures are kept opt-in: each test file iterates every fixture but
// individual cases run only when their name appears in the corresponding
// allowlist (see allowlist.go). This lets new pipeline features be enabled
// one fixture at a time without claiming coverage we have not yet verified.
package beancompat

import "encoding/json"

// Fixture is the top-level shape of every JSON file in beancompat's
// fixtures/parse and fixtures/check directories.
//
// The JSON schema is owned upstream; see beancompat/fixtures/README.md for
// the authoritative reference. KnownDivergences maps an adapter name (we
// use "go-beancount") to a human reason explaining why the adapter is
// permitted to skip this fixture. Such entries take precedence over the
// allowlist so that recorded divergences cannot be accidentally re-enabled.
type Fixture struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Source           string            `json:"source"`
	KnownDivergences map[string]string `json:"known_divergences,omitempty"`
	Expected         Result            `json:"expected"`
}

// Result is the canonical JSON envelope ({errors, directives, options})
// produced whenever a beancount ledger is rendered into beancompat's
// shared shape. It is constructed via two paths: SerializeParsed and
// SerializeChecked produce one from an AST ledger (the "actual" side of
// a fixture comparison), while Fixture.Expected carries one loaded from
// a fixture's JSON file (the "expected" side). The two are compared by
// Match (see match.go) with containment semantics — every field present
// on the expected side must be present and equivalent on the actual
// side, but the actual side may carry additional directives, options,
// or errors that the expected side does not mention.
type Result struct {
	Errors     []string        `json:"errors"`
	Directives []Directive     `json:"directives"`
	Options    json.RawMessage `json:"options,omitempty"`
}

// Directive is the shared envelope every directive shares in the fixture
// schema. Data is held as raw JSON because per-type decoding lives with
// the serializer (see the per-directive helpers in serialize.go) rather
// than being mirrored as a named type in this file.
type Directive struct {
	Type string          `json:"type"`
	Date string          `json:"date"`
	Meta json.RawMessage `json:"meta,omitempty"`
	Data json.RawMessage `json:"data"`
}
