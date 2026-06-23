package csvbase_test

import (
	"context"
	"regexp"
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

func TestSplitColumns_FeedsDownstreamSteps(t *testing.T) {
	// Verifies that a SplitColumns group key feeds downstream primitive steps.
	re := regexp.MustCompile(`(?P<payee>[^|]+)\|(?P<cat>.+)`)
	b := csvbase.NewBuilder()
	detail := csvbase.Column(b, "Detail")
	groups := csvbase.SplitColumns(b, detail, re)

	payKey := csvbase.JoinKeys(b, "", groups["payee"])
	accKey := csvbase.MapValue(b, groups["cat"],
		map[string]string{"food": "Expenses:Food"},
		csvkit.Strict, csvbase.DiagUnmappedAccount)

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
// Row / Merge / Template
// ---------------------------------------------------------------------------

func TestRow_YieldsRawColumns(t *testing.T) {
	b := csvbase.NewBuilder()
	rowKey := csvbase.Row(b)

	var got map[string]string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, rowKey)
		return nil, nil, nil
	})
	if _, _, err := p.Map(context.Background(), rowCtx("Payee", "Amazon", "Memo", "Book")); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got["Payee"] != "Amazon" || got["Memo"] != "Book" {
		t.Errorf("Row() = %v, want Payee=Amazon Memo=Book", got)
	}
}

func TestRow_RequiresNoColumns(t *testing.T) {
	// Row reads whatever columns exist but registers none as required.
	b := csvbase.NewBuilder()
	csvbase.Row(b)
	p := b.Emit(func(_ context.Context, _ *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		return nil, nil, nil
	})
	if req := p.Required(); len(req) != 0 {
		t.Errorf("Required() = %v, want [] (Row registers no header columns)", req)
	}
}

func TestTemplate_OK(t *testing.T) {
	tmpl, err := csvkit.CompileTemplate("{{.Payee}} — {{.Memo}}")
	if err != nil {
		t.Fatalf("CompileTemplate: %v", err)
	}
	v, d := singleString(t, rowCtx("Payee", "Amazon", "Memo", "Book"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.Template(b, tmpl, csvbase.Row(b))
		},
	)
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "Amazon — Book" {
		t.Errorf("rendered = %q, want %q", v, "Amazon — Book")
	}
}

func TestTemplate_UnknownRef(t *testing.T) {
	tmpl, err := csvkit.CompileTemplate("{{.NoSuchCol}}")
	if err != nil {
		t.Fatalf("CompileTemplate: %v", err)
	}
	_, d := singleString(t, rowCtx("Payee", "X"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.Template(b, tmpl, csvbase.Row(b))
		},
	)
	if d == nil || d.Code != csvbase.DiagBadTemplate {
		t.Errorf("diag = %v, want DiagBadTemplate", d)
	}
}

func TestMerge_OverlaysRawColumn(t *testing.T) {
	// An overlaid name shadows a same-named raw column.
	tmpl, err := csvkit.CompileTemplate("{{.Desc}}")
	if err != nil {
		t.Fatalf("CompileTemplate: %v", err)
	}
	b := csvbase.NewBuilder()
	// The raw column "Detail" holds "Amazon|Books order". The regex captures
	// everything before "|" as the group "Desc", yielding "Amazon". Merge overlays
	// the split-group key under the name "Desc", so the template renders "Amazon"
	// even though the only raw column is "Detail", not "Desc".
	detail := csvbase.Column(b, "Detail")
	groups := csvbase.SplitColumns(b, detail, regexp.MustCompile(`(?P<Desc>.+)\|.+`))

	narrKey := csvbase.Template(b, tmpl,
		csvbase.Merge(b, csvbase.Row(b),
			map[string]csvbase.Key[string]{"Desc": groups["Desc"]}))

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
		t.Errorf("rendered = %q, want %q (overlay must shadow raw column)", got, "Amazon")
	}
}

func TestMerge_RendersOverlaidValue(t *testing.T) {
	// An overlaid key's value is rendered by the template; no raw column named
	// "vendor" exists in the row.
	tmpl, err := csvkit.CompileTemplate("{{.vendor}}")
	if err != nil {
		t.Fatalf("CompileTemplate: %v", err)
	}
	b := csvbase.NewBuilder()
	vendorKey := csvbase.Const(b, "Acme")

	narrKey := csvbase.Template(b, tmpl,
		csvbase.Merge(b, csvbase.Row(b),
			map[string]csvbase.Key[string]{"vendor": vendorKey}))

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
		t.Errorf("rendered = %q, want %q", got, "Acme")
	}
}

