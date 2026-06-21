package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func touchLedger(t *testing.T) (rootDir, ledgerPath string) {
	t.Helper()
	rootDir = t.TempDir()
	ledgerPath = filepath.Join(rootDir, "main.beancount")
	if err := os.WriteFile(ledgerPath, nil, 0o644); err != nil {
		t.Fatalf("writing ledger stub: %v", err)
	}
	return rootDir, ledgerPath
}

func runCLI(t *testing.T, args []string, stdin string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exit := run(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)
	return exit, stdout.String(), stderr.String()
}

func TestRun_MissingLedgerFlag(t *testing.T) {
	exit, _, stderr := runCLI(t, nil, "")
	if exit != 2 {
		t.Errorf("exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr, "--ledger") {
		t.Errorf("stderr = %q, want mention of --ledger", stderr)
	}
}

func TestRun_MissingLedgerFile(t *testing.T) {
	exit, _, stderr := runCLI(t, []string{"--ledger", "/no/such.beancount"}, "")
	if exit != 2 {
		t.Errorf("exit = %d, want 2", exit)
	}
	if stderr == "" {
		t.Errorf("stderr empty, want stat error message")
	}
}

func TestRun_StdinPriceDirective(t *testing.T) {
	root, ledger := touchLedger(t)
	src := "2024-01-15 price USD 110 JPY\n"
	exit, stdout, stderr := runCLI(t, []string{"--ledger", ledger}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	dest := filepath.Join(root, "quotes/USD/202401.beancount")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading destination: %v", err)
	}
	want := "2024-01-15 price USD 110 JPY\n"
	if string(got) != want {
		t.Errorf("destination = %q, want %q", string(got), want)
	}
	if !strings.Contains(stderr, "quotes/USD/202401.beancount") {
		t.Errorf("stderr = %q, want path in stats", stderr)
	}
	if !strings.Contains(stderr, "written=1") {
		t.Errorf("stderr = %q, want written=1", stderr)
	}
	if !strings.Contains(stderr, "total: written=1") {
		t.Errorf("stderr = %q, want total written=1", stderr)
	}
	if !strings.Contains(stderr, "passthrough=0") {
		t.Errorf("stderr = %q, want passthrough=0", stderr)
	}
}

func TestRun_FileTransactionsGrouped(t *testing.T) {
	root, ledger := touchLedger(t)
	in := filepath.Join(t.TempDir(), "in.beancount")
	src := `2024-01-10 open Assets:Bank USD
2024-01-11 open Assets:Cash USD
2024-01-12 * "lunch"
  Assets:Bank   -10.00 USD
  Assets:Cash    10.00 USD
`
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatalf("writing input: %v", err)
	}
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, in}, "")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	bankPath := filepath.Join(root, "transactions/Assets/Bank/202401.beancount")
	cashPath := filepath.Join(root, "transactions/Assets/Cash/202401.beancount")
	if _, err := os.Stat(bankPath); err != nil {
		t.Errorf("bank dest missing: %v", err)
	}
	if _, err := os.Stat(cashPath); err != nil {
		t.Errorf("cash dest missing: %v", err)
	}
	if !strings.Contains(stderr, "transactions/Assets/Bank/202401.beancount") {
		t.Errorf("stderr missing bank path: %q", stderr)
	}
	if !strings.Contains(stderr, "transactions/Assets/Cash/202401.beancount") {
		t.Errorf("stderr missing cash path: %q", stderr)
	}
	if !strings.Contains(stderr, "total: written=3") {
		t.Errorf("stderr = %q, want total written=3", stderr)
	}
}

func TestRun_PassThroughDefaultErrors(t *testing.T) {
	root, ledger := touchLedger(t)
	src := `option "title" "x"
`
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger}, src)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stderr, "non-routable") {
		t.Errorf("stderr = %q, want non-routable mention", stderr)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("listing root: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "main.beancount" {
			t.Errorf("unexpected entry %q under root", e.Name())
		}
	}
}

func TestRun_PassThroughEmits(t *testing.T) {
	_, ledger := touchLedger(t)
	src := `option "title" "x"
`
	exit, stdout, stderr := runCLI(t, []string{"--ledger", ledger, "--pass-through"}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stdout, `option "title" "x"`) {
		t.Errorf("stdout = %q, want option directive", stdout)
	}
	if !strings.Contains(stderr, "passthrough=1") {
		t.Errorf("stderr = %q, want passthrough=1", stderr)
	}
}

