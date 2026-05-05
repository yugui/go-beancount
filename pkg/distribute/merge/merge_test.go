package merge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/distribute/route"
	"github.com/yugui/go-beancount/pkg/format"
)

// mustDate parses a YYYY-MM-DD date and fails the test on error. The
// helper keeps test fixtures readable while still rejecting typos.
func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing date %q: %v", s, err)
	}
	return d
}

// open builds a minimal Open directive for use in test fixtures. We
// intentionally use Open everywhere because it is the simplest dated
// directive and the merger does not care about the directive type.
func open(t *testing.T, dateStr, account string) *ast.Open {
	t.Helper()
	acct := ast.Account(account)
	return &ast.Open{Date: mustDate(t, dateStr), Account: acct}
}

// runMerge writes data to a fresh tempfile, runs Merge against it, and
// returns the file's resulting bytes (or empty bytes if the file does
// not exist after Merge). The destination is unique per call so a test
// can run multiple Merges side by side. Tests pass an empty data
// slice and a nil destination existence to exercise the new-file path.
func runMerge(t *testing.T, plan Plan, srcData []byte) ([]byte, Stats, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dest.beancount")
	if srcData != nil {
		if err := os.WriteFile(path, srcData, 0o644); err != nil {
			t.Fatalf("seeding destination file: %v", err)
		}
	}
	plan.Path = path
	stats, err := Merge(plan, Options{})
	if err != nil {
		return nil, stats, err
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("reading destination after Merge: %v", readErr)
	}
	if os.IsNotExist(readErr) {
		return nil, stats, nil
	}
	return got, stats, nil
}

// readFixture loads a testdata file relative to the test binary and
// fails on any I/O error. Paths are written relative to the test
// package directory; Bazel exposes them via data = glob(["testdata/**"]).
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return data
}

// defaultPlan builds a Plan with the common spacing configuration
// (B=true, N=1) used by most tests. Tests override fields as needed
// before the call to runMerge.
func defaultPlan(inserts []Insert) Plan {
	return Plan{
		Order:                             route.OrderAscending,
		BlankLinesBetweenDirectives:       1,
		InsertBlankLinesBetweenDirectives: true,
		Inserts:                           inserts,
	}
}

// --- New-file tests (no .in fixture; only .want) ---

func TestMerge_NewFile_SingleInsert(t *testing.T) {
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-01-15", "Assets:NewAccount")},
	})
	got, stats, err := runMerge(t, plan, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	want := readFixture(t, "single_insert.want.beancount")
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
	if stats.Written != 1 {
		t.Errorf("Written: got %d, want 1", stats.Written)
	}
}

func TestMerge_NewFile_MultipleAscending(t *testing.T) {
	// Inserts are deliberately mixed; the merger must sort them
	// ascending before printing.
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-03-30", "Assets:Late")},
		{Directive: open(t, "2024-01-01", "Assets:Early")},
		{Directive: open(t, "2024-02-15", "Assets:Middle")},
	})
	got, _, err := runMerge(t, plan, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	want := readFixture(t, "multiple_ascending.want.beancount")
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_NewFile_SameDateFIFO(t *testing.T) {
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-01-15", "Assets:First")},
		{Directive: open(t, "2024-01-15", "Assets:Second")},
		{Directive: open(t, "2024-01-15", "Assets:Third")},
	})
	got, _, err := runMerge(t, plan, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	want := readFixture(t, "same_date_fifo.want.beancount")
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_NewFile_BTrueN2(t *testing.T) {
	plan := Plan{
		Order:                             route.OrderAscending,
		BlankLinesBetweenDirectives:       2,
		InsertBlankLinesBetweenDirectives: true,
		Inserts: []Insert{
			{Directive: open(t, "2024-01-15", "Assets:First")},
			{Directive: open(t, "2024-02-15", "Assets:Second")},
		},
	}
	got, _, err := runMerge(t, plan, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	want := readFixture(t, "b_true_n_2.want.beancount")
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_NewFile_BFalse(t *testing.T) {
	plan := Plan{
		Order:                             route.OrderAscending,
		BlankLinesBetweenDirectives:       1,
		InsertBlankLinesBetweenDirectives: false,
		Inserts: []Insert{
			{Directive: open(t, "2024-01-15", "Assets:First")},
			{Directive: open(t, "2024-02-15", "Assets:Second")},
		},
	}
	got, _, err := runMerge(t, plan, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	want := readFixture(t, "b_false.want.beancount")
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_NewFile_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deeply", "nested", "file.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-01-15", "Assets:NewAccount")},
	})
	plan.Path = path
	if _, err := Merge(plan, Options{}); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("parent directory not created: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("destination file not created: %v", err)
	}
}

func TestMerge_NewFile_TrailingNewlineExactlyOne(t *testing.T) {
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-01-15", "Assets:A")},
	})
	got, _, err := runMerge(t, plan, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("output too short: %q", got)
	}
	if got[len(got)-1] != '\n' {
		t.Errorf("Merge: output does not end with newline: %q", got)
	}
	if got[len(got)-2] == '\n' {
		t.Errorf("Merge: output ends with multiple newlines: %q", got)
	}
}

