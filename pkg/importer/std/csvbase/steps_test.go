package csvbase_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/std/csvbase"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

// rowCtx constructs a RowContext from a flat header:value list.
func rowCtx(pairs ...string) csvbase.RowContext {
	if len(pairs)%2 != 0 {
		panic("rowCtx: odd number of arguments")
	}
	idx := make(map[string]int, len(pairs)/2)
	fields := make([]string, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		col, val := pairs[i], pairs[i+1]
		pos := i / 2
		idx[col] = pos
		fields[pos] = val
	}
	return csvbase.RowContext{Fields: fields, Index: idx, Path: "/f.csv", Line: 1}
}

// singleString runs a pipeline that yields a single string key and returns it.
func singleString(t *testing.T, rec csvbase.RowContext, build func(*csvbase.Builder) csvbase.Key[string]) (string, *ast.Diagnostic) {
	t.Helper()
	b := csvbase.NewBuilder()
	k := build(b)
	var gotV string
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		gotV, gotD = csvbase.Value(c, k)
		return nil, nil, nil
	})
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	return gotV, gotD
}

// ---------------------------------------------------------------------------
// Column
// ---------------------------------------------------------------------------

func TestColumn_RawValue(t *testing.T) {
	b := csvbase.NewBuilder()
	k := csvbase.Column(b, "Memo")
	var got string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("Memo", "  hello  ")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got != "  hello  " {
		t.Errorf("Column raw = %q, want %q", got, "  hello  ")
	}
}

func TestColumn_RegistersRequired(t *testing.T) {
	b := csvbase.NewBuilder()
	csvbase.Column(b, "Memo")
	p := b.Emit(func(_ context.Context, _ *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		return nil, nil, nil
	})
	req := p.Required()
	if len(req) != 1 || req[0] != "Memo" {
		t.Errorf("Required() = %v, want [Memo]", req)
	}
}

// ---------------------------------------------------------------------------
// Columns
// ---------------------------------------------------------------------------

func TestColumns_SameAsIndividualColumn(t *testing.T) {
	b := csvbase.NewBuilder()
	keys := csvbase.Columns(b, "A", "B", "C")

	var gotA, gotB, gotC string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		gotA, _ = csvbase.Value(c, keys[0])
		gotB, _ = csvbase.Value(c, keys[1])
		gotC, _ = csvbase.Value(c, keys[2])
		return nil, nil, nil
	})
	rec := rowCtx("A", "alpha", "B", "beta", "C", "gamma")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotA != "alpha" || gotB != "beta" || gotC != "gamma" {
		t.Errorf("Columns values = (%q, %q, %q), want (alpha, beta, gamma)", gotA, gotB, gotC)
	}
	req := p.Required()
	wantReq := []string{"A", "B", "C"}
	if diff := cmp.Diff(wantReq, req); diff != "" {
		t.Errorf("Required() mismatch (-want +got):\n%s", diff)
	}
}

// ---------------------------------------------------------------------------
// SplitColumns
// ---------------------------------------------------------------------------

func TestSplitColumns_MatchYieldsGroupKeys(t *testing.T) {
	re := regexp.MustCompile(`(?P<payee>.+?) / (?P<memo>.+)`)
	b := csvbase.NewBuilder()
	detail := csvbase.Column(b, "Detail")
	groups := csvbase.SplitColumns(b, detail, re)

	var gotPayee, gotMemo string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		gotPayee, _ = csvbase.Value(c, groups["payee"])
		gotMemo, _ = csvbase.Value(c, groups["memo"])
		return nil, nil, nil
	})
	rec := rowCtx("Detail", "Amazon / Books")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotPayee != "Amazon" {
		t.Errorf("payee group = %q, want %q", gotPayee, "Amazon")
	}
	if gotMemo != "Books" {
		t.Errorf("memo group = %q, want %q", gotMemo, "Books")
	}
}

func TestSplitColumns_NoMatch_GroupsReadEmpty(t *testing.T) {
	re := regexp.MustCompile(`(?P<payee>.+?) / (?P<memo>.+)`)
	b := csvbase.NewBuilder()
	detail := csvbase.Column(b, "Detail")
	groups := csvbase.SplitColumns(b, detail, re)

	var gotPayee, gotMemo string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		gotPayee, _ = csvbase.Value(c, groups["payee"])
		gotMemo, _ = csvbase.Value(c, groups["memo"])
		return nil, nil, nil
	})
	rec := rowCtx("Detail", "no separator here")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotPayee != "" {
		t.Errorf("no-match payee group = %q, want %q", gotPayee, "")
	}
	if gotMemo != "" {
		t.Errorf("no-match memo group = %q, want %q", gotMemo, "")
	}
}

