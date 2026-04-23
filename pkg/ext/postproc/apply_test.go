package postproc

import (
	"context"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"testing"
	"time"

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

	errs := Apply(context.Background(), l)
	if len(errs) != 0 {
		t.Errorf("Apply returned %d errors, want 0", len(errs))
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

	errs := Apply(context.Background(), l)
	if len(errs) != 0 {
		t.Errorf("Apply returned %d errors, want 0", len(errs))
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

	errs := Apply(context.Background(), l)
	if len(errs) != 0 {
		t.Errorf("Apply returned %d errors, want 0", len(errs))
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

	errs := Apply(context.Background(), l)
	if len(errs) != 0 {
		t.Errorf("Apply returned %d errors, want 0", len(errs))
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

	errs := Apply(context.Background(), l)
	if len(errs) != 0 {
		t.Errorf("Apply returned %d errors, want 0", len(errs))
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

	if errs := Apply(context.Background(), l); len(errs) != 0 {
		t.Fatalf("Apply returned unexpected errors: %v", errs)
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

	if errs := Apply(context.Background(), l); len(errs) != 0 {
		t.Fatalf("Apply returned unexpected errors: %v", errs)
	}

	if !secondSawSentinel {
		t.Error("Apply: second plugin did not see sentinel from first")
	}
}

func TestApply_UnknownPlugin(t *testing.T) {
	withCleanRegistry(t)

	pd := &ast.Plugin{
		Span: ast.Span{Start: ast.Position{Filename: "test.beancount", Line: 1, Column: 1}},
		Name: "missing",
	}
	l := newLedger(pd)

	errs := Apply(context.Background(), l)
	if len(errs) != 1 {
		t.Fatalf("Apply returned %d errors, want 1", len(errs))
	}
	if errs[0].Code != "plugin-not-registered" {
		t.Errorf("Code = %q, want %q", errs[0].Code, "plugin-not-registered")
	}
	if errs[0].Span != pd.Span {
		t.Errorf("Span = %v, want %v", errs[0].Span, pd.Span)
	}
}

func TestApply_PluginReportedErrors(t *testing.T) {
	pluginErr := api.Error{
		Code:    "custom-check",
		Message: "something is wrong",
	}
	fake := &fakePlugin{
		name: "example.com/fake/reporter",
		onApply: func(_ api.Input) (api.Result, error) {
			return api.Result{Errors: []api.Error{pluginErr}}, nil
		},
	}
	registerFake(t, fake)

	l := newLedger(&ast.Plugin{Name: "example.com/fake/reporter"})

	errs := Apply(context.Background(), l)
	if len(errs) != 1 {
		t.Fatalf("Apply returned %d errors, want 1", len(errs))
	}
	if errs[0] != pluginErr {
		t.Errorf("error = %v, want %v", errs[0], pluginErr)
	}
}

func TestApply_PluginReturnedErrorContinues(t *testing.T) {
	failing := &fakePlugin{
		name: "example.com/fake/failing",
		onApply: func(_ api.Input) (api.Result, error) {
			return api.Result{}, errors.New("boom")
		},
	}
	second := &fakePlugin{name: "example.com/fake/successor"}
	registerFake(t, failing, second)

	l := newLedger(
		&ast.Plugin{Name: "example.com/fake/failing"},
		&ast.Plugin{Name: "example.com/fake/successor"},
	)

	errs := Apply(context.Background(), l)
	// Should have one plugin-failed error from the first.
	var foundFailed bool
	for _, e := range errs {
		if e.Code == "plugin-failed" {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Error("Apply: no plugin-failed error found")
	}
	// Second plugin should still have been called.
	if len(second.calls) != 1 {
		t.Errorf("Apply: second plugin called %d times, want 1", len(second.calls))
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

	if errs := Apply(context.Background(), l); len(errs) != 0 {
		t.Fatalf("Apply returned unexpected errors: %v", errs)
	}

	if len(fake.calls) != 1 {
		t.Fatalf("Apply called plugin %d times, want 1", len(fake.calls))
	}
	if got := fake.calls[0].Options["title"]; got != "Y" {
		t.Errorf("Options[\"title\"] = %q, want %q", got, "Y")
	}
}

func TestApply_CtxCanceledBeforeRun(t *testing.T) {
	fake := &fakePlugin{name: "example.com/fake/never"}
	registerFake(t, fake)

	l := newLedger(&ast.Plugin{Name: "example.com/fake/never"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	errs := Apply(ctx, l)
	if len(errs) != 1 {
		t.Fatalf("Apply returned %d errors, want 1", len(errs))
	}
	if errs[0].Code != "plugin-canceled" {
		t.Errorf("Code = %q, want %q", errs[0].Code, "plugin-canceled")
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

	if errs := Apply(context.Background(), l); len(errs) != 0 {
		t.Fatalf("Apply returned unexpected errors: %v", errs)
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

	ledger, err := ast.Load(path)
	if err != nil {
		t.Fatalf("ast.Load: %v", err)
	}
	lenBefore := ledger.Len()

	pluginErrs := Apply(context.Background(), ledger)
	if len(pluginErrs) != 0 {
		t.Errorf("Apply returned errors: %v", pluginErrs)
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
