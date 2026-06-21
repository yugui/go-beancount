package dedup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/yugui/go-beancount/pkg/ast"
	"golang.org/x/text/unicode/norm"
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

// decp returns a freshly allocated *apd.Decimal parsed from s. Used
// by CostSpec test fixtures where PerUnit / Total are decimal pointers.
func decp(t *testing.T, s string) *apd.Decimal {
	t.Helper()
	d := dec(t, s)
	return &d
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

// txnOn is like txn but with an explicit date, for date-window tests.
func txnOn(date time.Time, narration string, postings ...ast.Posting) *ast.Transaction {
	return &ast.Transaction{
		Date:      date,
		Flag:      '*',
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

// elided constructs an amount-elided (auto-balanced) posting.
func elided(account ast.Account) ast.Posting {
	return ast.Posting{Account: account}
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

// idMeta builds directive metadata carrying a single id-style string key.
func idMeta(key, value string) ast.Metadata {
	return ast.Metadata{Props: map[string]ast.MetaValue{key: {Kind: ast.MetaString, String: value}}}
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
	matched, kind := idx.InDestination("main.beancount", d, MatchParams{})
	if !matched || kind != MatchExact {
		t.Errorf("InDestination: matched=%v kind=%v, want true MatchExact", matched, kind)
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
	matched, kind := idx.InDestination("main.beancount", d, MatchParams{})
	if !matched || kind != MatchExact {
		t.Errorf("InDestination over commented: matched=%v kind=%v, want true MatchExact", matched, kind)
	}
	// Commented entries elsewhere must not satisfy InOtherActive.
	matched, _ = idx.InOtherActive("other.beancount", d, MatchParams{})
	if matched {
		t.Errorf("InOtherActive matched a commented entry; want false")
	}
}

func TestBuildIndex_PathCanonicalization(t *testing.T) {
	configRoot := t.TempDir()
	outsideDir := t.TempDir()

	outsidePath := writeFile(t, outsideDir, "elsewhere.beancount", `2024-01-15 price USD 110 JPY
`)
	rootLedger := writeFile(t, configRoot, "main.beancount", `include "`+outsidePath+`"
`)

	idx, _, err := BuildIndex(context.Background(), rootLedger, configRoot)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	d := mustParse(t, "2024-01-15 price USD 110 JPY\n")

	matched, _ := idx.InDestination("quotes/USD/202401.beancount", d, MatchParams{})
	if matched {
		t.Errorf("InDestination from in-root path matched an outside-root entry; want false")
	}
	matched, _ = idx.InOtherActive("quotes/USD/202401.beancount", d, MatchParams{})
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

func TestEquivalent_IgnoresSpan(t *testing.T) {
	a := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	a.Span = ast.Span{Start: ast.Position{Filename: "x.beancount", Line: 1}}
	b := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	b.Span = ast.Span{Start: ast.Position{Filename: "y.beancount", Line: 99}}
	if k := equivalent(a, b, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent ignoring Span: got %v, want MatchExact", k)
	}
}

// TestEquivalent_MetadataIgnored pins the central change: metadata other
// than the configured id keys is annotation, not identity. Two
// directives that differ only in (non-id) metadata are MatchExact, so a
// re-import of a user-annotated directive deduplicates.
func TestEquivalent_MetadataIgnored(t *testing.T) {
	a := &ast.Open{
		Date:    mustDate(t, "2024-01-15"),
		Account: "Assets:A",
		Meta:    ast.Metadata{Props: map[string]ast.MetaValue{"note": {Kind: ast.MetaString, String: "hand-added"}, "route-account": {Kind: ast.MetaString, String: "Assets:Other"}}},
	}
	b := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	if k := equivalent(a, b, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent ignoring non-id metadata: got %v, want MatchExact", k)
	}
}

// TestEquivalent_PostingMetadataIgnored confirms posting-level metadata
// is also outside identity.
func TestEquivalent_PostingMetadataIgnored(t *testing.T) {
	pa := posting("Expenses:Food", dec(t, "1"), "USD")
	pa.Meta = ast.Metadata{Props: map[string]ast.MetaValue{"src": {Kind: ast.MetaString, String: "A"}}}
	pb := posting("Expenses:Food", dec(t, "1"), "USD")
	pb.Meta = ast.Metadata{Props: map[string]ast.MetaValue{"src": {Kind: ast.MetaString, String: "B"}}}
	cash := posting("Assets:Cash", dec(t, "-1"), "USD")
	a := txn("x", "", pa, cash)
	b := txn("x", "", pb, cash)
	if k := equivalent(a, b, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent ignoring posting metadata: got %v, want MatchExact", k)
	}
}

// TestEquivalent_IDEqual covers MatchID: an equal id-key value makes two
// otherwise-different directives equivalent.
func TestEquivalent_IDEqual(t *testing.T) {
	a := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A", Meta: idMeta("import-id", "abc")}
	b := &ast.Open{Date: mustDate(t, "2024-02-20"), Account: "Assets:B", Meta: idMeta("import-id", "abc")}
	if k := equivalent(a, b, MatchParams{IDKeys: []string{"import-id"}}); k != MatchID {
		t.Errorf("equivalent with equal import-id: got %v, want MatchID", k)
	}
}

// TestEquivalent_IDConflictVetoes is the S7 guard: two structurally
// identical directives carrying conflicting id keys are proven distinct
// and must NOT match, even though their content is identical.
func TestEquivalent_IDConflictVetoes(t *testing.T) {
	a := txn("coffee", "", posting("Assets:Cash", dec(t, "-1000"), "JPY"), posting("Expenses:Food", dec(t, "1000"), "JPY"))
	b := txn("coffee", "", posting("Assets:Cash", dec(t, "-1000"), "JPY"), posting("Expenses:Food", dec(t, "1000"), "JPY"))
	a.Meta = idMeta("fitid", "row-1")
	b.Meta = idMeta("fitid", "row-2")
	if k := equivalent(a, b, MatchParams{IDKeys: []string{"fitid"}}); k != MatchNone {
		t.Errorf("equivalent with conflicting fitid on identical txns: got %v, want MatchNone (veto)", k)
	}
	// Without the id key configured, the veto does not apply: identical
	// content is MatchExact.
	if k := equivalent(a, b, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent without id keys: got %v, want MatchExact", k)
	}
}

// TestEquivalent_IDConflictBeatsEqual confirms a conflict on one key
// wins over equality on another.
func TestEquivalent_IDConflictBeatsEqual(t *testing.T) {
	a := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A", Meta: ast.Metadata{Props: map[string]ast.MetaValue{
		"import-id": {Kind: ast.MetaString, String: "same"},
		"fitid":     {Kind: ast.MetaString, String: "row-1"},
	}}}
	b := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A", Meta: ast.Metadata{Props: map[string]ast.MetaValue{
		"import-id": {Kind: ast.MetaString, String: "same"},
		"fitid":     {Kind: ast.MetaString, String: "row-2"},
	}}}
	if k := equivalent(a, b, MatchParams{IDKeys: []string{"import-id", "fitid"}}); k != MatchNone {
		t.Errorf("equivalent with one equal and one conflicting id key: got %v, want MatchNone", k)
	}
}

