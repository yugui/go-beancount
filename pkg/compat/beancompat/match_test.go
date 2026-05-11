package beancompat

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// rawJSON marshals v into a json.RawMessage; test helper kept tiny so
// table entries stay readable.
func rawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal helper input: %v", err)
	}
	return json.RawMessage(b)
}

// directiveOf constructs a Directive with the given type/date and
// JSON-shaped data and meta payloads. It keeps test cases declarative.
func directiveOf(t *testing.T, typ, date string, data, meta any) Directive {
	t.Helper()
	d := Directive{Type: typ, Date: date}
	if data != nil {
		d.Data = rawJSON(t, data)
	}
	if meta != nil {
		d.Meta = rawJSON(t, meta)
	}
	return d
}

// findDiag returns the first diagnostic whose Path and Kind match, or
// nil. Tests use it to locate a specific diagnostic among potentially
// many without depending on slice order.
func findDiag(diags []Diagnostic, path string, kind DiagKind) *Diagnostic {
	for i := range diags {
		if diags[i].Path == path && diags[i].Kind == kind {
			return &diags[i]
		}
	}
	return nil
}

func TestDiagKindString(t *testing.T) {
	cases := map[DiagKind]string{
		MissingKey:               "MissingKey",
		ValueMismatch:            "ValueMismatch",
		DecimalValueMismatch:     "DecimalValueMismatch",
		DecimalPrecisionMismatch: "DecimalPrecisionMismatch",
		LengthMismatch:           "LengthMismatch",
		TypeMismatch:             "TypeMismatch",
		MissingError:             "MissingError",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("DiagKind(%d).String() = %q, want %q", int(k), got, want)
		}
	}
}

func TestMatchSuccessCases(t *testing.T) {
	t.Run("identical empty Result", func(t *testing.T) {
		if diags := Match(Result{}, Result{}); len(diags) != 0 {
			t.Fatalf("expected no diagnostics, got %v", diags)
		}
	})

	t.Run("identical non-trivial Result", func(t *testing.T) {
		want := Result{
			Errors: []string{"E1", "E2"},
			Directives: []Directive{
				directiveOf(t, "open", "2024-01-01",
					map[string]any{"account": "Assets:Cash", "currencies": []any{"USD"}},
					map[string]any{"foo": "bar"}),
			},
			Options: rawJSON(t, map[string]any{"title": "Personal"}),
		}
		got := want
		if diags := Match(want, got); len(diags) != 0 {
			t.Fatalf("identical inputs should match cleanly; got %v", diags)
		}
	})

	t.Run("actual has extra map keys", func(t *testing.T) {
		want := Result{
			Directives: []Directive{
				directiveOf(t, "open", "2024-01-01",
					map[string]any{"account": "Assets:Cash"}, nil),
			},
		}
		got := Result{
			Directives: []Directive{
				directiveOf(t, "open", "2024-01-01",
					map[string]any{"account": "Assets:Cash", "currencies": []any{"USD"}, "booking": "STRICT"},
					map[string]any{"line": float64(7)}),
			},
		}
		if diags := Match(want, got); len(diags) != 0 {
			t.Fatalf("containment must ignore actual-side extras; got %v", diags)
		}
	})

	t.Run("actual has extra errors", func(t *testing.T) {
		want := Result{Errors: []string{"E1"}}
		got := Result{Errors: []string{"E1", "E2", "E3"}}
		if diags := Match(want, got); len(diags) != 0 {
			t.Fatalf("extra actual errors are forgiven; got %v", diags)
		}
	})

	t.Run("empty expected vs empty directives but extras everywhere else", func(t *testing.T) {
		want := Result{}
		got := Result{
			Errors:  []string{"X"},
			Options: rawJSON(t, map[string]any{"title": "X"}),
		}
		if diags := Match(want, got); len(diags) != 0 {
			t.Fatalf("empty expected should accept any actual when lengths match; got %v", diags)
		}
	})
}