func TestMerge_SoftFailedBindingKeepsBase(t *testing.T) {
	// A soft-failed overlay is skipped, leaving the base map's entry intact.
	b := csvbase.NewBuilder()
	base := csvbase.Const(b, map[string]string{"k": "base"})
	failed := csvbase.Require(b, csvbase.Const(b, ""), csvbase.DiagMissingColumn)
	merged := csvbase.Merge(b, base, map[string]csvbase.Key[string]{"k": failed})

	var got map[string]string
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		got, _ = csvbase.Value(c, merged)
		return nil, nil, nil
	})
	rec := csvbase.RowContext{Fields: []string{}, Index: map[string]int{}}
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if got["k"] != "base" {
		t.Errorf("merged[k] = %q, want %q (soft-failed overlay must not overwrite base)", got["k"], "base")
	}
}

func TestMerge_NoRequireHeader(t *testing.T) {
	// A template referencing only an overlaid name does NOT require that name as
	// a raw header column. Required() should only list "Detail".
	tmpl, err := csvkit.CompileTemplate("{{.SyntheticKey}}")
	if err != nil {
		t.Fatalf("CompileTemplate: %v", err)
	}
	b := csvbase.NewBuilder()
	detail := csvbase.Column(b, "Detail")
	groups := csvbase.SplitColumns(b, detail, regexp.MustCompile(`(?P<SyntheticKey>.+)`))

	csvbase.Template(b, tmpl,
		csvbase.Merge(b, csvbase.Row(b),
			map[string]csvbase.Key[string]{"SyntheticKey": groups["SyntheticKey"]}))

	p := b.Emit(func(_ context.Context, _ *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		return nil, nil, nil
	})
	req := p.Required()
	if len(req) != 1 || req[0] != "Detail" {
		t.Errorf("Required() = %v, want [Detail] (SyntheticKey is not a raw column)", req)
	}
}

// ---------------------------------------------------------------------------
// Hint
// ---------------------------------------------------------------------------

func TestHint_ReturnsHintValue(t *testing.T) {
	v, d := singleString(t,
		csvbase.RowContext{Fields: []string{}, Index: map[string]int{},
			Hints: map[string]string{"account": "Assets:Checking"}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.Hint(b, "account")
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "Assets:Checking" {
		t.Errorf("Hint = %q, want %q", v, "Assets:Checking")
	}
}

func TestHint_AbsentYieldsEmpty(t *testing.T) {
	v, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.Hint(b, "missing")
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "" {
		t.Errorf("absent Hint = %q, want %q", v, "")
	}
}

func TestHint_EmptyStringYieldsEmpty(t *testing.T) {
	v, d := singleString(t,
		csvbase.RowContext{Fields: []string{}, Index: map[string]int{},
			Hints: map[string]string{"account": ""}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.Hint(b, "account")
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "" {
		t.Errorf("empty Hint = %q, want %q", v, "")
	}
}

// ---------------------------------------------------------------------------
// Coalesce
// ---------------------------------------------------------------------------

func TestCoalesce_FirstNonBlank(t *testing.T) {
	v, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			k1 := csvbase.Const(b, "")
			k2 := csvbase.Const(b, "  second  ")
			k3 := csvbase.Const(b, "third")
			return csvbase.Coalesce(b, k1, k2, k3)
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "second" {
		t.Errorf("Coalesce = %q, want %q", v, "second")
	}
}

func TestCoalesce_AllBlankYieldsEmpty(t *testing.T) {
	v, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			k1 := csvbase.Const(b, "")
			k2 := csvbase.Const(b, "   ")
			return csvbase.Coalesce(b, k1, k2)
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "" {
		t.Errorf("all-blank Coalesce = %q, want %q", v, "")
	}
}

func TestCoalesce_SoftFailedInputSkipped(t *testing.T) {
	// A soft-failed input is skipped (diagnostic NOT propagated); next non-blank wins.
	v, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			failing := csvbase.AddStep(b, func(*csvbase.MappingState) (string, *ast.Diagnostic, error) {
				d := csvbase.ErrorDiag("fail", "", 0, "x")
				return "", &d, nil
			})
			ok := csvbase.Const(b, "fallback")
			return csvbase.Coalesce(b, failing, ok)
		})
	if d != nil {
		t.Fatalf("soft-fail diag propagated by Coalesce, got: %v", d)
	}
	if v != "fallback" {
		t.Errorf("Coalesce past soft-fail = %q, want %q", v, "fallback")
	}
}

// ---------------------------------------------------------------------------
// Require
// ---------------------------------------------------------------------------

func TestRequire_NonBlankPassesTrimmed(t *testing.T) {
	v, d := singleString(t, rowCtx("X", "  hello  "), func(b *csvbase.Builder) csvbase.Key[string] {
		return csvbase.Require(b, csvbase.Column(b, "X"), "missing-x")
	})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "hello" {
		t.Errorf("Require trimmed = %q, want %q", v, "hello")
	}
}

func TestRequire_BlankSoftFails(t *testing.T) {
	_, d := singleString(t, rowCtx("X", ""), func(b *csvbase.Builder) csvbase.Key[string] {
		return csvbase.Require(b, csvbase.Column(b, "X"), "missing-x")
	})
	if d == nil || d.Code != "missing-x" {
		t.Errorf("blank Require diag = %v, want missing-x", d)
	}
}

func TestRequire_UpstreamSoftFailPropagates(t *testing.T) {
	_, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			upstream := csvbase.AddStep(b, func(*csvbase.MappingState) (string, *ast.Diagnostic, error) {
				d := csvbase.ErrorDiag("upstream-code", "", 0, "x")
				return "", &d, nil
			})
			return csvbase.Require(b, upstream, "require-code")
		})
	if d == nil || d.Code != "upstream-code" {
		t.Errorf("upstream soft-fail diag = %v, want upstream-code", d)
	}
}