// --- Existing-file insertion (paired .in/.want) ---

func TestMerge_Existing_InsertBeforeAll(t *testing.T) {
	in := readFixture(t, "insert_before_all.in.beancount")
	want := readFixture(t, "insert_before_all.want.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-01-15", "Assets:NewAccount")},
	})
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_Existing_InsertBetween(t *testing.T) {
	in := readFixture(t, "insert_between.in.beancount")
	want := readFixture(t, "insert_between.want.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-02-15", "Assets:C")},
	})
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_Existing_InsertAfterAll(t *testing.T) {
	in := readFixture(t, "insert_after_all.in.beancount")
	want := readFixture(t, "insert_after_all.want.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-03-15", "Assets:C")},
	})
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_Existing_NoDatedDirectives(t *testing.T) {
	in := readFixture(t, "no_dated_directives.in.beancount")
	want := readFixture(t, "no_dated_directives.want.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-01-15", "Assets:NewAccount")},
	})
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_Existing_EmptyFile(t *testing.T) {
	in := readFixture(t, "empty_file.in.beancount")
	want := readFixture(t, "empty_file.want.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-01-15", "Assets:NewAccount")},
	})
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

// --- Comment / blank-line preservation (the §9 7.5b headline) ---

func TestMerge_Existing_PreservesCommentBlock(t *testing.T) {
	in := readFixture(t, "preserves_comment_block.in.beancount")
	want := readFixture(t, "preserves_comment_block.want.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-01-15", "Assets:NewAccount")},
	})
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_Existing_PreservesExtraBlankLines(t *testing.T) {
	in := readFixture(t, "preserves_extra_blank_lines.in.beancount")
	want := readFixture(t, "preserves_extra_blank_lines.want.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-02-15", "Assets:C")},
	})
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_Existing_PreservesUndatedHeader(t *testing.T) {
	in := readFixture(t, "preserves_undated_header.in.beancount")
	want := readFixture(t, "preserves_undated_header.want.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-01-15", "Assets:NewAccount")},
	})
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

// --- Spacing rules ---

func TestMerge_Spacing_BTrueXLessThanN(t *testing.T) {
	in := readFixture(t, "b_true_x_lt_n.in.beancount")
	want := readFixture(t, "b_true_x_lt_n.want.beancount")
	plan := Plan{
		Order:                             route.OrderAscending,
		BlankLinesBetweenDirectives:       2,
		InsertBlankLinesBetweenDirectives: true,
		Inserts: []Insert{
			{Directive: open(t, "2024-02-15", "Assets:C")},
		},
	}
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_Spacing_BTrueXEqualsN(t *testing.T) {
	in := readFixture(t, "b_true_x_eq_n.in.beancount")
	want := readFixture(t, "b_true_x_eq_n.want.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-02-15", "Assets:C")},
	})
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_Spacing_BTrueXGreaterThanN(t *testing.T) {
	in := readFixture(t, "b_true_x_gt_n.in.beancount")
	want := readFixture(t, "b_true_x_gt_n.want.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-02-15", "Assets:C")},
	})
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_Spacing_BFalseExisting(t *testing.T) {
	in := readFixture(t, "b_false_existing.in.beancount")
	want := readFixture(t, "b_false_existing.want.beancount")
	plan := Plan{
		Order:                             route.OrderAscending,
		BlankLinesBetweenDirectives:       1,
		InsertBlankLinesBetweenDirectives: false,
		Inserts: []Insert{
			{Directive: open(t, "2024-02-15", "Assets:C")},
		},
	}
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_Spacing_FileStart(t *testing.T) {
	in := readFixture(t, "file_start.in.beancount")
	want := readFixture(t, "file_start.want.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-01-15", "Assets:NewAccount")},
	})
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