// TestEquivalent_IDNormalizesMetaString verifies the id layer normalizes
// MetaString values, so two sources stamping the same human-readable id
// with different formatting still match.
func TestEquivalent_IDNormalizesMetaString(t *testing.T) {
	const fullABC123 = "ＡＢＣ１２３" // "ＡＢＣ１２３" (full-width "ABC123")
	a := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A", Meta: idMeta("import-id", "ABC 123")}
	b := &ast.Open{Date: mustDate(t, "2024-02-20"), Account: "Assets:B", Meta: idMeta("import-id", fullABC123)}
	if k := equivalent(a, b, MatchParams{IDKeys: []string{"import-id"}}); k != MatchID {
		t.Errorf("equivalent across normalized MetaString import-id: got %v, want MatchID", k)
	}
}

// TestEquivalent_StructuralTransfer covers MatchStructural: the two
// perspectives of a transfer (postings with swapped signs) match under
// account + absolute amount within the date window, as a review-only
// (non-skip) result.
func TestEquivalent_StructuralTransfer(t *testing.T) {
	a := txnOn(mustDate(t, "2024-03-01"), "to savings",
		posting("Assets:Checking", dec(t, "-100"), "USD"),
		posting("Assets:Savings", dec(t, "100"), "USD"),
	)
	b := txnOn(mustDate(t, "2024-03-02"), "from checking",
		posting("Assets:Checking", dec(t, "100"), "USD"),
		posting("Assets:Savings", dec(t, "-100"), "USD"),
	)
	if k := equivalent(a, b, MatchParams{DateWindowDays: 3}); k != MatchStructural {
		t.Errorf("equivalent across transfer perspectives within window: got %v, want MatchStructural", k)
	}
	if MatchStructural.SkipCapable() {
		t.Error("MatchStructural.SkipCapable() = true, want false (review-only)")
	}
	// Outside the window: no match.
	c := txnOn(mustDate(t, "2024-03-20"), "from checking",
		posting("Assets:Checking", dec(t, "100"), "USD"),
		posting("Assets:Savings", dec(t, "-100"), "USD"),
	)
	if k := equivalent(a, c, MatchParams{DateWindowDays: 3}); k != MatchNone {
		t.Errorf("equivalent outside date window: got %v, want MatchNone", k)
	}
	// Window unset (0): the structural rule is disabled.
	if k := equivalent(a, b, MatchParams{}); k != MatchNone {
		t.Errorf("equivalent with structural rule disabled: got %v, want MatchNone", k)
	}
}