// ---------------------------------------------------------------------------
// CurrencyHint
// ---------------------------------------------------------------------------

func singlePtrAmount(t *testing.T, rec csvbase.RowContext, build func(*csvbase.Builder) csvbase.Key[*csvkit.Amount]) (*csvkit.Amount, *ast.Diagnostic) {
	t.Helper()
	b := csvbase.NewBuilder()
	k := build(b)
	var gotV *csvkit.Amount
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

func TestCurrencyHint_WithHint(t *testing.T) {
	v, d := singleString(t, rowCtx("Amt", "1000 JPY"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			amtKey := csvbase.ParseAmount(b, csvbase.Column(b, "Amt"),
				csvbase.ParseAmountConfig{SplitCurrency: true})
			return csvbase.CurrencyHint(b, amtKey)
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "JPY" {
		t.Errorf("CurrencyHint = %q, want %q", v, "JPY")
	}
}

func TestCurrencyHint_NilAmtYieldsEmpty(t *testing.T) {
	v, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			nilAmt := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return nil, nil, nil
			})
			return csvbase.CurrencyHint(b, nilAmt)
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "" {
		t.Errorf("CurrencyHint(nil) = %q, want %q", v, "")
	}
}

func TestCurrencyHint_SoftFailedAmtYieldsEmpty(t *testing.T) {
	v, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			failAmt := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				d := csvbase.ErrorDiag("amt-fail", "", 0, "x")
				return nil, &d, nil
			})
			return csvbase.CurrencyHint(b, failAmt)
		})
	// CurrencyHint swallows the soft-fail: returns "" with no diag.
	if d != nil {
		t.Errorf("CurrencyHint(soft-fail) propagated diag: %v", d)
	}
	if v != "" {
		t.Errorf("CurrencyHint(soft-fail) = %q, want %q", v, "")
	}
}

// ---------------------------------------------------------------------------
// MapEach
// ---------------------------------------------------------------------------

