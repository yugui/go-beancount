package routeconfig

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

func ptrInt(n int) *int    { return &n }
func ptrBool(b bool) *bool { return &b }
func ptrStrSlice(ss ...string) *[]string {
	s := make([]string, len(ss))
	copy(s, ss)
	return &s
}

func mustOpen(t *testing.T, account string) *ast.Open {
	t.Helper()
	parts := strings.Split(account, ":")
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
	got, err := Load(writeTOML(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := &route.Config{
		Routes: route.Routes{
			Account: route.AccountSection{
				Template:            "transactions/{account}/{date}.beancount",
				FilePattern:         "YYYYmm",
				Order:               "ascending",
				EquivalenceMetaKeys: ptrStrSlice("import-id"),
				Format:              route.FormatSection{IndentWidth: ptrInt(4)},
				Overrides: []route.AccountOverride{
					{
						Prefix: "Assets:JP",
						Format: route.FormatSection{EastAsianAmbiguousWidth: ptrInt(2)},
					},
					{
						Prefix:              "Expenses:Food",
						Template:            "transactions/expenses-food/{date}.beancount",
						EquivalenceMetaKeys: ptrStrSlice("receipt-id"),
					},
				},
			},
			Price: route.PriceSection{
				Template:            "quotes/{commodity}/{date}.beancount",
				FilePattern:         "YYYYmm",
				Order:               "ascending",
				EquivalenceMetaKeys: ptrStrSlice(),
				Format:              route.FormatSection{AmountColumn: ptrInt(30)},
				Overrides: []route.CommodityOverride{
					{
						Commodity: "JPY",
						Format:    route.FormatSection{AmountColumn: ptrInt(24)},
					},
				},
			},
			Transaction: route.TransactionSection{
				DefaultStrategy: "first-posting",
				OverrideMetaKey: "route-account",
			},
			Format: route.FormatSection{
				CommaGrouping:                     ptrBool(false),
				AlignAmounts:                      ptrBool(true),
				AmountColumn:                      ptrInt(52),
				EastAsianAmbiguousWidth:           ptrInt(2),
				IndentWidth:                       ptrInt(2),
				BlankLinesBetweenDirectives:       ptrInt(1),
				InsertBlankLinesBetweenDirectives: ptrBool(false),
			},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Load mismatch (-want +got):\n%s", diff)
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
	got, err := Load(writeTOML(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []route.AccountOverride{
		{Prefix: "Assets:JP", Template: "first/{account}/{date}.beancount"},
		{Prefix: "Assets:JP", Template: "second/{account}/{date}.beancount"},
	}
	if diff := cmp.Diff(want, got.Routes.Account.Overrides); diff != "" {
		t.Errorf("Load() override order mismatch (-want +got):\n%s", diff)
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
	got, err := Load(writeTOML(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := route.AccountSection{
		EquivalenceMetaKeys: ptrStrSlice("import-id"),
		Overrides: []route.AccountOverride{
			{Prefix: "Assets:JP", EquivalenceMetaKeys: ptrStrSlice("receipt-id")},
			{Prefix: "Assets:Silenced", EquivalenceMetaKeys: ptrStrSlice()},
		},
	}
	if diff := cmp.Diff(want, got.Routes.Account); diff != "" {
		t.Errorf("Load() Account section mismatch (-want +got):\n%s", diff)
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
	got, err := Load(writeTOML(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := route.Routes{
		Account: route.AccountSection{
			Format: route.FormatSection{IndentWidth: ptrInt(4)},
		},
		Format: route.FormatSection{
			AmountColumn: ptrInt(40),
			IndentWidth:  ptrInt(2),
		},
	}
	if diff := cmp.Diff(want, got.Routes); diff != "" {
		t.Errorf("Load() Routes mismatch (-want +got):\n%s", diff)
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
			if cfg.Routes.Transaction.DefaultStrategy != s {
				t.Errorf("DefaultStrategy = %q, want %q", cfg.Routes.Transaction.DefaultStrategy, s)
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
	const wantPath = "japan/Assets/JP/Cash/202401.beancount"
	if dec.Path != wantPath {
		t.Errorf("Decide(...).Path = %q, want %q", dec.Path, wantPath)
	}
	// Format defaults still apply when no format section is configured.
	resolved := formatopt.Resolve(dec.Format)
	if resolved.AmountColumn != formatopt.Default().AmountColumn {
		t.Errorf("AmountColumn = %d, want default %d", resolved.AmountColumn, formatopt.Default().AmountColumn)
	}
}