func TestMatchFailureCases(t *testing.T) {
	t.Run("missing key in meta", func(t *testing.T) {
		want := Result{Directives: []Directive{
			directiveOf(t, "open", "2024-01-01", map[string]any{}, map[string]any{"foo": "x"}),
		}}
		got := Result{Directives: []Directive{
			directiveOf(t, "open", "2024-01-01", map[string]any{}, map[string]any{"bar": "y"}),
		}}
		diags := Match(want, got)
		if d := findDiag(diags, "directives[0].meta.foo", MissingKey); d == nil {
			t.Fatalf("expected MissingKey at directives[0].meta.foo; got %v", diags)
		}
	})

	t.Run("value mismatch in narration", func(t *testing.T) {
		want := Result{Directives: []Directive{
			directiveOf(t, "txn", "2024-01-01", map[string]any{"narration": "abc"}, nil),
		}}
		got := Result{Directives: []Directive{
			directiveOf(t, "txn", "2024-01-01", map[string]any{"narration": "def"}, nil),
		}}
		diags := Match(want, got)
		d := findDiag(diags, "directives[0].data.narration", ValueMismatch)
		if d == nil {
			t.Fatalf("expected ValueMismatch at directives[0].data.narration; got %v", diags)
		}
		if d.Want != "abc" || d.Got != "def" {
			t.Errorf("Want/Got fields not preserved: %+v", d)
		}
	})

	t.Run("decimal value mismatch only", func(t *testing.T) {
		want := Result{Directives: []Directive{
			directiveOf(t, "balance", "2024-01-01",
				map[string]any{"amount": map[string]any{"number": "50", "currency": "USD"}}, nil),
		}}
		got := Result{Directives: []Directive{
			directiveOf(t, "balance", "2024-01-01",
				map[string]any{"amount": map[string]any{"number": "60", "currency": "USD"}}, nil),
		}}
		diags := Match(want, got)
		if findDiag(diags, "directives[0].data.amount.number", DecimalValueMismatch) == nil {
			t.Fatalf("expected DecimalValueMismatch; got %v", diags)
		}
		if findDiag(diags, "directives[0].data.amount.number", DecimalPrecisionMismatch) != nil {
			t.Fatalf("unexpected precision diagnostic for equal-exponent values; got %v", diags)
		}
	})

	t.Run("decimal precision mismatch only", func(t *testing.T) {
		want := Result{Directives: []Directive{
			directiveOf(t, "balance", "2024-01-01",
				map[string]any{"amount": map[string]any{"number": "50.00", "currency": "USD"}}, nil),
		}}
		got := Result{Directives: []Directive{
			directiveOf(t, "balance", "2024-01-01",
				map[string]any{"amount": map[string]any{"number": "50", "currency": "USD"}}, nil),
		}}
		diags := Match(want, got)
		if findDiag(diags, "directives[0].data.amount.number", DecimalPrecisionMismatch) == nil {
			t.Fatalf("expected DecimalPrecisionMismatch; got %v", diags)
		}
		if findDiag(diags, "directives[0].data.amount.number", DecimalValueMismatch) != nil {
			t.Fatalf("unexpected value diagnostic for numerically-equal decimals; got %v", diags)
		}
	})

	t.Run("decimal both value and precision", func(t *testing.T) {
		want := Result{Directives: []Directive{
			directiveOf(t, "balance", "2024-01-01",
				map[string]any{"amount": map[string]any{"number": "50.00", "currency": "USD"}}, nil),
		}}
		got := Result{Directives: []Directive{
			directiveOf(t, "balance", "2024-01-01",
				map[string]any{"amount": map[string]any{"number": "60", "currency": "USD"}}, nil),
		}}
		diags := Match(want, got)
		if findDiag(diags, "directives[0].data.amount.number", DecimalValueMismatch) == nil {
			t.Fatalf("expected DecimalValueMismatch; got %v", diags)
		}
		if findDiag(diags, "directives[0].data.amount.number", DecimalPrecisionMismatch) == nil {
			t.Fatalf("expected DecimalPrecisionMismatch; got %v", diags)
		}
	})

	t.Run("length mismatch with inner diagnostics on prefix", func(t *testing.T) {
		want := Result{Directives: []Directive{
			directiveOf(t, "open", "2024-01-01", map[string]any{"account": "Assets:A"}, nil),
			directiveOf(t, "open", "2024-01-02", map[string]any{"account": "Assets:B"}, nil),
		}}
		got := Result{Directives: []Directive{
			directiveOf(t, "open", "2024-01-01", map[string]any{"account": "Assets:WRONG"}, nil),
			directiveOf(t, "open", "2024-01-02", map[string]any{"account": "Assets:B"}, nil),
			directiveOf(t, "open", "2024-01-03", map[string]any{"account": "Assets:C"}, nil),
		}}
		diags := Match(want, got)
		if findDiag(diags, "directives", LengthMismatch) == nil {
			t.Fatalf("expected LengthMismatch at directives; got %v", diags)
		}
		// Inner diagnostic on the common-prefix index 0 must still appear.
		if findDiag(diags, "directives[0].data.account", ValueMismatch) == nil {
			t.Fatalf("expected inner ValueMismatch over prefix; got %v", diags)
		}
	})

	t.Run("type mismatch object vs scalar", func(t *testing.T) {
		want := Result{Directives: []Directive{
			directiveOf(t, "x", "2024-01-01",
				map[string]any{"foo": map[string]any{"nested": "v"}}, nil),
		}}
		got := Result{Directives: []Directive{
			directiveOf(t, "x", "2024-01-01",
				map[string]any{"foo": "x"}, nil),
		}}
		diags := Match(want, got)
		if findDiag(diags, "directives[0].data.foo", TypeMismatch) == nil {
			t.Fatalf("expected TypeMismatch at directives[0].data.foo; got %v", diags)
		}
	})

	t.Run("missing error", func(t *testing.T) {
		want := Result{Errors: []string{"E1", "E2"}}
		got := Result{Errors: []string{"E1"}}
		diags := Match(want, got)
		if len(diags) != 1 {
			t.Fatalf("expected exactly one diagnostic; got %v", diags)
		}
		if diags[0].Kind != MissingError || diags[0].Path != "errors[1]" {
			t.Errorf("unexpected diagnostic: %+v", diags[0])
		}
		if diags[0].Want != "E2" {
			t.Errorf("Want = %v, want %q", diags[0].Want, "E2")
		}
	})

	t.Run("empty expected vs populated actual produces length mismatch", func(t *testing.T) {
		// directives length differs (0 vs 1) — containment treats a
		// length mismatch as a violation per the plan, so this case
		// surfaces exactly one LengthMismatch. This is a failure case
		// and belongs here, not in the success suite.
		want := Result{}
		got := Result{
			Errors: []string{"E1"},
			Directives: []Directive{
				directiveOf(t, "open", "2024-01-01", map[string]any{"account": "Assets:Cash"}, nil),
			},
		}
		diags := Match(want, got)
		if len(diags) != 1 || diags[0].Kind != LengthMismatch || diags[0].Path != "directives" {
			t.Fatalf("expected one LengthMismatch at directives, got %v", diags)
		}
	})
}