func TestMapEach_ParallelMap(t *testing.T) {
	v, d := singleString(t, rowCtx("A", "x", "B", "y"),
		func(b *csvbase.Builder) csvbase.Key[string] {
			ins := csvbase.Columns(b, "A", "B")
			m := map[string]string{"x": "X", "y": "Y"}
			outs := csvbase.MapEach(b, ins, m, csvkit.Verbatim, "miss")
			return csvbase.JoinKeys(b, ",", outs...)
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "X,Y" {
		t.Errorf("MapEach joined = %q, want %q", v, "X,Y")
	}
}

func TestMapEach_StrictMissFailsEntry(t *testing.T) {
	b := csvbase.NewBuilder()
	ins := csvbase.Columns(b, "A")
	m := map[string]string{"x": "X"}
	outs := csvbase.MapEach(b, ins, m, csvkit.Strict, "miss-code")
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		_, gotD = csvbase.Value(c, outs[0])
		return nil, nil, nil
	})
	if _, _, err := p.Map(context.Background(), rowCtx("A", "unknown")); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotD == nil || gotD.Code != "miss-code" {
		t.Errorf("MapEach strict miss diag = %v, want miss-code", gotD)
	}
}

func TestMapEach_SoftFailedEntryPropagates(t *testing.T) {
	b := csvbase.NewBuilder()
	failKey := csvbase.AddStep(b, func(*csvbase.MappingState) (string, *ast.Diagnostic, error) {
		d := csvbase.ErrorDiag("entry-fail", "", 0, "x")
		return "", &d, nil
	})
	m := map[string]string{"x": "X"}
	outs := csvbase.MapEach(b, []csvbase.Key[string]{failKey}, m, csvkit.Verbatim, "")
	var gotD *ast.Diagnostic
	p := b.Emit(func(_ context.Context, c *csvbase.MappingState) ([]ast.Directive, []ast.Diagnostic, error) {
		_, gotD = csvbase.Value(c, outs[0])
		return nil, nil, nil
	})
	rec := csvbase.RowContext{Fields: []string{}, Index: map[string]int{}}
	if _, _, err := p.Map(context.Background(), rec); err != nil {
		t.Fatalf("Map: %v", err)
	}
	if gotD == nil || gotD.Code != "entry-fail" {
		t.Errorf("MapEach propagated diag = %v, want entry-fail", gotD)
	}
}

// ---------------------------------------------------------------------------
// DiagAsWarning
// ---------------------------------------------------------------------------

func TestDiagAsWarning_ErrorToWarning(t *testing.T) {
	_, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			failKey := csvbase.AddStep(b, func(*csvbase.MappingState) (string, *ast.Diagnostic, error) {
				d := csvbase.ErrorDiag("orig-code", "", 0, "x")
				return "", &d, nil
			})
			return csvbase.DiagAsWarning(b, failKey, "new-code")
		})
	if d == nil {
		t.Fatal("expected diag, got nil")
	}
	if d.Severity != ast.Warning {
		t.Errorf("DiagAsWarning severity = %v, want Warning", d.Severity)
	}
	if d.Code != "new-code" {
		t.Errorf("DiagAsWarning code = %q, want %q", d.Code, "new-code")
	}
}

func TestDiagAsWarning_WarningInputAlsoRewrites(t *testing.T) {
	_, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			warnKey := csvbase.AddStep(b, func(*csvbase.MappingState) (string, *ast.Diagnostic, error) {
				diag := ast.Diagnostic{Severity: ast.Warning, Code: "old", Message: "w"}
				return "", &diag, nil
			})
			return csvbase.DiagAsWarning(b, warnKey, "new")
		})
	if d == nil {
		t.Fatal("expected diag, got nil")
	}
	if d.Severity != ast.Warning {
		t.Errorf("severity = %v, want Warning", d.Severity)
	}
	if d.Code != "new" {
		t.Errorf("code = %q, want %q", d.Code, "new")
	}
}

func TestDiagAsWarning_SuccessPassesThrough(t *testing.T) {
	v, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			ok := csvbase.Const(b, "value")
			return csvbase.DiagAsWarning(b, ok, "unused-code")
		})
	if d != nil {
		t.Fatalf("DiagAsWarning(success) produced diag: %v", d)
	}
	if v != "value" {
		t.Errorf("DiagAsWarning(success) = %q, want %q", v, "value")
	}
}

// ---------------------------------------------------------------------------
// ParseAmount
// ---------------------------------------------------------------------------

func TestParseAmount_BlankYieldsNil(t *testing.T) {
	v, d := singlePtrAmount(t, rowCtx("Amt", ""), func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
		return csvbase.ParseAmount(b, csvbase.Column(b, "Amt"), csvbase.ParseAmountConfig{})
	})
	if d != nil {
		t.Fatalf("blank ParseAmount diag = %v, want nil", d)
	}
	if v != nil {
		t.Errorf("blank ParseAmount = %v, want nil", v)
	}
}

