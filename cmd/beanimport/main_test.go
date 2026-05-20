package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer"
	"github.com/yugui/go-beancount/pkg/importer/hook"
)

type fakeImporter struct {
	name string
}

func (f *fakeImporter) Name() string                                      { return f.name }
func (f *fakeImporter) Identify(_ context.Context, _ importer.Input) bool { return false }
func (f *fakeImporter) Extract(_ context.Context, _ importer.Input) (importer.Output, error) {
	return importer.Output{}, nil
}

// sentinelHook emits a Warning carrying the hook's name so tests can observe
// chain order by reading stderr.
type sentinelHook struct {
	name string
}

func (h *sentinelHook) Name() string { return h.name }
func (h *sentinelHook) Apply(_ context.Context, in hook.HookInput) (hook.HookResult, error) {
	return hook.HookResult{
		Directives: in.Directives,
		Diagnostics: []ast.Diagnostic{{
			Code:     "test-sentinel",
			Severity: ast.Warning,
			Message:  "sentinel from hook " + h.name,
		}},
	}, nil
}

func init() {
	importer.RegisterFactory("_beanimport_fake_imp", importer.FactoryFunc(
		func(name string, decode func(dest any) error) (importer.Importer, error) {
			var cfg struct {
				Fail bool `toml:"fail"`
			}
			if err := decode(&cfg); err != nil {
				return nil, fmt.Errorf("fake: decode: %v", err)
			}
			if cfg.Fail {
				return nil, fmt.Errorf("fake: configured to fail")
			}
			return &fakeImporter{name: name}, nil
		},
	))

	hook.RegisterFactory("_beanimport_fake_hook", hook.FactoryFunc(
		func(name string, decode func(dest any) error) (hook.Hook, error) {
			var cfg struct{}
			if err := decode(&cfg); err != nil {
				return nil, err
			}
			return &sentinelHook{name: name}, nil
		},
	))
}

func writeCSV(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "statement.csv")
	content := "Date,Payee,Description,Memo,Amount\n" +
		"2024-01-15,Coffee Shop,Latte,Morning,-4.50\n"
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeConfig(t *testing.T, dir, account string) string {
	t.Helper()
	content := fmt.Sprintf(`
[[importer]]
kind             = "csv"
name             = "test"
date_col         = "Date"
date_format      = "2006-01-02"
account          = %q
default_currency = "USD"

[[importer.amount]]
col    = "Amount"
negate = false
`, account)
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRun_SingleInstanceSmoke(t *testing.T) {
	dir := t.TempDir()
	csv := writeCSV(t, dir)
	cfg := writeConfig(t, dir, "Assets:Checking")

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"-config", cfg, csv}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, want 0; stderr: %q\nstdout: %q", code, stderr.String(), stdout.String())
	}
	if stdout.Len() == 0 {
		t.Error("stdout is empty, want non-empty beancount output")
	}
}

// TestRun_FailFast covers the exit-2 paths whose only contract is "given
// these args, exit 2 with a substring in stderr." More elaborate failure
// modes (forced Identify, strict, hook order, cancellation) have dedicated
// tests below.
func TestRun_FailFast(t *testing.T) {
	dir := t.TempDir()
	csv := writeCSV(t, dir)
	cfg := writeConfig(t, dir, "Assets:Checking")

	cases := []struct {
		name      string
		args      []string
		substring string
	}{
		{
			name:      "MissingConfig",
			args:      []string{csv},
			substring: "-config is required",
		},
		{
			name:      "ZeroPositional",
			args:      []string{"-config", cfg},
			substring: "exactly one input file required",
		},
		{
			name:      "TwoPositionals",
			args:      []string{"-config", cfg, csv, csv},
			substring: "exactly one input file required",
		},
		{
			name:      "UnknownImporter",
			args:      []string{"-config", cfg, "-importer", "foo", csv},
			substring: "unknown importer",
		},
		{
			name:      "UnknownHook",
			args:      []string{"-config", cfg, "-hook", "foo", csv},
			substring: "unknown hook",
		},
		{
			name:      "PluginCommaIsLiteral",
			args:      []string{"-config", cfg, "-plugin", "no,such.so", csv},
			substring: "no,such.so",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tc.args, &stdout, &stderr)
			if code != 2 {
				t.Errorf("run() = %d, want 2; stderr: %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.substring) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tc.substring)
			}
		})
	}
}