// TestEquivalent_StructuralDistinctAmounts confirms the structural rule
// does not bridge transactions with different amounts.
func TestEquivalent_StructuralDistinctAmounts(t *testing.T) {
	a := txnOn(mustDate(t, "2024-03-01"), "x",
		posting("Assets:A", dec(t, "-100"), "USD"), posting("Assets:B", dec(t, "100"), "USD"))
	b := txnOn(mustDate(t, "2024-03-01"), "x",
		posting("Assets:A", dec(t, "-90"), "USD"), posting("Assets:B", dec(t, "90"), "USD"))
	if k := equivalent(a, b, MatchParams{DateWindowDays: 3}); k != MatchNone {
		t.Errorf("equivalent across differing amounts: got %v, want MatchNone", k)
	}
}

// TestEquivalent_AutoPostingExact is the auto-balanced posting case for
// MatchExact: an input that elides one leg matches a fully-booked ledger
// entry, and the result is skip-capable.
func TestEquivalent_AutoPostingExact(t *testing.T) {
	input := txn("groceries", "",
		posting("Assets:Cash", dec(t, "-50"), "USD"),
		elided("Expenses:Food"),
	)
	booked := txn("groceries", "",
		posting("Assets:Cash", dec(t, "-50"), "USD"),
		posting("Expenses:Food", dec(t, "50"), "USD"),
	)
	if k := equivalent(input, booked, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent with one elided posting vs booked: got %v, want MatchExact", k)
	}
	if !MatchExact.SkipCapable() {
		t.Error("MatchExact.SkipCapable() = false, want true")
	}
	// The elided leg's account must match the booked leftover; a different
	// account is a genuine difference.
	other := txn("groceries", "",
		posting("Assets:Cash", dec(t, "-50"), "USD"),
		posting("Expenses:Travel", dec(t, "50"), "USD"),
	)
	if k := equivalent(input, other, MatchParams{}); k != MatchNone {
		t.Errorf("equivalent with elided account mismatch: got %v, want MatchNone", k)
	}
}

// TestEquivalent_TwoElidedPostings confirms the fail-safe: beancount
// permits at most one elided posting per transaction, so two elided
// postings on one side never match.
func TestEquivalent_TwoElidedPostings(t *testing.T) {
	a := txn("x", "", posting("Assets:Cash", dec(t, "-50"), "USD"), elided("Expenses:A"), elided("Expenses:B"))
	b := txn("x", "", posting("Assets:Cash", dec(t, "-50"), "USD"), posting("Expenses:A", dec(t, "25"), "USD"), posting("Expenses:B", dec(t, "25"), "USD"))
	if k := equivalent(a, b, MatchParams{}); k != MatchNone {
		t.Errorf("equivalent with two elided postings: got %v, want MatchNone", k)
	}
}

