package dedup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"golang.org/x/text/unicode/norm"

	"github.com/yugui/go-beancount/pkg/ast"
)

// dec parses a literal decimal for use in Posting/Amount fields.
func dec(t *testing.T, s string) apd.Decimal {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString(s); err != nil {
		t.Fatalf("decimal %q: %v", s, err)
	}
	return d
}

// txn builds a Transaction with the given narration, payee, and
// postings — enough to drive equality tests without going through the
// parser, which keeps Unicode encoding under direct control.
func txn(narration, payee string, postings ...ast.Posting) *ast.Transaction {
	return &ast.Transaction{
		Date:      time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		Flag:      '*',
		Payee:     payee,
		Narration: narration,
		Postings:  postings,
	}
}

// posting constructs a single ast.Posting with an explicit amount.
// Callers needing posting-level metadata, cost, price, or a flag
// fill those fields on the returned value.
func posting(account ast.Account, num apd.Decimal, currency string) ast.Posting {
	return ast.Posting{Account: account, Amount: &ast.Amount{Number: num, Currency: currency}}
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing date %q: %v", s, err)
	}
	return d
}

func mustParse(t *testing.T, src string) ast.Directive {
	t.Helper()
	ledger, err := ast.Load(src, ast.WithBaseDir(""))
	if err != nil {
		t.Fatalf("ast.Load(%q): %v", src, err)
	}
	if ledger.Len() == 0 {
		t.Fatalf("ast.Load(%q): no directives parsed", src)
	}
	return ledger.At(0)
}

// writeFile writes contents under dir/relPath, creating parents as
// needed, and returns the absolute path.
func writeFile(t *testing.T, dir, relPath, contents string) string {
	t.Helper()
	abs := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing %q: %v", abs, err)
	}
	return abs
}

func TestBuildIndex_RecordsActive(t *testing.T) {
	root := t.TempDir()
	ledgerPath := writeFile(t, root, "main.beancount", `2024-01-15 price USD 110 JPY
`)
	idx, diags, err := BuildIndex(context.Background(), ledgerPath, root)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("diagnostics = %+v, want none", diags)
	}
	d := mustParse(t, "2024-01-15 price USD 110 JPY\n")
	matched, kind := idx.InDestination("main.beancount", d, nil)
	if !matched || kind != MatchAST {
		t.Errorf("InDestination: matched=%v kind=%v, want true MatchAST", matched, kind)
	}
}

func TestBuildIndex_RecordsCommented(t *testing.T) {
	root := t.TempDir()
	ledgerPath := writeFile(t, root, "main.beancount", `; 2024-01-15 price USD 110 JPY
`)
	idx, _, err := BuildIndex(context.Background(), ledgerPath, root)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	d := mustParse(t, "2024-01-15 price USD 110 JPY\n")
	matched, kind := idx.InDestination("main.beancount", d, nil)
	if !matched || kind != MatchAST {
		t.Errorf("InDestination over commented: matched=%v kind=%v, want true MatchAST", matched, kind)
	}
	// Commented entries elsewhere must not satisfy InOtherActive.
	matched, _ = idx.InOtherActive("other.beancount", d, nil)
	if matched {
		t.Errorf("InOtherActive matched a commented entry; want false")
	}
}

func TestBuildIndex_PathCanonicalization(t *testing.T) {
	configRoot := t.TempDir()
	outsideDir := t.TempDir()

	// Write the active price into a file outside configRoot, then a
	// root ledger inside configRoot that includes it via absolute path.
	outsidePath := writeFile(t, outsideDir, "elsewhere.beancount", `2024-01-15 price USD 110 JPY
`)
	rootLedger := writeFile(t, configRoot, "main.beancount", `include "`+outsidePath+`"
`)

	idx, _, err := BuildIndex(context.Background(), rootLedger, configRoot)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	d := mustParse(t, "2024-01-15 price USD 110 JPY\n")

	// The included file is outside configRoot, so its key is `..`-prefixed.
	// A query for any in-root path must miss it under InDestination.
	matched, _ := idx.InDestination("quotes/USD/202401.beancount", d, nil)
	if matched {
		t.Errorf("InDestination from in-root path matched an outside-root entry; want false")
	}
	// But InOtherActive should still see the active outside entry: the
	// key differs from the queried path, satisfying the "elsewhere"
	// scope, and outside-root files are walked just like in-root ones.
	matched, _ = idx.InOtherActive("quotes/USD/202401.beancount", d, nil)
	if !matched {
		t.Errorf("InOtherActive did not see the outside-root active entry; want true")
	}
}