func TestRun_ImporterIdentifyFalseWithFlag(t *testing.T) {
	dir := t.TempDir()
	csv := writeCSV(t, dir)
	cfg := filepath.Join(dir, "config.toml")
	content := `
[[importer]]
kind = "_beanimport_fake_imp"
name = "test"
`
	if err := os.WriteFile(cfg, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"-config", cfg, "-importer", "test", csv}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("run(identify-false forced) = %d, want 0; stderr: %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), codeIdentifyForced) {
		t.Errorf("stderr = %q, want it to contain code %q", stderr.String(), codeIdentifyForced)
	}
	if !strings.Contains(stderr.String(), "extracting anyway") {
		t.Errorf("stderr = %q, want it to contain 'extracting anyway'", stderr.String())
	}
}

func TestRun_StrictPromotesWarningToExit1(t *testing.T) {
	dir := t.TempDir()
	csv := writeCSV(t, dir)
	cfg := filepath.Join(dir, "config.toml")
	content := `
[[importer]]
kind = "_beanimport_fake_imp"
name = "test"
`
	if err := os.WriteFile(cfg, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	var stdout1, stderr1 bytes.Buffer
	code1 := run(context.Background(), []string{"-config", cfg, "-importer", "test", csv}, &stdout1, &stderr1)
	if code1 != 0 {
		t.Errorf("run(no strict) = %d, want 0; stderr: %q", code1, stderr1.String())
	}

	var stdout2, stderr2 bytes.Buffer
	code2 := run(context.Background(), []string{"-config", cfg, "-importer", "test", "-strict", csv}, &stdout2, &stderr2)
	if code2 != 1 {
		t.Errorf("run(-strict) = %d, want 1; stderr: %q", code2, stderr2.String())
	}

	if stderr1.String() != stderr2.String() {
		t.Errorf("stderr mismatch:\n  no-strict: %q\n  strict:    %q", stderr1.String(), stderr2.String())
	}
}

func TestRun_AccountHintPlumbed(t *testing.T) {
	dir := t.TempDir()
	csv := writeCSV(t, dir)
	cfg := writeConfig(t, dir, "Assets:FromShape")

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"-config", cfg, "-account", "Assets:FromCLI", csv}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, want 0; stderr: %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Assets:FromCLI") {
		t.Errorf("stdout = %q, want it to contain 'Assets:FromCLI'", stdout.String())
	}
}

func TestRun_HookFlag_CommaAndRepeatCompose(t *testing.T) {
	dir := t.TempDir()
	csv := writeCSV(t, dir)

	cfg := filepath.Join(dir, "config.toml")
	content := `
[[importer]]
kind = "_beanimport_fake_imp"
name = "imp"

[[hook]]
kind = "_beanimport_fake_hook"
name = "A"

[[hook]]
kind = "_beanimport_fake_hook"
name = "B"

[[hook]]
kind = "_beanimport_fake_hook"
name = "C"
`
	if err := os.WriteFile(cfg, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"-config", cfg, "-importer", "imp", "-hook", "B,A", "-hook", "C", csv}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("run() = %d, want 0; stderr: %q", code, stderr.String())
	}

	stderrStr := stderr.String()
	posB := strings.Index(stderrStr, "sentinel from hook B")
	posA := strings.Index(stderrStr, "sentinel from hook A")
	posC := strings.Index(stderrStr, "sentinel from hook C")
	if posB == -1 || posA == -1 || posC == -1 {
		t.Fatalf("stderr missing expected sentinel messages; stderr: %q", stderrStr)
	}
	if !(posB < posA && posA < posC) {
		t.Errorf("hook chain order wrong: got B@%d A@%d C@%d, want B<A<C", posB, posA, posC)
	}
}

