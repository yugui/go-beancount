package postproc

import (
	"context"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/ext/postproc/api"
)

// fakePlugin is a configurable api.Plugin for testing the runner.
type fakePlugin struct {
	name    string
	onApply func(api.Input) (api.Result, error)
	calls   []api.Input // records each Apply call
}

func (f *fakePlugin) Apply(_ context.Context, in api.Input) (api.Result, error) {
	f.calls = append(f.calls, in)
	if f.onApply != nil {
		return f.onApply(in)
	}
	return api.Result{}, nil
}

// registerFake registers the fakePlugin in a clean registry for the test.
func registerFake(t *testing.T, fakes ...*fakePlugin) {
	t.Helper()
	withCleanRegistry(t)
	for _, f := range fakes {
		Register(f.name, f)
	}
}

// newLedger creates a Ledger containing the given directives.
func newLedger(ds ...ast.Directive) *ast.Ledger {
	l := &ast.Ledger{}
	l.InsertAll(ds)
	return l
}

// collectFromIter materializes directives from an iter.Seq2.
func collectFromIter(seq iter.Seq2[int, ast.Directive]) []ast.Directive {
	var out []ast.Directive
	for _, d := range seq {
		out = append(out, d)
	}
	return out
}

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return d
}

func TestApply_NoPluginDirectives(t *testing.T) {
	withCleanRegistry(t)
	l := newLedger(
		&ast.Open{Date: day(t, "2024-01-01"), Account: "Assets:Cash"},
	)
	lenBefore := l.Len()

	if err := Apply(context.Background(), l); err != nil {
		t.Errorf("Apply returned error %v, want nil", err)
	}
	if got := len(l.Diagnostics); got != 0 {
		t.Errorf("ledger.Diagnostics len = %d, want 0", got)
	}
	if l.Len() != lenBefore {
		t.Errorf("Len = %d, want %d", l.Len(), lenBefore)
	}
}

func TestApply_DiagnosticPluginNoChange(t *testing.T) {
	fake := &fakePlugin{
		name: "example.com/fake/diag",
		onApply: func(_ api.Input) (api.Result, error) {
			return api.Result{Directives: nil}, nil // no change
		},
	}
	registerFake(t, fake)

	open := &ast.Open{Date: day(t, "2024-01-01"), Account: "Assets:Cash"}
	l := newLedger(
		&ast.Plugin{Name: "example.com/fake/diag"},
		open,
	)
	lenBefore := l.Len()

	if err := Apply(context.Background(), l); err != nil {
		t.Errorf("Apply returned error %v, want nil", err)
	}
	if got := len(l.Diagnostics); got != 0 {
		t.Errorf("ledger.Diagnostics len = %d, want 0", got)
	}
	if l.Len() != lenBefore {
		t.Errorf("Len = %d, want %d", l.Len(), lenBefore)
	}
}

func TestApply_AdditivePlugin(t *testing.T) {
	newPrice := &ast.Price{
		Date:      day(t, "2024-06-01"),
		Commodity: "USD",
		Amount:    ast.Amount{Currency: "EUR"},
	}
	fake := &fakePlugin{
		name: "example.com/fake/adder",
		onApply: func(in api.Input) (api.Result, error) {
			dirs := collectFromIter(in.Directives)
			dirs = append(dirs, newPrice)
			return api.Result{Directives: dirs}, nil
		},
	}
	registerFake(t, fake)

	l := newLedger(
		&ast.Plugin{Name: "example.com/fake/adder"},
		&ast.Open{Date: day(t, "2024-01-01"), Account: "Assets:Cash"},
	)
	lenBefore := l.Len()

	if err := Apply(context.Background(), l); err != nil {
		t.Errorf("Apply returned error %v, want nil", err)
	}
	if l.Len() != lenBefore+1 {
		t.Errorf("Len = %d, want %d", l.Len(), lenBefore+1)
	}
	// Verify the new Price is in the ledger.
	found := false
	for _, d := range l.All() {
		if d == newPrice {
			found = true
			break
		}
	}
	if !found {
		t.Error("Apply: new Price not found in ledger after Apply")
	}
}