func TestBuildIndex_ContextCancellation(t *testing.T) {
	root := t.TempDir()
	incPath := writeFile(t, root, "inc.beancount", `2024-01-15 price USD 110 JPY
`)
	ledgerPath := writeFile(t, root, "main.beancount", `include "`+incPath+`"
2024-02-15 price USD 111 JPY
`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := BuildIndex(ctx, ledgerPath, root)
	if err == nil {
		t.Fatal("BuildIndex with cancelled ctx: got nil error, want ctx.Err()")
	}
	if err != context.Canceled {
		t.Errorf("BuildIndex error = %v, want context.Canceled", err)
	}
}

func TestEqualityOpts_IgnoresSpan(t *testing.T) {
	a := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	a.Span = ast.Span{Start: ast.Position{Filename: "x.beancount", Line: 1}}
	b := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	b.Span = ast.Span{Start: ast.Position{Filename: "y.beancount", Line: 99}}
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent ignoring Span: got %v, want MatchAST", k)
	}
}

func TestEqualityOpts_IgnoresOverrideKey(t *testing.T) {
	a := &ast.Open{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"route-account": {Kind: ast.MetaString, String: "Assets:Other"}}},
	}
	b := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent ignoring override key (nil meta): got %v, want MatchAST", k)
	}
	// Two directives that disagree only on the override key value also compare equal.
	c := &ast.Open{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"route-account": {Kind: ast.MetaString, String: "Assets:X"}}},
	}
	if k := equivalent(a, c, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent with both override keys: got %v, want MatchAST", k)
	}
}

func TestEquivalent_MetaKeyMatch(t *testing.T) {
	a := &ast.Open{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"import-id": {Kind: ast.MetaString, String: "abc"}}},
	}
	b := &ast.Open{
		Date:    mustDate(t, "2024-02-20"),
		Account: "Assets:B",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"import-id": {Kind: ast.MetaString, String: "abc"}}},
	}
	if k := equivalent(a, b, "route-account", []string{"import-id"}); k != MatchMeta {
		t.Errorf("equivalent with import-id match: got %v, want MatchMeta", k)
	}
	// Different values must not match.
	b.Meta.Props["import-id"] = ast.MetaValue{Kind: ast.MetaString, String: "xyz"}
	if k := equivalent(a, b, "route-account", []string{"import-id"}); k != MatchNone {
		t.Errorf("equivalent with differing import-id: got %v, want MatchNone", k)
	}
}

func TestIndex_InOtherActiveScope(t *testing.T) {
	idx := &memoryIndex{}
	d := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	idx.Add("Q.beancount", d, false)

	probe := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	if matched, _ := idx.InOtherActive("P.beancount", probe, nil); !matched {
		t.Error("InOtherActive: active entry at Q should match query at P")
	}

	idx2 := &memoryIndex{}
	idx2.Add("Q.beancount", d, true)
	if matched, _ := idx2.InOtherActive("P.beancount", probe, nil); matched {
		t.Error("InOtherActive: commented entry at Q must not match query at P")
	}
}

func TestIndex_AddAffectsSubsequentQueries(t *testing.T) {
	idx := &memoryIndex{}
	d := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	if matched, _ := idx.InDestination("P.beancount", d, nil); matched {
		t.Fatal("InDestination on empty index: matched=true, want false")
	}

	idx.Add("P.beancount", d, false)
	if matched, kind := idx.InDestination("P.beancount", d, nil); !matched || kind != MatchAST {
		t.Errorf("after Add(active): matched=%v kind=%v, want true MatchAST", matched, kind)
	}

	idx2 := &memoryIndex{}
	idx2.Add("P.beancount", d, true)
	if matched, kind := idx2.InDestination("P.beancount", d, nil); !matched || kind != MatchAST {
		t.Errorf("after Add(commented): matched=%v kind=%v, want true MatchAST", matched, kind)
	}
}