func TestRun_MultipleSourcesNoInterleave(t *testing.T) {
	_, ledger := touchLedger(t)
	dir := t.TempDir()
	a := filepath.Join(dir, "a.beancount")
	b := filepath.Join(dir, "b.beancount")
	if err := os.WriteFile(a, []byte(`option "title" "first-a"
option "operating_currency" "AAA"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte(`option "title" "first-b"
option "operating_currency" "BBB"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := runCLI(t, []string{"--ledger", ledger, "--pass-through", a, b}, "")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	lastA := -1
	firstB := len(stdout)
	for _, marker := range []string{"first-a", "AAA"} {
		if i := strings.Index(stdout, marker); i > lastA {
			lastA = i
		} else if i < 0 {
			t.Fatalf("stdout missing %q: %q", marker, stdout)
		}
	}
	for _, marker := range []string{"first-b", "BBB"} {
		i := strings.Index(stdout, marker)
		if i < 0 {
			t.Fatalf("stdout missing %q: %q", marker, stdout)
		}
		if i < firstB {
			firstB = i
		}
	}
	if lastA >= firstB {
		t.Errorf("interleaved stdout = %q (lastA=%d firstB=%d)", stdout, lastA, firstB)
	}
	if !strings.Contains(stderr, "passthrough=4") {
		t.Errorf("stderr = %q, want passthrough=4", stderr)
	}
}

func TestRun_InputParseError(t *testing.T) {
	root, ledger := touchLedger(t)
	// Mix a well-formed routable directive with a malformed line. Without
	// the Error guard in emitDiagnostics, the price would be routed and a
	// quotes/ subtree would appear under root; asserting its absence proves
	// the "no destination files are touched on error" guarantee.
	src := "2024-01-15 price USD 110 JPY\nthis is not valid @@@\n"
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger}, src)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", exit, stderr)
	}
	if stderr == "" {
		t.Errorf("stderr empty, want diagnostic")
	}
	if _, err := os.Stat(filepath.Join(root, "quotes")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("quotes/ exists under root after parse error: err=%v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("listing root: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "main.beancount" {
			t.Errorf("unexpected entry %q under root after parse error", e.Name())
		}
	}
}

func TestRun_RelativeIncludeRejected(t *testing.T) {
	_, ledger := touchLedger(t)
	src := `include "other.beancount"
`
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger}, src)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stderr, "other.beancount") {
		t.Errorf("stderr = %q, want diagnostic naming the unresolved include", stderr)
	}
}

func TestRun_QuietSuppressesStats(t *testing.T) {
	_, ledger := touchLedger(t)
	src := "2024-01-15 price USD 110 JPY\n"
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "--quiet"}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty under --quiet", stderr)
	}
}

func TestRun_DashAsStdin(t *testing.T) {
	root, ledger := touchLedger(t)
	src := "2024-01-15 price USD 110 JPY\n"
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "-"}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	dest := filepath.Join(root, "quotes/USD/202401.beancount")
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("destination missing: %v", err)
	}
}

func TestRun_QuietDoesNotSuppressErrors(t *testing.T) {
	_, ledger := touchLedger(t)
	src := "this is not valid @@@\n"
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "--quiet"}, src)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", exit, stderr)
	}
	if stderr == "" {
		t.Errorf("stderr empty under --quiet, want error diagnostic to surface")
	}
}

func TestRun_HelpExitsZero(t *testing.T) {
	exit, _, stderr := runCLI(t, []string{"-h"}, "")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stderr, "Usage: beanfile") {
		t.Errorf("stderr = %q, want usage banner", stderr)
	}
}

func TestRun_AbsoluteIncludeResolves(t *testing.T) {
	root, ledger := touchLedger(t)
	incDir := t.TempDir()
	incPath := filepath.Join(incDir, "inc.beancount")
	if err := os.WriteFile(incPath, []byte("2024-01-15 price USD 110 JPY\n"), 0o644); err != nil {
		t.Fatalf("writing include: %v", err)
	}
	src := fmt.Sprintf("include %q\n", incPath)
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	dest := filepath.Join(root, "quotes/USD/202401.beancount")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading destination: %v", err)
	}
	if !strings.Contains(string(got), "2024-01-15 price USD 110 JPY") {
		t.Errorf("destination = %q, want included price directive", string(got))
	}
}

// seedLedger writes a ledger root that includes every supplied
// destination file (each authored with provided contents) so the dedup
// index walks them. The ledger root and root directory are returned.
func seedLedger(t *testing.T, files map[string]string) (rootDir, ledgerPath string) {
	t.Helper()
	rootDir = t.TempDir()
	ledgerPath = filepath.Join(rootDir, "main.beancount")
	relPaths := make([]string, 0, len(files))
	for relPath := range files {
		relPaths = append(relPaths, relPath)
	}
	sort.Strings(relPaths)
	var includes strings.Builder
	for _, relPath := range relPaths {
		abs := filepath.Join(rootDir, relPath)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte(files[relPath]), 0o644); err != nil {
			t.Fatalf("writing %q: %v", abs, err)
		}
		fmt.Fprintf(&includes, "include %q\n", abs)
	}
	if err := os.WriteFile(ledgerPath, []byte(includes.String()), 0o644); err != nil {
		t.Fatalf("writing ledger %q: %v", ledgerPath, err)
	}
	return rootDir, ledgerPath
}

