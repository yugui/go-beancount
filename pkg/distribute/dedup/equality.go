package dedup

import (
	"reflect"
	"strings"
	"time"
	"unicode"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/yugui/go-beancount/pkg/ast"
	"golang.org/x/text/unicode/norm"
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

// exactOpts implements the MatchExact rule (M1+M2): structural AST
// equality with source location and all metadata removed from the
// comparison. Span is ignored everywhere it appears; Metadata is
// ignored everywhere it appears (directive- and posting-level), because
// identity-bearing metadata is handled separately by the id layer
// (idCompare) and every other metadata entry is treated as a pure
// annotation. apd.Decimal is compared numerically. Narration / Payee /
// Note.Comment are NFKC-normalized. Posting lists are compared by
// postingsExactEqual, which canonicalizes order and tolerates one
// auto-balanced posting per side.
var exactOpts = cmp.Options{
	cmpopts.IgnoreTypes(ast.Span{}, ast.Metadata{}),
	decimalCmp,
	freeTextCmp,
	cmp.Comparer(func(a, b []ast.Posting) bool { return postingsExactEqual(a, b) }),
}

// postingFieldOpts compares the per-field content of two fully-specified
// postings (cost and price annotations) numerically and free of source
// location. Posting metadata is excluded from identity, mirroring
// exactOpts.
var postingFieldOpts = cmp.Options{
	cmpopts.IgnoreTypes(ast.Span{}, ast.Metadata{}),
	decimalCmp,
}

// freeTextCmp implements the package doc's free-text normalization rule
// for Transaction.Narration, Transaction.Payee, and Note.Comment.
//
// The predicate inspects the last two cmp.Path steps: p.Last() must be a
// [cmp.StructField] (the leaf field) and p.Index(-2) must land on
// [ast.Transaction] or [ast.Note] (the containing struct after any
// pointer indirection). The len(p) < 2 guard rules out paths shorter
// than two steps, where p.Index(-2) would return an empty step.
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
// cross-source comparison. It applies NFKC and then drops every
// unicode.IsSpace rune (the standard Unicode whitespace set:
// ASCII whitespace plus U+0085, U+00A0, U+1680, U+2000–U+200A,
// U+2028, U+2029, U+202F, U+205F, U+3000).
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

// metaValueEqual reports whether two MetaValue values are equal,
// applying free-text normalization to the MetaString variant only. It
// is the value comparator for the id layer, so two importers stamping
// the same stable id with different free-text formatting still match.
func metaValueEqual(a, b ast.MetaValue) bool {
	if a.Kind == ast.MetaString && b.Kind == ast.MetaString {
		return normalizeFreeText(a.String) == normalizeFreeText(b.String)
	}
	return cmp.Equal(a, b, decimalCmp)
}

// equivalent classifies the relationship between two directives under
// the layered match rules, in priority order:
//
//  1. id conflict (a designated id key present on both with differing
//     values) vetoes any match — the directives are proven distinct,
//     so MatchNone is returned even if their content is identical.
//  2. id equality (a designated id key present on both with equal
//     values) yields MatchID.
//  3. structural AST equality (modulo metadata, with auto-posting
//     tolerance) yields MatchExact.
//  4. transfer-aware structural similarity within the date window
//     yields MatchStructural.
//
// MatchID and MatchExact are equivalence relations and are safe to
// skip on; MatchStructural is non-transitive and callers must treat it
// as review-only (see MatchKind.SkipCapable).
func equivalent(a, b ast.Directive, p MatchParams) MatchKind {
	switch idCompare(a, b, p.IDKeys) {
	case idConflict:
		return MatchNone
	case idEqual:
		return MatchID
	}
	if cmp.Equal(a, b, exactOpts...) {
		return MatchExact
	}
	if structuralMatch(a, b, p.DateWindowDays) {
		return MatchStructural
	}
	return MatchNone
}

// idVerdict is the result of comparing two directives over the
// configured id keys.
type idVerdict int

const (
	idNone idVerdict = iota
	idEqual
	idConflict
)

// idCompare evaluates the directive-level id keys (M4/M5). A key counts
// only when present on both sides; an equal shared value is identity
// evidence, an unequal shared value is a distinctness proof. A conflict
// on any key wins over equality on any other, because contradictory id
// data is safest resolved as "distinct".
func idCompare(a, b ast.Directive, idKeys []string) idVerdict {
	if len(idKeys) == 0 {
		return idNone
	}
	ma, mb := a.DirMeta(), b.DirMeta()
	verdict := idNone
	for _, k := range idKeys {
		va, oka := ma.Props[k]
		vb, okb := mb.Props[k]
		if !oka || !okb {
			continue
		}
		if metaValueEqual(va, vb) {
			verdict = idEqual
		} else {
			return idConflict
		}
	}
	return verdict
}

// structuralMatch implements the transfer-aware structural rule
// (M8+M7): two transactions match when their postings form the same
// multiset under account + absolute amount + currency (the absolute
// value absorbs the sign flip between the two sides of a transfer) and
// their dates fall within windowDays. It tolerates one auto-balanced
// posting per side. windowDays <= 0 disables the rule. Only
// Transactions participate; all other directive kinds return false.
func structuralMatch(a, b ast.Directive, windowDays int) bool {
	if windowDays <= 0 {
		return false
	}
	ta, ok := a.(*ast.Transaction)
	if !ok {
		return false
	}
	tb, ok := b.(*ast.Transaction)
	if !ok {
		return false
	}
	if !withinDays(ta.Date, tb.Date, windowDays) {
		return false
	}
	return matchPostings(ta.Postings, tb.Postings, structuralPostingEqual)
}

// withinDays reports whether a and b are at most days apart.
func withinDays(a, b time.Time, days int) bool {
	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	return diff <= time.Duration(days)*24*time.Hour
}

// postingsExactEqual reports whether two posting lists are the same
// multiset of fully-specified postings, tolerating one auto-balanced
// posting per side. It is the []ast.Posting comparator inside exactOpts.
func postingsExactEqual(a, b []ast.Posting) bool {
	return matchPostings(a, b, exactPostingEqual)
}

// exactPostingEqual compares two fully-specified postings (both Amount
// non-nil) for MatchExact: account, flag, signed amount, currency,
// cost, and price must all agree. Posting metadata is excluded.
func exactPostingEqual(x, y ast.Posting) bool {
	if x.Account != y.Account || x.Flag != y.Flag {
		return false
	}
	if x.Amount.Currency != y.Amount.Currency || x.Amount.Number.Cmp(&y.Amount.Number) != 0 {
		return false
	}
	return cmp.Equal(x.Cost, y.Cost, postingFieldOpts...) &&
		cmp.Equal(x.Price, y.Price, postingFieldOpts...)
}

// structuralPostingEqual compares two fully-specified postings for
// MatchStructural: account and currency must agree and the amounts must
// be equal in absolute value, so the two legs of a transfer (which
// differ only in sign) are treated as the same.
func structuralPostingEqual(x, y ast.Posting) bool {
	if x.Account != y.Account || x.Amount.Currency != y.Amount.Currency {
		return false
	}
	return absEqual(&x.Amount.Number, &y.Amount.Number)
}

// absEqual reports whether x and y have equal absolute value.
func absEqual(x, y *apd.Decimal) bool {
	var ax, ay apd.Decimal
	if _, err := apd.BaseContext.Abs(&ax, x); err != nil {
		return false
	}
	if _, err := apd.BaseContext.Abs(&ay, y); err != nil {
		return false
	}
	return ax.Cmp(&ay) == 0
}

// matchPostings reports whether a and b describe the same posting
// multiset, tolerating the auto-balanced (Amount == nil) posting that
// beancount permits at most once per transaction. keyedEqual compares
// two fully-specified postings; an elided posting is matched by account
// alone, which is sound because its amount is fixed by the balance
// constraint once every other posting is matched.
//
// At most one elided posting is allowed per side (two would make the
// balance ambiguous). The total posting count must agree, so a genuine
// difference in shape never matches.
func matchPostings(a, b []ast.Posting, keyedEqual func(x, y ast.Posting) bool) bool {
	if len(a) != len(b) {
		return false
	}
	ka, ea := partitionElided(a)
	kb, eb := partitionElided(b)
	if len(ea) > 1 || len(eb) > 1 {
		return false
	}
	switch {
	case len(ea) == 0 && len(eb) == 0:
		return multisetEqual(ka, kb, keyedEqual)
	case len(ea) == 1 && len(eb) == 1:
		// Both sides elide a leg: the keyed postings must match exactly
		// and the elided legs must name the same account, which makes
		// their (balance-determined) amounts equal too.
		return ea[0] == eb[0] && multisetEqual(ka, kb, keyedEqual)
	case len(ea) == 1:
		// a elides a leg that b states explicitly: every keyed posting of
		// a must match one of b, leaving exactly one b posting whose
		// account is the elided account.
		return subsetWithLeftoverAccount(ka, kb, keyedEqual, ea[0])
	default:
		return subsetWithLeftoverAccount(kb, ka, keyedEqual, eb[0])
	}
}

// partitionElided splits ps into fully-specified postings and the
// accounts of the amount-elided (auto-balanced) postings.
func partitionElided(ps []ast.Posting) (keyed []ast.Posting, elided []ast.Account) {
	for _, p := range ps {
		if p.Amount == nil {
			elided = append(elided, p.Account)
		} else {
			keyed = append(keyed, p)
		}
	}
	return keyed, elided
}

// multisetEqual reports whether a and b are equal multisets under the
// equivalence relation eq. Greedy first-match assignment is correct
// because eq is an equivalence relation: any element equal to a[i] is
// interchangeable.
func multisetEqual(a, b []ast.Posting, eq func(x, y ast.Posting) bool) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
	for i := range a {
		matched := false
		for j := range b {
			if !used[j] && eq(a[i], b[j]) {
				used[j] = true
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// subsetWithLeftoverAccount reports whether super equals sub plus
// exactly one extra posting whose account is acct. It models the
// auto-posting case where sub omits the leg that super states: the
// leftover super posting is the stated counterpart of sub's elided leg,
// and its amount is guaranteed correct by the balance constraint.
func subsetWithLeftoverAccount(sub, super []ast.Posting, eq func(x, y ast.Posting) bool, acct ast.Account) bool {
	if len(super) != len(sub)+1 {
		return false
	}
	used := make([]bool, len(super))
	for i := range sub {
		matched := false
		for j := range super {
			if !used[j] && eq(sub[i], super[j]) {
				used[j] = true
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	leftover := -1
	for j := range super {
		if used[j] {
			continue
		}
		if leftover != -1 {
			return false
		}
		leftover = j
	}
	return leftover != -1 && super[leftover].Account == acct
}
