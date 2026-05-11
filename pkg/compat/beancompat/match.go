package beancompat

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
)

// DiagKind classifies a single containment mismatch surfaced by Match.
//
// Each Kind exists so that a human reading a failure can distinguish among
// qualitatively different defects without parsing the message string —
// e.g. a value mismatch (real divergence) is different from a precision
// mismatch (often a serializer-formatting issue), and both differ from a
// length mismatch (a structural omission).
type DiagKind int

const (
	// MissingKey indicates an object key required by expected is absent in actual.
	MissingKey DiagKind = iota
	// ValueMismatch indicates a primitive (non-decimal) value differs.
	ValueMismatch
	// DecimalValueMismatch indicates a numeric value differs (apd.Cmp returned non-zero).
	DecimalValueMismatch
	// DecimalPrecisionMismatch indicates two decimal values are numerically equal
	// but their stored precision (apd.Decimal.Exponent) differs.
	DecimalPrecisionMismatch
	// LengthMismatch indicates an array length differs.
	LengthMismatch
	// TypeMismatch indicates expected and actual disagree on shape (object vs scalar vs array).
	TypeMismatch
	// MissingError indicates an error string declared in expected is absent in actual.
	MissingError
)

// String returns the constant's name for use in logs and test failure
// messages. It exists so failures print a self-describing token rather
// than a bare integer.
func (k DiagKind) String() string {
	switch k {
	case MissingKey:
		return "MissingKey"
	case ValueMismatch:
		return "ValueMismatch"
	case DecimalValueMismatch:
		return "DecimalValueMismatch"
	case DecimalPrecisionMismatch:
		return "DecimalPrecisionMismatch"
	case LengthMismatch:
		return "LengthMismatch"
	case TypeMismatch:
		return "TypeMismatch"
	case MissingError:
		return "MissingError"
	default:
		return fmt.Sprintf("DiagKind(%d)", int(k))
	}
}

// Diagnostic carries enough context for a human to reproduce a Match
// failure without re-running the test. Path is a JSON-pointer-style locator
// (e.g. "directives[3].data.postings[1].cost.number"), and Got/Want hold
// the offending values verbatim.
type Diagnostic struct {
	Path string
	Kind DiagKind
	Got  any
	Want any
	Msg  string
}

// String returns "<Path>: <Msg>" so a slice of diagnostics renders as a
// readable, locator-prefixed list. Msg is pre-formatted at construction
// time; this method only adds the path prefix.
func (d Diagnostic) String() string {
	return d.Path + ": " + d.Msg
}

// decimalKeys lists object keys whose string values are interpreted as
// decimal literals rather than opaque strings. Beancompat encodes amounts
// (and their per-unit / total variants) as decimal strings so trailing
// zeros survive the round-trip; routing these through matchDecimal is
// what lets the matcher distinguish "value differs" from "precision
// differs". Any other string field — account names, currency codes,
// narrations — compares by exact equality even if it happens to look
// numeric.
//
// Note: the Units sentinel comparison (a "__missing__": true marker that
// the serializer is expected to emit for explicitly-absent Units in
// transactions) is not currently special-cased here. Once the serializer
// emits that sentinel in Step 4, this matcher's existing map-key
// containment behavior already does the right thing; no Kind-level
// special case is anticipated.
var decimalKeys = map[string]bool{
	"number":       true,
	"number_per":   true,
	"number_total": true,
}