func TestParseAmount_PlaceholderYieldsNil(t *testing.T) {
	v, d := singlePtrAmount(t, rowCtx("Amt", "-"), func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
		return csvbase.ParseAmount(b, csvbase.Column(b, "Amt"),
			csvbase.ParseAmountConfig{Format: csvkit.NumberFormat{Placeholders: []string{"-"}}})
	})
	if d != nil {
		t.Fatalf("placeholder ParseAmount diag = %v, want nil", d)
	}
	if v != nil {
		t.Errorf("placeholder ParseAmount = %v, want nil", v)
	}
}

func TestParseAmount_ParseableYieldsAmount(t *testing.T) {
	v, d := singlePtrAmount(t, rowCtx("Amt", "42.50"), func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
		return csvbase.ParseAmount(b, csvbase.Column(b, "Amt"), csvbase.ParseAmountConfig{})
	})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v == nil {
		t.Fatal("ParseAmount returned nil, want *Amount")
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "42.50")
	if v.Number.Cmp(want) != 0 {
		t.Errorf("ParseAmount number = %v, want 42.50", v.Number)
	}
}

func TestParseAmount_BadValueSoftFails(t *testing.T) {
	_, d := singlePtrAmount(t, rowCtx("Amt", "bad"), func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
		return csvbase.ParseAmount(b, csvbase.Column(b, "Amt"), csvbase.ParseAmountConfig{})
	})
	if d == nil || d.Code != csvbase.DiagBadAmount {
		t.Errorf("bad ParseAmount diag = %v, want DiagBadAmount", d)
	}
}

func TestParseAmount_SplitCurrencyPopulatesHint(t *testing.T) {
	v, d := singlePtrAmount(t, rowCtx("Amt", "1000 JPY"), func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
		return csvbase.ParseAmount(b, csvbase.Column(b, "Amt"),
			csvbase.ParseAmountConfig{SplitCurrency: true})
	})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v == nil {
		t.Fatal("ParseAmount returned nil")
	}
	if v.CurrencyHint != "JPY" {
		t.Errorf("CurrencyHint = %q, want JPY", v.CurrencyHint)
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "1000")
	if v.Number.Cmp(want) != 0 {
		t.Errorf("ParseAmount number = %v, want 1000", v.Number)
	}
}

func TestParseAmount_SoftFailedSrcPropagates(t *testing.T) {
	v, d := singlePtrAmount(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			failSrc := csvbase.AddStep(b, func(*csvbase.MappingState) (string, *ast.Diagnostic, error) {
				d := csvbase.ErrorDiag("src-fail", "", 0, "x")
				return "", &d, nil
			})
			return csvbase.ParseAmount(b, failSrc, csvbase.ParseAmountConfig{})
		})
	if v != nil {
		t.Errorf("soft-fail src ParseAmount = %v, want nil", v)
	}
	if d == nil || d.Code != "src-fail" {
		t.Errorf("propagated diag = %v, want src-fail", d)
	}
}

// ---------------------------------------------------------------------------
// NegateAmount
// ---------------------------------------------------------------------------

func TestNegateAmount_NegatesValue(t *testing.T) {
	v, d := singlePtrAmount(t, rowCtx("Amt", "50"),
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			src := csvbase.ParseAmount(b, csvbase.Column(b, "Amt"), csvbase.ParseAmountConfig{})
			return csvbase.NegateAmount(b, src)
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v == nil {
		t.Fatal("NegateAmount returned nil")
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "-50")
	if v.Number.Cmp(want) != 0 {
		t.Errorf("NegateAmount = %v, want -50", v.Number)
	}
}

func TestNegateAmount_PreservesHint(t *testing.T) {
	n, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "100")
	v, d := singlePtrAmount(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			src := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return &csvkit.Amount{Number: *n, CurrencyHint: "JPY"}, nil, nil
			})
			return csvbase.NegateAmount(b, src)
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v == nil {
		t.Fatal("NegateAmount returned nil")
	}
	if v.CurrencyHint != "JPY" {
		t.Errorf("NegateAmount CurrencyHint = %q, want JPY", v.CurrencyHint)
	}
}