func TestApply_ModifyPlugin(t *testing.T) {
	origTx := &ast.Transaction{
		Date:      day(t, "2024-02-01"),
		Narration: "original",
		Postings: []ast.Posting{
			{Account: "Assets:Cash"},
		},
	}
	modifiedTx := &ast.Transaction{
		Date:      day(t, "2024-02-01"),
		Narration: "original",
		Postings: []ast.Posting{
			{Account: "Assets:Cash"},
			{Account: "Expenses:Food"},
		},
	}
	fake := &fakePlugin{
		name: "example.com/fake/modifier",
		onApply: func(in api.Input) (api.Result, error) {
			var result []ast.Directive
			for _, d := range in.Directives {
				if _, ok := d.(*ast.Transaction); ok {
					result = append(result, modifiedTx)
				} else {
					result = append(result, d)
				}
			}
			return api.Result{Directives: result}, nil
		},
	}
	registerFake(t, fake)

	l := newLedger(
		&ast.Plugin{Name: "example.com/fake/modifier"},
		origTx,
	)

	if err := Apply(context.Background(), l); err != nil {
		t.Errorf("Apply returned error %v, want nil", err)
	}
	// Find the transaction in the ledger.
	for _, d := range l.All() {
		if tx, ok := d.(*ast.Transaction); ok {
			if len(tx.Postings) != 2 {
				t.Errorf("Apply: transaction has %d postings, want 2", len(tx.Postings))
			}
			return
		}
	}
	t.Error("Apply: no transaction found in ledger after Apply")
}

func TestApply_DeletePlugin(t *testing.T) {
	open := &ast.Open{Date: day(t, "2024-01-01"), Account: "Assets:Cash"}
	price := &ast.Price{Date: day(t, "2024-03-01"), Commodity: "USD"}
	fake := &fakePlugin{
		name: "example.com/fake/deleter",
		onApply: func(in api.Input) (api.Result, error) {
			var result []ast.Directive
			for _, d := range in.Directives {
				if _, ok := d.(*ast.Price); !ok {
					result = append(result, d)
				}
			}
			return api.Result{Directives: result}, nil
		},
	}
	registerFake(t, fake)

	l := newLedger(
		&ast.Plugin{Name: "example.com/fake/deleter"},
		open,
		price,
	)

	if err := Apply(context.Background(), l); err != nil {
		t.Errorf("Apply returned error %v, want nil", err)
	}
	for _, d := range l.All() {
		if _, ok := d.(*ast.Price); ok {
			t.Error("Apply: Price directive still in ledger after deletion")
		}
	}
}

func TestApply_ConfigPassThrough(t *testing.T) {
	fake := &fakePlugin{name: "example.com/fake/cfg"}
	registerFake(t, fake)

	l := newLedger(
		&ast.Plugin{Name: "example.com/fake/cfg", Config: "my-config-value"},
	)

	if err := Apply(context.Background(), l); err != nil {
		t.Fatalf("Apply returned unexpected error: %v", err)
	}

	if len(fake.calls) != 1 {
		t.Fatalf("Apply called plugin %d times, want 1", len(fake.calls))
	}
	if fake.calls[0].Config != "my-config-value" {
		t.Errorf("Config = %q, want %q", fake.calls[0].Config, "my-config-value")
	}
}

func TestApply_SeesPriorPluginOutput(t *testing.T) {
	sentinel := &ast.Price{
		Date:      day(t, "2024-12-25"),
		Commodity: "SENTINEL",
	}
	first := &fakePlugin{
		name: "example.com/fake/first",
		onApply: func(in api.Input) (api.Result, error) {
			dirs := collectFromIter(in.Directives)
			dirs = append(dirs, sentinel)
			return api.Result{Directives: dirs}, nil
		},
	}
	var secondSawSentinel bool
	second := &fakePlugin{
		name: "example.com/fake/second",
		onApply: func(in api.Input) (api.Result, error) {
			for _, d := range in.Directives {
				if d == sentinel {
					secondSawSentinel = true
				}
			}
			return api.Result{}, nil
		},
	}
	registerFake(t, first, second)

	l := newLedger(
		&ast.Plugin{Name: "example.com/fake/first"},
		&ast.Plugin{Name: "example.com/fake/second"},
	)

	if err := Apply(context.Background(), l); err != nil {
		t.Fatalf("Apply returned unexpected error: %v", err)
	}

	if !secondSawSentinel {
		t.Error("Apply: second plugin did not see sentinel from first")
	}
}