// Match returns nil iff actual contains expected per beancompat's
// containment rules. On mismatch it returns one Diagnostic per discovered
// violation; it deliberately does not short-circuit, so a single test run
// surfaces every divergence at once instead of forcing a whack-a-mole fix
// cycle.
//
// Order is errors-first (in the order declared by expected), then
// directives (index-major, depth-first within each), then options.
func Match(expected, actual Result) []Diagnostic {
	var diags []Diagnostic

	// 1. Errors — multiset-subset semantics. Each expected error string
	// must consume one matching un-consumed entry in actual.Errors so
	// duplicate expectations are honored.
	consumed := make([]bool, len(actual.Errors))
	for i, want := range expected.Errors {
		matched := false
		for j, got := range actual.Errors {
			if consumed[j] {
				continue
			}
			if got == want {
				consumed[j] = true
				matched = true
				break
			}
		}
		if !matched {
			diags = append(diags, Diagnostic{
				Path: fmt.Sprintf("errors[%d]", i),
				Kind: MissingError,
				Got:  actual.Errors,
				Want: want,
				Msg:  fmt.Sprintf("expected error %q not present in actual.errors", want),
			})
		}
	}

	// 2. Directives — length must match exactly. If it doesn't we still
	// walk the common prefix so nested diagnostics are not hidden behind
	// the length error.
	wantN := len(expected.Directives)
	gotN := len(actual.Directives)
	if wantN != gotN {
		diags = append(diags, Diagnostic{
			Path: "directives",
			Kind: LengthMismatch,
			Got:  gotN,
			Want: wantN,
			Msg:  fmt.Sprintf("directives length differs: want=%d got=%d", wantN, gotN),
		})
	}
	minN := wantN
	if gotN < minN {
		minN = gotN
	}
	for i := 0; i < minN; i++ {
		diags = append(diags, matchDirective(fmt.Sprintf("directives[%d]", i),
			expected.Directives[i], actual.Directives[i])...)
	}

	// 3. Options — containment over the decoded JSON tree.
	diags = append(diags, matchRawJSON("options", expected.Options, actual.Options)...)

	return diags
}

// matchDirective containment-matches one Directive pair. Type and Date
// are compared exactly as scalars; Meta and Data are decoded as JSON
// trees so the recursive matcher can apply containment semantics.
func matchDirective(path string, want, got Directive) []Diagnostic {
	var diags []Diagnostic
	if want.Type != got.Type {
		diags = append(diags, Diagnostic{
			Path: path + ".type",
			Kind: ValueMismatch,
			Got:  got.Type,
			Want: want.Type,
			Msg:  fmt.Sprintf("directive type differs: want=%q got=%q", want.Type, got.Type),
		})
	}
	if want.Date != got.Date {
		diags = append(diags, Diagnostic{
			Path: path + ".date",
			Kind: ValueMismatch,
			Got:  got.Date,
			Want: want.Date,
			Msg:  fmt.Sprintf("directive date differs: want=%q got=%q", want.Date, got.Date),
		})
	}
	diags = append(diags, matchRawJSON(path+".meta", want.Meta, got.Meta)...)
	diags = append(diags, matchRawJSON(path+".data", want.Data, got.Data)...)
	return diags
}

// matchRawJSON decodes two json.RawMessage payloads into generic trees
// and containment-matches them. An empty raw message on the expected
// side imposes no constraint, mirroring the "key absent in expected"
// case at the top level.
func matchRawJSON(path string, want, got json.RawMessage) []Diagnostic {
	// An unset expected Meta/Options/Data field (len(want) == 0) imposes
	// no constraint. The matcher does not distinguish "field absent" from
	// "field present-and-empty {}" here; both are treated as "no
	// constraint" on actual.
	if len(want) == 0 {
		return nil
	}
	var wv any
	if err := json.Unmarshal(want, &wv); err != nil {
		return []Diagnostic{{
			Path: path,
			Kind: ValueMismatch,
			Want: string(want),
			Got:  string(got),
			Msg:  "expected JSON is not valid: " + err.Error(),
		}}
	}
	if len(got) == 0 {
		// Result wants something here but actual has nothing.
		return []Diagnostic{{
			Path: path,
			Kind: MissingKey,
			Want: wv,
			Got:  nil,
			Msg:  "expected value present but actual is missing",
		}}
	}
	var gv any
	if err := json.Unmarshal(got, &gv); err != nil {
		return []Diagnostic{{
			Path: path,
			Kind: ValueMismatch,
			Want: string(want),
			Got:  string(got),
			Msg:  "actual JSON is not valid: " + err.Error(),
		}}
	}
	return containsAny(path, "", wv, gv)
}