func TestNegateAmount_NilYieldsNil(t *testing.T) {
	v, d := singlePtrAmount(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			nilKey := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return nil, nil, nil
			})
			return csvbase.NegateAmount(b, nilKey)
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != nil {
		t.Errorf("NegateAmount(nil) = %v, want nil", v)
	}
}

func TestNegateAmount_SoftFailPropagates(t *testing.T) {
	_, d := singlePtrAmount(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			failKey := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				d := csvbase.ErrorDiag("neg-fail", "", 0, "x")
				return nil, &d, nil
			})
			return csvbase.NegateAmount(b, failKey)
		})
	if d == nil || d.Code != "neg-fail" {
		t.Errorf("NegateAmount soft-fail propagated = %v, want neg-fail", d)
	}
}

// ---------------------------------------------------------------------------
// AddAmounts
// ---------------------------------------------------------------------------

func TestAddAmounts_IdentityNilNil(t *testing.T) {
	v, d := singlePtrAmount(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			nilA := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return nil, nil, nil
			})
			nilB := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return nil, nil, nil
			})
			return csvbase.AddAmounts(b, nilA, nilB, "")
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != nil {
		t.Errorf("AddAmounts(nil,nil) = %v, want nil", v)
	}
}

func TestAddAmounts_NilPlusV(t *testing.T) {
	v, d := singlePtrAmount(t, rowCtx("Amt", "30"),
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			nilKey := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return nil, nil, nil
			})
			parsed := csvbase.ParseAmount(b, csvbase.Column(b, "Amt"), csvbase.ParseAmountConfig{})
			return csvbase.AddAmounts(b, nilKey, parsed, "")
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v == nil {
		t.Fatal("AddAmounts(nil,v) = nil, want v")
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "30")
	if v.Number.Cmp(want) != 0 {
		t.Errorf("AddAmounts(nil,v) = %v, want 30", v.Number)
	}
}

func TestAddAmounts_VPlusNil(t *testing.T) {
	v, d := singlePtrAmount(t, rowCtx("Amt", "30"),
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			parsed := csvbase.ParseAmount(b, csvbase.Column(b, "Amt"), csvbase.ParseAmountConfig{})
			nilKey := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return nil, nil, nil
			})
			return csvbase.AddAmounts(b, parsed, nilKey, "")
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v == nil {
		t.Fatal("AddAmounts(v,nil) = nil, want v")
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "30")
	if v.Number.Cmp(want) != 0 {
		t.Errorf("AddAmounts(v,nil) = %v, want 30", v.Number)
	}
}

func TestAddAmounts_Sum(t *testing.T) {
	v, d := singlePtrAmount(t, rowCtx("Credit", "100", "Debit", "30"),
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			credit := csvbase.ParseAmount(b, csvbase.Column(b, "Credit"), csvbase.ParseAmountConfig{})
			debit := csvbase.ParseAmount(b, csvbase.Column(b, "Debit"), csvbase.ParseAmountConfig{})
			return csvbase.AddAmounts(b, credit, csvbase.NegateAmount(b, debit), "")
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v == nil {
		t.Fatal("AddAmounts returned nil")
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "70")
	if v.Number.Cmp(want) != 0 {
		t.Errorf("AddAmounts(100,-30) = %v, want 70", v.Number)
	}
}

func TestAddAmounts_ConflictingHintSoftFails(t *testing.T) {
	n, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "1")
	_, d := singlePtrAmount(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			aKey := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return &csvkit.Amount{Number: *n, CurrencyHint: "JPY"}, nil, nil
			})
			cKey := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return &csvkit.Amount{Number: *n, CurrencyHint: "EUR"}, nil, nil
			})
			return csvbase.AddAmounts(b, aKey, cKey, "conflict-code")
		})
	if d == nil || d.Code != "conflict-code" {
		t.Errorf("conflicting hints diag = %v, want conflict-code", d)
	}
}

// ---------------------------------------------------------------------------
// SubAmounts / AbsAmount
// ---------------------------------------------------------------------------