func TestSplitColumns_RequiredOnlySourceColumn(t *testing.T) {
	re := regexp.MustCompile(`(?P<payee>.+?) / (?P<memo>.+)`)
	b := csvbase.NewBuilder()
	detail := csvbase.Column(b, "Detail")
	csvbase.SplitColumns(b, detail, re)
	p := b.Emit(func(_ context.Context, _ *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		return nil, nil, nil
	})
	req := p.Required()
	if len(req) != 1 || req[0] != "Detail" {
		t.Errorf("Required() = %v, want [Detail] (groups are not required columns)", req)
	}
}

func TestSplitColumns_FeedsResolverSteps(t *testing.T) {
	// Verifies that a SplitColumns group key feeds standard resolver steps.
	re := regexp.MustCompile(`(?P<payee>[^|]+)\|(?P<cat>.+)`)
	b := csvbase.NewBuilder()
	detail := csvbase.Column(b, "Detail")
	groups := csvbase.SplitColumns(b, detail, re)

	payKey := csvbase.ResolvePayee(b, csvbase.PayeeConfig{
		Sources: []csvbase.Key[string]{groups["payee"]},
	})
	accKey := csvbase.ResolveAccount(b, csvbase.AccountConfig{
		Sources: []csvbase.Key[string]{groups["cat"]},
		Map:     map[string]string{"food": "Expenses:Food"},
	})

	var gotPayee, gotAcc string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		gotPayee, _ = csvbase.Value(c, payKey)
		gotAcc, _ = csvbase.Value(c, accKey)
		return nil, nil, nil
	})
	rec := rowCtx("Detail", "Amazon|food")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotPayee != "Amazon" {
		t.Errorf("payee = %q, want %q", gotPayee, "Amazon")
	}
	if gotAcc != "Expenses:Food" {
		t.Errorf("account = %q, want %q", gotAcc, "Expenses:Food")
	}
}

// ---------------------------------------------------------------------------
// Const
// ---------------------------------------------------------------------------

func TestConst(t *testing.T) {
	b := csvbase.NewBuilder()
	k := csvbase.Const(b, 42)
	var got int
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := csvbase.RowContext{Fields: []string{}, Index: map[string]int{}}
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got != 42 {
		t.Errorf("Const = %d, want 42", got)
	}
}

// ---------------------------------------------------------------------------
// ParseDate
// ---------------------------------------------------------------------------

func TestParseDate_OK(t *testing.T) {
	b := csvbase.NewBuilder()
	raw := csvbase.Column(b, "Date")
	k := csvbase.ParseDate(b, raw, "2006-01-02", "")
	var got time.Time
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, gotD = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("Date", "2024-03-15")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotD != nil {
		t.Fatalf("unexpected diag: %v", gotD)
	}
	want := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("date = %v, want %v", got, want)
	}
}

func TestParseDate_Bad_DefaultCode(t *testing.T) {
	b := csvbase.NewBuilder()
	raw := csvbase.Column(b, "Date")
	k := csvbase.ParseDate(b, raw, "2006-01-02", "")
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		_, gotD = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("Date", "not-a-date")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotD == nil {
		t.Fatal("expected diagnostic, got nil")
	}
	if gotD.Code != csvbase.DiagBadDate {
		t.Errorf("diag code = %q, want %q", gotD.Code, csvbase.DiagBadDate)
	}
	if gotD.Severity != ast.Error {
		t.Errorf("severity = %v, want Error", gotD.Severity)
	}
}

func TestParseDate_PropagatesSoftFail(t *testing.T) {
	// If the upstream Column step soft-fails, ParseDate propagates the diag.
	b := csvbase.NewBuilder()
	upstream := csvbase.AddStep(b, func(*csvbase.MappingState) (string, *ast.Diagnostic, error) {
		d := csvbase.ErrorDiag("upstream-err", "/f.csv", 1, "upstream")
		return "", &d, nil
	})
	k := csvbase.ParseDate(b, upstream, "2006-01-02", "")
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		_, gotD = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := csvbase.RowContext{Fields: []string{}, Index: map[string]int{}}
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotD == nil || gotD.Code != "upstream-err" {
		t.Errorf("propagated diag = %v, want upstream-err", gotD)
	}
}

// ---------------------------------------------------------------------------
// SumAmounts
// ---------------------------------------------------------------------------

func TestSumAmounts_OK(t *testing.T) {
	b := csvbase.NewBuilder()
	cols := csvbase.Columns(b, "Debit", "Credit")
	k := csvbase.SumAmounts(b, csvbase.AmountConfig{
		Cols: []csvbase.AmountInput{
			{Source: cols[0], Negate: true},
			{Source: cols[1]},
		},
	})
	var got csvkit.Amount
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, k)
		return nil, nil, nil
	})
	// Credit 100, Debit 30 => 100 - 30 = 70
	rec := rowCtx("Debit", "30", "Credit", "100")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "70")
	if got.Number.Cmp(want) != 0 {
		t.Errorf("amount = %v, want 70", got.Number)
	}
}