// containsAny is the recursive heart of the matcher. It walks two
// JSON-decoded values (produced by encoding/json into the generic
// any-tree of map[string]any / []any / string / bool / float64 / nil)
// and emits one Diagnostic per containment violation. parentKey is the
// key under which want/got live in their enclosing object, used solely
// to route decimal-coded fields through matchDecimal.
func containsAny(path, parentKey string, want, got any) []Diagnostic {
	// nil on either side: containment treats nil-want as "no constraint"
	// only when it represents an absent value; here we arrive only when
	// expected actually emitted JSON null, so demand the same on actual.
	if want == nil {
		if got == nil {
			return nil
		}
		return []Diagnostic{{
			Path: path,
			Kind: ValueMismatch,
			Want: nil,
			Got:  got,
			Msg:  fmt.Sprintf("value differs: want=null got=%v", got),
		}}
	}

	switch wv := want.(type) {
	case map[string]any:
		gv, ok := got.(map[string]any)
		if !ok {
			return []Diagnostic{{
				Path: path,
				Kind: TypeMismatch,
				Want: want,
				Got:  got,
				Msg:  fmt.Sprintf("type differs: want=object got=%s", jsonTypeName(got)),
			}}
		}
		var diags []Diagnostic
		// Sort keys for stable diagnostic order.
		for _, k := range sortedKeys(wv) {
			subPath := joinPath(path, k)
			subWant := wv[k]
			subGot, present := gv[k]
			if !present {
				diags = append(diags, Diagnostic{
					Path: subPath,
					Kind: MissingKey,
					Want: subWant,
					Got:  nil,
					Msg:  fmt.Sprintf("key %q missing in actual", k),
				})
				continue
			}
			diags = append(diags, containsAny(subPath, k, subWant, subGot)...)
		}
		return diags

	case []any:
		gv, ok := got.([]any)
		if !ok {
			return []Diagnostic{{
				Path: path,
				Kind: TypeMismatch,
				Want: want,
				Got:  got,
				Msg:  fmt.Sprintf("type differs: want=array got=%s", jsonTypeName(got)),
			}}
		}
		var diags []Diagnostic
		if len(wv) != len(gv) {
			diags = append(diags, Diagnostic{
				Path: path,
				Kind: LengthMismatch,
				Want: len(wv),
				Got:  len(gv),
				Msg:  fmt.Sprintf("array length differs: want=%d got=%d", len(wv), len(gv)),
			})
		}
		minN := len(wv)
		if len(gv) < minN {
			minN = len(gv)
		}
		for i := 0; i < minN; i++ {
			// Array elements have no enclosing object key, so they
			// cannot be decimal-keyed; pass empty parentKey.
			diags = append(diags, containsAny(fmt.Sprintf("%s[%d]", path, i), "", wv[i], gv[i])...)
		}
		return diags

	case string:
		gv, ok := got.(string)
		if !ok {
			return []Diagnostic{{
				Path: path,
				Kind: TypeMismatch,
				Want: want,
				Got:  got,
				Msg:  fmt.Sprintf("type differs: want=string got=%s", jsonTypeName(got)),
			}}
		}
		// Route decimal-coded fields through matchDecimal whenever both
		// sides are strings; matchDecimal itself decides validity via
		// apd.SetString and reports a ValueMismatch on unparseable input.
		// Gating only on parentKey (rather than a regex pre-filter) avoids
		// silently dropping valid apd inputs like "1e2" back to plain
		// string equality.
		if decimalKeys[parentKey] {
			return matchDecimal(path, wv, gv)
		}
		if wv != gv {
			return []Diagnostic{{
				Path: path,
				Kind: ValueMismatch,
				Want: wv,
				Got:  gv,
				Msg:  fmt.Sprintf("value differs: want=%q got=%q", wv, gv),
			}}
		}
		return nil

	case bool:
		gv, ok := got.(bool)
		if !ok {
			return []Diagnostic{{
				Path: path,
				Kind: TypeMismatch,
				Want: want,
				Got:  got,
				Msg:  fmt.Sprintf("type differs: want=bool got=%s", jsonTypeName(got)),
			}}
		}
		if wv != gv {
			return []Diagnostic{{
				Path: path,
				Kind: ValueMismatch,
				Want: wv,
				Got:  gv,
				Msg:  fmt.Sprintf("value differs: want=%v got=%v", wv, gv),
			}}
		}
		return nil

	case float64:
		// Beancompat numbers are decimal strings by design; a float64
		// arriving here indicates either a non-decimal numeric field
		// (e.g. display_precision_by_currency, where the value is an
		// integer count of digits) or a fixture/schema bug. Both sides
		// are statically float64 after the type assertion, so a plain
		// != comparison suffices.
		gv, ok := got.(float64)
		if !ok {
			return []Diagnostic{{
				Path: path,
				Kind: TypeMismatch,
				Want: want,
				Got:  got,
				Msg:  fmt.Sprintf("type differs: want=number got=%s", jsonTypeName(got)),
			}}
		}
		if wv != gv {
			return []Diagnostic{{
				Path: path,
				Kind: ValueMismatch,
				Want: wv,
				Got:  gv,
				Msg:  fmt.Sprintf("value differs: want=%v got=%v", wv, gv),
			}}
		}
		return nil

	default:
		// encoding/json decodes only into map[string]any, []any, string,
		// bool, float64, or nil; reaching here means a programmer error
		// upstream (e.g. a hand-built tree mixing in non-JSON types).
		// Failing loudly is safer than masking the bug with a silent
		// fallback comparison.
		panic(fmt.Sprintf("beancompat: unexpected JSON type %T in containsAny", want))
	}
}

