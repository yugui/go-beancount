package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/yugui/go-beancount/internal/formatopt"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/distribute/route"
)

func mustOpen(t *testing.T, account string) *ast.Open {
	t.Helper()
	parts := strings.Split(account, ":")
	if len(parts) < 1 {
		t.Fatalf("invalid account %q", account)
	}
	root := ast.Account(parts[0])
	a := root.MustSub(parts[1:]...)
	return &ast.Open{
		Date:    time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC),
		Account: a,
	}
}

func writeTOML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "beanfile.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("writing toml: %v", err)
	}
	return p
}

func TestLoad_FullSchemaRoundTrip(t *testing.T) {
	body := `
[routes.account]
template              = "transactions/{account}/{date}.beancount"
file_pattern          = "YYYYmm"
order                 = "ascending"
equivalence_meta_keys = ["import-id"]

[routes.price]
template              = "quotes/{commodity}/{date}.beancount"
file_pattern          = "YYYYmm"
order                 = "ascending"
equivalence_meta_keys = []

[routes.transaction]
default_strategy  = "first-posting"
override_meta_key = "route-account"

[routes.format]
comma_grouping                        = false
align_amounts                         = true
amount_column                         = 52
east_asian_ambiguous_width            = 2
indent_width                          = 2
blank_lines_between_directives        = 1
insert_blank_lines_between_directives = false

[routes.account.format]
indent_width = 4

[routes.price.format]
amount_column = 30

[[routes.account.override]]
prefix = "Assets:JP"

[routes.account.override.format]
east_asian_ambiguous_width = 2

[[routes.account.override]]
prefix                = "Expenses:Food"
template              = "transactions/expenses-food/{date}.beancount"
equivalence_meta_keys = ["receipt-id"]

[[routes.price.override]]
commodity = "JPY"

[routes.price.override.format]
amount_column = 24
`
	cfg, err := Load(writeTOML(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Account.Template != "transactions/{account}/{date}.beancount" {
		t.Errorf("Account.Template = %q", cfg.Account.Template)
	}
	if got, want := cfg.Account.EquivalenceMetaKeys, []string{"import-id"}; !cmp.Equal(got, want) {
		t.Errorf("Account.EquivalenceMetaKeys = %v, want %v", got, want)
	}
	if got := cfg.Price.EquivalenceMetaKeys; got == nil || len(got) != 0 {
		t.Errorf("Price.EquivalenceMetaKeys = %v, want empty (non-nil)", got)
	}
	if cfg.Transaction.DefaultStrategy != "first-posting" {
		t.Errorf("Transaction.DefaultStrategy = %q", cfg.Transaction.DefaultStrategy)
	}
	if cfg.Transaction.OverrideMetaKey != "route-account" {
		t.Errorf("Transaction.OverrideMetaKey = %q", cfg.Transaction.OverrideMetaKey)
	}
	if cfg.Format.AmountColumn == nil || *cfg.Format.AmountColumn != 52 {
		t.Errorf("Format.AmountColumn = %v, want *52", cfg.Format.AmountColumn)
	}
	if cfg.Account.Format.IndentWidth == nil || *cfg.Account.Format.IndentWidth != 4 {
		t.Errorf("Account.Format.IndentWidth = %v, want *4", cfg.Account.Format.IndentWidth)
	}
	if len(cfg.AccountOverrides) != 2 {
		t.Fatalf("AccountOverrides len = %d, want 2", len(cfg.AccountOverrides))
	}
	if cfg.AccountOverrides[0].Prefix != "Assets:JP" {
		t.Errorf("override[0].Prefix = %q", cfg.AccountOverrides[0].Prefix)
	}
	if w := cfg.AccountOverrides[0].Format.EastAsianAmbiguousWidth; w == nil || *w != 2 {
		t.Errorf("override[0].Format.EastAsianAmbiguousWidth = %v, want *2", w)
	}
	if cfg.AccountOverrides[1].Prefix != "Expenses:Food" {
		t.Errorf("override[1].Prefix = %q", cfg.AccountOverrides[1].Prefix)
	}
	if !cfg.AccountOverrides[1].HasEqMetaKeys {
		t.Error("override[1].HasEqMetaKeys = false, want true")
	}
	if got, want := cfg.AccountOverrides[1].EquivalenceMetaKeys, []string{"receipt-id"}; !cmp.Equal(got, want) {
		t.Errorf("override[1].EquivalenceMetaKeys = %v, want %v", got, want)
	}
	if len(cfg.CommodityOverrides) != 1 {
		t.Fatalf("CommodityOverrides len = %d, want 1", len(cfg.CommodityOverrides))
	}
	if c := cfg.CommodityOverrides[0]; c.Commodity != "JPY" || c.Format.AmountColumn == nil || *c.Format.AmountColumn != 24 {
		t.Errorf("price override = %+v", c)
	}
}

func TestLoad_OverrideOrderPreserved(t *testing.T) {
	body := `
[[routes.account.override]]
prefix = "Assets:JP"
template = "first/{account}/{date}.beancount"

[[routes.account.override]]
prefix = "Assets:JP"
template = "second/{account}/{date}.beancount"
`
	cfg, err := Load(writeTOML(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AccountOverrides[0].Template != "first/{account}/{date}.beancount" {
		t.Errorf("first override Template = %q", cfg.AccountOverrides[0].Template)
	}
	if cfg.AccountOverrides[1].Template != "second/{account}/{date}.beancount" {
		t.Errorf("second override Template = %q", cfg.AccountOverrides[1].Template)
	}
}

func TestLoad_EquivalenceMetaKeysReplacementInheritance(t *testing.T) {
	body := `
[routes.account]
equivalence_meta_keys = ["import-id"]

[[routes.account.override]]
prefix                = "Assets:JP"
equivalence_meta_keys = ["receipt-id"]

[[routes.account.override]]
prefix                = "Assets:Silenced"
equivalence_meta_keys = []
`
	cfg, err := Load(writeTOML(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.AccountOverrides[0].HasEqMetaKeys {
		t.Error("override[0].HasEqMetaKeys = false")
	}
	if got, want := cfg.AccountOverrides[0].EquivalenceMetaKeys, []string{"receipt-id"}; !cmp.Equal(got, want) {
		t.Errorf("override[0] keys = %v, want %v", got, want)
	}
	if !cfg.AccountOverrides[1].HasEqMetaKeys {
		t.Error("override[1].HasEqMetaKeys = false (explicit empty list should still set Has)")
	}
	if got := cfg.AccountOverrides[1].EquivalenceMetaKeys; len(got) != 0 {
		t.Errorf("override[1] keys = %v, want empty", got)
	}
}

func TestLoad_FormatFieldWiseInheritance(t *testing.T) {
	body := `
[routes.format]
amount_column = 40
indent_width  = 2

[routes.account.format]
indent_width = 4
`
	cfg, err := Load(writeTOML(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Format.AmountColumn == nil || *cfg.Format.AmountColumn != 40 {
		t.Errorf("global AmountColumn = %v", cfg.Format.AmountColumn)
	}
	if cfg.Account.Format.IndentWidth == nil || *cfg.Account.Format.IndentWidth != 4 {
		t.Errorf("Account.Format.IndentWidth = %v", cfg.Account.Format.IndentWidth)
	}
	// The account section did NOT set amount_column; that field stays nil
	// in the loaded value (inheritance happens at Decide time, not at load).
	if cfg.Account.Format.AmountColumn != nil {
		t.Errorf("Account.Format.AmountColumn = %v, want nil", cfg.Account.Format.AmountColumn)
	}
}

func TestLoad_RejectsUnsupportedOrder(t *testing.T) {
	body := `
[routes.account]
order = "descending"
`
	_, err := Load(writeTOML(t, body))
	if err == nil {
		t.Fatal("Load: got nil error, want unsupported order")
	}
	if !strings.Contains(err.Error(), "descending") {
		t.Errorf("error = %v, want mention of descending", err)
	}
}

func TestLoad_RejectsUnsupportedFilePattern(t *testing.T) {
	body := `
[routes.account]
file_pattern = "YYYY"
`
	_, err := Load(writeTOML(t, body))
	if err == nil {
		t.Fatal("Load: got nil error, want unsupported file_pattern")
	}
	if !strings.Contains(err.Error(), "YYYY") {
		t.Errorf("error = %v, want mention of YYYY", err)
	}
}

func TestLoad_RejectsUnknownKey(t *testing.T) {
	body := `
[routes.account]
template = "x"
nonsense = 42
`
	_, err := Load(writeTOML(t, body))
	if err == nil {
		t.Fatal("Load: got nil error, want unknown-key error")
	}
	if !strings.Contains(err.Error(), "nonsense") {
		t.Errorf("error = %v, want mention of nonsense", err)
	}
}

func TestLoad_AcceptsAllStrategies(t *testing.T) {
	for _, s := range []string{"first-posting", "last-posting", "first-debit", "first-credit"} {
		t.Run(s, func(t *testing.T) {
			body := "[routes.transaction]\ndefault_strategy = \"" + s + "\"\n"
			cfg, err := Load(writeTOML(t, body))
			if err != nil {
				t.Fatalf("Load(%q): %v", s, err)
			}
			if cfg.Transaction.DefaultStrategy != s {
				t.Errorf("DefaultStrategy = %q, want %q", cfg.Transaction.DefaultStrategy, s)
			}
		})
	}
}

func TestLoad_RejectsUnknownStrategy(t *testing.T) {
	body := `
[routes.transaction]
default_strategy = "round-robin"
`
	_, err := Load(writeTOML(t, body))
	if err == nil {
		t.Fatal("Load: got nil error, want unknown-strategy error")
	}
	if !strings.Contains(err.Error(), "round-robin") {
		t.Errorf("error = %v, want mention of round-robin", err)
	}
}

func TestLoad_RejectsMalformedTOML(t *testing.T) {
	body := `
[routes.account
template = "x"
`
	_, err := Load(writeTOML(t, body))
	if err == nil {
		t.Fatal("Load: got nil error, want decoding error")
	}
	if !strings.Contains(err.Error(), "decoding") {
		t.Errorf("error = %v, want mention of decoding", err)
	}
}

func TestLoadIfExists_MissingFileReturnsNil(t *testing.T) {
	cfg, err := LoadIfExists(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("LoadIfExists: %v", err)
	}
	if cfg != nil {
		t.Errorf("LoadIfExists missing: got %+v, want nil", cfg)
	}
}

// Loaded config drives Decide end-to-end: confirm a full chain (toml →
// route.Config → Decide) produces a destination path that reflects the
// configured override.
func TestLoad_DecidesAccountOverridePath(t *testing.T) {
	body := `
[[routes.account.override]]
prefix   = "Assets:JP"
template = "japan/{account}/{date}.beancount"
`
	cfg, err := Load(writeTOML(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d := mustOpen(t, "Assets:JP:Cash")
	dec, err := route.Decide(d, cfg)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if dec.Path != "japan/Assets/JP/Cash/202401.beancount" {
		t.Errorf("Path = %q, want override-driven path", dec.Path)
	}
	// Format defaults still apply when no format section is configured.
	resolved := formatopt.Resolve(dec.Format)
	if resolved.AmountColumn != formatopt.Default().AmountColumn {
		t.Errorf("AmountColumn = %d, want default %d", resolved.AmountColumn, formatopt.Default().AmountColumn)
	}
}