func TestSumAmounts_ThousandsSep(t *testing.T) {
	b := csvbase.NewBuilder()
	k := csvbase.SumAmounts(b, csvbase.AmountConfig{
		Cols:   []csvbase.AmountInput{{Source: csvbase.Column(b, "Amount")}},
		Format: csvkit.NumberFormat{ThousandsSep: ","},
	})
	var got csvkit.Amount
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("Amount", "1,234.56")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "1234.56")
	if got.Number.Cmp(want) != 0 {
		t.Errorf("amount = %v, want 1234.56", got.Number)
	}
}

func TestSumAmounts_Placeholders(t *testing.T) {
	b := csvbase.NewBuilder()
	k := csvbase.SumAmounts(b, csvbase.AmountConfig{
		Cols:   []csvbase.AmountInput{{Source: csvbase.Column(b, "Amount")}},
		Format: csvkit.NumberFormat{Placeholders: []string{"-"}},
	})
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		_, gotD = csvbase.Value(c, k)
		return nil, nil, nil
	})
	// "-" is a placeholder => AmountAllBlank
	rec := rowCtx("Amount", "-")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotD == nil || gotD.Code != csvbase.DiagAllBlankAmount {
		t.Errorf("diag = %v, want DiagAllBlankAmount", gotD)
	}
}

func TestSumAmounts_AmountBad(t *testing.T) {
	b := csvbase.NewBuilder()
	k := csvbase.SumAmounts(b, csvbase.AmountConfig{
		Cols: []csvbase.AmountInput{{Source: csvbase.Column(b, "Amt")}},
	})
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		_, gotD = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("Amt", "not-a-number")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotD == nil || gotD.Code != csvbase.DiagBadAmount {
		t.Errorf("diag = %v, want DiagBadAmount", gotD)
	}
}

func TestSumAmounts_AmountBad_MessageByPosition(t *testing.T) {
	// The bad-input diagnostic must identify the entry by position (#N), not by column name.
	b := csvbase.NewBuilder()
	cols := csvbase.Columns(b, "Good", "Bad")
	k := csvbase.SumAmounts(b, csvbase.AmountConfig{
		Cols: []csvbase.AmountInput{
			{Source: cols[0]},
			{Source: cols[1]},
		},
	})
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		_, gotD = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("Good", "10", "Bad", "not-a-number")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotD == nil || gotD.Code != csvbase.DiagBadAmount {
		t.Errorf("diag = %v, want DiagBadAmount", gotD)
	}
	if !strings.Contains(gotD.Message, "#1") {
		t.Errorf("diag message %q should contain %q (0-based index of bad input)", gotD.Message, "#1")
	}
}

func TestSumAmounts_AllBlank(t *testing.T) {
	b := csvbase.NewBuilder()
	k := csvbase.SumAmounts(b, csvbase.AmountConfig{
		Cols: []csvbase.AmountInput{{Source: csvbase.Column(b, "Amt")}},
	})
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		_, gotD = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("Amt", "")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotD == nil || gotD.Code != csvbase.DiagAllBlankAmount {
		t.Errorf("diag = %v, want DiagAllBlankAmount", gotD)
	}
}

func TestSumAmounts_SplitCurrencyHint(t *testing.T) {
	b := csvbase.NewBuilder()
	k := csvbase.SumAmounts(b, csvbase.AmountConfig{
		Cols:          []csvbase.AmountInput{{Source: csvbase.Column(b, "Amt")}},
		SplitCurrency: true,
	})
	var got csvkit.Amount
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("Amt", "1000 JPY")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got.CurrencyHint != "JPY" {
		t.Errorf("CurrencyHint = %q, want %q", got.CurrencyHint, "JPY")
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "1000")
	if got.Number.Cmp(want) != 0 {
		t.Errorf("amount = %v, want 1000", got.Number)
	}
}

// ---------------------------------------------------------------------------
// Split / Group
// ---------------------------------------------------------------------------

func TestSplit_Match(t *testing.T) {
	re := regexp.MustCompile(`(?P<payee>.+?) / (?P<memo>.+)`)
	b := csvbase.NewBuilder()
	raw := csvbase.Column(b, "Desc")
	sp := csvbase.Split(b, raw, re)
	kPayee := csvbase.Group(b, sp, "payee")
	kMemo := csvbase.Group(b, sp, "memo")

	var gotPayee, gotMemo string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		gotPayee, _ = csvbase.Value(c, kPayee)
		gotMemo, _ = csvbase.Value(c, kMemo)
		return nil, nil, nil
	})
	rec := rowCtx("Desc", "Amazon / Order #123")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotPayee != "Amazon" {
		t.Errorf("payee = %q, want %q", gotPayee, "Amazon")
	}
	if gotMemo != "Order #123" {
		t.Errorf("memo = %q, want %q", gotMemo, "Order #123")
	}
}

