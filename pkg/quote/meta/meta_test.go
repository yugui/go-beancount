package meta

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// diagCodes extracts just the Code fields from diagnostics so tests can
// pin the classifier without locking in the exact human-readable
// message wording.
func diagCodes(diags []ast.Diagnostic) []string {
	if len(diags) == 0 {
		return nil
	}
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.Code
	}
	return out
}

func TestParsePriceMeta_Grammar(t *testing.T) {
	cases := []struct {
		name      string
		commodity string
		raw       string
		want      []api.PriceRequest
	}{
		{
			name:      "single source",
			commodity: "AAPL",
			raw:       "USD:yahoo/AAPL",
			want: []api.PriceRequest{{
				Pair:    api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"},
				Sources: []api.SourceRef{{Source: "yahoo", Symbol: "AAPL"}},
			}},
		},
		{
			name:      "fallback chain",
			commodity: "AAPL",
			raw:       "USD:yahoo/AAPL,google/AAPL",
			want: []api.PriceRequest{{
				Pair: api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"},
				Sources: []api.SourceRef{
					{Source: "yahoo", Symbol: "AAPL"},
					{Source: "google", Symbol: "AAPL"},
				},
			}},
		},
		{
			name:      "two quote currencies",
			commodity: "X",
			raw:       "USD:yahoo/X JPY:yahoo/XJPY",
			want: []api.PriceRequest{
				{
					Pair:    api.Pair{Commodity: "X", QuoteCurrency: "USD"},
					Sources: []api.SourceRef{{Source: "yahoo", Symbol: "X"}},
				},
				{
					Pair:    api.Pair{Commodity: "X", QuoteCurrency: "JPY"},
					Sources: []api.SourceRef{{Source: "yahoo", Symbol: "XJPY"}},
				},
			},
		},
		{
			name:      "two quote currencies with chain",
			commodity: "X",
			raw:       "USD:yahoo/X,google/X JPY:google/XJPY",
			want: []api.PriceRequest{
				{
					Pair: api.Pair{Commodity: "X", QuoteCurrency: "USD"},
					Sources: []api.SourceRef{
						{Source: "yahoo", Symbol: "X"},
						{Source: "google", Symbol: "X"},
					},
				},
				{
					Pair:    api.Pair{Commodity: "X", QuoteCurrency: "JPY"},
					Sources: []api.SourceRef{{Source: "google", Symbol: "XJPY"}},
				},
			},
		},
		{
			name:      "symbol contains colon",
			commodity: "GOOG",
			raw:       "USD:google/NASDAQ:GOOG",
			want: []api.PriceRequest{{
				Pair:    api.Pair{Commodity: "GOOG", QuoteCurrency: "USD"},
				Sources: []api.SourceRef{{Source: "google", Symbol: "NASDAQ:GOOG"}},
			}},
		},
		{
			name:      "tab as whitespace separator",
			commodity: "X",
			raw:       "USD:yahoo/X\tJPY:yahoo/XJPY",
			want: []api.PriceRequest{
				{
					Pair:    api.Pair{Commodity: "X", QuoteCurrency: "USD"},
					Sources: []api.SourceRef{{Source: "yahoo", Symbol: "X"}},
				},
				{
					Pair:    api.Pair{Commodity: "X", QuoteCurrency: "JPY"},
					Sources: []api.SourceRef{{Source: "yahoo", Symbol: "XJPY"}},
				},
			},
		},
		{
			name:      "leading and trailing whitespace",
			commodity: "AAPL",
			raw:       "  USD:yahoo/AAPL  ",
			want: []api.PriceRequest{{
				Pair:    api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"},
				Sources: []api.SourceRef{{Source: "yahoo", Symbol: "AAPL"}},
			}},
		},
		{
			name:      "multiple spaces between psources",
			commodity: "X",
			raw:       "USD:yahoo/X    JPY:yahoo/XJPY",
			want: []api.PriceRequest{
				{
					Pair:    api.Pair{Commodity: "X", QuoteCurrency: "USD"},
					Sources: []api.SourceRef{{Source: "yahoo", Symbol: "X"}},
				},
				{
					Pair:    api.Pair{Commodity: "X", QuoteCurrency: "JPY"},
					Sources: []api.SourceRef{{Source: "yahoo", Symbol: "XJPY"}},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, diags := ParsePriceMeta(tc.commodity, tc.raw)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ParsePriceMeta(%q, %q) requests mismatch (-want +got):\n%s",
					tc.commodity, tc.raw, diff)
			}
			if len(diags) != 0 {
				t.Errorf("ParsePriceMeta(%q, %q) unexpected diags: %v",
					tc.commodity, tc.raw, diags)
			}
		})
	}
}