func TestMerge_Spacing_FileEndNoTrailingNewline(t *testing.T) {
	in := readFixture(t, "file_end_no_trailing_newline.in.beancount")
	want := readFixture(t, "file_end_no_trailing_newline.want.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-02-15", "Assets:C")},
	})
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
	if got[len(got)-1] != '\n' || got[len(got)-2] == '\n' {
		t.Errorf("Merge: output should end with exactly one newline, got %q", got)
	}
}

// --- Same-offset collisions ---

func TestMerge_SameDayFIFOAtSameOffset(t *testing.T) {
	in := readFixture(t, "same_day_fifo_at_same_offset.in.beancount")
	want := readFixture(t, "same_day_fifo_at_same_offset.want.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-02-15", "Assets:A")},
		{Directive: open(t, "2024-02-15", "Assets:B")},
		{Directive: open(t, "2024-02-15", "Assets:C")},
	})
	got, _, err := runMerge(t, plan, in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

// --- Out-of-scope guards ---

func TestMerge_CommentedInsert_NewFile(t *testing.T) {
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-01-15", "Assets:A"), Commented: true, Prefix: "; "},
	})
	got, stats, err := runMerge(t, plan, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if stats.Written != 0 {
		t.Errorf("Written: got %d, want 0", stats.Written)
	}
	if stats.Commented != 1 {
		t.Errorf("Commented: got %d, want 1", stats.Commented)
	}
	if want := "; 2024-01-15 open Assets:A\n"; string(got) != want {
		t.Errorf("output = %q, want %q", string(got), want)
	}
}

func TestMerge_CommentedInsert_DefaultsPrefix(t *testing.T) {
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-01-15", "Assets:A"), Commented: true},
	})
	got, _, err := runMerge(t, plan, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if want := "; 2024-01-15 open Assets:A\n"; string(got) != want {
		t.Errorf("output = %q, want %q", string(got), want)
	}
}

// TestMerge_Existing_CommentedInsert exercises the existing-file patch
// path with a commented insert between two existing dated directives.
// Mirrors TestMerge_Existing_InsertBetween's shape so that the only
// observable difference is the "; " prefix on the new line — confirming
// that the commented branch in printInsert is wired through the
// patch-composition path, not just the new-file path.
func TestMerge_Existing_CommentedInsert(t *testing.T) {
	src := readFixture(t, "insert_between.in.beancount")
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-02-15", "Assets:C"), Commented: true, Prefix: "; "},
	})
	got, stats, err := runMerge(t, plan, src)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	want := readFixture(t, "commented_insert_between.want.beancount")
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
	if stats.Written != 0 {
		t.Errorf("Written: got %d, want 0", stats.Written)
	}
	if stats.Commented != 1 {
		t.Errorf("Commented: got %d, want 1", stats.Commented)
	}
}

// TestMerge_CommentedInsert_MultiLine verifies that comment.Emit
// prefixes every line of a multi-line directive, including the indented
// posting continuation lines. A Transaction with two postings is the
// realistic dedup cross-posting case.
func TestMerge_CommentedInsert_MultiLine(t *testing.T) {
	dec := func(s string) apd.Decimal {
		t.Helper()
		d, _, err := apd.NewFromString(s)
		if err != nil {
			t.Fatalf("apd.NewFromString(%q): %v", s, err)
		}
		return *d
	}
	txn := &ast.Transaction{
		Date:      mustDate(t, "2024-01-15"),
		Flag:      '*',
		Narration: "Coffee",
		Postings: []ast.Posting{
			{Account: ast.Expenses.MustSub("Food"), Amount: &ast.Amount{Number: dec("3.50"), Currency: "USD"}},
			{Account: ast.Assets.MustSub("Cash"), Amount: &ast.Amount{Number: dec("-3.50"), Currency: "USD"}},
		},
	}
	plan := defaultPlan([]Insert{{Directive: txn, Commented: true, Prefix: "; "}})
	got, stats, err := runMerge(t, plan, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if stats.Commented != 1 {
		t.Errorf("Commented: got %d, want 1", stats.Commented)
	}
	lines := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (header + 2 postings), got %q", got)
	}
	for i, line := range lines {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, ";") {
			t.Errorf("line %d not commented-prefixed: %q", i, line)
		}
	}
}