// Sanity: the MatchKind String form is not part of the API but making
// failures readable helps debug; the test keeps it import-less by
// using a simple compare against raw integers.
func TestMatchKindValues(t *testing.T) {
	if MatchNone != 0 || MatchAST != 1 || MatchMeta != 2 {
		t.Errorf("MatchKind iota drifted: None=%d AST=%d Meta=%d", MatchNone, MatchAST, MatchMeta)
	}
}

// TestEquivalent_PostingOrderSwap verifies that two transactions whose
// postings differ only in emission order compare equal. This is the
// minimum dedup property when the same transaction comes from two
// importers that disagree on which leg to emit first.
func TestEquivalent_PostingOrderSwap(t *testing.T) {
	d := dec(t, "100")
	dn := dec(t, "-100")
	a := txn("groceries", "",
		posting("Expenses:Food", d, "USD"),
		posting("Assets:Cash", dn, "USD"),
	)
	b := txn("groceries", "",
		posting("Assets:Cash", dn, "USD"),
		posting("Expenses:Food", d, "USD"),
	)
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent across posting swap: got %v, want MatchAST", k)
	}
}

// TestEquivalent_PostingMetaDifferent verifies the canonicalization
// does not collapse postings whose Meta differs — only field-level
// equality is granted, not posting deduplication.
func TestEquivalent_PostingMetaDifferent(t *testing.T) {
	d := dec(t, "100")
	dn := dec(t, "-100")
	pa := posting("Expenses:Food", d, "USD")
	pa.Meta = ast.Metadata{Props: map[string]ast.MetaValue{"src": {Kind: ast.MetaString, String: "A"}}}
	pb := posting("Expenses:Food", d, "USD")
	pb.Meta = ast.Metadata{Props: map[string]ast.MetaValue{"src": {Kind: ast.MetaString, String: "B"}}}
	cash := posting("Assets:Cash", dn, "USD")
	a := txn("x", "", pa, cash)
	b := txn("x", "", pb, cash)
	if k := equivalent(a, b, "route-account", nil); k != MatchNone {
		t.Errorf("equivalent with diverging posting Meta: got %v, want MatchNone", k)
	}
}

// nfcCafe and nfdCafe spell "café" precomposed and as base+combining
// codepoints. Constructing the NFD form via norm.NFD.String keeps the
// distinction explicit and immune to editor normalization that would
// otherwise silently rewrite the source bytes and break the test
// premise.
const nfcCafe = "caf\u00e9" // "café"

var nfdCafe = norm.NFD.String(nfcCafe)

// TestEquivalent_NarrationNFC_NFD covers the most common Unicode
// normalization mismatch — the same character emitted as a precomposed
// codepoint by one source and as base + combining mark by another.
func TestEquivalent_NarrationNFC_NFD(t *testing.T) {
	a := txn(nfcCafe, "", posting("Assets:A", dec(t, "1"), "USD"))
	b := txn(nfdCafe, "", posting("Assets:A", dec(t, "1"), "USD"))
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent across NFC/NFD narration: got %v, want MatchAST", k)
	}
}

// TestEquivalent_NarrationFullWidth verifies NFKC behavior: full-width
// Latin and digit characters fold to their half-width equivalents.
// The full-width string is built from \u escapes so the test does not
// rely on rare glyphs surviving editor round-trips.
func TestEquivalent_NarrationFullWidth(t *testing.T) {
	const fullWidth = "\uff21\uff22\uff23\uff11\uff12\uff13" // "ＡＢＣ１２３" (full-width "ABC123")
	a := txn("ABC123", "", posting("Assets:A", dec(t, "1"), "USD"))
	b := txn(fullWidth, "", posting("Assets:A", dec(t, "1"), "USD"))
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent across full-width narration: got %v, want MatchAST", k)
	}
}