func TestRun_DedupSkipsExisting(t *testing.T) {
	priceLine := "2024-01-15 price USD 110 JPY\n"
	root, ledger := seedLedger(t, map[string]string{
		"quotes/USD/202401.beancount": priceLine,
	})
	dest := filepath.Join(root, "quotes/USD/202401.beancount")
	before, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading seeded dest: %v", err)
	}

	exit, _, stderr := runCLI(t, []string{"--ledger", ledger}, priceLine)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}

	after, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading dest after run: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("destination changed; before=%q after=%q", string(before), string(after))
	}
	if !strings.Contains(stderr, "skipped=1") {
		t.Errorf("stderr = %q, want skipped=1", stderr)
	}
	if !strings.Contains(stderr, "total: written=0 commented=0 skipped=1") {
		t.Errorf("stderr = %q, want total written=0 commented=0 skipped=1", stderr)
	}
}

func TestRun_DedupCrossPostingComments(t *testing.T) {
	openLine := "2024-01-10 open Assets:Bank USD\n"
	root, ledger := seedLedger(t, map[string]string{
		// Same Open directive at a non-default destination.
		"transactions/Assets/Other/202401.beancount": openLine,
	})

	exit, _, stderr := runCLI(t, []string{"--ledger", ledger}, openLine)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}

	dest := filepath.Join(root, "transactions/Assets/Bank/202401.beancount")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading bank dest: %v", err)
	}
	if !strings.Contains(string(got), "; 2024-01-10 open Assets:Bank") {
		t.Errorf("bank dest = %q, want commented Open", string(got))
	}
	if !strings.Contains(stderr, "transactions/Assets/Bank/202401.beancount") {
		t.Errorf("stderr missing bank path: %q", stderr)
	}
	if !strings.Contains(stderr, "commented=1") {
		t.Errorf("stderr = %q, want commented=1", stderr)
	}
	if !strings.Contains(stderr, "total: written=0 commented=1 skipped=0") {
		t.Errorf("stderr = %q, want total written=0 commented=1", stderr)
	}
}

func TestRun_DedupStreamInternal(t *testing.T) {
	_, ledger := touchLedger(t)
	src := "2024-01-15 price USD 110 JPY\n2024-01-15 price USD 110 JPY\n"
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stderr, "total: written=1 commented=0 skipped=1") {
		t.Errorf("stderr = %q, want total written=1 commented=0 skipped=1", stderr)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "beanfile.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return p
}

func TestRun_ExplicitConfigChangesAmountColumn(t *testing.T) {
	root, ledger := touchLedger(t)
	cfgPath := writeConfig(t, `
[routes.format]
amount_column = 30
`)
	src := `2024-01-12 * "lunch"
  Assets:Bank   -10.00 USD
  Assets:Cash    10.00 USD
`
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "--config", cfgPath}, src)
	if exit != 0 {
		t.Fatalf("exit = %d; stderr=%q", exit, stderr)
	}
	dest := filepath.Join(root, "transactions/Assets/Bank/202401.beancount")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	// With amount_column=30 the right edge of the amount falls at
	// column 30 (1-based). The default of 52 would push it out further.
	for _, line := range strings.Split(string(got), "\n") {
		if !strings.Contains(line, "USD") || !strings.Contains(line, "10.00") {
			continue
		}
		// The amount's last character of the numeric part must end
		// before column 30 + len(" USD") slack. A regression that
		// ignored amount_column would push the number to align at 52.
		usdIdx := strings.Index(line, " USD")
		if usdIdx < 0 || usdIdx >= 50 {
			t.Errorf("amount alignment unexpected (USD at %d): %q", usdIdx, line)
		}
	}
}

