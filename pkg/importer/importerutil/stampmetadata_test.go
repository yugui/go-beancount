package importerutil_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/importer/importerutil"
)

func TestStampMetadata_SetNew(t *testing.T) {
	txn := &ast.Transaction{
		Date:      time.Now(),
		Flag:      '*',
		Narration: "t",
	}
	got := importerutil.StampMetadata(txn, "mykey", "myval")
	result, ok := got.(*ast.Transaction)
	if !ok {
		t.Fatalf("StampMetadata returned %T, want *ast.Transaction", got)
	}
	want := ast.MetaValue{Kind: ast.MetaString, String: "myval"}
	if diff := cmp.Diff(want, result.Meta.Props["mykey"], cmpOpts); diff != "" {
		t.Errorf("Props[mykey] mismatch (-want +got):\n%s", diff)
	}
}

func TestStampMetadata_Overwrite(t *testing.T) {
	txn := &ast.Transaction{
		Date:      time.Now(),
		Flag:      '*',
		Narration: "t",
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"mykey": {Kind: ast.MetaString, String: "old"},
		}},
	}
	got := importerutil.StampMetadata(txn, "mykey", "new")
	result := got.(*ast.Transaction)
	wantNew := ast.MetaValue{Kind: ast.MetaString, String: "new"}
	if diff := cmp.Diff(wantNew, result.Meta.Props["mykey"], cmpOpts); diff != "" {
		t.Errorf("result Props[mykey] mismatch (-want +got):\n%s", diff)
	}
	wantOld := ast.MetaValue{Kind: ast.MetaString, String: "old"}
	if diff := cmp.Diff(wantOld, txn.Meta.Props["mykey"], cmpOpts); diff != "" {
		t.Errorf("original Props[mykey] mutated (-want +got):\n%s", diff)
	}
}

func TestStampMetadata_IdempotentSameValue(t *testing.T) {
	txn := &ast.Transaction{
		Date:      time.Now(),
		Flag:      '*',
		Narration: "t",
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"mykey": {Kind: ast.MetaString, String: "val"},
		}},
	}
	got := importerutil.StampMetadata(txn, "mykey", "val")
	if got != ast.Directive(txn) {
		t.Errorf("StampMetadata idempotent case: expected same pointer, got new allocation")
	}
}

func TestStampMetadata_InputUnchanged(t *testing.T) {
	txn := &ast.Transaction{
		Date:      time.Now(),
		Flag:      '*',
		Narration: "t",
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"existing": {Kind: ast.MetaString, String: "keep"},
		}},
	}
	origCopy := txn.Clone()
	_ = importerutil.StampMetadata(txn, "newkey", "newval")
	if diff := cmp.Diff(origCopy, txn, cmpOpts); diff != "" {
		t.Errorf("StampMetadata mutated the input directive (-want +got):\n%s", diff)
	}
}

// stampCase exercises StampMetadata on a directive that already carries a
// pre-existing Meta entry. getMeta extracts the Metadata from any directive
// of this case's concrete type; it is applied to both the StampMetadata
// result and the original input so the test can compare them.
type stampCase struct {
	name      string
	directive ast.Directive
	getMeta   func(ast.Directive) ast.Metadata
}