// TestEquivalent_NarrationIdeographicSpace verifies that Unicode
// whitespace (here U+3000 IDEOGRAPHIC SPACE) is stripped before
// comparison — including whitespace embedded inside the string, not
// just at its edges. The ideographic space is written as \u3000 so
// the intent is unambiguous; an ASCII space looks identical in many
// editors.
func TestEquivalent_NarrationIdeographicSpace(t *testing.T) {
	a := txn("Foo Bar", "", posting("Assets:A", dec(t, "1"), "USD"))
	b := txn("Foo\u3000Bar", "", posting("Assets:A", dec(t, "1"), "USD")) // "Foo<U+3000 IDEOGRAPHIC SPACE>Bar"
	c := txn("FooBar", "", posting("Assets:A", dec(t, "1"), "USD"))
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent across ASCII vs ideographic space: got %v, want MatchAST", k)
	}
	if k := equivalent(a, c, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent across embedded vs no whitespace: got %v, want MatchAST", k)
	}
}

// TestEquivalent_PayeeNFC_NFD pins normalization on Transaction.Payee,
// which lives behind a different FilterPath branch than Narration.
func TestEquivalent_PayeeNFC_NFD(t *testing.T) {
	a := txn("x", nfcCafe, posting("Assets:A", dec(t, "1"), "USD"))
	b := txn("x", nfdCafe, posting("Assets:A", dec(t, "1"), "USD"))
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent across NFC/NFD payee: got %v, want MatchAST", k)
	}
}

// TestEquivalent_NoteCommentNormalized exercises the noteType branch
// of freeTextCmp. Without this test that branch is unreachable from
// the suite, and a regression that mis-scopes the FilterPath would go
// undetected.
func TestEquivalent_NoteCommentNormalized(t *testing.T) {
	a := &ast.Note{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Comment: nfcCafe + " visit",
	}
	b := &ast.Note{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Comment: nfdCafe + "\u3000visit", // NFD "café" + <U+3000 IDEOGRAPHIC SPACE> + "visit"
	}
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent across normalized Note.Comment: got %v, want MatchAST", k)
	}
}

// TestEquivalent_AccountUnicodeNotCollapsed confirms the
// normalization scope: account names are identifiers, so two postings
// whose accounts differ only in NFC vs NFD encoding must NOT compare
// equal — collapsing them would mask routing mistakes.
func TestEquivalent_AccountUnicodeNotCollapsed(t *testing.T) {
	a := txn("x", "", posting(ast.Account("Assets:"+nfcCafe), dec(t, "1"), "USD"))
	b := txn("x", "", posting(ast.Account("Assets:"+nfdCafe), dec(t, "1"), "USD"))
	if k := equivalent(a, b, "route-account", nil); k != MatchNone {
		t.Errorf("equivalent across account NFC/NFD: got %v, want MatchNone (account is an identifier)", k)
	}
}

// TestEquivalent_CurrencyFullWidth confirms currency codes are
// treated as identifiers — full-width vs half-width must not compare
// equal even though both render the same.
func TestEquivalent_CurrencyFullWidth(t *testing.T) {
	const fullUSD = "\uff35\uff33\uff24" // "ＵＳＤ" (full-width "USD")
	a := txn("x", "", posting("Assets:A", dec(t, "1"), "USD"))
	b := txn("x", "", posting("Assets:A", dec(t, "1"), fullUSD))
	if k := equivalent(a, b, "route-account", nil); k != MatchNone {
		t.Errorf("equivalent across currency NFKC: got %v, want MatchNone", k)
	}
}

// TestEquivalent_MetaStringWhitespace verifies that MetaString values
// inside metadata get the same normalization as top-level free-text
// fields — both AST equality and meta-key matching paths must agree on
// what counts as the same string.
func TestEquivalent_MetaStringWhitespace(t *testing.T) {
	a := &ast.Open{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"note": {Kind: ast.MetaString, String: "Hello World"}}},
	}
	b := &ast.Open{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"note": {Kind: ast.MetaString, String: "Hello\u3000World"}}}, // "Hello<U+3000 IDEOGRAPHIC SPACE>World"
	}
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent across MetaString whitespace: got %v, want MatchAST", k)
	}
}