func TestRun_CancelledContext(t *testing.T) {
	dir := t.TempDir()
	csv := writeCSV(t, dir)
	cfg := writeConfig(t, dir, "Assets:Checking")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stdout, stderr bytes.Buffer
	code := run(ctx, []string{"-config", cfg, csv}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("run(cancelled) = %d, want 1; stderr: %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), codeCancelled) {
		t.Errorf("stderr = %q, want it to contain code %q", stderr.String(), codeCancelled)
	}
}

func TestLoadConfig_HappyPath(t *testing.T) {
	toml := `
[[importer]]
kind             = "csv"
name             = "first"
date_col         = "Date"
date_format      = "2006-01-02"
account          = "Assets:A"
default_currency = "USD"
[[importer.amount]]
col = "Amount"

[[importer]]
kind             = "csv"
name             = "second"
date_col         = "Date"
date_format      = "2006-01-02"
account          = "Assets:B"
default_currency = "USD"
[[importer.amount]]
col = "Amount"

[[hook]]
kind = "classify"
name = "cls"
`
	importers, hooks, err := loadConfig(strings.NewReader(toml), "test")
	if err != nil {
		t.Fatalf("loadConfig error: %v", err)
	}
	if len(importers) != 2 {
		t.Errorf("len(importers) = %d, want 2", len(importers))
	}
	if len(hooks) != 1 {
		t.Errorf("len(hooks) = %d, want 1", len(hooks))
	}
	if importers[0].Name() != "first" {
		t.Errorf("importers[0].Name() = %q, want %q", importers[0].Name(), "first")
	}
	if importers[1].Name() != "second" {
		t.Errorf("importers[1].Name() = %q, want %q", importers[1].Name(), "second")
	}
	if hooks[0].Name() != "cls" {
		t.Errorf("hooks[0].Name() = %q, want %q", hooks[0].Name(), "cls")
	}
}

func TestLoadConfig_UnknownTopLevelKey(t *testing.T) {
	toml := `
importers = []

[[importer]]
kind = "_beanimport_fake_imp"
name = "x"
`
	_, _, err := loadConfig(strings.NewReader(toml), "test")
	if err == nil {
		t.Fatal("loadConfig: want error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown top-level key") {
		t.Errorf("error = %q, want 'unknown top-level key'", err)
	}
	if !strings.Contains(err.Error(), "importers") {
		t.Errorf("error = %q, want to name 'importers'", err)
	}
}

func TestLoadConfig_UnknownBodyKey(t *testing.T) {
	toml := `
[[importer]]
kind             = "csv"
name             = "x"
bogus            = "bad"
date_col         = "Date"
date_format      = "2006-01-02"
account          = "Assets:A"
default_currency = "USD"
[[importer.amount]]
col = "Amount"
`
	_, _, err := loadConfig(strings.NewReader(toml), "test")
	if err == nil {
		t.Fatal("loadConfig: want error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown body key") {
		t.Errorf("error = %q, want 'unknown body key'", err)
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error = %q, want to name 'bogus'", err)
	}
}

func TestLoadConfig_MissingKind(t *testing.T) {
	toml := `
[[importer]]
name = "x"
`
	_, _, err := loadConfig(strings.NewReader(toml), "test")
	if err == nil {
		t.Fatal("loadConfig: want error, got nil")
	}
	if !strings.Contains(err.Error(), `[[importer]] #1`) {
		t.Errorf("error = %q, want '[[importer]] #1'", err)
	}
	if !strings.Contains(err.Error(), `missing "kind"`) {
		t.Errorf("error = %q, want 'missing \"kind\"'", err)
	}
}

func TestLoadConfig_MissingName(t *testing.T) {
	toml := `
[[importer]]
kind = "_beanimport_fake_imp"
`
	_, _, err := loadConfig(strings.NewReader(toml), "test")
	if err == nil {
		t.Fatal("loadConfig: want error, got nil")
	}
	if !strings.Contains(err.Error(), `[[importer]] #1`) {
		t.Errorf("error = %q, want '[[importer]] #1'", err)
	}
	if !strings.Contains(err.Error(), `missing "name"`) {
		t.Errorf("error = %q, want 'missing \"name\"'", err)
	}
}

func TestLoadConfig_FactoryError(t *testing.T) {
	toml := `
[[importer]]
kind = "_beanimport_fake_imp"
name = "broken"
fail = true
`
	_, _, err := loadConfig(strings.NewReader(toml), "test")
	if err == nil {
		t.Fatal("loadConfig: want error, got nil")
	}
	if !strings.Contains(err.Error(), `[[importer]] #1 ("broken")`) {
		t.Errorf("error = %q, want '[[importer]] #1 (\"broken\")'", err)
	}
}

func TestLoadConfig_NoImporterEntries(t *testing.T) {
	toml := `
[[hook]]
kind = "classify"
name = "cls"
`
	_, _, err := loadConfig(strings.NewReader(toml), "test")
	if err == nil {
		t.Fatal("loadConfig: want error, got nil")
	}
	if !strings.Contains(err.Error(), "no [[importer]] entries") {
		t.Errorf("error = %q, want 'no [[importer]] entries'", err)
	}
}

func makeHooks(names ...string) []hook.Hook {
	out := make([]hook.Hook, len(names))
	for i, n := range names {
		out[i] = &sentinelHook{name: n}
	}
	return out
}

func TestSelectHooks_Subset(t *testing.T) {
	all := makeHooks("A", "B", "C")
	got, err := selectHooks(all, []string{"A", "B"})
	if err != nil {
		t.Fatalf("selectHooks: %v", err)
	}
	if len(got) != 2 || got[0].Name() != "A" || got[1].Name() != "B" {
		t.Errorf("selectHooks = %v, want [A B]", got)
	}
}

func TestSelectHooks_ReorderFromArgs(t *testing.T) {
	all := makeHooks("A", "B", "C")
	got, err := selectHooks(all, []string{"C", "B", "A"})
	if err != nil {
		t.Fatalf("selectHooks: %v", err)
	}
	if len(got) != 3 || got[0].Name() != "C" || got[1].Name() != "B" || got[2].Name() != "A" {
		names := make([]string, len(got))
		for i, h := range got {
			names[i] = h.Name()
		}
		t.Errorf("selectHooks = %v, want [C B A]", names)
	}
}

func TestSelectHooks_Unknown(t *testing.T) {
	all := makeHooks("A", "B")
	_, err := selectHooks(all, []string{"X"})
	if err == nil {
		t.Fatal("selectHooks: want error for unknown hook")
	}
	if !strings.Contains(err.Error(), `unknown hook`) {
		t.Errorf("error = %q, want 'unknown hook'", err)
	}
	if !strings.Contains(err.Error(), `"X"`) {
		t.Errorf("error = %q, want name 'X'", err)
	}
}

func TestSelectHooks_NilAndEmpty(t *testing.T) {
	all := makeHooks("A", "B")

	got1, err := selectHooks(all, nil)
	if err != nil {
		t.Fatalf("selectHooks(nil): %v", err)
	}
	if len(got1) != len(all) {
		t.Errorf("selectHooks(nil) len = %d, want %d", len(got1), len(all))
	}

	got2, err := selectHooks(all, []string{})
	if err != nil {
		t.Fatalf("selectHooks([]): %v", err)
	}
	if len(got2) != len(all) {
		t.Errorf("selectHooks([]) len = %d, want %d", len(got2), len(all))
	}
}

func TestPrintDiagnostics_Format(t *testing.T) {
	diags := []ast.Diagnostic{
		{
			Code:     "err-code",
			Severity: ast.Error,
			Message:  "something broke",
			Span:     ast.Span{Start: ast.Position{Filename: "a.csv", Line: 3, Column: 1}},
		},
		{
			Code:     "warn-code",
			Severity: ast.Warning,
			Message:  "minor issue",
		},
	}

	var buf bytes.Buffer
	printDiagnostics(&buf, diags)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != len(diags) {
		t.Fatalf("printed %d lines, want %d; output: %q", len(lines), len(diags), buf.String())
	}
	for i, d := range diags {
		want := d.String()
		if lines[i] != want {
			t.Errorf("line %d = %q, want %q", i, lines[i], want)
		}
	}
}

func TestPrintDiagnostics_NoOp(t *testing.T) {
	var buf bytes.Buffer
	printDiagnostics(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("printDiagnostics(nil) wrote %q, want empty", buf.String())
	}
}