func TestRun_AutoDiscoveredConfig(t *testing.T) {
	root, ledger := touchLedger(t)
	// Stage a beanfile.toml in a temp CWD; the CLI auto-discovers it.
	cwd := t.TempDir()
	tomlPath := filepath.Join(cwd, "beanfile.toml")
	if err := os.WriteFile(tomlPath, []byte(`
[[routes.account.override]]
prefix   = "Assets:Bank"
template = "auto/{account}/{date}.beancount"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Chdir(cwd)

	src := "2024-01-10 open Assets:Bank USD\n"
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger}, src)
	if exit != 0 {
		t.Fatalf("exit = %d; stderr=%q", exit, stderr)
	}
	dest := filepath.Join(root, "auto/Assets/Bank/202401.beancount")
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("expected auto-discovered destination %q: %v", dest, err)
	}
}

func TestRun_IDKeysMatchCommentsCrossPath(t *testing.T) {
	openLine := `2024-01-10 open Assets:Bank USD
  import-id: "abc"
`
	root, ledger := seedLedger(t, map[string]string{
		"transactions/Assets/Bank/202401.beancount": openLine,
	})
	cfgPath := writeConfig(t, `
[routes.transaction]
id_keys = ["import-id"]
`)
	// Different account, same import-id → id-key match against the
	// active entry under transactions/Assets/Other path. We probe a
	// different account so the exact-equality branch can't fire (different
	// account values), forcing the id branch to be the only path.
	probe := `2024-02-20 open Assets:Other USD
  import-id: "abc"
`
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "--config", cfgPath}, probe)
	if exit != 0 {
		t.Fatalf("exit = %d; stderr=%q", exit, stderr)
	}
	// The destination of the probe is transactions/Assets/Other/202402.beancount.
	destOther := filepath.Join(root, "transactions/Assets/Other/202402.beancount")
	got, err := os.ReadFile(destOther)
	if err != nil {
		t.Fatalf("read other dest: %v", err)
	}
	// Because import-id matched against the seeded active entry under a
	// different path, this directive must land commented-out.
	if !strings.Contains(string(got), "; 2024-02-20 open Assets:Other") {
		t.Errorf("expected commented Open at other dest, got: %q", string(got))
	}
	if !strings.Contains(stderr, "commented=1") {
		t.Errorf("stderr = %q, want commented=1", stderr)
	}
}

func TestRun_AccountOverrideRedirectsPath(t *testing.T) {
	root, ledger := touchLedger(t)
	cfgPath := writeConfig(t, `
[[routes.account.override]]
prefix   = "Assets:JP"
template = "japan/{account}/{date}.beancount"
`)
	src := "2024-01-10 open Assets:JP:Cash USD\n"
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "--config", cfgPath}, src)
	if exit != 0 {
		t.Fatalf("exit = %d; stderr=%q", exit, stderr)
	}
	dest := filepath.Join(root, "japan/Assets/JP/Cash/202401.beancount")
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("expected override destination %q: %v", dest, err)
	}
}

func TestRun_CommodityOverrideRedirectsPath(t *testing.T) {
	root, ledger := touchLedger(t)
	cfgPath := writeConfig(t, `
[[routes.price.override]]
commodity = "JPY"
template  = "yen/{commodity}/{date}.beancount"
`)
	src := "2024-01-15 price JPY 0.0066 USD\n"
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "--config", cfgPath}, src)
	if exit != 0 {
		t.Fatalf("exit = %d; stderr=%q", exit, stderr)
	}
	dest := filepath.Join(root, "yen/JPY/202401.beancount")
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("expected override destination %q: %v", dest, err)
	}
}

func TestRun_FormatFlagOverridesTOML(t *testing.T) {
	root, ledger := touchLedger(t)
	cfgPath := writeConfig(t, `
[routes.format]
amount_column = 30
`)
	src := `2024-01-12 * "lunch"
  Assets:Bank   -10.00 USD
  Assets:Cash    10.00 USD
`
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "--config", cfgPath, "--format-amount-column", "70"}, src)
	if exit != 0 {
		t.Fatalf("exit = %d; stderr=%q", exit, stderr)
	}
	dest := filepath.Join(root, "transactions/Assets/Bank/202401.beancount")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	// CLI flag set amount_column=70 (overrides the TOML's 30). The
	// amount's right edge must therefore align well past column 30.
	for _, line := range strings.Split(string(got), "\n") {
		if !strings.Contains(line, "USD") || !strings.Contains(line, "10.00") {
			continue
		}
		usdIdx := strings.Index(line, " USD")
		if usdIdx < 50 {
			t.Errorf("USD at index %d, want >=50 (CLI 70 should beat TOML 30): %q", usdIdx, line)
		}
	}
}

func TestRun_BadConfigRejectedClearly(t *testing.T) {
	_, ledger := touchLedger(t)
	cfgPath := writeConfig(t, `
[routes.account]
order = "asc"
`)
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "--config", cfgPath}, "")
	if exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr, `"asc"`) {
		t.Errorf("stderr = %q, want quoted \"asc\" mention", stderr)
	}
}

func TestRun_UnknownConfigKeyRejected(t *testing.T) {
	_, ledger := touchLedger(t)
	cfgPath := writeConfig(t, `
[routes.account]
template = "x"
nonsense = 42
`)
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "--config", cfgPath}, "")
	if exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr, "nonsense") {
		t.Errorf("stderr = %q, want nonsense mention", stderr)
	}
}

// TestRun_TransactionRouteAccountStripped verifies the end-to-end
// route-account pipeline: a Transaction carrying route-account metadata
// reaches its overridden destination, and the emitted file does not
// contain the route-account key.
func TestRun_TransactionRouteAccountStripped(t *testing.T) {
	root, ledger := touchLedger(t)
	// A transaction whose route-account override points to Assets:Savings.
	// Without stripping, the metadata key would appear in the emitted file.
	src := `2024-03-15 * "Transfer"
  route-account: "Assets:Savings"
  Assets:Savings  100.00 USD
  Assets:Bank    -100.00 USD
`
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	// The transaction is routed to the Assets:Savings destination.
	dest := filepath.Join(root, "transactions/Assets/Savings/202403.beancount")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading destination %q: %v", dest, err)
	}
	if strings.Contains(string(got), "route-account") {
		t.Errorf("emitted file still contains route-account key:\n%s", string(got))
	}
	if !strings.Contains(string(got), "Transfer") {
		t.Errorf("emitted file missing the transaction narration:\n%s", string(got))
	}
	if !strings.Contains(stderr, "written=1") {
		t.Errorf("stderr = %q, want written=1", stderr)
	}
}

// TestRun_OrderAppend verifies that --order=append always places new
// directives at the end of the destination file, regardless of their dates.
// The destination is pre-seeded with a 2024-01-15 price so that an incoming
// 2024-01-01 price (which is chronologically earlier) would be inserted
// BEFORE the existing directive under ascending order, but must land AFTER it
// under append order. A regression that hardcoded OrderAscending would place
// the new directive before the existing one.
func TestRun_OrderAppend(t *testing.T) {
	// Pre-seed the destination with a mid-month price.
	existingLine := "2024-01-15 price USD 115 JPY\n"
	root, ledger := seedLedger(t, map[string]string{
		"quotes/USD/202401.beancount": existingLine,
	})

	// Input: an older-dated price that ascending order would insert before the
	// existing 2024-01-15 line. Append order must ignore the date and place it
	// unconditionally at end-of-file.
	src := "2024-01-01 price USD 100 JPY\n"
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "--order=append"}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	dest := filepath.Join(root, "quotes/USD/202401.beancount")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading destination %q: %v", dest, err)
	}
	// Both directives must be present.
	if !strings.Contains(string(got), "2024-01-15 price USD 115 JPY") {
		t.Errorf("dest = %q, want existing 2024-01-15 price directive", string(got))
	}
	if !strings.Contains(string(got), "2024-01-01 price USD 100 JPY") {
		t.Errorf("dest = %q, want new 2024-01-01 price directive", string(got))
	}
	// Append order: the new directive (2024-01-01) must appear AFTER the
	// pre-existing directive (2024-01-15) in the file.
	// Under ascending order, 2024-01-01 would be inserted before 2024-01-15.
	idx15 := strings.Index(string(got), "2024-01-15")
	idx01 := strings.Index(string(got), "2024-01-01")
	if idx15 < 0 || idx01 < 0 {
		t.Fatalf("one or both directives missing in output: %q", string(got))
	}
	if idx01 <= idx15 {
		t.Errorf("append order not honoured: 2024-01-01 at byte %d, 2024-01-15 at byte %d; want 01 AFTER 15", idx01, idx15)
	}
	if !strings.Contains(stderr, "written=1") {
		t.Errorf("stderr = %q, want written=1", stderr)
	}
}

// TestRun_OrderDescending verifies that --order=descending places newer
// directives before older ones in an existing destination file. The
// destination is pre-seeded with a 2024-01-15 price; an incoming 2024-01-20
// price (newer) must land BEFORE it. A regression that used ascending order
// would place the newer directive after the existing one.
func TestRun_OrderDescending(t *testing.T) {
	// Pre-seed the destination with a mid-month price.
	existingLine := "2024-01-15 price USD 115 JPY\n"
	root, ledger := seedLedger(t, map[string]string{
		"quotes/USD/202401.beancount": existingLine,
	})

	// Input: a newer-dated price that descending order must insert before the
	// existing 2024-01-15 line. Under ascending order it would go after.
	src := "2024-01-20 price USD 120 JPY\n"
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "--order=descending"}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	dest := filepath.Join(root, "quotes/USD/202401.beancount")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading destination %q: %v", dest, err)
	}
	// Both directives must be present.
	if !strings.Contains(string(got), "2024-01-15 price USD 115 JPY") {
		t.Errorf("dest = %q, want existing 2024-01-15 price directive", string(got))
	}
	if !strings.Contains(string(got), "2024-01-20 price USD 120 JPY") {
		t.Errorf("dest = %q, want new 2024-01-20 price directive", string(got))
	}
	// Descending order: the newer directive (2024-01-20) must appear BEFORE
	// the older one (2024-01-15) in the file.
	// Under ascending order, 2024-01-20 would be placed after 2024-01-15.
	idx20 := strings.Index(string(got), "2024-01-20")
	idx15 := strings.Index(string(got), "2024-01-15")
	if idx20 < 0 || idx15 < 0 {
		t.Fatalf("one or both directives missing in output: %q", string(got))
	}
	if idx20 >= idx15 {
		t.Errorf("descending order not honoured: 2024-01-20 at byte %d, 2024-01-15 at byte %d; want 20 BEFORE 15", idx20, idx15)
	}
	if !strings.Contains(stderr, "written=1") {
		t.Errorf("stderr = %q, want written=1", stderr)
	}
}

// TestRun_FilePatternYYYYmmdd verifies that --file-pattern=YYYYmmdd embeds the
// full calendar date (year, month, day) in the destination path. A Price
// directive dated 2024-03-07 must land in a path containing "20240307".
func TestRun_FilePatternYYYYmmdd(t *testing.T) {
	root, ledger := touchLedger(t)
	src := "2024-03-07 price USD 150 JPY\n"
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "--file-pattern=YYYYmmdd"}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	dest := filepath.Join(root, "quotes/USD/20240307.beancount")
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("expected destination %q: %v", dest, err)
	}
	if !strings.Contains(stderr, "quotes/USD/20240307.beancount") {
		t.Errorf("stderr = %q, want destination path with day in stats", stderr)
	}
}

// TestRun_DryRunSinglePrice exercises the dry-run preview format on a
// single-line directive. The destination file MUST NOT be
// created; stdout MUST carry the "--- <path> ---" header followed by a
// "+ "-prefixed render of the directive; stderr MUST still report
// stats so users can see written/commented/skipped counts.
func TestRun_DryRunSinglePrice(t *testing.T) {
	root, ledger := touchLedger(t)
	src := "2024-01-15 price USD 110 JPY\n"
	exit, stdout, stderr := runCLI(t, []string{"--ledger", ledger, "--dry-run"}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	dest := filepath.Join(root, "quotes/USD/202401.beancount")
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("destination created under --dry-run (err=%v)", err)
	}
	wantHeader := "--- quotes/USD/202401.beancount ---"
	if !strings.Contains(stdout, wantHeader) {
		t.Errorf("stdout = %q, want header %q", stdout, wantHeader)
	}
	if !strings.Contains(stdout, "+ 2024-01-15 price USD 110 JPY") {
		t.Errorf("stdout = %q, want active-prefixed price line", stdout)
	}
	if !strings.Contains(stderr, "written=1") {
		t.Errorf("stderr = %q, want written=1", stderr)
	}
}

// TestRun_DryRunMultilineTransaction verifies that every line of a
// multi-line directive (a Transaction with two postings) is prefixed
// with "+ " in dry-run output. The original directive header and the
// indented posting continuation lines must each carry the prefix.
func TestRun_DryRunMultilineTransaction(t *testing.T) {
	root, ledger := touchLedger(t)
	src := `2024-01-12 * "lunch"
  Assets:Bank   -10.00 USD
  Assets:Cash    10.00 USD
`
	exit, stdout, stderr := runCLI(t, []string{"--ledger", ledger, "--dry-run"}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	// No destination file should be created (transactions/ subtree absent).
	if _, err := os.Stat(filepath.Join(root, "transactions")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("transactions/ subtree created under --dry-run (err=%v)", err)
	}
	// Each of the three lines must be prefixed with "+ ".
	for _, want := range []string{
		`+ 2024-01-12 * "lunch"`,
		"+   Assets:Bank",
		"+   Assets:Cash",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

// TestRun_DryRunCommentedPrefix verifies that a directive that would
// land as a commented insert (active equivalent at another path) is
// rendered with the ";+ " prefix in dry-run mode and that no actual
// commented marker reaches the existing file.
func TestRun_DryRunCommentedPrefix(t *testing.T) {
	openLine := "2024-01-10 open Assets:Bank USD\n"
	root, ledger := seedLedger(t, map[string]string{
		"transactions/Assets/Other/202401.beancount": openLine,
	})
	exit, stdout, stderr := runCLI(t, []string{"--ledger", ledger, "--dry-run"}, openLine)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	// The bank destination must NOT have been created.
	dest := filepath.Join(root, "transactions/Assets/Bank/202401.beancount")
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("destination created under --dry-run (err=%v)", err)
	}
	if !strings.Contains(stdout, "--- transactions/Assets/Bank/202401.beancount ---") {
		t.Errorf("stdout = %q, want bank dest header", stdout)
	}
	if !strings.Contains(stdout, ";+ 2024-01-10 open Assets:Bank") {
		t.Errorf("stdout = %q, want commented-prefixed open line", stdout)
	}
	if !strings.Contains(stderr, "commented=1") {
		t.Errorf("stderr = %q, want commented=1", stderr)
	}
}

// TestRun_DryRunSkippedNothingPrinted exercises an input fully shadowed
// by the existing ledger: no inserts, only skips. The dry-run header
// must NOT appear (no insertions to preview), but stats still describe
// the skip.
func TestRun_DryRunSkippedNothingPrinted(t *testing.T) {
	priceLine := "2024-01-15 price USD 110 JPY\n"
	_, ledger := seedLedger(t, map[string]string{
		"quotes/USD/202401.beancount": priceLine,
	})
	exit, stdout, stderr := runCLI(t, []string{"--ledger", ledger, "--dry-run"}, priceLine)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	// Skip-only runs have no insertions, no pass-through directives, and
	// no other stdout output, so stdout must be empty.
	if stdout != "" {
		t.Errorf("stdout = %q, want empty for skip-only run", stdout)
	}
	if !strings.Contains(stderr, "total: written=0 commented=0 skipped=1") {
		t.Errorf("stderr = %q, want skip-only total", stderr)
	}
}

// TestRun_DryRunQuietSuppressesStats confirms --dry-run honours --quiet
// for stats while still emitting the patch preview to stdout.
func TestRun_DryRunQuietSuppressesStats(t *testing.T) {
	_, ledger := touchLedger(t)
	src := "2024-01-15 price USD 110 JPY\n"
	exit, stdout, stderr := runCLI(t, []string{"--ledger", ledger, "--dry-run", "--quiet"}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty under --quiet", stderr)
	}
	if !strings.Contains(stdout, "+ 2024-01-15 price USD 110 JPY") {
		t.Errorf("stdout = %q, want preview line", stdout)
	}
}

// TestRun_DryRunWithPassThrough pins the documented stream contract
// for the combined --dry-run --pass-through mode: non-routable
// directives are emitted on stdout in input order during the
// directive-processing loop, before any "--- <path> ---" preview
// blocks. Sources are processed sequentially so the two streams never
// interleave: pass-through output finishes first, dry-run blocks
// follow.
func TestRun_DryRunWithPassThrough(t *testing.T) {
	root, ledger := touchLedger(t)
	src := `option "title" "x"
2024-01-15 price USD 110 JPY
`
	exit, stdout, stderr := runCLI(t, []string{"--ledger", ledger, "--dry-run", "--pass-through"}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "quotes")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("destination created under --dry-run (err=%v)", err)
	}
	idxOption := strings.Index(stdout, `option "title" "x"`)
	idxHeader := strings.Index(stdout, "--- quotes/USD/202401.beancount ---")
	if idxOption < 0 {
		t.Fatalf("stdout missing pass-through option:\n%s", stdout)
	}
	if idxHeader < 0 {
		t.Fatalf("stdout missing dry-run header:\n%s", stdout)
	}
	if idxOption >= idxHeader {
		t.Errorf("expected pass-through before dry-run header; option at %d, header at %d:\n%s",
			idxOption, idxHeader, stdout)
	}
	if !strings.Contains(stdout, "+ 2024-01-15 price USD 110 JPY") {
		t.Errorf("stdout missing dry-run preview line:\n%s", stdout)
	}
	if !strings.Contains(stderr, "passthrough=1") {
		t.Errorf("stderr = %q, want passthrough=1", stderr)
	}
}

// TestRun_RouteWarningWithPercentByte exercises the route warning
// path with a '%' byte in the warning's argument data: a transaction
// whose route-account is wrong-kind triggers a fall-through warning,
// and the warning's quoted-narration argument contains a literal '%'.
// The captured stderr must render the literal '%' as text and must
// not contain a "%!" format-failure marker. The test serves as a
// sentinel against regressions in the warn-sink wiring (currently
// pre-formats with fmt.Sprintf so any '%' in arguments stays inert
// once the resulting string is embedded under a fixed "%s").
func TestRun_RouteWarningWithPercentByte(t *testing.T) {
	_, ledger := touchLedger(t)
	// A transaction that triggers a Rule-1 warning: route-account
	// metadata is present but its kind is bool, not string. The
	// transaction's narration carries a literal '%' that shows up in
	// the warning's quoted label.
	src := `2024-03-15 * "100% sure"
  route-account: TRUE
  Assets:Cash    -5.00 USD
  Expenses:Food   5.00 USD
`
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stderr, "100% sure") {
		t.Errorf("stderr = %q, want literal %%-bearing narration in warning", stderr)
	}
	// A misinterpreted format verb would surface as "%!s(MISSING)"
	// or similar runtime-format artifact.
	if strings.Contains(stderr, "%!") {
		t.Errorf("stderr = %q, contains %%! format-failure marker", stderr)
	}
}

// TestRun_DryRunMultiplePathsSorted verifies that when an input lands
// at multiple destinations, the dry-run blocks come out in
// lexicographically sorted order so the user gets a stable preview
// regardless of input order.
func TestRun_DryRunMultiplePathsSorted(t *testing.T) {
	_, ledger := touchLedger(t)
	src := `2024-01-15 price USD 110 JPY
2024-01-10 open Assets:Bank USD
2024-01-15 price JPY 0.0066 USD
`
	exit, stdout, stderr := runCLI(t, []string{"--ledger", ledger, "--dry-run"}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	wants := []string{
		"--- quotes/JPY/202401.beancount ---",
		"--- quotes/USD/202401.beancount ---",
		"--- transactions/Assets/Bank/202401.beancount ---",
	}
	idx := -1
	for _, h := range wants {
		i := strings.Index(stdout, h)
		if i < 0 {
			t.Errorf("stdout missing %q:\n%s", h, stdout)
			continue
		}
		if i <= idx {
			t.Errorf("headers out of order at %q (idx=%d, prev=%d): %s", h, i, idx, stdout)
		}
		idx = i
	}
}

// TestRun_MessyExistingFilePreserved is an end-to-end regression for
// the merger's byte-exact preservation invariant: a destination file
// pre-seeded with a blank-line-rich, comment-rich layout MUST keep
// every byte outside the merger's own insertion intact, with the new
// directive landing in the right place. The expected output is built
// explicitly so any spacing or ordering regression — collapsed blank
// lines, lost comment, reordered directive — fails the diff.
func TestRun_MessyExistingFilePreserved(t *testing.T) {
	// The seeded file mixes:
	//   * an undated header comment block
	//   * a blank-line gap
	//   * a dated directive
	//   * a multi-blank-line gap
	//   * a commented annotation that does NOT shape-match (no date
	//     after the prefix), so it's treated as plain comment text
	//   * a second dated directive
	//   * trailing blank lines
	const existing = `; --- header notes ---
;
; This file is hand-edited by humans.

2024-02-05 balance Assets:Bank 100.00 USD


; ad-hoc note about the bank account
2024-02-25 balance Assets:Bank 200.00 USD


`
	root, ledger := seedLedger(t, map[string]string{
		"transactions/Assets/Bank/202402.beancount": existing,
	})
	dest := filepath.Join(root, "transactions/Assets/Bank/202402.beancount")

	src := "2024-02-15 balance Assets:Bank 150.00 USD\n"
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading destination: %v", err)
	}
	// Expected layout: under ascending order the merger places the new
	// directive at existing[0].endOff — the byte offset immediately
	// past 2024-02-05's line terminator, BEFORE the trailing blank
	// gap. With B=false (the default) the merger contributes no
	// padding on either side: the new directive's own "\n" terminates
	// it cleanly, and the original "\n\n" gap that followed
	// 2024-02-05 now follows the new directive instead. Every other
	// byte (header comments, intra-content blank lines, plain-comment
	// annotation, trailing blank lines) is preserved exactly.
	const want = `; --- header notes ---
;
; This file is hand-edited by humans.

2024-02-05 balance Assets:Bank 100.00 USD
2024-02-15 balance Assets:Bank 150.00 USD


; ad-hoc note about the bank account
2024-02-25 balance Assets:Bank 200.00 USD


`
	if diff := cmp.Diff(want, string(got)); diff != "" {
		t.Errorf("destination not byte-preserved (-want +got):\n%s", diff)
	}
}

// TestRun_StatsFormattingAlignment verifies that per-file stats lines
// align their "written=" columns regardless of path-length variation,
// and that the "total:" line is on its own.
func TestRun_StatsFormattingAlignment(t *testing.T) {
	root, ledger := touchLedger(t)
	// Two destinations of distinctly different path lengths so we can
	// see the alignment column in action.
	src := `2024-01-10 open Assets:Bank USD
2024-01-15 price USD 110 JPY
`
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "quotes/USD/202401.beancount")); err != nil {
		t.Fatalf("price destination missing: %v", err)
	}
	// Verify that the per-path lines and the total line are present and
	// in the expected relative order: per-path lines (sorted), then total.
	// We classify each line once, recording both the per-path lines'
	// indices and the total's index so the ordering check needs no second
	// pass over lines.
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	var perPath []string
	var perPathIdx []int
	totalIdx := -1
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "beanfile: total:"):
			totalIdx = i
		case strings.HasPrefix(line, "beanfile: ") && strings.Contains(line, "written="):
			perPath = append(perPath, line)
			perPathIdx = append(perPathIdx, i)
		}
	}
	if totalIdx < 0 {
		t.Fatalf("stderr missing total: line:\n%s", stderr)
	}
	if len(perPath) != 2 {
		t.Errorf("got %d per-path lines, want 2:\n%s", len(perPath), stderr)
	}
	// Per-path lines must precede the total line.
	for i, idx := range perPathIdx {
		if idx >= totalIdx {
			t.Errorf("per-path line at index %d (%q) appears at or after total at %d", idx, perPath[i], totalIdx)
		}
	}
	// All per-path "written=" tokens must align in the same column. The
	// column anchor is the leading "beanfile: " literal plus the padded
	// "<path>:" field; if alignment regressed the column would jitter.
	if len(perPath) >= 2 {
		col := strings.Index(perPath[0], "written=")
		for _, line := range perPath[1:] {
			if c := strings.Index(line, "written="); c != col {
				t.Errorf("written= column not aligned: %d vs %d in %q", c, col, line)
			}
		}
	}
}

func TestRun_DedupCrossPostingCascade(t *testing.T) {
	openLine := "2024-01-10 open Assets:Bank USD\n"
	root, ledger := seedLedger(t, map[string]string{
		"transactions/Assets/Other/202401.beancount": openLine,
	})

	// Two equivalent inputs: first writes commented (active match
	// elsewhere), second is then skipped because the destination now
	// contains a commented equivalent.
	src := openLine + openLine
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger}, src)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr)
	}

	dest := filepath.Join(root, "transactions/Assets/Bank/202401.beancount")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading bank dest: %v", err)
	}
	// Exactly one commented entry should land; the second is skipped.
	if n := strings.Count(string(got), "; 2024-01-10 open Assets:Bank"); n != 1 {
		t.Errorf("commented occurrences = %d, want 1; content=%q", n, string(got))
	}
	if !strings.Contains(stderr, "total: written=0 commented=1 skipped=1") {
		t.Errorf("stderr = %q, want total written=0 commented=1 skipped=1", stderr)
	}
}