// TestEquivalent_PostingMetaStringNormalized verifies that the
// per-key normalization in metadataEqual fires on Posting.Meta as
// well as directive Meta — both go through the type-keyed Metadata
// Comparer, but a regression that special-cased one location could
// silently lose the other.
func TestEquivalent_PostingMetaStringNormalized(t *testing.T) {
	pa := posting("Expenses:Food", dec(t, "1"), "USD")
	pa.Meta = ast.Metadata{Props: map[string]ast.MetaValue{"note": {Kind: ast.MetaString, String: nfcCafe}}}
	pb := posting("Expenses:Food", dec(t, "1"), "USD")
	pb.Meta = ast.Metadata{Props: map[string]ast.MetaValue{"note": {Kind: ast.MetaString, String: nfdCafe + "\u3000"}}} // NFD "café" + trailing <U+3000 IDEOGRAPHIC SPACE>
	cash := posting("Assets:Cash", dec(t, "-1"), "USD")
	a := txn("x", "", pa, cash)
	b := txn("x", "", pb, cash)
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent across normalized posting MetaString: got %v, want MatchAST", k)
	}
}

// TestEquivalent_MetaTagWhitespace confirms identifier-bearing
// MetaValue kinds (MetaTag here) stay byte-exact: a tag carrying
// whitespace differs from one without, since tag identifiers cannot
// contain whitespace anyway and any difference is meaningful.
func TestEquivalent_MetaTagWhitespace(t *testing.T) {
	a := &ast.Open{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"t": {Kind: ast.MetaTag, String: "ab"}}},
	}
	b := &ast.Open{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"t": {Kind: ast.MetaTag, String: "a b"}}},
	}
	if k := equivalent(a, b, "route-account", nil); k != MatchNone {
		t.Errorf("equivalent across MetaTag whitespace: got %v, want MatchNone (tag is an identifier)", k)
	}
}

// TestEquivalent_NonStringMetaKindsByteExact confirms that
// MetaAccount, MetaCurrency, and MetaLink — which carry strings that
// look free-text-ish but are identifiers — do not get normalized.
// Without these checks a regression that broadened the MetaString
// branch in metaValueEqual would go unnoticed.
func TestEquivalent_NonStringMetaKindsByteExact(t *testing.T) {
	cases := []struct {
		name string
		a, b ast.MetaValue
	}{
		{
			name: "MetaAccount NFC vs NFD",
			a:    ast.MetaValue{Kind: ast.MetaAccount, String: "Assets:" + nfcCafe},
			b:    ast.MetaValue{Kind: ast.MetaAccount, String: "Assets:" + nfdCafe},
		},
		{
			name: "MetaCurrency full-width",
			a:    ast.MetaValue{Kind: ast.MetaCurrency, String: "USD"},
			b:    ast.MetaValue{Kind: ast.MetaCurrency, String: "\uff35\uff33\uff24"}, // "ＵＳＤ" (full-width "USD")
		},
		{
			name: "MetaLink whitespace",
			a:    ast.MetaValue{Kind: ast.MetaLink, String: "id-1"},
			b:    ast.MetaValue{Kind: ast.MetaLink, String: "id-1\u3000"}, // "id-1" + trailing <U+3000 IDEOGRAPHIC SPACE>
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &ast.Open{
				Date:    mustDate(t, "2024-01-15"),
				Account: "Assets:A",
				Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"k": tc.a}},
			}
			b := &ast.Open{
				Date:    mustDate(t, "2024-01-15"),
				Account: "Assets:A",
				Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"k": tc.b}},
			}
			if k := equivalent(a, b, "route-account", nil); k != MatchNone {
				t.Errorf("equivalent(%s): got %v, want MatchNone", tc.name, k)
			}
		})
	}
}