func TestSplit_NoMatch(t *testing.T) {
	re := regexp.MustCompile(`(?P<payee>.+?) / (?P<memo>.+)`)
	b := csvbase.NewBuilder()
	raw := csvbase.Column(b, "Desc")
	sp := csvbase.Split(b, raw, re)
	kPayee := csvbase.Group(b, sp, "payee")

	var got string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, kPayee)
		return nil, nil, nil
	})
	rec := rowCtx("Desc", "No separator here")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got != "" {
		t.Errorf("no-match group = %q, want %q", got, "")
	}
}

func TestGroup_Absent(t *testing.T) {
	re := regexp.MustCompile(`(?P<payee>.+)`)
	b := csvbase.NewBuilder()
	raw := csvbase.Column(b, "Desc")
	sp := csvbase.Split(b, raw, re)
	kMissing := csvbase.Group(b, sp, "nonexistent")

	var got string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, kMissing)
		return nil, nil, nil
	})
	rec := rowCtx("Desc", "something")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got != "" {
		t.Errorf("absent group = %q, want %q", got, "")
	}
}

// ---------------------------------------------------------------------------
// MapValue
// ---------------------------------------------------------------------------

func TestMapValue_StrictHit(t *testing.T) {
	m := map[string]string{"A": "Assets:A", "B": "Assets:B"}
	v, d := singleString(t, rowCtx("X", "A"), func(b *csvbase.Builder) csvbase.Key[string] {
		in := csvbase.Column(b, "X")
		return csvbase.MapValue(b, in, m, csvkit.Strict, "test-miss")
	})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "Assets:A" {
		t.Errorf("mapped = %q, want %q", v, "Assets:A")
	}
}

func TestMapValue_StrictMiss(t *testing.T) {
	m := map[string]string{"A": "Assets:A"}
	_, d := singleString(t, rowCtx("X", "Z"), func(b *csvbase.Builder) csvbase.Key[string] {
		in := csvbase.Column(b, "X")
		return csvbase.MapValue(b, in, m, csvkit.Strict, "test-miss")
	})
	if d == nil || d.Code != "test-miss" {
		t.Errorf("diag = %v, want test-miss", d)
	}
}

func TestMapValue_Verbatim(t *testing.T) {
	m := map[string]string{"A": "Assets:A"}
	v, d := singleString(t, rowCtx("X", "Z"), func(b *csvbase.Builder) csvbase.Key[string] {
		in := csvbase.Column(b, "X")
		return csvbase.MapValue(b, in, m, csvkit.Verbatim, "")
	})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "Z" {
		t.Errorf("verbatim passthrough = %q, want %q", v, "Z")
	}
}

// ---------------------------------------------------------------------------
// JoinKeys
// ---------------------------------------------------------------------------

func TestJoinKeys_TrimDropBlank(t *testing.T) {
	b := csvbase.NewBuilder()
	k1 := csvbase.Column(b, "A")
	k2 := csvbase.Column(b, "B")
	k3 := csvbase.Column(b, "C")
	kj := csvbase.JoinKeys(b, "-", k1, k2, k3)

	var got string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, kj)
		return nil, nil, nil
	})
	// B is blank, A and C have leading/trailing spaces
	rec := rowCtx("A", "  foo  ", "B", "", "C", "  bar  ")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got != "foo-bar" {
		t.Errorf("joined = %q, want %q", got, "foo-bar")
	}
}

func TestJoinKeys_SoftFailedTreatedBlank(t *testing.T) {
	b := csvbase.NewBuilder()
	failing := csvbase.AddStep(b, func(*csvbase.MappingState) (string, *ast.Diagnostic, error) {
		d := csvbase.ErrorDiag("fail", "", 0, "x")
		return "", &d, nil
	})
	ok := csvbase.Const(b, "kept")
	kj := csvbase.JoinKeys(b, "-", failing, ok)

	var got string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, kj)
		return nil, nil, nil
	})
	rec := csvbase.RowContext{Fields: []string{}, Index: map[string]int{}}
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got != "kept" {
		t.Errorf("joined with soft-fail blank = %q, want %q", got, "kept")
	}
}

// ---------------------------------------------------------------------------
// ResolveAccount
// ---------------------------------------------------------------------------