func TestParsePriceMeta_Rejection(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantCode string
	}{
		{name: "inverted prefix", raw: "^USD:yahoo/X", wantCode: CodeUnsupported},
		{name: "missing CCY", raw: "yahoo/X", wantCode: CodeUnsupported},
		{name: "no chain", raw: "USD:", wantCode: CodeSyntax},
		{name: "entry without slash", raw: "USD:yahoo", wantCode: CodeSyntax},
		{name: "empty symbol", raw: "USD:yahoo/", wantCode: CodeSyntax},
		{name: "empty source", raw: "USD:/X", wantCode: CodeSyntax},
		{name: "empty value", raw: "", wantCode: CodeSyntax},
		{name: "whitespace-only value", raw: "   ", wantCode: CodeSyntax},
		{name: "trailing comma", raw: "USD:yahoo/X,", wantCode: CodeSyntax},
		{name: "leading comma", raw: "USD:,yahoo/X", wantCode: CodeSyntax},
		{name: "double comma", raw: "USD:yahoo/X,,google/X", wantCode: CodeSyntax},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, diags := ParsePriceMeta("AAPL", tc.raw)
			if len(got) != 0 {
				t.Errorf("ParsePriceMeta(%q) requests = %v, want none", tc.raw, got)
			}
			if len(diags) != 1 {
				t.Fatalf("len(diags) = %d, want 1; diags = %#v", len(diags), diags)
			}
			want := ast.Diagnostic{
				Code:     tc.wantCode,
				Severity: ast.Warning,
			}
			if diff := cmp.Diff(want, diags[0],
				cmpopts.IgnoreFields(ast.Diagnostic{}, "Message")); diff != "" {
				t.Errorf("diag mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParsePriceMeta_PartialRecovery(t *testing.T) {
	got, diags := ParsePriceMeta("AAPL", "USD:yahoo/AAPL ^EUR:google/X")

	want := []api.PriceRequest{{
		Pair:    api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"},
		Sources: []api.SourceRef{{Source: "yahoo", Symbol: "AAPL"}},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("requests mismatch (-want +got):\n%s", diff)
	}

	codes := diagCodes(diags)
	if len(codes) != 1 || codes[0] != CodeUnsupported {
		t.Errorf("diag codes = %v, want [%s]", codes, CodeUnsupported)
	}
}

func TestParsePriceMeta_PartialRecovery_Multiple(t *testing.T) {
	// One good, one missing-slash, one good — both good ones survive,
	// the bad one yields exactly one syntax diagnostic.
	got, diags := ParsePriceMeta("X", "USD:yahoo/X JPY:google EUR:google/XEUR")

	want := []api.PriceRequest{
		{
			Pair:    api.Pair{Commodity: "X", QuoteCurrency: "USD"},
			Sources: []api.SourceRef{{Source: "yahoo", Symbol: "X"}},
		},
		{
			Pair:    api.Pair{Commodity: "X", QuoteCurrency: "EUR"},
			Sources: []api.SourceRef{{Source: "google", Symbol: "XEUR"}},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("requests mismatch (-want +got):\n%s", diff)
	}
	codes := diagCodes(diags)
	if len(codes) != 1 || codes[0] != CodeSyntax {
		t.Errorf("diag codes = %v, want [%s]", codes, CodeSyntax)
	}
}

// stubSpan is a non-zero Span used by ExtractFromCommodity tests to
// confirm wrapper-level span enrichment.
func stubSpan() ast.Span {
	return ast.Span{
		Start: ast.Position{Filename: "f.bean", Line: 3, Column: 1, Offset: 42},
		End:   ast.Position{Filename: "f.bean", Line: 3, Column: 30, Offset: 71},
	}
}

func TestExtractFromCommodity_Present(t *testing.T) {
	c := &ast.Commodity{
		Currency: "AAPL",
		Span:     stubSpan(),
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			DefaultMetaKey: {Kind: ast.MetaString, String: "USD:yahoo/AAPL"},
		}},
	}

	got, diags := ExtractFromCommodity(c, DefaultMetaKey)
	want := []api.PriceRequest{{
		Pair:    api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"},
		Sources: []api.SourceRef{{Source: "yahoo", Symbol: "AAPL"}},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("requests mismatch (-want +got):\n%s", diff)
	}
	if len(diags) != 0 {
		t.Errorf("unexpected diags: %v", diags)
	}
}

func TestExtractFromCommodity_Absent(t *testing.T) {
	t.Run("nil props", func(t *testing.T) {
		c := &ast.Commodity{Currency: "AAPL"}
		got, diags := ExtractFromCommodity(c, DefaultMetaKey)
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
		if diags != nil {
			t.Errorf("diags = %v, want nil", diags)
		}
	})
	t.Run("other keys present", func(t *testing.T) {
		c := &ast.Commodity{
			Currency: "AAPL",
			Meta: ast.Metadata{Props: map[string]ast.MetaValue{
				"name": {Kind: ast.MetaString, String: "Apple"},
			}},
		}
		got, diags := ExtractFromCommodity(c, DefaultMetaKey)
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
		if diags != nil {
			t.Errorf("diags = %v, want nil", diags)
		}
	})
	t.Run("nil commodity", func(t *testing.T) {
		got, diags := ExtractFromCommodity(nil, DefaultMetaKey)
		if got != nil || diags != nil {
			t.Errorf("got (%v, %v), want (nil, nil)", got, diags)
		}
	})
}

func TestExtractFromCommodity_WrongType(t *testing.T) {
	c := &ast.Commodity{
		Currency: "AAPL",
		Span:     stubSpan(),
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			DefaultMetaKey: {Kind: ast.MetaBool, Bool: true},
		}},
	}

	got, diags := ExtractFromCommodity(c, DefaultMetaKey)
	if got != nil {
		t.Errorf("got requests %v, want nil", got)
	}
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1; diags = %#v", len(diags), diags)
	}
	want := ast.Diagnostic{
		Code:     CodeWrongType,
		Span:     c.Span,
		Severity: ast.Warning,
	}
	if diff := cmp.Diff(want, diags[0],
		cmpopts.IgnoreFields(ast.Diagnostic{}, "Message")); diff != "" {
		t.Errorf("diag mismatch (-want +got):\n%s", diff)
	}
}