// TestEquivalent_TagsLinksByteExact confirms Transaction.Tags and
// Transaction.Links remain byte-exact. They're string-typed but
// identifiers, intentionally outside the freeTextCmp scope; a
// regression that added them would silently flip semantics.
func TestEquivalent_TagsLinksByteExact(t *testing.T) {
	bare := func() *ast.Transaction {
		return txn("x", "", posting("Assets:A", dec(t, "1"), "USD"))
	}
	t.Run("Tags", func(t *testing.T) {
		a := bare()
		a.Tags = []string{"trip-" + nfcCafe}
		b := bare()
		b.Tags = []string{"trip-" + nfdCafe}
		if k := equivalent(a, b, "route-account", nil); k != MatchNone {
			t.Errorf("equivalent across tag NFC/NFD: got %v, want MatchNone (tag is an identifier)", k)
		}
	})
	t.Run("Links", func(t *testing.T) {
		a := bare()
		a.Links = []string{"\uff21\uff22"} // "ＡＢ" (full-width "AB")
		b := bare()
		b.Links = []string{"AB"}
		if k := equivalent(a, b, "route-account", nil); k != MatchNone {
			t.Errorf("equivalent across link full-width: got %v, want MatchNone (link is an identifier)", k)
		}
	})
}

// TestEquivalent_CostDifference confirms postings differing only in
// cost spec stay distinguishable. postingKey writes Cost components,
// so two transactions whose only difference is Cost.Label or
// Cost.Date must not collapse.
func TestEquivalent_CostDifference(t *testing.T) {
	cash := posting("Assets:Cash", dec(t, "-10"), "USD")
	stock := func(cost *ast.CostSpec) ast.Posting {
		p := posting("Assets:Stock", dec(t, "1"), "AAPL")
		p.Cost = cost
		return p
	}
	t.Run("Label", func(t *testing.T) {
		a := txn("buy", "", stock(&ast.CostSpec{PerUnit: &ast.Amount{Number: dec(t, "10"), Currency: "USD"}, Label: "lot-A"}), cash)
		b := txn("buy", "", stock(&ast.CostSpec{PerUnit: &ast.Amount{Number: dec(t, "10"), Currency: "USD"}, Label: "lot-B"}), cash)
		if k := equivalent(a, b, "route-account", nil); k != MatchNone {
			t.Errorf("equivalent across diverging Cost.Label: got %v, want MatchNone", k)
		}
	})
	t.Run("Date", func(t *testing.T) {
		tA := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		tB := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
		a := txn("buy", "", stock(&ast.CostSpec{PerUnit: &ast.Amount{Number: dec(t, "10"), Currency: "USD"}, Date: &tA}), cash)
		b := txn("buy", "", stock(&ast.CostSpec{PerUnit: &ast.Amount{Number: dec(t, "10"), Currency: "USD"}, Date: &tB}), cash)
		if k := equivalent(a, b, "route-account", nil); k != MatchNone {
			t.Errorf("equivalent across diverging Cost.Date: got %v, want MatchNone", k)
		}
	})
}

// TestEquivalent_PriceDifference pins that postings differing only in
// price annotation (per-unit vs total marker, or amount) remain
// distinguishable.
func TestEquivalent_PriceDifference(t *testing.T) {
	cash := posting("Assets:Cash", dec(t, "-1.10"), "USD")
	leg := func(price *ast.PriceAnnotation) ast.Posting {
		p := posting("Assets:A", dec(t, "1"), "EUR")
		p.Price = price
		return p
	}
	t.Run("IsTotal", func(t *testing.T) {
		a := txn("fx", "", leg(&ast.PriceAnnotation{Amount: ast.Amount{Number: dec(t, "1.10"), Currency: "USD"}, IsTotal: false}), cash)
		b := txn("fx", "", leg(&ast.PriceAnnotation{Amount: ast.Amount{Number: dec(t, "1.10"), Currency: "USD"}, IsTotal: true}), cash)
		if k := equivalent(a, b, "route-account", nil); k != MatchNone {
			t.Errorf("equivalent across diverging Price.IsTotal: got %v, want MatchNone", k)
		}
	})
	t.Run("Amount", func(t *testing.T) {
		a := txn("fx", "", leg(&ast.PriceAnnotation{Amount: ast.Amount{Number: dec(t, "1.10"), Currency: "USD"}, IsTotal: false}), cash)
		b := txn("fx", "", leg(&ast.PriceAnnotation{Amount: ast.Amount{Number: dec(t, "1.20"), Currency: "USD"}, IsTotal: false}), cash)
		if k := equivalent(a, b, "route-account", nil); k != MatchNone {
			t.Errorf("equivalent across diverging Price amount: got %v, want MatchNone", k)
		}
	})
}