// TestMerge_NewFile_MixedActiveAndCommented verifies that a Plan
// containing both active and commented inserts produces a file with
// both forms, that Stats correctly splits the count between Written
// and Commented, and that B/N spacing is applied between them the
// same way as between two active inserts.
func TestMerge_NewFile_MixedActiveAndCommented(t *testing.T) {
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-01-15", "Assets:A")},
		{Directive: open(t, "2024-02-15", "Assets:B"), Commented: true, Prefix: "; "},
	})
	got, stats, err := runMerge(t, plan, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	want := readFixture(t, "mixed_active_and_commented.want.beancount")
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
	if stats.Written != 1 {
		t.Errorf("Written: got %d, want 1", stats.Written)
	}
	if stats.Commented != 1 {
		t.Errorf("Commented: got %d, want 1", stats.Commented)
	}
}

func TestMerge_RejectsDescendingOrder(t *testing.T) {
	plan := Plan{
		Path:                              filepath.Join(t.TempDir(), "dest.beancount"),
		Order:                             route.OrderDescending,
		BlankLinesBetweenDirectives:       1,
		InsertBlankLinesBetweenDirectives: true,
		Inserts: []Insert{
			{Directive: open(t, "2024-01-15", "Assets:A")},
		},
	}
	_, err := Merge(plan, Options{})
	if err == nil {
		t.Fatal("Merge with OrderDescending: got nil error, want ErrOrderNotSupported")
	}
	if !errors.Is(err, ErrOrderNotSupported) {
		t.Errorf("Merge(Order=Descending) error = %v; want one matching errors.Is(err, ErrOrderNotSupported)", err)
	}
}

func TestMerge_RejectsAppendOrder(t *testing.T) {
	plan := Plan{
		Path:                              filepath.Join(t.TempDir(), "dest.beancount"),
		Order:                             route.OrderAppend,
		BlankLinesBetweenDirectives:       1,
		InsertBlankLinesBetweenDirectives: true,
		Inserts: []Insert{
			{Directive: open(t, "2024-01-15", "Assets:A")},
		},
	}
	_, err := Merge(plan, Options{})
	if err == nil {
		t.Fatal("Merge with OrderAppend: got nil error, want ErrOrderNotSupported")
	}
	if !errors.Is(err, ErrOrderNotSupported) {
		t.Errorf("Merge(Order=Append) error = %v; want one matching errors.Is(err, ErrOrderNotSupported)", err)
	}
}

// --- Parse-failure guard ---

func TestMerge_ExistingParseError(t *testing.T) {
	in := readFixture(t, "parse_error.in.beancount")
	dir := t.TempDir()
	path := filepath.Join(dir, "dest.beancount")
	if err := os.WriteFile(path, in, 0o644); err != nil {
		t.Fatalf("seeding destination: %v", err)
	}
	plan := defaultPlan([]Insert{
		{Directive: open(t, "2024-03-15", "Assets:A")},
	})
	plan.Path = path
	_, err := Merge(plan, Options{})
	if err == nil {
		t.Fatal("Merge against malformed file: got nil error, want parse error")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading destination after failed Merge: %v", readErr)
	}
	if diff := cmp.Diff(string(in), string(got)); diff != "" {
		t.Errorf("file modified despite parse error (-want +got):\n%s", diff)
	}
}

// --- Empty inserts ---