// TestApply_UnknownPlugin verifies that a `plugin "..."` directive whose
// name has no registered implementation is reported as a Diagnostic on
// the ledger (not a system-level error) and the pipeline continues.
func TestApply_UnknownPlugin(t *testing.T) {
	withCleanRegistry(t)

	pd := &ast.Plugin{
		Span: ast.Span{Start: ast.Position{Filename: "test.beancount", Line: 1, Column: 1}},
		Name: "missing",
	}
	l := newLedger(pd)

	if err := Apply(context.Background(), l); err != nil {
		t.Errorf("Apply returned error %v, want nil (unknown plugin is a ledger-content issue)", err)
	}
	if len(l.Diagnostics) != 1 {
		t.Fatalf("ledger.Diagnostics len = %d, want 1", len(l.Diagnostics))
	}
	want := ast.Diagnostic{
		Code:    "plugin-not-registered",
		Span:    pd.Span,
		Message: `plugin "missing" is not registered`,
	}
	if diff := cmp.Diff(want, l.Diagnostics[0]); diff != "" {
		t.Errorf("Diagnostics[0] mismatch (-want +got):\n%s", diff)
	}
}

// TestApply_PluginReportedDiagnostics verifies that diagnostics returned
// in api.Result.Diagnostics land on ledger.Diagnostics (not on the
// runner's return value).
func TestApply_PluginReportedDiagnostics(t *testing.T) {
	pluginDiag := ast.Diagnostic{
		Code:    "custom-check",
		Message: "something is wrong",
	}
	fake := &fakePlugin{
		name: "example.com/fake/reporter",
		onApply: func(_ api.Input) (api.Result, error) {
			return api.Result{Diagnostics: []ast.Diagnostic{pluginDiag}}, nil
		},
	}
	registerFake(t, fake)

	l := newLedger(&ast.Plugin{Name: "example.com/fake/reporter"})

	if err := Apply(context.Background(), l); err != nil {
		t.Fatalf("Apply returned unexpected error: %v", err)
	}
	if len(l.Diagnostics) != 1 {
		t.Fatalf("ledger.Diagnostics len = %d, want 1", len(l.Diagnostics))
	}
	if diff := cmp.Diff(pluginDiag, l.Diagnostics[0]); diff != "" {
		t.Errorf("Apply() Diagnostics[0] mismatch (-want +got):\n%s", diff)
	}
}

// TestApply_PluginErrorHalts verifies that a non-nil error from a
// plugin's Apply halts the pipeline immediately: subsequent plugins do
// NOT run, and the error is wrapped with the failing plugin's name and
// returned to the caller. errors.Is must observe the original cause.
func TestApply_PluginErrorHalts(t *testing.T) {
	cause := errors.New("boom")
	failing := &fakePlugin{
		name: "example.com/fake/failing",
		onApply: func(_ api.Input) (api.Result, error) {
			return api.Result{}, cause
		},
	}
	second := &fakePlugin{name: "example.com/fake/successor"}
	registerFake(t, failing, second)

	l := newLedger(
		&ast.Plugin{Name: "example.com/fake/failing"},
		&ast.Plugin{Name: "example.com/fake/successor"},
	)

	err := Apply(context.Background(), l)
	if err == nil {
		t.Fatal("Apply returned nil, want non-nil error from failing plugin")
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false, want true; err = %v", err)
	}
	if len(second.calls) != 0 {
		t.Errorf("successor plugin was called %d times, want 0 (pipeline must halt on first error)", len(second.calls))
	}
	if got := len(l.Diagnostics); got != 0 {
		t.Errorf("ledger.Diagnostics len = %d, want 0 (system-level errors must not become diagnostics): %v", got, l.Diagnostics)
	}
}

func TestApply_OptionsSnapshotLastWins(t *testing.T) {
	fake := &fakePlugin{name: "example.com/fake/opts"}
	registerFake(t, fake)

	l := newLedger(
		&ast.Option{Key: "title", Value: "X"},
		&ast.Option{Key: "title", Value: "Y"},
		&ast.Plugin{Name: "example.com/fake/opts"},
	)

	if err := Apply(context.Background(), l); err != nil {
		t.Fatalf("Apply returned unexpected error: %v", err)
	}

	if len(fake.calls) != 1 {
		t.Fatalf("Apply called plugin %d times, want 1", len(fake.calls))
	}
	if got := fake.calls[0].Options["title"]; got != "Y" {
		t.Errorf("Options[\"title\"] = %q, want %q", got, "Y")
	}
}

