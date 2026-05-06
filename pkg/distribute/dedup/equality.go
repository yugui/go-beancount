package dedup

import (
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/text/unicode/norm"

	"github.com/yugui/go-beancount/pkg/ast"
)

// decimalCmp equates apd.Decimal values numerically. Its underlying
// big.Int has unexported fields cmp cannot reflect into, so any cmp
// option set that walks decimal-bearing AST nodes must include this.
var decimalCmp = cmp.Comparer(func(a, b apd.Decimal) bool { return a.Cmp(&b) == 0 })

// transactionType and noteType are used by the free-text FilterPath
// predicate to scope per-field string normalization to specific
// directive types.
var (
	transactionType = reflect.TypeOf((*ast.Transaction)(nil)).Elem()
	noteType        = reflect.TypeOf((*ast.Note)(nil)).Elem()
)

// equalityOpts returns the cmp options that strip cross-cutting fields
// from AST equality: every Span (so spans from different source files
// compare equal), the override metadata key (so a directive that
// gained a route-account hint compares equal to one that did not), and
// numeric apd.Decimal comparison.
//
// In addition, it canonicalizes posting order — two transactions whose
// postings differ only in order are equal — and applies NFKC + Unicode
// whitespace removal to a narrow set of free-text fields
// (Transaction.Narration, Transaction.Payee, Note.Comment, and
// MetaValue values of MetaString kind), so that transactions emitted by
// different importers with cosmetic encoding differences still
// deduplicate. Identifier-bearing strings (account names, currency
// codes, tag/link names, metadata keys, file paths, etc.) remain
// byte-exact.
//
// Metadata is compared via a dedicated Comparer rather than the
// cmpopts.IgnoreMapEntries filter so that {overrideMetaKey: X} and the
// nil map compare equal: filtering a single-entry map down to zero
// entries yields an empty (non-nil) map, which cmp does not treat as
// equal to a nil map even with cmpopts.EquateEmpty.
//
// overrideMetaKey names a single metadata key to strip from AST
// equality (typically the transaction routing override key); the
// caller flows it from Config.TransactionSection.OverrideMetaKey.
func equalityOpts(overrideMetaKey string) cmp.Options {
	return cmp.Options{
		cmpopts.IgnoreTypes(ast.Span{}),
		cmp.Comparer(func(a, b ast.Metadata) bool {
			return metadataEqual(a, b, overrideMetaKey)
		}),
		decimalCmp,
		sortPostings,
		freeTextCmp,
	}
}

// sortPostings reorders []ast.Posting into a canonical order before
// comparison so that two transactions whose postings differ only in
// emission order compare equal. The transformer is keyed on
// []ast.Posting; that slice type appears in the AST only as
// Transaction.Postings, so the type-wide rule has no other effect.
//
// cmp re-enters the transformer's output, so the existing Span /
// Metadata / Decimal / free-text rules continue to apply to each
// Posting after sorting.
//
// The less-fn calls postingKey on each compare rather than
// pre-computing a parallel []string of keys. Real ledgers are
// dominated by 2-posting transactions, where insertion sort issues a
// single compare and per-call costs exactly two key constructions —
// the same number a precompute pass would require, plus one fewer
// slice allocation. Precomputing only starts to win at 3+ postings
// (where the comparator is invoked more than n times), and even at
// n=10 the absolute saving is small relative to the cmp.Equal walk
// over the rest of the transaction. Per-call keeps the code shorter
// and matches the typical case best.
var sortPostings = cmp.Transformer("dedup.sortPostings", func(ps []ast.Posting) []ast.Posting {
	out := append([]ast.Posting(nil), ps...)
	sort.SliceStable(out, func(i, j int) bool { return postingKey(out[i]) < postingKey(out[j]) })
	return out
})