// TestEquivalent_PostingOrderSwap verifies that two transactions whose
// postings differ only in emission order compare equal.
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
	if k := equivalent(a, b, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent across posting swap: got %v, want MatchExact", k)
	}
}

// TestEquivalent_MultisetSameAccount verifies multiset matching pairs
// same-account postings by amount: a swap across importers still pairs
// correctly.
func TestEquivalent_MultisetSameAccount(t *testing.T) {
	build := func(firstNum, secondNum string) *ast.Transaction {
		return txn("x", "",
			posting("Expenses:Food", dec(t, firstNum), "USD"),
			posting("Expenses:Food", dec(t, secondNum), "USD"),
			posting("Assets:Cash", dec(t, "-3"), "USD"),
		)
	}
	a := build("1", "2")
	b := build("2", "1")
	if k := equivalent(a, b, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent across multiset-equal swap: got %v, want MatchExact", k)
	}
	// A genuine amount difference at the shared account is not equal.
	c := build("1", "3")
	if k := equivalent(a, c, MatchParams{}); k != MatchNone {
		t.Errorf("equivalent across differing same-account amounts: got %v, want MatchNone", k)
	}
}

func TestEquivalent_NarrationNFC_NFD(t *testing.T) {
	a := txn(nfcCafe, "", posting("Assets:A", dec(t, "1"), "USD"))
	b := txn(nfdCafe, "", posting("Assets:A", dec(t, "1"), "USD"))
	if k := equivalent(a, b, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent across NFC/NFD narration: got %v, want MatchExact", k)
	}
}

func TestEquivalent_NarrationFullWidth(t *testing.T) {
	const fullWidth = "ＡＢＣ１２３" // "ＡＢＣ１２３" (full-width "ABC123")
	a := txn("ABC123", "", posting("Assets:A", dec(t, "1"), "USD"))
	b := txn(fullWidth, "", posting("Assets:A", dec(t, "1"), "USD"))
	if k := equivalent(a, b, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent across full-width narration: got %v, want MatchExact", k)
	}
}

func TestEquivalent_NarrationIdeographicSpace(t *testing.T) {
	a := txn("Foo Bar", "", posting("Assets:A", dec(t, "1"), "USD"))
	b := txn("Foo　Bar", "", posting("Assets:A", dec(t, "1"), "USD")) // "Foo<U+3000 IDEOGRAPHIC SPACE>Bar"
	c := txn("FooBar", "", posting("Assets:A", dec(t, "1"), "USD"))
	if k := equivalent(a, b, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent across ASCII vs ideographic space: got %v, want MatchExact", k)
	}
	if k := equivalent(a, c, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent across embedded vs no whitespace: got %v, want MatchExact", k)
	}
}

func TestEquivalent_PayeeNFC_NFD(t *testing.T) {
	a := txn("x", nfcCafe, posting("Assets:A", dec(t, "1"), "USD"))
	b := txn("x", nfdCafe, posting("Assets:A", dec(t, "1"), "USD"))
	if k := equivalent(a, b, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent across NFC/NFD payee: got %v, want MatchExact", k)
	}
}

func TestEquivalent_NoteCommentNormalized(t *testing.T) {
	a := &ast.Note{Date: mustDate(t, "2024-01-15"), Account: "Assets:A", Comment: nfcCafe + " visit"}
	b := &ast.Note{Date: mustDate(t, "2024-01-15"), Account: "Assets:A", Comment: nfdCafe + "　visit"}
	if k := equivalent(a, b, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent across normalized Note.Comment: got %v, want MatchExact", k)
	}
}

// TestEquivalent_AccountUnicodeNotCollapsed confirms account names are
// identifiers: NFC vs NFD must NOT compare equal.
func TestEquivalent_AccountUnicodeNotCollapsed(t *testing.T) {
	a := txn("x", "", posting(ast.Account("Assets:"+nfcCafe), dec(t, "1"), "USD"))
	b := txn("x", "", posting(ast.Account("Assets:"+nfdCafe), dec(t, "1"), "USD"))
	if k := equivalent(a, b, MatchParams{}); k != MatchNone {
		t.Errorf("equivalent across account NFC/NFD: got %v, want MatchNone (account is an identifier)", k)
	}
}