func TestExtractFromCommodity_CustomKey(t *testing.T) {
	c := &ast.Commodity{
		Currency: "AAPL",
		Span:     stubSpan(),
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			// Default key carries garbage; the custom key carries the
			// real value. Asserting that the custom key wins also
			// rules out an accidental hard-coded reference to "price".
			DefaultMetaKey: {Kind: ast.MetaString, String: "should-not-be-read"},
			"quote-spec":   {Kind: ast.MetaString, String: "USD:yahoo/AAPL"},
		}},
	}

	got, diags := ExtractFromCommodity(c, "quote-spec")
	want := []api.PriceRequest{{
		Pair:    api.Pair{Commodity: "AAPL", QuoteCurrency: "USD"},
		Sources: []api.SourceRef{{Source: "yahoo", Symbol: "AAPL"}},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("requests mismatch (-want +got):\n%s", diff)
	}
	if len(diags) != 0 {
		t.Errorf("unexpected diags: %v", diags)
	}
}

func TestExtractFromCommodity_SpanEnrichment(t *testing.T) {
	// A malformed meta value's diagnostic should be enriched with the
	// owning Commodity's span so the caller can locate it in source.
	c := &ast.Commodity{
		Currency: "AAPL",
		Span:     stubSpan(),
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			DefaultMetaKey: {Kind: ast.MetaString, String: "USD:yahoo"},
		}},
	}

	got, diags := ExtractFromCommodity(c, DefaultMetaKey)
	if got != nil {
		t.Errorf("got requests %v, want nil", got)
	}
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1; diags = %v", len(diags), diags)
	}
	if diags[0].Code != CodeSyntax {
		t.Errorf("diag.Code = %q, want %q", diags[0].Code, CodeSyntax)
	}
	if diags[0].Span != c.Span {
		t.Errorf("diag.Span = %+v, want %+v", diags[0].Span, c.Span)
	}
}