func TestResolveAccount_HintOverride(t *testing.T) {
	v, d := singleString(t,
		csvbase.RowContext{
			Fields: []string{"col-val"},
			Index:  map[string]int{"Cat": 0},
			Hints:  map[string]string{"account": "Assets:Override"},
		},
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolveAccount(b, csvbase.AccountConfig{
				Sources: csvbase.Columns(b, "Cat"),
				Map:     map[string]string{"col-val": "Expenses:Food"},
				HintKey: "account",
				Default: "Expenses:Default",
			})
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "Assets:Override" {
		t.Errorf("account = %q, want %q", v, "Assets:Override")
	}
}

func TestResolveAccount_ColMapHit(t *testing.T) {
	v, d := singleString(t, rowCtx("Cat", "food"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolveAccount(b, csvbase.AccountConfig{
				Sources: csvbase.Columns(b, "Cat"),
				Map:     map[string]string{"food": "Expenses:Food"},
			})
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "Expenses:Food" {
		t.Errorf("account = %q, want %q", v, "Expenses:Food")
	}
}

func TestResolveAccount_Unmapped(t *testing.T) {
	_, d := singleString(t, rowCtx("Cat", "unknown"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolveAccount(b, csvbase.AccountConfig{
				Sources: csvbase.Columns(b, "Cat"),
				Map:     map[string]string{"food": "Expenses:Food"},
			})
		},
	)
	if d == nil || d.Code != csvbase.DiagUnmappedAccount {
		t.Errorf("diag = %v, want DiagUnmappedAccount", d)
	}
	if d.Severity != ast.Error {
		t.Errorf("severity = %v, want Error", d.Severity)
	}
}

func TestResolveAccount_VerbatimNoMap(t *testing.T) {
	v, d := singleString(t, rowCtx("Cat", "Expenses:Custom"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolveAccount(b, csvbase.AccountConfig{
				Sources: csvbase.Columns(b, "Cat"),
				// no Map: verbatim passthrough
				Default: "Expenses:Default",
			})
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "Expenses:Custom" {
		t.Errorf("account = %q, want %q", v, "Expenses:Custom")
	}
}

func TestResolveAccount_DefaultFallback(t *testing.T) {
	v, d := singleString(t, rowCtx("Cat", ""), // blank col => fall to default
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolveAccount(b, csvbase.AccountConfig{
				Sources: csvbase.Columns(b, "Cat"),
				Default: "Expenses:Default",
			})
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "Expenses:Default" {
		t.Errorf("account = %q, want %q", v, "Expenses:Default")
	}
}

func TestResolveAccount_Missing(t *testing.T) {
	_, d := singleString(t, rowCtx("Cat", ""),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolveAccount(b, csvbase.AccountConfig{
				Sources: csvbase.Columns(b, "Cat"),
				// no Map, no Default
			})
		},
	)
	if d == nil || d.Code != csvbase.DiagMissingAccount {
		t.Errorf("diag = %v, want DiagMissingAccount", d)
	}
}

// ---------------------------------------------------------------------------
// ResolveCounter
// ---------------------------------------------------------------------------

func TestResolveCounter_MapHit(t *testing.T) {
	v, d := singleString(t, rowCtx("Type", "income"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolveCounter(b, csvbase.CounterConfig{
				Sources: csvbase.Columns(b, "Type"),
				Map:     map[string]string{"income": "Income:Salary"},
			})
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "Income:Salary" {
		t.Errorf("counter = %q, want %q", v, "Income:Salary")
	}
}

func TestResolveCounter_UnmappedWarning(t *testing.T) {
	v, d := singleString(t, rowCtx("Type", "mystery"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolveCounter(b, csvbase.CounterConfig{
				Sources: csvbase.Columns(b, "Type"),
				Map:     map[string]string{"income": "Income:Salary"},
			})
		},
	)
	if d == nil || d.Code != csvbase.DiagUnmappedCounterAccount {
		t.Errorf("diag = %v, want DiagUnmappedCounterAccount", d)
	}
	if d.Severity != ast.Warning {
		t.Errorf("severity = %v, want Warning", d.Severity)
	}
	if v != "" {
		t.Errorf("counter value on unmapped = %q, want %q", v, "")
	}
}

func TestResolveCounter_BlankYieldsEmpty(t *testing.T) {
	v, d := singleString(t, rowCtx("Type", ""),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolveCounter(b, csvbase.CounterConfig{
				Sources: csvbase.Columns(b, "Type"),
				Map:     map[string]string{"income": "Income:Salary"},
			})
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "" {
		t.Errorf("blank counter = %q, want %q", v, "")
	}
}

func TestResolveCounter_Default(t *testing.T) {
	v, d := singleString(t, rowCtx("Type", ""),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolveCounter(b, csvbase.CounterConfig{
				Sources: csvbase.Columns(b, "Type"),
				Default: "Expenses:Unknown",
			})
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "Expenses:Unknown" {
		t.Errorf("default counter = %q, want %q", v, "Expenses:Unknown")
	}
}

// ---------------------------------------------------------------------------
// ResolveCurrency
// ---------------------------------------------------------------------------

func TestResolveCurrency_ColPrecedence(t *testing.T) {
	b := csvbase.NewBuilder()
	amtKey := csvbase.SumAmounts(b, csvbase.AmountConfig{
		Cols:          []csvbase.AmountInput{{Source: csvbase.Column(b, "Amt")}},
		SplitCurrency: true,
	})
	k := csvbase.ResolveCurrency(b, csvbase.CurrencyConfig{
		Source:     csvbase.Column(b, "Cur"),
		Default:    "USD",
		FromAmount: true,
		Amount:     amtKey,
	})
	var got string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, k)
		return nil, nil, nil
	})
	// Source has explicit "EUR" and amount hint would be "JPY"; Source wins
	rec := rowCtx("Cur", "EUR", "Amt", "1000 JPY")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got != "EUR" {
		t.Errorf("currency = %q, want %q", got, "EUR")
	}
}

func TestResolveCurrency_AmountHint(t *testing.T) {
	b := csvbase.NewBuilder()
	amtKey := csvbase.SumAmounts(b, csvbase.AmountConfig{
		Cols:          []csvbase.AmountInput{{Source: csvbase.Column(b, "Amt")}},
		SplitCurrency: true,
	})
	k := csvbase.ResolveCurrency(b, csvbase.CurrencyConfig{
		FromAmount: true,
		Default:    "USD",
		Amount:     amtKey,
	})
	var got string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("Amt", "1000 JPY")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got != "JPY" {
		t.Errorf("currency from amount hint = %q, want %q", got, "JPY")
	}
}

func TestResolveCurrency_Default(t *testing.T) {
	v, d := singleString(t, rowCtx("Cur", ""),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolveCurrency(b, csvbase.CurrencyConfig{
				Source:  csvbase.Column(b, "Cur"),
				Default: "USD",
			})
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "USD" {
		t.Errorf("default currency = %q, want %q", v, "USD")
	}
}

func TestResolveCurrency_MapVerbatim(t *testing.T) {
	v, d := singleString(t, rowCtx("Cur", "yen"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolveCurrency(b, csvbase.CurrencyConfig{
				Source: csvbase.Column(b, "Cur"),
				Map:    map[string]string{"yen": "JPY"},
			})
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "JPY" {
		t.Errorf("mapped currency = %q, want %q", v, "JPY")
	}
}

func TestResolveCurrency_Empty_SoftFail(t *testing.T) {
	_, d := singleString(t, rowCtx("Cur", ""),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolveCurrency(b, csvbase.CurrencyConfig{
				Source: csvbase.Column(b, "Cur"),
				// no Default, no FromAmount
			})
		},
	)
	if d == nil || d.Code != csvbase.DiagMissingCurrency {
		t.Errorf("diag = %v, want DiagMissingCurrency", d)
	}
}

// ---------------------------------------------------------------------------
// ResolvePayee
// ---------------------------------------------------------------------------

func TestResolvePayee_JoinMap(t *testing.T) {
	v, d := singleString(t, rowCtx("Name", "amazon"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolvePayee(b, csvbase.PayeeConfig{
				Sources: csvbase.Columns(b, "Name"),
				Map:     map[string]string{"amazon": "Amazon"},
			})
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "Amazon" {
		t.Errorf("payee = %q, want %q", v, "Amazon")
	}
}

func TestResolvePayee_MappedEmptySuppresses(t *testing.T) {
	// A map entry of "" suppresses the payee.
	v, d := singleString(t, rowCtx("Name", "noise"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolvePayee(b, csvbase.PayeeConfig{
				Sources: csvbase.Columns(b, "Name"),
				Map:     map[string]string{"noise": ""},
			})
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "" {
		t.Errorf("suppressed payee = %q, want %q", v, "")
	}
}

func TestResolvePayee_BlankYieldsEmpty(t *testing.T) {
	v, d := singleString(t, rowCtx("Name", ""),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.ResolvePayee(b, csvbase.PayeeConfig{
				Sources: csvbase.Columns(b, "Name"),
			})
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "" {
		t.Errorf("blank payee = %q, want %q", v, "")
	}
}

// ---------------------------------------------------------------------------
// NarrationFromSources
// ---------------------------------------------------------------------------

func TestNarrationFromSources_PerCellMapDropBlank(t *testing.T) {
	v, d := singleString(t, rowCtx("A", "keep", "B", "noise", "C", "  also  "),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.NarrationFromSources(b, csvbase.Columns(b, "A", "B", "C"), " | ",
				map[string]string{"noise": ""}) // "noise" => "" => dropped
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "keep | also" {
		t.Errorf("narration = %q, want %q", v, "keep | also")
	}
}

// ---------------------------------------------------------------------------
// NarrationFromTemplate
// ---------------------------------------------------------------------------

func TestNarrationFromTemplate_OK(t *testing.T) {
	tmpl, err := csvkit.CompileNarration("{{.Payee}} — {{.Memo}}")
	if err != nil {
		t.Fatalf("CompileNarration: %v", err)
	}
	v, d := singleString(t, rowCtx("Payee", "Amazon", "Memo", "Book"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.NarrationFromTemplate(b, tmpl, nil, "")
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "Amazon — Book" {
		t.Errorf("narration = %q, want %q", v, "Amazon — Book")
	}
}

func TestNarrationFromTemplate_UnknownRef(t *testing.T) {
	tmpl, err := csvkit.CompileNarration("{{.NoSuchCol}}")
	if err != nil {
		t.Fatalf("CompileNarration: %v", err)
	}
	_, d := singleString(t, rowCtx("Payee", "X"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.NarrationFromTemplate(b, tmpl, nil, "")
		},
	)
	if d == nil || d.Code != csvbase.DiagBadNarrationTemplate {
		t.Errorf("diag = %v, want DiagBadNarrationTemplate", d)
	}
}

func TestNarrationFromTemplate_BindingOverlaysRawColumn(t *testing.T) {
	// A binding name shadows a same-named raw column.
	tmpl, err := csvkit.CompileNarration("{{.Desc}}")
	if err != nil {
		t.Fatalf("CompileNarration: %v", err)
	}
	b := csvbase.NewBuilder()
	// The raw column "Detail" holds "Amazon|Books order". The regex captures
	// everything before "|" as the group "Desc", yielding "Amazon". The binding
	// key "Desc" overlays the raw-column data map under that name, so the template
	// renders "Amazon" (from the split group) even though the only raw column is
	// "Detail", not "Desc".
	detail := csvbase.Column(b, "Detail")
	groups := csvbase.SplitColumns(b, detail, regexp.MustCompile(`(?P<Desc>.+)\|.+`))

	narrKey := csvbase.NarrationFromTemplate(b, tmpl,
		map[string]csvbase.Key[string]{"Desc": groups["Desc"]}, "")

	var got string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, narrKey)
		return nil, nil, nil
	})
	rec := rowCtx("Detail", "Amazon|Books order")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got != "Amazon" {
		t.Errorf("narration = %q, want %q (binding must shadow raw column)", got, "Amazon")
	}
}

func TestNarrationFromTemplate_BindingRendersOverlaidValue(t *testing.T) {
	// A binding key's value is rendered by the template; no raw column named
	// "vendor" exists in the row.
	tmpl, err := csvkit.CompileNarration("{{.vendor}}")
	if err != nil {
		t.Fatalf("CompileNarration: %v", err)
	}
	b := csvbase.NewBuilder()
	vendorKey := csvbase.Const(b, "Acme")

	narrKey := csvbase.NarrationFromTemplate(b, tmpl,
		map[string]csvbase.Key[string]{"vendor": vendorKey}, "")

	var got string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, narrKey)
		return nil, nil, nil
	})
	rec := csvbase.RowContext{Fields: []string{}, Index: map[string]int{}}
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got != "Acme" {
		t.Errorf("narration = %q, want %q", got, "Acme")
	}
}

func TestNarrationFromTemplate_BindingNoRequireHeader(t *testing.T) {
	// A template referencing only a binding name does NOT require that name as a
	// raw header column. Required() should only list "Detail".
	tmpl, err := csvkit.CompileNarration("{{.SyntheticKey}}")
	if err != nil {
		t.Fatalf("CompileNarration: %v", err)
	}
	b := csvbase.NewBuilder()
	detail := csvbase.Column(b, "Detail")
	groups := csvbase.SplitColumns(b, detail, regexp.MustCompile(`(?P<SyntheticKey>.+)`))

	csvbase.NarrationFromTemplate(b, tmpl,
		map[string]csvbase.Key[string]{"SyntheticKey": groups["SyntheticKey"]}, "")

	p := b.Emit(func(_ context.Context, _ *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		return nil, nil, nil
	})
	req := p.Required()
	if len(req) != 1 || req[0] != "Detail" {
		t.Errorf("Required() = %v, want [Detail] (SyntheticKey is not a raw column)", req)
	}
}

// ---------------------------------------------------------------------------
// ResolveCost
// ---------------------------------------------------------------------------

func TestResolveCost_BlankYieldsNil(t *testing.T) {
	b := csvbase.NewBuilder()
	k := csvbase.ResolveCost(b, csvbase.CostConfig{
		Number:          csvbase.Column(b, "CostNum"),
		DefaultCurrency: "USD",
	})
	var got *ast.CostSpec
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("CostNum", "")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got != nil {
		t.Errorf("blank cost = %v, want nil", got)
	}
}

func TestResolveCost_PerUnit(t *testing.T) {
	b := csvbase.NewBuilder()
	k := csvbase.ResolveCost(b, csvbase.CostConfig{
		Number:          csvbase.Column(b, "CostNum"),
		DefaultCurrency: "USD",
	})
	var got *ast.CostSpec
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("CostNum", "12.50")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got == nil {
		t.Fatal("expected CostSpec, got nil")
	}
	if got.PerUnit == nil {
		t.Fatal("PerUnit is nil")
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "12.50")
	if got.PerUnit.Cmp(want) != 0 {
		t.Errorf("PerUnit = %v, want 12.50", got.PerUnit)
	}
	if got.Currency != "USD" {
		t.Errorf("currency = %q, want %q", got.Currency, "USD")
	}
	if got.Total != nil {
		t.Errorf("Total should be nil for per-unit cost")
	}
}

func TestResolveCost_Total(t *testing.T) {
	b := csvbase.NewBuilder()
	k := csvbase.ResolveCost(b, csvbase.CostConfig{
		Number:          csvbase.Column(b, "CostNum"),
		IsTotal:         true,
		DefaultCurrency: "USD",
	})
	var got *ast.CostSpec
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("CostNum", "100")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got == nil || got.Total == nil {
		t.Fatal("expected CostSpec with Total set")
	}
}

func TestResolveCost_CurrencyFromCol(t *testing.T) {
	b := csvbase.NewBuilder()
	k := csvbase.ResolveCost(b, csvbase.CostConfig{
		Number:   csvbase.Column(b, "CostNum"),
		Currency: csvbase.Column(b, "CostCur"),
	})
	var got *ast.CostSpec
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("CostNum", "10", "CostCur", "EUR")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got == nil || got.Currency != "EUR" {
		t.Errorf("cost currency = %v, want EUR", got)
	}
}

func TestResolveCost_DateParsed(t *testing.T) {
	b := csvbase.NewBuilder()
	k := csvbase.ResolveCost(b, csvbase.CostConfig{
		Number:          csvbase.Column(b, "CostNum"),
		DefaultCurrency: "USD",
		Date:            csvbase.Column(b, "AcqDate"),
		DateFormat:      "2006-01-02",
	})
	var got *ast.CostSpec
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("CostNum", "10", "AcqDate", "2020-06-01")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got == nil || got.Date == nil {
		t.Fatal("expected Date in CostSpec")
	}
	want := time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC)
	if !got.Date.Equal(want) {
		t.Errorf("cost date = %v, want %v", got.Date, want)
	}
}

func TestResolveCost_BadNumber(t *testing.T) {
	b := csvbase.NewBuilder()
	k := csvbase.ResolveCost(b, csvbase.CostConfig{
		Number:          csvbase.Column(b, "CostNum"),
		DefaultCurrency: "USD",
	})
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		_, gotD = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("CostNum", "bad")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotD == nil || gotD.Code != csvbase.DiagBadCost {
		t.Errorf("diag = %v, want DiagBadCost", gotD)
	}
}

func TestResolveCost_NoCurrency_DiagBadCost(t *testing.T) {
	// Non-blank number but no Currency key and no DefaultCurrency => DiagBadCost.
	b := csvbase.NewBuilder()
	k := csvbase.ResolveCost(b, csvbase.CostConfig{
		Number: csvbase.Column(b, "CostNum"),
		// no Currency, no DefaultCurrency
	})
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		_, gotD = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("CostNum", "10")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotD == nil || gotD.Code != csvbase.DiagBadCost {
		t.Errorf("ResolveCost() diag = %v, want DiagBadCost", gotD)
	}
}

func TestResolveCost_BadDate_DiagBadCost(t *testing.T) {
	// Valid number and currency, but unparseable date cell => DiagBadCost.
	b := csvbase.NewBuilder()
	k := csvbase.ResolveCost(b, csvbase.CostConfig{
		Number:          csvbase.Column(b, "CostNum"),
		DefaultCurrency: "USD",
		Date:            csvbase.Column(b, "AcqDate"),
		DateFormat:      "2006-01-02",
	})
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		_, gotD = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("CostNum", "10", "AcqDate", "not-a-date")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotD == nil || gotD.Code != csvbase.DiagBadCost {
		t.Errorf("ResolveCost() diag = %v, want DiagBadCost", gotD)
	}
}

// ---------------------------------------------------------------------------
// SumAmounts — custom BadCode override
// ---------------------------------------------------------------------------

func TestSumAmounts_CustomBadCode(t *testing.T) {
	// BadCode override: a non-blank unparseable input should use the custom code.
	b := csvbase.NewBuilder()
	k := csvbase.SumAmounts(b, csvbase.AmountConfig{
		Cols:    []csvbase.AmountInput{{Source: csvbase.Column(b, "Amt")}},
		BadCode: "my-bad-amount",
	})
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		_, gotD = csvbase.Value(c, k)
		return nil, nil, nil
	})
	rec := rowCtx("Amt", "not-a-number")
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotD == nil || gotD.Code != "my-bad-amount" {
		t.Errorf("SumAmounts() diag code = %v, want %q", gotD, "my-bad-amount")
	}
}
