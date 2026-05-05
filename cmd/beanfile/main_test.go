package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
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
	if _, err := os.Stat(filepath.Join(root, "quotes")); !os.IsNotExist(err) {
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

func TestRun_EquivalenceMetaKeysSkipsByMetaMatch(t *testing.T) {
	openLine := `2024-01-10 open Assets:Bank USD
  import-id: "abc"
`
	root, ledger := seedLedger(t, map[string]string{
		"transactions/Assets/Bank/202401.beancount": openLine,
	})
	cfgPath := writeConfig(t, `
[routes.account]
equivalence_meta_keys = ["import-id"]
`)
	// Different account, same import-id → meta-key match against the
	// active entry under transactions/Assets/Other path. We probe a
	// different account so the AST-equality branch can't fire (different
	// account values), forcing the meta branch to be the only path.
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
	// different path, this directive must land commented-out (Rule 2).
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
order = "descending"
`)
	exit, _, stderr := runCLI(t, []string{"--ledger", ledger, "--config", cfgPath}, "")
	if exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr, "descending") {
		t.Errorf("stderr = %q, want descending mention", stderr)
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