func TestMatchMultisetErrors(t *testing.T) {
	t.Run("duplicate expected satisfied by duplicate actual", func(t *testing.T) {
		want := Result{Errors: []string{"a", "a"}}
		got := Result{Errors: []string{"a", "b", "a"}}
		if diags := Match(want, got); len(diags) != 0 {
			t.Fatalf("multiset subset should match; got %v", diags)
		}
	})

	t.Run("duplicate expected not satisfied", func(t *testing.T) {
		want := Result{Errors: []string{"a", "a"}}
		got := Result{Errors: []string{"a", "b"}}
		diags := Match(want, got)
		if len(diags) != 1 {
			t.Fatalf("expected exactly one MissingError; got %v", diags)
		}
		if diags[0].Kind != MissingError {
			t.Errorf("Kind = %v, want MissingError", diags[0].Kind)
		}
		if diags[0].Path != "errors[1]" {
			t.Errorf("Path = %q, want %q", diags[0].Path, "errors[1]")
		}
	})
}

func TestMatchMultipleViolations(t *testing.T) {
	want := Result{
		Errors: []string{"missing-err"},
		Directives: []Directive{
			directiveOf(t, "open", "2024-01-01",
				map[string]any{"account": "Assets:A", "booking": "STRICT"}, nil),
		},
	}
	got := Result{
		Errors: []string{"other"},
		Directives: []Directive{
			directiveOf(t, "open", "2024-01-01",
				map[string]any{"account": "Assets:B"}, nil),
		},
	}
	diags := Match(want, got)
	if len(diags) != 3 {
		t.Fatalf("expected 3 diagnostics across error, value, missing-key paths; got %d: %v", len(diags), diags)
	}
	if findDiag(diags, "errors[0]", MissingError) == nil {
		t.Errorf("missing MissingError; got %v", diags)
	}
	if findDiag(diags, "directives[0].data.account", ValueMismatch) == nil {
		t.Errorf("missing ValueMismatch at account; got %v", diags)
	}
	if findDiag(diags, "directives[0].data.booking", MissingKey) == nil {
		t.Errorf("missing MissingKey at booking; got %v", diags)
	}
}

