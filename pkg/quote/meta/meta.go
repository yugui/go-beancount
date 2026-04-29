// Package meta parses bean-price-compatible "price" metadata strings
// off ast.Commodity directives into the typed PriceRequest values that
// the orchestrator consumes.
//
// This is the single point of bean-price syntactic compatibility in
// Phase 7: every other layer of pkg/quote works in Go-native terms.
// The grammar accepted here is a subset of the upstream bean-price
// meta syntax — see ParsePriceMeta for the formal definition and
// rejected constructs.
package meta

import (
	"fmt"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/quote/api"
)

// DefaultMetaKey is the bean-price-compatible meta key on a
// Commodity directive whose value carries the price-source
// specification. The CLI's --meta-key flag may override it.
const DefaultMetaKey = "price"

// Diagnostic codes emitted by this package. They are exported so
// out-of-package callers (the CLI, downstream tooling) can branch on
// a typed constant rather than a copy-pasted string literal.
const (
	// CodeUnsupported flags grammar shapes that bean-price accepts
	// but Phase 7 deliberately rejects (the '^' inverted-quote
	// prefix, and a missing CCY: prefix). They are surfaced under
	// their own code so a future relaxation can be detected.
	CodeUnsupported = "quote-meta-unsupported"
	// CodeSyntax flags malformed input that bean-price would also
	// reject.
	CodeSyntax = "quote-meta-syntax"
	// CodeWrongType flags a price meta value that exists but is not
	// a string.
	CodeWrongType = "quote-meta-wrong-type"
)

// ParsePriceMeta interprets a Commodity directive's "price" meta
// value according to the bean-price grammar (subset supported by
// Phase 7).
//
//	value   := psource (WS+ psource)*
//	psource := CCY ":" chain
//	chain   := entry ("," entry)*
//	entry   := SOURCE "/" SYMBOL                 // SYMBOL may contain ':'
//	CCY     := beancount currency
//	SOURCE  := registry source name
//
// One PriceRequest is produced per psource (so distinct quote
// currencies for the same commodity become distinct requests).
// Within a single psource, the chain becomes Sources[] in priority
// order.
//
// Unsupported syntax (currently rejected): the '^' inverted-quote
// prefix, and a leading psource with no CCY (the quote currency must
// be explicit). Both produce a Diagnostic with code
// "quote-meta-unsupported"; other malformed input produces
// "quote-meta-syntax".
//
// Recovery is per-psource: a single malformed psource is reported
// and skipped while the remaining psources are still parsed.
//
// commodity is the owning Commodity directive's currency, used to
// fill the produced Pair.Commodity. raw is the meta value string.
func ParsePriceMeta(commodity, raw string) ([]api.PriceRequest, []ast.Diagnostic) {
	if strings.TrimSpace(raw) == "" {
		return nil, []ast.Diagnostic{{
			Code:     CodeSyntax,
			Severity: ast.Warning,
			Message:  "price meta value is empty",
		}}
	}

	var (
		requests []api.PriceRequest
		diags    []ast.Diagnostic
	)
	for _, psource := range splitWhitespace(raw) {
		req, d, ok := parsePsource(commodity, psource)
		diags = append(diags, d...)
		if !ok {
			continue
		}
		requests = append(requests, req)
	}
	return requests, diags
}

// ExtractFromCommodity reads metaKey from c.Meta and parses it via
// ParsePriceMeta. metaKey is typically DefaultMetaKey but can be
// overridden by the CLI's --meta-key flag. Returns (nil, nil) if the
// meta key is absent. If the value is not a string, returns a
// diagnostic with code "quote-meta-wrong-type" carrying c.Span.
func ExtractFromCommodity(c *ast.Commodity, metaKey string) ([]api.PriceRequest, []ast.Diagnostic) {
	if c == nil {
		return nil, nil
	}
	v, ok := c.Meta.Props[metaKey]
	if !ok {
		return nil, nil
	}
	if v.Kind != ast.MetaString {
		return nil, []ast.Diagnostic{{
			Code:     CodeWrongType,
			Span:     c.Span,
			Severity: ast.Warning,
			Message: fmt.Sprintf(
				"commodity %q meta key %q must be a string, got kind %v",
				c.Currency, metaKey, v.Kind),
		}}
	}
	requests, diags := ParsePriceMeta(c.Currency, v.String)
	// Enrich the per-psource diagnostics with the directive's span so
	// the caller can locate the offending Commodity in the source.
	for i := range diags {
		diags[i].Span = c.Span
	}
	return requests, diags
}