func TestSubAmounts_Difference(t *testing.T) {
	v, d := singlePtrAmount(t, rowCtx("A", "100", "B", "30"),
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			a := csvbase.ParseAmount(b, csvbase.Column(b, "A"), csvbase.ParseAmountConfig{})
			bb := csvbase.ParseAmount(b, csvbase.Column(b, "B"), csvbase.ParseAmountConfig{})
			return csvbase.SubAmounts(b, a, bb, "")
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "70")
	if v == nil || v.Number.Cmp(want) != 0 {
		t.Errorf("SubAmounts(100,30) = %v, want 70", v)
	}
}

func TestSubAmounts_NilLhsNegatesRhs(t *testing.T) {
	v, d := singlePtrAmount(t, rowCtx("B", "30"),
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			nilKey := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return nil, nil, nil
			})
			rhs := csvbase.ParseAmount(b, csvbase.Column(b, "B"), csvbase.ParseAmountConfig{})
			return csvbase.SubAmounts(b, nilKey, rhs, "")
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "-30")
	if v == nil || v.Number.Cmp(want) != 0 {
		t.Errorf("SubAmounts(nil,30) = %v, want -30", v)
	}
}

func TestSubAmounts_NilRhsYieldsLhs(t *testing.T) {
	v, d := singlePtrAmount(t, rowCtx("A", "30"),
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			lhs := csvbase.ParseAmount(b, csvbase.Column(b, "A"), csvbase.ParseAmountConfig{})
			nilKey := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return nil, nil, nil
			})
			return csvbase.SubAmounts(b, lhs, nilKey, "")
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "30")
	if v == nil || v.Number.Cmp(want) != 0 {
		t.Errorf("SubAmounts(30,nil) = %v, want 30", v)
	}
}

func TestSubAmounts_ConflictingHintSoftFails(t *testing.T) {
	n, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "1")
	_, d := singlePtrAmount(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			aKey := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return &csvkit.Amount{Number: *n, CurrencyHint: "JPY"}, nil, nil
			})
			cKey := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return &csvkit.Amount{Number: *n, CurrencyHint: "EUR"}, nil, nil
			})
			return csvbase.SubAmounts(b, aKey, cKey, "sub-conflict")
		})
	if d == nil || d.Code != "sub-conflict" {
		t.Errorf("conflicting hints diag = %v, want sub-conflict", d)
	}
}

func TestSubAmounts_BothNilYieldsNil(t *testing.T) {
	v, d := singlePtrAmount(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			nilA := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return nil, nil, nil
			})
			nilB := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return nil, nil, nil
			})
			return csvbase.SubAmounts(b, nilA, nilB, "")
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != nil {
		t.Errorf("SubAmounts(nil,nil) = %v, want nil", v)
	}
}

func TestAbsAmount(t *testing.T) {
	v, d := singlePtrAmount(t, rowCtx("A", "-42"),
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			return csvbase.AbsAmount(b, csvbase.ParseAmount(b, csvbase.Column(b, "A"), csvbase.ParseAmountConfig{}))
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	want, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "42")
	if v == nil || v.Number.Cmp(want) != 0 {
		t.Errorf("AbsAmount(-42) = %v, want 42", v)
	}
}

func TestAbsAmount_PreservesHint(t *testing.T) {
	n, _, _ := apd.BaseContext.SetString(new(apd.Decimal), "-7")
	v, d := singlePtrAmount(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			in := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return &csvkit.Amount{Number: *n, CurrencyHint: "JPY"}, nil, nil
			})
			return csvbase.AbsAmount(b, in)
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v == nil || v.CurrencyHint != "JPY" {
		t.Errorf("AbsAmount hint = %v, want JPY", v)
	}
}

func TestAbsAmount_NilYieldsNil(t *testing.T) {
	v, d := singlePtrAmount(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[*csvkit.Amount] {
			nilKey := csvbase.AddStep(b, func(*csvbase.MappingState) (*csvkit.Amount, *ast.Diagnostic, error) {
				return nil, nil, nil
			})
			return csvbase.AbsAmount(b, nilKey)
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != nil {
		t.Errorf("AbsAmount(nil) = %v, want nil", v)
	}
}

// ---------------------------------------------------------------------------
// Trim
// ---------------------------------------------------------------------------

func TestTrim_NonBlankTrimmed(t *testing.T) {
	v, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.Trim(b, csvbase.Const(b, "  hello  "))
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "hello" {
		t.Errorf("Trim = %q, want %q", v, "hello")
	}
}

func TestTrim_SoftFailPropagates(t *testing.T) {
	_, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			failing := csvbase.Require(b, csvbase.Const(b, ""), "trim-upstream-fail")
			return csvbase.Trim(b, failing)
		})
	if d == nil || d.Code != "trim-upstream-fail" {
		t.Errorf("Trim soft-fail propagated diag = %v, want trim-upstream-fail", d)
	}
}