func TestMatchDecimalEdgeCases(t *testing.T) {
	t.Run("equal value equal precision", func(t *testing.T) {
		if d := matchDecimal("x", "50", "50"); len(d) != 0 {
			t.Errorf("matchDecimal(\"50\",\"50\") = %v; want empty", d)
		}
	})

	t.Run("equal value preserved precision both sides", func(t *testing.T) {
		if d := matchDecimal("x", "-50.00", "-50.00"); len(d) != 0 {
			t.Errorf("matchDecimal(\"-50.00\",\"-50.00\") = %v; want empty", d)
		}
	})

	t.Run("zero with different trailing zeros", func(t *testing.T) {
		d := matchDecimal("x", "0", "0.0")
		if len(d) != 1 || d[0].Kind != DecimalPrecisionMismatch {
			t.Errorf("matchDecimal(\"0\",\"0.0\") = %v; want one DecimalPrecisionMismatch", d)
		}
	})

	t.Run("invalid want", func(t *testing.T) {
		d := matchDecimal("x", "abc", "50")
		if len(d) != 1 || d[0].Kind != ValueMismatch {
			t.Fatalf("matchDecimal(\"abc\",\"50\") = %v; want one ValueMismatch", d)
		}
	})

	t.Run("invalid got", func(t *testing.T) {
		d := matchDecimal("x", "50", "xyz")
		if len(d) != 1 || d[0].Kind != ValueMismatch {
			t.Fatalf("matchDecimal(\"50\",\"xyz\") = %v; want one ValueMismatch", d)
		}
	})
}

func TestMatchNonDecimalKeyWithNumericString(t *testing.T) {
	// A field whose key is not in decimalKeys must compare as a string
	// even if both values look numeric. This guards against accidental
	// routing of currency codes or line numbers through matchDecimal.
	want := Result{Directives: []Directive{
		directiveOf(t, "x", "2024-01-01", map[string]any{"line": "42"}, nil),
	}}
	got := Result{Directives: []Directive{
		directiveOf(t, "x", "2024-01-01", map[string]any{"line": "42.0"}, nil),
	}}
	diags := Match(want, got)
	if findDiag(diags, "directives[0].data.line", ValueMismatch) == nil {
		t.Fatalf("non-decimal-key numeric strings must compare exact; got %v", diags)
	}
	if findDiag(diags, "directives[0].data.line", DecimalPrecisionMismatch) != nil {
		t.Fatalf("non-decimal-key strings must not be routed through matchDecimal; got %v", diags)
	}
}

func TestMatchOptionsContainment(t *testing.T) {
	t.Run("missing key in options", func(t *testing.T) {
		want := Result{Options: rawJSON(t, map[string]any{"title": "T", "operating_currency": []any{"USD"}})}
		got := Result{Options: rawJSON(t, map[string]any{"title": "T"})}
		diags := Match(want, got)
		if findDiag(diags, "options.operating_currency", MissingKey) == nil {
			t.Fatalf("expected MissingKey at options.operating_currency; got %v", diags)
		}
	})

	t.Run("array length differs inside options", func(t *testing.T) {
		want := Result{Options: rawJSON(t, map[string]any{"operating_currency": []any{"USD", "EUR"}})}
		got := Result{Options: rawJSON(t, map[string]any{"operating_currency": []any{"USD"}})}
		diags := Match(want, got)
		if findDiag(diags, "options.operating_currency", LengthMismatch) == nil {
			t.Fatalf("expected LengthMismatch at options.operating_currency; got %v", diags)
		}
	})
}

func TestFormatFailureSnapshot(t *testing.T) {
	want := Result{
		Directives: []Directive{
			directiveOf(t, "open", "2024-01-01",
				map[string]any{"account": "Assets:A", "booking": "STRICT"}, nil),
		},
	}
	got := Result{
		Directives: []Directive{
			directiveOf(t, "open", "2024-01-01",
				map[string]any{"account": "Assets:B"}, nil),
		},
	}
	diags := Match(want, got)
	if len(diags) < 2 {
		t.Fatalf("setup produced %d diagnostics; want >=2 for snapshot", len(diags))
	}

	out := formatFailure(want, got, diags)
	mustContain := []string{
		"containment failure: " + strconv.Itoa(len(diags)) + " diagnostic(s)",
		"directives[0].data.account",
		"directives[0].data.booking",
		"--- expected (from fixture) ---",
		"--- actual (from serializer) ---",
		"--- structural diff (cmp.Diff) ---",
	}
	for _, sub := range mustContain {
		if !strings.Contains(out, sub) {
			t.Errorf("formatFailure output missing %q\n--- output ---\n%s", sub, out)
		}
	}
}