// parsePsource parses one whitespace-separated psource segment. ok is
// false when the segment was malformed; in that case diags carries a
// single explanatory entry and req is the zero value.
func parsePsource(commodity, psource string) (req api.PriceRequest, diags []ast.Diagnostic, ok bool) {
	// '^' inverted-quote prefix is reserved syntax in bean-price; we
	// reject it explicitly so a future implementation can detect the
	// extension point.
	if strings.HasPrefix(psource, "^") {
		return api.PriceRequest{}, []ast.Diagnostic{{
			Code:     CodeUnsupported,
			Severity: ast.Warning,
			Message: fmt.Sprintf(
				"inverted-quote prefix '^' is not supported in price meta %q",
				psource),
		}}, false
	}

	colon := strings.IndexByte(psource, ':')
	if colon < 0 {
		// bean-price upstream tolerates a missing CCY when an
		// external --quote default is provided; Phase 7 has no such
		// default and treats this as the "unsupported" extension
		// point rather than a syntax error.
		return api.PriceRequest{}, []ast.Diagnostic{{
			Code:     CodeUnsupported,
			Severity: ast.Warning,
			Message: fmt.Sprintf(
				"price meta %q has no quote currency prefix; CCY: is required",
				psource),
		}}, false
	}

	quoteCurrency := psource[:colon]
	chain := psource[colon+1:]
	if quoteCurrency == "" {
		return api.PriceRequest{}, []ast.Diagnostic{{
			Code:     CodeSyntax,
			Severity: ast.Warning,
			Message: fmt.Sprintf(
				"price meta %q has empty quote currency", psource),
		}}, false
	}
	if chain == "" {
		return api.PriceRequest{}, []ast.Diagnostic{{
			Code:     CodeSyntax,
			Severity: ast.Warning,
			Message: fmt.Sprintf(
				"price meta %q has no source chain after %q", psource, quoteCurrency+":"),
		}}, false
	}

	entries := strings.Split(chain, ",")
	sources := make([]api.SourceRef, 0, len(entries))
	for _, entry := range entries {
		ref, d, entryOK := parseEntry(psource, entry)
		if !entryOK {
			return api.PriceRequest{}, d, false
		}
		sources = append(sources, ref)
	}
	return api.PriceRequest{
		Pair:    api.Pair{Commodity: commodity, QuoteCurrency: quoteCurrency},
		Sources: sources,
	}, nil, true
}

// parseEntry parses one SOURCE "/" SYMBOL fragment. The SYMBOL may
// itself contain ':' characters — only the first '/' separates source
// from symbol — so we use IndexByte rather than Split to preserve
// "NASDAQ:GOOG"-style symbols.
func parseEntry(psource, entry string) (api.SourceRef, []ast.Diagnostic, bool) {
	slash := strings.IndexByte(entry, '/')
	if slash < 0 {
		return api.SourceRef{}, []ast.Diagnostic{{
			Code:     CodeSyntax,
			Severity: ast.Warning,
			Message: fmt.Sprintf(
				"price meta %q entry %q is missing '/' separating source from symbol",
				psource, entry),
		}}, false
	}
	source := entry[:slash]
	symbol := entry[slash+1:]
	if source == "" {
		return api.SourceRef{}, []ast.Diagnostic{{
			Code:     CodeSyntax,
			Severity: ast.Warning,
			Message: fmt.Sprintf(
				"price meta %q entry %q has empty source name",
				psource, entry),
		}}, false
	}
	if symbol == "" {
		return api.SourceRef{}, []ast.Diagnostic{{
			Code:     CodeSyntax,
			Severity: ast.Warning,
			Message: fmt.Sprintf(
				"price meta %q entry %q has empty symbol",
				psource, entry),
		}}, false
	}
	return api.SourceRef{Source: source, Symbol: symbol}, nil, true
}

// splitWhitespace splits raw on runs of ASCII whitespace (space, tab)
// and discards empty fields. strings.Fields would also split on
// newlines and other Unicode whitespace, which the grammar does not
// promise to accept; restricting to ' ' and '\t' keeps the
// space-separator contract narrow.
func splitWhitespace(raw string) []string {
	var out []string
	start := -1
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == ' ' || c == '\t' {
			if start >= 0 {
				out = append(out, raw[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, raw[start:])
	}
	return out
}