// postingKey produces a deterministic ordering key for a posting,
// covering every field that participates in equality. The key serves
// two purposes:
//
//  1. It puts truly-distinct postings into distinct sort positions, so
//     that two transactions whose postings are the same multiset land
//     on the same canonical order across both sides — which is the
//     whole point of sortPostings.
//  2. It uses the *normalized* form for content that goes through
//     normalizeFreeText (MetaString values), so two postings whose
//     Meta differs only in encoding still sort to the same position.
//
// False collisions (two distinct postings producing the same key) are
// fine: cmp walks the sorted slices pairwise after the Transformer
// runs, and the existing Span/Metadata/Decimal options catch any
// remaining difference. The danger is the inverse — equivalent
// postings ending up at different sort positions — which would cause
// cmp to pair them with the wrong neighbour and report a false
// inequality. Every field that is checked by cmp must therefore feed
// into the key.
func postingKey(p ast.Posting) string {
	var b strings.Builder
	b.WriteByte(p.Flag)
	b.WriteByte('|')
	b.WriteString(string(p.Account))
	b.WriteByte('|')
	if p.Amount != nil {
		b.WriteString(p.Amount.Number.String())
		b.WriteByte(':')
		b.WriteString(p.Amount.Currency)
	}
	b.WriteByte('|')
	if p.Cost != nil {
		if p.Cost.PerUnit != nil {
			b.WriteString(p.Cost.PerUnit.Number.String())
			b.WriteByte(':')
			b.WriteString(p.Cost.PerUnit.Currency)
		}
		b.WriteByte('/')
		if p.Cost.Total != nil {
			b.WriteString(p.Cost.Total.Number.String())
			b.WriteByte(':')
			b.WriteString(p.Cost.Total.Currency)
		}
		b.WriteByte('/')
		if p.Cost.Date != nil {
			b.WriteString(p.Cost.Date.Format(time.RFC3339))
		}
		b.WriteByte('/')
		b.WriteString(p.Cost.Label)
	}
	b.WriteByte('|')
	if p.Price != nil {
		if p.Price.IsTotal {
			b.WriteByte('T')
		} else {
			b.WriteByte('U')
		}
		b.WriteString(p.Price.Amount.Number.String())
		b.WriteByte(':')
		b.WriteString(p.Price.Amount.Currency)
	}
	b.WriteByte('|')
	writeMetaKeyPairs(&b, p.Meta.Props)
	return b.String()
}

// writeMetaKeyPairs writes a deterministic encoding of a Metadata map
// onto b: keys are sorted, and each value is rendered via
// metaValueKey, which uses the same normalization as the comparator
// (so encoding-equivalent values produce identical key bytes).
func writeMetaKeyPairs(b *strings.Builder, props map[string]ast.MetaValue) {
	if len(props) == 0 {
		return
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(metaValueKey(props[k]))
		b.WriteByte(';')
	}
}