// TestEquivalent_PostingFlagDifference confirms posting flags ('*',
// '!') participate in equality.
func TestEquivalent_PostingFlagDifference(t *testing.T) {
	pa := posting("Expenses:Food", dec(t, "1"), "USD")
	pa.Flag = '*'
	pb := posting("Expenses:Food", dec(t, "1"), "USD")
	pb.Flag = '!'
	cash := posting("Assets:Cash", dec(t, "-1"), "USD")
	a := txn("x", "", pa, cash)
	b := txn("x", "", pb, cash)
	if k := equivalent(a, b, "route-account", nil); k != MatchNone {
		t.Errorf("equivalent across diverging posting flag: got %v, want MatchNone", k)
	}
}

// TestEquivalent_MultisetSameAccountMeta is the regression test for
// the postingKey contract: two postings against the same account
// with diverging Meta must sort to distinct positions, so a
// multiset-equal swap across importers still pairs correctly. With a
// value-blind postingKey, both postings would collide at the same
// key and stable sort would preserve input order, mispairing them
// after swap.
func TestEquivalent_MultisetSameAccountMeta(t *testing.T) {
	build := func(srcFirst, srcSecond string) *ast.Transaction {
		first := posting("Expenses:Food", dec(t, "1"), "USD")
		first.Meta = ast.Metadata{Props: map[string]ast.MetaValue{"src": {Kind: ast.MetaString, String: srcFirst}}}
		second := posting("Expenses:Food", dec(t, "1"), "USD")
		second.Meta = ast.Metadata{Props: map[string]ast.MetaValue{"src": {Kind: ast.MetaString, String: srcSecond}}}
		cash := posting("Assets:Cash", dec(t, "-2"), "USD")
		return txn("x", "", first, second, cash)
	}
	a := build("A", "B")
	b := build("B", "A")
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent across multiset-equal swap with diverging Meta: got %v, want MatchAST", k)
	}
}

// TestEquivalent_EmptyPostings exercises the degenerate cases at the
// boundary of sortPostings: zero or one posting must not panic and
// must compare correctly.
func TestEquivalent_EmptyPostings(t *testing.T) {
	a := txn("x", "")
	b := txn("x", "")
	if k := equivalent(a, b, "route-account", nil); k != MatchAST {
		t.Errorf("equivalent on empty postings: got %v, want MatchAST", k)
	}
	c := txn("x", "", posting("Assets:A", dec(t, "1"), "USD"))
	if k := equivalent(a, c, "route-account", nil); k != MatchNone {
		t.Errorf("equivalent on empty vs one-posting: got %v, want MatchNone", k)
	}
}

// TestMetaMatch_NormalizesMetaString verifies that the cross-source
// meta-key dedup path also normalizes MetaString values, so two
// importers writing the same human-readable id with different
// formatting still match.
func TestMetaMatch_NormalizesMetaString(t *testing.T) {
	const fullABC123 = "\uff21\uff22\uff23\uff11\uff12\uff13" // "ＡＢＣ１２３" (full-width "ABC123")
	a := &ast.Open{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"import-id": {Kind: ast.MetaString, String: "ABC 123"}}},
	}
	b := &ast.Open{
		Date:    mustDate(t, "2024-02-20"),
		Account: "Assets:B",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"import-id": {Kind: ast.MetaString, String: fullABC123}}},
	}
	if k := equivalent(a, b, "route-account", []string{"import-id"}); k != MatchMeta {
		t.Errorf("equivalent across normalized MetaString import-id: got %v, want MatchMeta", k)
	}
}

// BuildIndex must surface ledger diagnostics so the CLI's policy can
// decide whether to abort. Use a missing include to provoke an error.
func TestBuildIndex_SurfacesDiagnostics(t *testing.T) {
	root := t.TempDir()
	ledgerPath := writeFile(t, root, "main.beancount", `include "missing.beancount"
`)
	_, diags, err := BuildIndex(context.Background(), ledgerPath, root)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	hasError := false
	for _, d := range diags {
		if d.Severity == ast.Error {
			hasError = true
		}
	}
	if !hasError {
		t.Errorf("expected error diagnostic for unresolved include; got %+v", diags)
	}
}