func TestMerge_EmptyInsertsNoOp_FileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.beancount")
	plan := Plan{Path: path, Order: route.OrderAscending}
	stats, err := Merge(plan, Options{})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if stats.Path != path {
		t.Errorf("Stats.Path: got %q, want %q", stats.Path, path)
	}
	if stats.Written != 0 {
		t.Errorf("Stats.Written: got %d, want 0", stats.Written)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("Merge with no inserts should not create file; stat err = %v", statErr)
	}
}

func TestMerge_EmptyInsertsNoOp_FileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.beancount")
	original := []byte("2024-01-01 open Assets:A\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("seeding destination: %v", err)
	}
	plan := Plan{Path: path, Order: route.OrderAscending}
	stats, err := Merge(plan, Options{})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if stats.Written != 0 {
		t.Errorf("Stats.Written: got %d, want 0", stats.Written)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading destination: %v", err)
	}
	if diff := cmp.Diff(string(original), string(got)); diff != "" {
		t.Errorf("file modified despite no inserts (-want +got):\n%s", diff)
	}
}

// --- Helper unit tests for spacing primitives ---

func TestPaddingFor(t *testing.T) {
	cases := []struct {
		name string
		b    bool
		n, x int
		want string
	}{
		{"BFalse", false, 5, 0, ""},
		{"BFalseXBig", false, 5, 100, ""},
		{"XEqualsN", true, 1, 1, ""},
		{"XGreaterThanN", true, 1, 3, ""},
		{"XLessThanN", true, 2, 1, "\n"},
		{"XZeroNZero", true, 0, 0, ""},
		{"XZeroN1", true, 1, 0, "\n"},
		{"XZeroN2", true, 2, 0, "\n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := paddingFor(tc.b, tc.n, tc.x); got != tc.want {
				t.Errorf("paddingFor(%v,%d,%d) = %q, want %q", tc.b, tc.n, tc.x, got, tc.want)
			}
		})
	}
}

func TestCountTrailingBlanks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"Empty", "", 0},
		{"NoTerminator", "abc", 0},
		{"OneTerminator", "abc\n", 0},
		{"TwoTerminators", "abc\n\n", 1},
		{"ThreeTerminators", "abc\n\n\n", 2},
		{"OnlyTerminators", "\n\n", 2},
		{"CRLFOne", "abc\r\n", 0},
		{"CRLFBlank", "abc\r\n\r\n", 1},
		{"MixedCRLFAndLF", "abc\n\r\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countTrailingBlanks([]byte(tc.in)); got != tc.want {
				t.Errorf("countTrailingBlanks(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestCountLeadingBlanks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"Empty", "", 0},
		{"NoBlanks", "abc", 0},
		{"OneBlank", "\nabc", 1},
		{"TwoBlanks", "\n\nabc", 2},
		{"AllBlanks", "\n\n", 2},
		{"CRLFOne", "\r\nabc", 1},
		{"CRLFTwo", "\r\n\r\nabc", 2},
		{"MixedCRLFAndLF", "\n\r\nabc", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countLeadingBlanks([]byte(tc.in)); got != tc.want {
				t.Errorf("countLeadingBlanks(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// Sanity check that body-level Format options on Insert reach the
// printer. We render a transaction with a posting and override
// AmountColumn so the posting amount lands at the requested column.
func TestMerge_PassesFormatOptions(t *testing.T) {
	amt := ast.Amount{Currency: "USD"}
	txn := &ast.Transaction{
		Date: mustDate(t, "2024-01-15"),
		Flag: '*',
		Postings: []ast.Posting{
			{Account: ast.Assets.MustSub("Bank"), Amount: &amt},
		},
	}
	plan := defaultPlan([]Insert{
		{
			Directive: txn,
			Format:    []format.Option{format.WithAmountColumn(80)},
		},
	})
	got, _, err := runMerge(t, plan, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	// The posting line right-aligns the amount currency at column 80.
	// Find the posting line (the second line of output) and verify its
	// printable length is at least the requested column.
	lines := splitLines(string(got))
	if len(lines) < 2 {
		t.Fatalf("expected at least two output lines, got %q", got)
	}
	if len(lines[1]) < 80 {
		t.Errorf("posting line shorter than AmountColumn=80: %q (len %d)", lines[1], len(lines[1]))
	}
}

// splitLines splits s on '\n' but, unlike strings.Split, drops a
// trailing empty element produced by a final newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