// TestApply_CtxCanceledBeforeRun verifies that a canceled context halts
// the pipeline before the first plugin runs and surfaces as a returned
// error — cancellation does not become a diagnostic on the ledger.
func TestApply_CtxCanceledBeforeRun(t *testing.T) {
	fake := &fakePlugin{name: "example.com/fake/never"}
	registerFake(t, fake)

	l := newLedger(&ast.Plugin{Name: "example.com/fake/never"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := Apply(ctx, l)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply err = %v, want context.Canceled", err)
	}
	if len(l.Diagnostics) != 0 {
		t.Errorf("ledger.Diagnostics = %v, want empty (cancellation must not become a diagnostic)", l.Diagnostics)
	}
	if len(fake.calls) != 0 {
		t.Error("plugin was called despite canceled context")
	}
}

func TestApply_IteratorIsLive(t *testing.T) {
	sentinel := &ast.Price{
		Date:      day(t, "2024-12-25"),
		Commodity: "LIVE",
	}
	first := &fakePlugin{
		name: "example.com/fake/replacer",
		onApply: func(in api.Input) (api.Result, error) {
			dirs := collectFromIter(in.Directives)
			dirs = append(dirs, sentinel)
			return api.Result{Directives: dirs}, nil
		},
	}
	var secondSeenLen int
	second := &fakePlugin{
		name: "example.com/fake/observer",
		onApply: func(in api.Input) (api.Result, error) {
			dirs := collectFromIter(in.Directives)
			secondSeenLen = len(dirs)
			return api.Result{}, nil
		},
	}
	registerFake(t, first, second)

	l := newLedger(
		&ast.Plugin{Name: "example.com/fake/replacer"},
		&ast.Plugin{Name: "example.com/fake/observer"},
	)
	lenBefore := l.Len()

	if err := Apply(context.Background(), l); err != nil {
		t.Fatalf("Apply returned unexpected error: %v", err)
	}

	// Second plugin should see the sentinel added by first.
	if secondSeenLen != lenBefore+1 {
		t.Errorf("Apply: second saw %d directives, want %d", secondSeenLen, lenBefore+1)
	}
}

func TestApply_Integration(t *testing.T) {
	// End-to-end test with ast.Load. Write a beancount fixture
	// containing a plugin directive and some real directives.
	dir := t.TempDir()
	content := `option "title" "Integration Test"
plugin "example.com/fake/auto_open"

2024-01-01 open Assets:Cash USD

2024-02-01 * "Coffee"
  Assets:Cash  -5.00 USD
  Expenses:Food  5.00 USD
`
	path := filepath.Join(dir, "main.beancount")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Register a fake "auto-open" that adds an Open directive.
	autoOpen := &ast.Open{Date: day(t, "2024-01-01"), Account: "Expenses:Food"}
	fake := &fakePlugin{
		name: "example.com/fake/auto_open",
		onApply: func(in api.Input) (api.Result, error) {
			dirs := collectFromIter(in.Directives)
			dirs = append(dirs, autoOpen)
			return api.Result{Directives: dirs}, nil
		},
	}
	registerFake(t, fake)

	ledger, err := ast.LoadFile(path)
	if err != nil {
		t.Fatalf("ast.LoadFile: %v", err)
	}
	lenBefore := ledger.Len()
	diagsBefore := len(ledger.Diagnostics)

	if err := Apply(context.Background(), ledger); err != nil {
		t.Errorf("Apply returned error: %v", err)
	}
	if got := len(ledger.Diagnostics) - diagsBefore; got != 0 {
		t.Errorf("Apply added %d diagnostics, want 0", got)
	}
	if ledger.Len() != lenBefore+1 {
		t.Errorf("Apply: Len = %d, want %d", ledger.Len(), lenBefore+1)
	}

	// Verify the auto-opened account is in the ledger.
	found := false
	for _, d := range ledger.All() {
		if d == autoOpen {
			found = true
			break
		}
	}
	if !found {
		t.Error("Apply: auto-opened directive not found in ledger")
	}
}