// metaValueKey renders a MetaValue into a string suitable for use in
// postingKey. It uses normalizeFreeText for the MetaString variant so
// that the sort order matches the equivalence relation enforced by
// metaValueEqual; other variants are rendered byte-exact in their
// natural form because metaValueEqual treats them structurally.
func metaValueKey(mv ast.MetaValue) string {
	var b strings.Builder
	b.WriteByte('K')
	b.WriteString(strconv.Itoa(int(mv.Kind)))
	b.WriteByte(':')
	switch mv.Kind {
	case ast.MetaString:
		b.WriteString(normalizeFreeText(mv.String))
	case ast.MetaAccount, ast.MetaCurrency, ast.MetaTag, ast.MetaLink:
		b.WriteString(mv.String)
	case ast.MetaDate:
		b.WriteString(mv.Date.Format(time.RFC3339))
	case ast.MetaNumber:
		b.WriteString(mv.Number.String())
	case ast.MetaAmount:
		b.WriteString(mv.Amount.Number.String())
		b.WriteByte(':')
		b.WriteString(mv.Amount.Currency)
	case ast.MetaBool:
		if mv.Bool {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	default:
		// A new MetaValueKind without a case here would fall through
		// to an empty body and produce a weak ordering key. Panic so
		// the omission is caught at the test boundary instead of
		// silently mispairing postings.
		panic("dedup: metaValueKey: unhandled MetaValueKind")
	}
	return b.String()
}

// freeTextCmp normalizes a narrow set of free-text string fields
// (Transaction.Narration, Transaction.Payee, Note.Comment) before
// comparison: NFKC normalization plus removal of every Unicode
// whitespace rune. Identifier-bearing strings are left alone.
//
// Free-text MetaValue contents (Kind == MetaString) get the same
// treatment via metadataEqual / metaMatch; that path runs through the
// Metadata Comparer rather than this FilterPath because the per-key
// walk there already discriminates by MetaValueKind.
//
// Implementation: cmp.Path is the chain of PathSteps from the
// comparison root to the value currently being inspected. For a
// string field on a directive, the chain ends in
//
//	... → cmp.Indirect (deref *T) → cmp.StructField{Name: "F"}
//
// (or omits the Indirect step when the directive was passed by
// value). The predicate inspects the last two steps:
//
//   - p.Last() is the leaf — the StructField step naming the field.
//     If it's anything else (slice index, map key, …) we don't
//     normalize.
//   - p.Index(-2) is the step that landed on the containing struct;
//     its Type() is the struct type after any pointer indirection.
//     We dispatch on that type so the filter only fires inside
//     ast.Transaction or ast.Note.
//
// The len(p) < 2 guard rules out paths shorter than two steps, where
// a parent step doesn't exist; those would otherwise return an empty
// step with a nil Type, which would simply fall through the type
// switch but the explicit check is clearer.
var freeTextCmp = cmp.FilterPath(func(p cmp.Path) bool {
	if len(p) < 2 {
		return false
	}
	sf, ok := p.Last().(cmp.StructField)
	if !ok {
		return false
	}
	switch p.Index(-2).Type() {
	case transactionType:
		return sf.Name() == "Narration" || sf.Name() == "Payee"
	case noteType:
		return sf.Name() == "Comment"
	}
	return false
}, cmp.Comparer(func(a, b string) bool {
	return normalizeFreeText(a) == normalizeFreeText(b)
}))

// normalizeFreeText canonicalizes a human-typed string for
// cross-source comparison: NFKC folds compatibility variants
// (full-width vs. half-width, presentation forms, ligatures, etc.)
// into a single representation, then every Unicode whitespace rune is
// removed. unicode.IsSpace covers ASCII whitespace plus U+0085,
// U+00A0, U+1680, U+2000–U+200A, U+2028, U+2029, U+202F, U+205F, and
// U+3000 — the standard "Unicode whitespace" set.
func normalizeFreeText(s string) string {
	s = norm.NFKC.String(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// metadataEqual reports whether two Metadata values are equal after
// stripping the override key. nil and empty maps compare equal.
// MetaString values are normalized via normalizeFreeText so that
// cosmetic encoding differences (NFC vs NFD, full-width vs half-width,
// inserted whitespace) do not block dedup; every other MetaValueKind
// is compared structurally without normalization, so identifier-shaped
// content (MetaAccount, MetaCurrency, MetaTag, MetaLink) and typed
// scalars (MetaDate, MetaNumber, MetaAmount, MetaBool) keep their
// usual equality semantics.
func metadataEqual(a, b ast.Metadata, overrideMetaKey string) bool {
	stripped := func(props map[string]ast.MetaValue) map[string]ast.MetaValue {
		if overrideMetaKey == "" {
			return props
		}
		if _, ok := props[overrideMetaKey]; !ok {
			return props
		}
		out := make(map[string]ast.MetaValue, len(props))
		for k, v := range props {
			if k == overrideMetaKey {
				continue
			}
			out[k] = v
		}
		return out
	}
	pa, pb := stripped(a.Props), stripped(b.Props)
	if len(pa) != len(pb) {
		return false
	}
	for k, va := range pa {
		vb, ok := pb[k]
		if !ok {
			return false
		}
		if !metaValueEqual(va, vb) {
			return false
		}
	}
	return true
}

// metaValueEqual reports whether two MetaValue values are equal,
// applying free-text normalization to the MetaString variant only.
func metaValueEqual(a, b ast.MetaValue) bool {
	if a.Kind == ast.MetaString && b.Kind == ast.MetaString {
		return normalizeFreeText(a.String) == normalizeFreeText(b.String)
	}
	return cmp.Equal(a, b, decimalCmp)
}

// equivalent reports whether a and b are equivalent under the design's
// OR-combined rule. AST equality (with Span and the override key
// stripped) wins first; otherwise a metadata-key match against any of
// eqKeys produces MatchMeta. MatchNone otherwise.
func equivalent(a, b ast.Directive, overrideMetaKey string, eqKeys []string) MatchKind {
	if cmp.Equal(a, b, equalityOpts(overrideMetaKey)...) {
		return MatchAST
	}
	if metaMatch(a, b, eqKeys) {
		return MatchMeta
	}
	return MatchNone
}

// metaMatch reports whether a and b carry the same value under any key
// listed in eqKeys. The first match wins. MetaString values go through
// normalizeFreeText so that the cross-source dedup path agrees with
// the AST-equality path on what counts as "the same string".
func metaMatch(a, b ast.Directive, eqKeys []string) bool {
	if len(eqKeys) == 0 {
		return false
	}
	ma, mb := metadataOf(a), metadataOf(b)
	if ma == nil || mb == nil {
		return false
	}
	for _, k := range eqKeys {
		va, oka := ma.Props[k]
		vb, okb := mb.Props[k]
		if !oka || !okb {
			continue
		}
		if metaValueEqual(va, vb) {
			return true
		}
	}
	return false
}

// metadataOf returns the metadata pointer of a directive that carries
// metadata, or nil for directive types that do not.
func metadataOf(d ast.Directive) *ast.Metadata {
	switch v := d.(type) {
	case *ast.Open:
		return &v.Meta
	case *ast.Close:
		return &v.Meta
	case *ast.Commodity:
		return &v.Meta
	case *ast.Balance:
		return &v.Meta
	case *ast.Pad:
		return &v.Meta
	case *ast.Note:
		return &v.Meta
	case *ast.Document:
		return &v.Meta
	case *ast.Price:
		return &v.Meta
	case *ast.Event:
		return &v.Meta
	case *ast.Query:
		return &v.Meta
	case *ast.Custom:
		return &v.Meta
	case *ast.Transaction:
		return &v.Meta
	}
	return nil
}