// TestEquivalent_CurrencyFullWidth confirms currency codes are
// identifiers — full-width vs half-width must not compare equal.
func TestEquivalent_CurrencyFullWidth(t *testing.T) {
	const fullUSD = "ＵＳＤ" // "ＵＳＤ" (full-width "USD")
	a := txn("x", "", posting("Assets:A", dec(t, "1"), "USD"))
	b := txn("x", "", posting("Assets:A", dec(t, "1"), fullUSD))
	if k := equivalent(a, b, MatchParams{}); k != MatchNone {
		t.Errorf("equivalent across currency NFKC: got %v, want MatchNone", k)
	}
}

// TestEquivalent_CostDifference confirms postings differing only in cost
// spec stay distinguishable under MatchExact.
func TestEquivalent_CostDifference(t *testing.T) {
	cash := posting("Assets:Cash", dec(t, "-10"), "USD")
	stock := func(cost *ast.CostSpec) ast.Posting {
		p := posting("Assets:Stock", dec(t, "1"), "AAPL")
		p.Cost = cost
		return p
	}
	t.Run("Label", func(t *testing.T) {
		a := txn("buy", "", stock(&ast.CostSpec{PerUnit: decp(t, "10"), Currency: "USD", Label: "lot-A"}), cash)
		b := txn("buy", "", stock(&ast.CostSpec{PerUnit: decp(t, "10"), Currency: "USD", Label: "lot-B"}), cash)
		if k := equivalent(a, b, MatchParams{}); k != MatchNone {
			t.Errorf("equivalent across diverging Cost.Label: got %v, want MatchNone", k)
		}
	})
	t.Run("Date", func(t *testing.T) {
		tA := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		tB := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
		a := txn("buy", "", stock(&ast.CostSpec{PerUnit: decp(t, "10"), Currency: "USD", Date: &tA}), cash)
		b := txn("buy", "", stock(&ast.CostSpec{PerUnit: decp(t, "10"), Currency: "USD", Date: &tB}), cash)
		if k := equivalent(a, b, MatchParams{}); k != MatchNone {
			t.Errorf("equivalent across diverging Cost.Date: got %v, want MatchNone", k)
		}
	})
}

// TestEquivalent_PriceDifference pins that postings differing only in
// price annotation remain distinguishable.
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
		if k := equivalent(a, b, MatchParams{}); k != MatchNone {
			t.Errorf("equivalent across diverging Price.IsTotal: got %v, want MatchNone", k)
		}
	})
	t.Run("Amount", func(t *testing.T) {
		a := txn("fx", "", leg(&ast.PriceAnnotation{Amount: ast.Amount{Number: dec(t, "1.10"), Currency: "USD"}, IsTotal: false}), cash)
		b := txn("fx", "", leg(&ast.PriceAnnotation{Amount: ast.Amount{Number: dec(t, "1.20"), Currency: "USD"}, IsTotal: false}), cash)
		if k := equivalent(a, b, MatchParams{}); k != MatchNone {
			t.Errorf("equivalent across diverging Price amount: got %v, want MatchNone", k)
		}
	})
}

// TestEquivalent_PostingFlagDifference confirms posting flags participate
// in MatchExact equality.
func TestEquivalent_PostingFlagDifference(t *testing.T) {
	pa := posting("Expenses:Food", dec(t, "1"), "USD")
	pa.Flag = '*'
	pb := posting("Expenses:Food", dec(t, "1"), "USD")
	pb.Flag = '!'
	cash := posting("Assets:Cash", dec(t, "-1"), "USD")
	a := txn("x", "", pa, cash)
	b := txn("x", "", pb, cash)
	if k := equivalent(a, b, MatchParams{}); k != MatchNone {
		t.Errorf("equivalent across diverging posting flag: got %v, want MatchNone", k)
	}
}