// allMetaBearingCases enumerates one constructed instance of every directive
// concrete type that carries a Meta field. Each instance is seeded with a
// preexisting Meta.Props entry under "preexisting" so the mutation-isolation
// asserts in TestStampMetadata_AllMetaBearingKinds have something to observe.
//
// Adding a new Meta-bearing directive kind to pkg/ast/directives.go must be
// reflected here; TestStampMetadata_DirectiveKindCoverage parses
// pkg/ast/directives.go at test time to detect missing cases.
func allMetaBearingCases() []stampCase {
	date := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	preexisting := func() ast.Metadata {
		return ast.Metadata{Props: map[string]ast.MetaValue{
			"preexisting": {Kind: ast.MetaString, String: "kept"},
		}}
	}
	return []stampCase{
		{
			name:      "Transaction",
			directive: &ast.Transaction{Date: date, Flag: '*', Narration: "t", Meta: preexisting()},
			getMeta:   func(d ast.Directive) ast.Metadata { return d.(*ast.Transaction).Meta },
		},
		{
			name:      "Open",
			directive: &ast.Open{Date: date, Account: ast.Assets.MustSub("Cash"), Meta: preexisting()},
			getMeta:   func(d ast.Directive) ast.Metadata { return d.(*ast.Open).Meta },
		},
		{
			name:      "Close",
			directive: &ast.Close{Date: date, Account: ast.Assets.MustSub("Cash"), Meta: preexisting()},
			getMeta:   func(d ast.Directive) ast.Metadata { return d.(*ast.Close).Meta },
		},
		{
			name:      "Balance",
			directive: &ast.Balance{Date: date, Account: ast.Assets.MustSub("Cash"), Amount: ast.Amount{Number: mustDecimal("0"), Currency: "USD"}, Meta: preexisting()},
			getMeta:   func(d ast.Directive) ast.Metadata { return d.(*ast.Balance).Meta },
		},
		{
			name:      "Pad",
			directive: &ast.Pad{Date: date, Account: ast.Assets.MustSub("Cash"), PadAccount: ast.Equity.MustSub("Opening"), Meta: preexisting()},
			getMeta:   func(d ast.Directive) ast.Metadata { return d.(*ast.Pad).Meta },
		},
		{
			name:      "Note",
			directive: &ast.Note{Date: date, Account: ast.Assets.MustSub("Cash"), Comment: "hi", Meta: preexisting()},
			getMeta:   func(d ast.Directive) ast.Metadata { return d.(*ast.Note).Meta },
		},
		{
			name:      "Document",
			directive: &ast.Document{Date: date, Account: ast.Assets.MustSub("Cash"), Path: "/p", Meta: preexisting()},
			getMeta:   func(d ast.Directive) ast.Metadata { return d.(*ast.Document).Meta },
		},
		{
			name:      "Price",
			directive: &ast.Price{Date: date, Commodity: "USD", Amount: ast.Amount{Number: mustDecimal("1"), Currency: "EUR"}, Meta: preexisting()},
			getMeta:   func(d ast.Directive) ast.Metadata { return d.(*ast.Price).Meta },
		},
		{
			name:      "Commodity",
			directive: &ast.Commodity{Date: date, Currency: "USD", Meta: preexisting()},
			getMeta:   func(d ast.Directive) ast.Metadata { return d.(*ast.Commodity).Meta },
		},
		{
			name:      "Event",
			directive: &ast.Event{Date: date, Name: "location", Value: "NYC", Meta: preexisting()},
			getMeta:   func(d ast.Directive) ast.Metadata { return d.(*ast.Event).Meta },
		},
		{
			name:      "Query",
			directive: &ast.Query{Date: date, Name: "q", BQL: "SELECT *", Meta: preexisting()},
			getMeta:   func(d ast.Directive) ast.Metadata { return d.(*ast.Query).Meta },
		},
		{
			name:      "Custom",
			directive: &ast.Custom{Date: date, TypeName: "t", Meta: preexisting()},
			getMeta:   func(d ast.Directive) ast.Metadata { return d.(*ast.Custom).Meta },
		},
	}
}

// TestStampMetadata_AllMetaBearingKinds verifies stamp-and-isolation for every
// Meta-bearing directive concrete type. For each kind it asserts:
//   - the stamp writes the new key on the returned directive,
//   - the preexisting key is preserved on the returned directive,
//   - mutating the returned directive's Meta.Props (delete + write) does not
//     affect the input directive's Meta.Props, and
//   - overwriting an existing key returns a directive whose value reflects the
//     new value while the input still holds the old one.
func TestStampMetadata_AllMetaBearingKinds(t *testing.T) {
	const key = "stamp"
	const val = "stamped"

	for _, tc := range allMetaBearingCases() {
		t.Run(tc.name, func(t *testing.T) {
			origMeta := tc.getMeta(tc.directive)
			origPreexisting := origMeta.Props["preexisting"]

			got := importerutil.StampMetadata(tc.directive, key, val)
			gotMeta := tc.getMeta(got)

			wantStamp := ast.MetaValue{Kind: ast.MetaString, String: val}
			if diff := cmp.Diff(wantStamp, gotMeta.Props[key], cmpOpts); diff != "" {
				t.Errorf("StampMetadata(%s): Props[%q] mismatch (-want +got):\n%s", tc.name, key, diff)
			}
			if diff := cmp.Diff(origPreexisting, gotMeta.Props["preexisting"], cmpOpts); diff != "" {
				t.Errorf("StampMetadata(%s): preexisting key altered (-want +got):\n%s", tc.name, diff)
			}

			delete(gotMeta.Props, "preexisting")
			gotMeta.Props["postmutation"] = ast.MetaValue{Kind: ast.MetaString, String: "added"}

			origMetaAfter := tc.getMeta(tc.directive)
			if got, want := origMetaAfter.Props["preexisting"], origPreexisting; got != want {
				t.Errorf("StampMetadata(%s): mutating result Meta.Props affected input preexisting entry: got %+v, want %+v", tc.name, got, want)
			}
			if _, ok := origMetaAfter.Props["postmutation"]; ok {
				t.Errorf("StampMetadata(%s): mutating result Meta.Props added a key to input Meta.Props", tc.name)
			}
			if _, ok := origMetaAfter.Props[key]; ok {
				t.Errorf("StampMetadata(%s): input Meta.Props gained the stamped key %q", tc.name, key)
			}
		})
	}
}