// ---------------------------------------------------------------------------
// Else
// ---------------------------------------------------------------------------

func TestElse_PrimaryNonBlank(t *testing.T) {
	// Primary is non-blank: returns primary's trimmed value.
	v, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.Else(b, csvbase.Const(b, "  primary  "), csvbase.Const(b, "fallback"))
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "primary" {
		t.Errorf("Else(non-blank primary) = %q, want %q", v, "primary")
	}
}

func TestElse_PrimaryBlankFallbackNonBlank(t *testing.T) {
	// Primary is blank, fallback is non-blank: returns fallback's trimmed value.
	v, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.Else(b, csvbase.Const(b, ""), csvbase.Const(b, "  fallback  "))
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "fallback" {
		t.Errorf("Else(blank primary, non-blank fallback) = %q, want %q", v, "fallback")
	}
}

func TestElse_PrimarySoftFailPropagated(t *testing.T) {
	// Primary soft-fails: propagates primary's diagnostic without consulting fallback.
	_, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			failing := csvbase.Require(b, csvbase.Const(b, ""), "primary-fail")
			return csvbase.Else(b, failing, csvbase.Const(b, "fallback-value"))
		})
	if d == nil || d.Code != "primary-fail" {
		t.Errorf("Else(primary soft-fail) diag = %v, want primary-fail", d)
	}
}

func TestElse_PrimaryBlankFallbackSoftFail(t *testing.T) {
	// Primary is blank, fallback soft-fails: propagates fallback's diagnostic.
	_, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			failingFallback := csvbase.Require(b, csvbase.Const(b, ""), "fallback-fail")
			return csvbase.Else(b, csvbase.Const(b, ""), failingFallback)
		})
	if d == nil || d.Code != "fallback-fail" {
		t.Errorf("Else(blank primary, fallback soft-fail) diag = %v, want fallback-fail", d)
	}
}

func TestElse_BothBlankYieldsEmpty(t *testing.T) {
	// Primary is blank, fallback is blank: returns "".
	v, d := singleString(t, csvbase.RowContext{Fields: []string{}, Index: map[string]int{}},
		func(b *csvbase.Builder) csvbase.Key[string] {
			return csvbase.Else(b, csvbase.Const(b, ""), csvbase.Const(b, "   "))
		})
	if d != nil {
		t.Fatalf("unexpected diag: %v", d)
	}
	if v != "" {
		t.Errorf("Else(both blank) = %q, want %q", v, "")
	}
}

// ---------------------------------------------------------------------------
// MapValue refinement (blank input short-circuit)
// ---------------------------------------------------------------------------

func TestMapValue_StrictBlankYieldsEmpty(t *testing.T) {
	// Blank input yields "" with no diagnostic (new short-circuit, not a miss).
	v, d := singleString(t, rowCtx("X", ""), func(b *csvbase.Builder) csvbase.Key[string] {
		in := csvbase.Column(b, "X")
		return csvbase.MapValue(b, in, map[string]string{"": "should-not-match"}, csvkit.Strict, "test-miss")
	})
	if d != nil {
		t.Errorf("MapValue(Strict, blank) diag = %v, want nil", d)
	}
	if v != "" {
		t.Errorf("MapValue(Strict, blank) = %q, want %q", v, "")
	}
}

func TestMapValue_VerbatimBlankYieldsEmpty(t *testing.T) {
	// Verbatim blank input also yields "" (regression guard).
	v, d := singleString(t, rowCtx("X", ""), func(b *csvbase.Builder) csvbase.Key[string] {
		in := csvbase.Column(b, "X")
		return csvbase.MapValue(b, in, nil, csvkit.Verbatim, "")
	})
	if d != nil {
		t.Errorf("MapValue(Verbatim, blank) diag = %v, want nil", d)
	}
	if v != "" {
		t.Errorf("MapValue(Verbatim, blank) = %q, want %q", v, "")
	}
}