// matchDecimal compares two decimal-string fields per beancompat
// semantics. It returns 0..2 diagnostics:
//
//   - DecimalValueMismatch if the numeric values differ.
//   - DecimalPrecisionMismatch if the values are numerically equal
//     but the stored precision (apd.Decimal.Exponent) differs.
//
// Splitting these into separate Kinds lets a reader instantly tell
// "wrong number" (implementation bug) from "right number, lost a
// trailing zero" (serializer precision divergence). An unparseable
// input yields a single ValueMismatch carrying the parser error.
func matchDecimal(path, want, got string) []Diagnostic {
	var w, g apd.Decimal
	if _, _, err := w.SetString(want); err != nil {
		return []Diagnostic{{
			Path: path,
			Kind: ValueMismatch,
			Want: want,
			Got:  got,
			Msg:  "expected is not a valid decimal: " + err.Error(),
		}}
	}
	if _, _, err := g.SetString(got); err != nil {
		return []Diagnostic{{
			Path: path,
			Kind: ValueMismatch,
			Want: want,
			Got:  got,
			Msg:  "actual is not a valid decimal: " + err.Error(),
		}}
	}
	var diags []Diagnostic
	if w.Cmp(&g) != 0 {
		diags = append(diags, Diagnostic{
			Path: path,
			Kind: DecimalValueMismatch,
			Want: want,
			Got:  got,
			Msg:  fmt.Sprintf("decimal value differs: want=%s got=%s", want, got),
		})
	}
	if w.Exponent != g.Exponent {
		diags = append(diags, Diagnostic{
			Path: path,
			Kind: DecimalPrecisionMismatch,
			Want: want,
			Got:  got,
			Msg: fmt.Sprintf("decimal precision differs: want=%s (exp=%d) got=%s (exp=%d)",
				want, w.Exponent, got, g.Exponent),
		})
	}
	return diags
}

// jsonTypeName names a JSON-decoded value's effective type for use in
// type-mismatch messages. The intent is a stable, schema-level label
// (object/array/string/...) rather than Go's reflect.Kind.
func jsonTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "bool"
	case float64:
		return "number"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// sortedKeys returns m's keys in lexicographic order. Diagnostics need a
// deterministic walk order so the slice the matcher returns is
// reproducible across runs.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// joinPath appends an object-key segment to a dotted JSON-pointer-style
// path, omitting the leading dot when path is empty.
func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// formatFailure renders the bundle of information a test driver should
// dump when Match returns diagnostics. The output bundles five layers —
// a one-line count, per-diagnostic locators, pretty-printed expected and
// actual JSON, and a cmp.Diff — so a reader can localize the divergence,
// see both sides verbatim, and inspect a structural diff without
// re-running the test.
func formatFailure(expected, actual Result, diags []Diagnostic) string {
	var b strings.Builder
	fmt.Fprintf(&b, "containment failure: %d diagnostic(s)\n", len(diags))
	for i, d := range diags {
		fmt.Fprintf(&b, "  [%d] %s\n", i+1, d.String())
	}
	fmt.Fprintf(&b, "\n--- expected (from fixture) ---\n%s\n", mustJSONIndent(expected))
	fmt.Fprintf(&b, "\n--- actual (from serializer) ---\n%s\n", mustJSONIndent(actual))
	fmt.Fprintf(&b, "\n--- structural diff (cmp.Diff) ---\n%s\n", cmp.Diff(expected, actual))
	return b.String()
}

// mustJSONIndent serializes v with two-space indentation. The only way
// json.MarshalIndent fails on an Result (or its sub-values) is an
// unencodable type, which our schema does not contain; the panic
// therefore signals a programmer error, not a runtime condition the
// caller could recover from.
func mustJSONIndent(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("beancompat: marshal Result for diagnostics: %v", err))
	}
	return string(b)
}