// TestStampMetadata_OverwriteIsolation verifies that overwriting a value on a
// non-Transaction Meta-bearing directive leaves the input's prior value
// intact. This is the regression specific to "Meta is shared by convention":
// without forkMeta, the write would mutate the input's map.
func TestStampMetadata_OverwriteIsolation(t *testing.T) {
	const key = "preexisting"
	const newVal = "rewritten"

	for _, tc := range allMetaBearingCases() {
		t.Run(tc.name, func(t *testing.T) {
			origMeta := tc.getMeta(tc.directive)
			origPreexisting := origMeta.Props[key]

			got := importerutil.StampMetadata(tc.directive, key, newVal)
			gotMeta := tc.getMeta(got)

			wantNew := ast.MetaValue{Kind: ast.MetaString, String: newVal}
			if diff := cmp.Diff(wantNew, gotMeta.Props[key], cmpOpts); diff != "" {
				t.Errorf("StampMetadata(%s) overwrite: result Props[%q] mismatch (-want +got):\n%s", tc.name, key, diff)
			}

			origMetaAfter := tc.getMeta(tc.directive)
			if got, want := origMetaAfter.Props[key], origPreexisting; got != want {
				t.Errorf("StampMetadata(%s) overwrite mutated input Props[%q]: got %+v, want %+v", tc.name, key, got, want)
			}
		})
	}
}

// TestStampMetadata_DirectiveKindCoverage asserts that allMetaBearingCases
// names every Meta-bearing directive type StampMetadata's switch handles.
// Both the case fixtures (allMetaBearingCases) and the expected list below
// are hand-maintained: when a new Meta-bearing directive is added to
// pkg/ast/directives.go, all three of (a) StampMetadata's switch arm,
// (b) allMetaBearingCases, and (c) wantNames below must be updated together.
func TestStampMetadata_DirectiveKindCoverage(t *testing.T) {
	wantNames := []string{
		"Balance",
		"Close",
		"Commodity",
		"Custom",
		"Document",
		"Event",
		"Note",
		"Open",
		"Pad",
		"Price",
		"Query",
		"Transaction",
	}
	want := make(map[string]struct{}, len(wantNames))
	for _, n := range wantNames {
		want[n] = struct{}{}
	}

	got := make(map[string]struct{}, len(allMetaBearingCases()))
	for _, tc := range allMetaBearingCases() {
		got[tc.name] = struct{}{}
	}

	for name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("allMetaBearingCases is missing %q", name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("allMetaBearingCases has extra %q not in wantNames", name)
		}
	}
}

func TestStampMetadata_NoopHeaderTypes(t *testing.T) {
	directives := []ast.Directive{
		&ast.Option{Key: "k", Value: "v"},
		&ast.Plugin{Name: "p"},
		&ast.Include{Path: "/f"},
	}
	for _, d := range directives {
		got := importerutil.StampMetadata(d, "key", "val")
		if got != d {
			t.Errorf("StampMetadata(%T): expected same pointer (no-op), got new value", d)
		}
	}
}

func TestStampMetadata_NoopNil(t *testing.T) {
	got := importerutil.StampMetadata(nil, "key", "val")
	if got != nil {
		t.Errorf("StampMetadata(nil) = %v, want nil", got)
	}
}

func TestStampMetadata_EnsuresPropsMapAllocated(t *testing.T) {
	txn := &ast.Transaction{Date: time.Now(), Flag: '*', Narration: "t"}
	got := importerutil.StampMetadata(txn, "k", "v").(*ast.Transaction)
	if got.Meta.Props == nil {
		t.Fatal("StampMetadata did not allocate Props map on a nil-Props input")
	}
	want := ast.MetaValue{Kind: ast.MetaString, String: "v"}
	if diff := cmp.Diff(want, got.Meta.Props["k"], cmpOpts); diff != "" {
		t.Errorf("Props[k] mismatch after allocation (-want +got):\n%s", diff)
	}
}

func BenchmarkStampMetadata_FreshKey(b *testing.B) {
	txn := &ast.Transaction{Date: time.Now(), Flag: '*', Narration: "t"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = importerutil.StampMetadata(txn, "k", "v")
	}
}

func BenchmarkStampMetadata_Overwrite(b *testing.B) {
	txn := &ast.Transaction{
		Date:      time.Now(),
		Flag:      '*',
		Narration: "t",
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"k": {Kind: ast.MetaString, String: "old"},
		}},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = importerutil.StampMetadata(txn, "k", "new")
	}
}

func BenchmarkStampMetadata_Idempotent(b *testing.B) {
	txn := &ast.Transaction{
		Date:      time.Now(),
		Flag:      '*',
		Narration: "t",
		Meta: ast.Metadata{Props: map[string]ast.MetaValue{
			"k": {Kind: ast.MetaString, String: "v"},
		}},
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = importerutil.StampMetadata(txn, "k", "v")
	}
}