// TestEquivalent_TagsLinksByteExact confirms Transaction.Tags and Links
// remain byte-exact identifiers.
func TestEquivalent_TagsLinksByteExact(t *testing.T) {
	bare := func() *ast.Transaction {
		return txn("x", "", posting("Assets:A", dec(t, "1"), "USD"))
	}
	t.Run("Tags", func(t *testing.T) {
		a := bare()
		a.Tags = []string{"trip-" + nfcCafe}
		b := bare()
		b.Tags = []string{"trip-" + nfdCafe}
		if k := equivalent(a, b, MatchParams{}); k != MatchNone {
			t.Errorf("equivalent across tag NFC/NFD: got %v, want MatchNone (tag is an identifier)", k)
		}
	})
	t.Run("Links", func(t *testing.T) {
		a := bare()
		a.Links = []string{"ＡＢ"} // "ＡＢ" (full-width "AB")
		b := bare()
		b.Links = []string{"AB"}
		if k := equivalent(a, b, MatchParams{}); k != MatchNone {
			t.Errorf("equivalent across link full-width: got %v, want MatchNone (link is an identifier)", k)
		}
	})
}

func TestEquivalent_EmptyPostings(t *testing.T) {
	a := txn("x", "")
	b := txn("x", "")
	if k := equivalent(a, b, MatchParams{}); k != MatchExact {
		t.Errorf("equivalent on empty postings: got %v, want MatchExact", k)
	}
	c := txn("x", "", posting("Assets:A", dec(t, "1"), "USD"))
	if k := equivalent(a, c, MatchParams{}); k != MatchNone {
		t.Errorf("equivalent on empty vs one-posting: got %v, want MatchNone", k)
	}
}

func TestIndex_InOtherActiveScope(t *testing.T) {
	idx := &memoryIndex{}
	d := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	idx.Add("Q.beancount", d, false)

	probe := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	if matched, _ := idx.InOtherActive("P.beancount", probe, MatchParams{}); !matched {
		t.Error("InOtherActive: active entry at Q should match query at P")
	}

	idx2 := &memoryIndex{}
	idx2.Add("Q.beancount", d, true)
	if matched, _ := idx2.InOtherActive("P.beancount", probe, MatchParams{}); matched {
		t.Error("InOtherActive: commented entry at Q must not match query at P")
	}
}

func TestIndex_AddAffectsSubsequentQueries(t *testing.T) {
	idx := &memoryIndex{}
	d := &ast.Open{Date: mustDate(t, "2024-01-15"), Account: "Assets:A"}
	if matched, _ := idx.InDestination("P.beancount", d, MatchParams{}); matched {
		t.Fatal("InDestination on empty index: matched=true, want false")
	}

	idx.Add("P.beancount", d, false)
	if matched, kind := idx.InDestination("P.beancount", d, MatchParams{}); !matched || kind != MatchExact {
		t.Errorf("after Add(active): matched=%v kind=%v, want true MatchExact", matched, kind)
	}

	idx2 := &memoryIndex{}
	idx2.Add("P.beancount", d, true)
	if matched, kind := idx2.InDestination("P.beancount", d, MatchParams{}); !matched || kind != MatchExact {
		t.Errorf("after Add(commented): matched=%v kind=%v, want true MatchExact", matched, kind)
	}
}

// TestMatchKindValues pins the iota order and the skip-capability split.
func TestMatchKindValues(t *testing.T) {
	if MatchNone != 0 || MatchID != 1 || MatchExact != 2 || MatchStructural != 3 || MatchFuzzy != 4 {
		t.Errorf("MatchKind iota drifted: None=%d ID=%d Exact=%d Structural=%d Fuzzy=%d",
			MatchNone, MatchID, MatchExact, MatchStructural, MatchFuzzy)
	}
	skip := map[MatchKind]bool{MatchID: true, MatchExact: true}
	for _, k := range []MatchKind{MatchNone, MatchID, MatchExact, MatchStructural, MatchFuzzy} {
		if got := k.SkipCapable(); got != skip[k] {
			t.Errorf("MatchKind(%d).SkipCapable() = %v, want %v", k, got, skip[k])
		}
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

// nfcCafe and nfdCafe spell "café" precomposed and as base+combining
// codepoints. Constructing the NFD form via norm.NFD.String keeps the
// distinction explicit and immune to editor normalization.
const nfcCafe = "café" // "café"

var nfdCafe = norm.NFD.String(nfcCafe)
