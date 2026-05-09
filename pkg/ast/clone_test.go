package ast

import (
	"reflect"
	"testing"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/go-cmp/cmp"
)

// astCloneCmpOpts is the standard option set for deep-comparing AST
// values in clone tests. apd.Decimal has unexported fields and
// time.Time carries monotonic state, so each gets a custom comparer
// that defers to the type's own equality semantics.
var astCloneCmpOpts = cmp.Options{
	cmp.Comparer(func(x, y apd.Decimal) bool { return x.Cmp(&y) == 0 }),
	cmp.Comparer(func(x, y time.Time) bool { return x.Equal(y) }),
}

// cloneTestDecimal parses s as an apd.Decimal. It panics if s is not
// a valid decimal string, which is acceptable since callers pass
// compile-time-constant test inputs.
func cloneTestDecimal(s string) apd.Decimal {
	d, _, err := apd.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return *d
}

// sampleAmount builds a fully populated *Amount with a non-trivial
// decimal value so coefficient aliasing is observable.
func sampleAmount() *Amount {
	return &Amount{
		Number:   cloneTestDecimal("123.45"),
		Currency: "USD",
	}
}

// samplePriceAnnotation builds a fully populated *PriceAnnotation.
func samplePriceAnnotation() *PriceAnnotation {
	return &PriceAnnotation{
		Span:    Span{Start: Position{Filename: "f.bean", Offset: 1, Line: 2, Column: 3}},
		Amount:  Amount{Number: cloneTestDecimal("9.99"), Currency: "EUR"},
		IsTotal: true,
	}
}

// sampleCostSpec builds a fully populated *CostSpec including
// PerUnit, Total, Date, and Label so all branches of Clone exercise.
func sampleCostSpec() *CostSpec {
	d := time.Date(2024, time.March, 15, 0, 0, 0, 0, time.UTC)
	return &CostSpec{
		Span:    Span{Start: Position{Filename: "f.bean", Line: 5}},
		PerUnit: &Amount{Number: cloneTestDecimal("100"), Currency: "USD"},
		Total:   &Amount{Number: cloneTestDecimal("1.50"), Currency: "USD"},
		Date:    &d,
		Label:   "lot-a",
	}
}

// samplePosting builds a fully populated Posting with all optional
// fields set.
func samplePosting() Posting {
	return Posting{
		Span:    Span{Start: Position{Filename: "f.bean", Line: 7}},
		Flag:    '!',
		Account: Account("Assets:Cash"),
		Amount:  sampleAmount(),
		Cost:    sampleCostSpec(),
		Price:   samplePriceAnnotation(),
		Meta:    Metadata{Props: map[string]MetaValue{"k": {Kind: MetaString, String: "v"}}},
	}
}

// sampleTransaction builds a fully populated *Transaction with two
// postings.
func sampleTransaction() *Transaction {
	return &Transaction{
		Span:      Span{Start: Position{Filename: "f.bean", Line: 10}},
		Date:      time.Date(2024, time.March, 15, 0, 0, 0, 0, time.UTC),
		Flag:      '*',
		Payee:     "Grocer",
		Narration: "Weekly shop",
		Tags:      []string{"trip-2024"},
		Links:     []string{"invoice-1"},
		Postings:  []Posting{samplePosting(), samplePosting()},
		Meta:      Metadata{Props: map[string]MetaValue{"docref": {Kind: MetaString, String: "r-1"}}},
	}
}

func TestAmountClone(t *testing.T) {
	orig := sampleAmount()
	got := orig.Clone()

	if got == orig {
		t.Fatalf("Amount.Clone returned same pointer; want fresh allocation")
	}
	if diff := cmp.Diff(orig, got, astCloneCmpOpts); diff != "" {
		t.Errorf("Amount.Clone result differs from original (-want +got):\n%s", diff)
	}

	// Mutating the clone's number must not affect the original.
	mutated := cloneTestDecimal("999")
	got.Number.Set(&mutated)
	if orig.Number.Cmp(&got.Number) == 0 {
		t.Errorf("Amount.Clone: mutating clone changed original Number; want independent buffers")
	}

	// Vice versa: mutating the original must not affect the clone.
	orig2 := sampleAmount()
	clone2 := orig2.Clone()
	orig2.Number.Set(&mutated)
	if clone2.Number.Cmp(&orig2.Number) == 0 {
		t.Errorf("Amount.Clone: mutating original after clone changed clone Number; want independent buffers")
	}
}

func TestAmountCloneNil(t *testing.T) {
	if got := (*Amount)(nil).Clone(); got != nil {
		t.Errorf("Amount.Clone on nil = %v, want nil", got)
	}
}

func TestPriceAnnotationClone(t *testing.T) {
	orig := samplePriceAnnotation()
	got := orig.Clone()

	if got == orig {
		t.Fatalf("PriceAnnotation.Clone returned same pointer; want fresh allocation")
	}
	if diff := cmp.Diff(orig, got, astCloneCmpOpts); diff != "" {
		t.Errorf("PriceAnnotation.Clone result differs from original (-want +got):\n%s", diff)
	}

	mutated := cloneTestDecimal("0.01")
	got.Amount.Number.Set(&mutated)
	if orig.Amount.Number.Cmp(&got.Amount.Number) == 0 {
		t.Errorf("PriceAnnotation.Clone: mutating clone Amount.Number changed original; want independent buffers")
	}
}

func TestPriceAnnotationCloneNil(t *testing.T) {
	if got := (*PriceAnnotation)(nil).Clone(); got != nil {
		t.Errorf("PriceAnnotation.Clone on nil = %v, want nil", got)
	}
}

func TestCostSpecClone(t *testing.T) {
	orig := sampleCostSpec()
	got := orig.Clone()

	if got == orig {
		t.Fatalf("CostSpec.Clone returned same pointer; want fresh allocation")
	}
	if got.PerUnit == orig.PerUnit {
		t.Errorf("CostSpec.Clone: PerUnit aliases original; want fresh allocation")
	}
	if got.Total == orig.Total {
		t.Errorf("CostSpec.Clone: Total aliases original; want fresh allocation")
	}
	if got.Date == orig.Date {
		t.Errorf("CostSpec.Clone: Date aliases original; want fresh allocation")
	}
	if diff := cmp.Diff(orig, got, astCloneCmpOpts); diff != "" {
		t.Errorf("CostSpec.Clone result differs from original (-want +got):\n%s", diff)
	}

	// Mutating clone fields must not affect the original.
	mutated := cloneTestDecimal("777")
	got.PerUnit.Number.Set(&mutated)
	if orig.PerUnit.Number.Cmp(&got.PerUnit.Number) == 0 {
		t.Errorf("CostSpec.Clone: mutating clone PerUnit changed original; want independent buffers")
	}
	got.Total.Number.Set(&mutated)
	if orig.Total.Number.Cmp(&got.Total.Number) == 0 {
		t.Errorf("CostSpec.Clone: mutating clone Total changed original; want independent buffers")
	}
	*got.Date = time.Date(2099, time.December, 31, 0, 0, 0, 0, time.UTC)
	if orig.Date.Equal(*got.Date) {
		t.Errorf("CostSpec.Clone: mutating clone Date changed original; want independent allocations")
	}
}

func TestCostSpecCloneEmptyParts(t *testing.T) {
	// CostSpec with PerUnit=nil, Total=nil, Date=nil exercises the
	// nil-pointer branches of Clone.
	orig := &CostSpec{Label: "empty"}
	got := orig.Clone()
	if got == orig {
		t.Fatalf("CostSpec.Clone returned same pointer; want fresh allocation")
	}
	if got.PerUnit != nil || got.Total != nil || got.Date != nil {
		t.Errorf("CostSpec.Clone unexpectedly populated nil fields: %+v", got)
	}
	if got.Label != "empty" {
		t.Errorf("CostSpec.Clone Label = %q, want %q", got.Label, "empty")
	}
}

func TestCostSpecCloneNil(t *testing.T) {
	if got := (*CostSpec)(nil).Clone(); got != nil {
		t.Errorf("CostSpec.Clone on nil = %v, want nil", got)
	}
}

func TestPostingClone(t *testing.T) {
	orig := samplePosting()
	got := orig.Clone()

	if got.Amount == orig.Amount {
		t.Errorf("Posting.Clone: Amount aliases original; want fresh allocation")
	}
	if got.Cost == orig.Cost {
		t.Errorf("Posting.Clone: Cost aliases original; want fresh allocation")
	}
	if got.Price == orig.Price {
		t.Errorf("Posting.Clone: Price aliases original; want fresh allocation")
	}
	if diff := cmp.Diff(orig, got, astCloneCmpOpts); diff != "" {
		t.Errorf("Posting.Clone result differs from original (-want +got):\n%s", diff)
	}

	// Mutating clone Amount must not change original.
	mutated := cloneTestDecimal("42")
	got.Amount.Number.Set(&mutated)
	if orig.Amount.Number.Cmp(&got.Amount.Number) == 0 {
		t.Errorf("Posting.Clone: mutating clone Amount changed original; want independent buffers")
	}

	// Meta must be a fresh map: mutating the clone's Meta must not affect the original.
	orig2 := samplePosting()
	got2 := orig2.Clone()
	got2.Meta.Props["injected"] = MetaValue{Kind: MetaString, String: "yes"}
	if _, present := orig2.Meta.Props["injected"]; present {
		t.Errorf("Posting.Clone: mutating clone Meta changed original Meta; want independent maps")
	}
}

func TestPostingCloneZeroOptionalFields(t *testing.T) {
	// A Posting where Amount, Cost, Price are all nil must clone
	// without panic and yield identical zero pointers.
	orig := Posting{Account: Account("Equity:Opening")}
	got := orig.Clone()
	if got.Amount != nil || got.Cost != nil || got.Price != nil {
		t.Errorf("Posting.Clone unexpectedly populated nil fields: %+v", got)
	}
	if got.Account != orig.Account {
		t.Errorf("Posting.Clone Account = %q, want %q", got.Account, orig.Account)
	}
}

func TestTransactionClone(t *testing.T) {
	orig := sampleTransaction()
	got := orig.Clone()

	if got == orig {
		t.Fatalf("Transaction.Clone returned same pointer; want fresh allocation")
	}
	if &got.Postings[0] == &orig.Postings[0] {
		t.Errorf("Transaction.Clone: Postings backing array aliases original; want fresh allocation")
	}
	if got.Postings[0].Amount == orig.Postings[0].Amount {
		t.Errorf("Transaction.Clone: Postings[0].Amount aliases original; want fresh allocation")
	}
	if diff := cmp.Diff(orig, got, astCloneCmpOpts); diff != "" {
		t.Errorf("Transaction.Clone result differs from original (-want +got):\n%s", diff)
	}

	// Mutating a posting amount on the clone must not affect the
	// original.
	mutated := cloneTestDecimal("0")
	got.Postings[0].Amount.Number.Set(&mutated)
	if orig.Postings[0].Amount.Number.Cmp(&got.Postings[0].Amount.Number) == 0 {
		t.Errorf("Transaction.Clone: mutating clone posting Amount changed original; want independent buffers")
	}

	// Tags and Links are deliberately shared (immutable by
	// convention). Confirm the clone observes the same backing
	// slice header so the documented contract holds.
	if len(got.Tags) > 0 && &got.Tags[0] != &orig.Tags[0] {
		t.Errorf("Transaction.Clone: Tags backing array unexpectedly reallocated; clone is meant to share")
	}

	// Transaction Meta must be a fresh map: mutating the clone's Meta must
	// not affect the original.
	orig2 := sampleTransaction()
	got2 := orig2.Clone()
	got2.Meta.Props["injected"] = MetaValue{Kind: MetaString, String: "yes"}
	if _, present := orig2.Meta.Props["injected"]; present {
		t.Errorf("Transaction.Clone: mutating clone Meta changed original Meta; want independent maps")
	}

	// Posting Meta must also be fresh maps.
	orig3 := sampleTransaction()
	got3 := orig3.Clone()
	got3.Postings[0].Meta.Props["posting-injected"] = MetaValue{Kind: MetaString, String: "x"}
	if _, present := orig3.Postings[0].Meta.Props["posting-injected"]; present {
		t.Errorf("Transaction.Clone: mutating clone posting Meta changed original posting Meta; want independent maps")
	}
}

func TestTransactionCloneNilPostings(t *testing.T) {
	orig := &Transaction{Date: time.Now(), Flag: '*'}
	got := orig.Clone()
	if got == orig {
		t.Fatalf("Transaction.Clone returned same pointer; want fresh allocation")
	}
	if got.Postings != nil {
		t.Errorf("Transaction.Clone Postings = %v, want nil", got.Postings)
	}
}

func TestTransactionCloneNil(t *testing.T) {
	if got := (*Transaction)(nil).Clone(); got != nil {
		t.Errorf("Transaction.Clone on nil = %v, want nil", got)
	}
}

// sampleBalance builds a fully populated *Balance including a non-nil
// Tolerance so all branches of Clone exercise.
func sampleBalance() *Balance {
	tol := cloneTestDecimal("0.01")
	return &Balance{
		Span:      Span{Start: Position{Filename: "f.bean", Line: 12}},
		Date:      time.Date(2024, time.March, 15, 0, 0, 0, 0, time.UTC),
		Account:   Account("Assets:Cash"),
		Amount:    Amount{Number: cloneTestDecimal("1000.50"), Currency: "USD"},
		Tolerance: &tol,
		Meta:      Metadata{Props: map[string]MetaValue{"k": {Kind: MetaString, String: "v"}}},
	}
}

func TestBalanceClone(t *testing.T) {
	orig := sampleBalance()
	got := orig.Clone()

	if got == orig {
		t.Fatalf("Balance.Clone returned same pointer; want fresh allocation")
	}
	if got.Tolerance == orig.Tolerance {
		t.Errorf("Balance.Clone: Tolerance aliases original; want fresh allocation")
	}
	if diff := cmp.Diff(orig, got, astCloneCmpOpts); diff != "" {
		t.Errorf("Balance.Clone result differs from original (-want +got):\n%s", diff)
	}

	// Mutating the clone's Amount.Number must not affect the original.
	mutated := cloneTestDecimal("999")
	got.Amount.Number.Set(&mutated)
	if orig.Amount.Number.Cmp(&got.Amount.Number) == 0 {
		t.Errorf("Balance.Clone: mutating clone Amount.Number changed original; want independent buffers")
	}

	// Mutating the clone's Tolerance must not affect the original.
	got.Tolerance.Set(&mutated)
	if orig.Tolerance.Cmp(got.Tolerance) == 0 {
		t.Errorf("Balance.Clone: mutating clone Tolerance changed original; want independent buffers")
	}
}

func TestBalanceCloneNilTolerance(t *testing.T) {
	orig := &Balance{
		Date:    time.Date(2024, time.March, 15, 0, 0, 0, 0, time.UTC),
		Account: Account("Assets:Cash"),
		Amount:  Amount{Number: cloneTestDecimal("1"), Currency: "USD"},
	}
	got := orig.Clone()
	if got == orig {
		t.Fatalf("Balance.Clone returned same pointer; want fresh allocation")
	}
	if got.Tolerance != nil {
		t.Errorf("Balance.Clone Tolerance = %v, want nil", got.Tolerance)
	}
}

func TestBalanceCloneNil(t *testing.T) {
	if got := (*Balance)(nil).Clone(); got != nil {
		t.Errorf("Balance.Clone on nil = %v, want nil", got)
	}
}

// --- Metadata.Without tests ---

func TestMetadataWithout_EmptyKeys(t *testing.T) {
	m := Metadata{Props: map[string]MetaValue{
		"k": {Kind: MetaString, String: "v"},
	}}
	got := m.Without()
	// No keys listed: receiver returned unchanged (same map pointer).
	if got.Props == nil {
		t.Fatal("Without() with no keys returned nil Props")
	}
	// Pointer identity proves no reallocation (allocation-free no-op contract).
	if reflect.ValueOf(got.Props).Pointer() != reflect.ValueOf(m.Props).Pointer() {
		t.Errorf("Without() with no keys reallocated Props: got different map pointer, want same map pointer")
	}
}

func TestMetadataWithout_NoPresentKey(t *testing.T) {
	m := Metadata{Props: map[string]MetaValue{
		"a": {Kind: MetaString, String: "1"},
	}}
	got := m.Without("absent")
	// Key not present: receiver returned unchanged (same map pointer, no allocation).
	if reflect.ValueOf(got.Props).Pointer() != reflect.ValueOf(m.Props).Pointer() {
		t.Errorf("Without(absent): reallocated Props; want same map pointer (allocation-free no-op)")
	}
}

func TestMetadataWithout_SingleKey(t *testing.T) {
	m := Metadata{Props: map[string]MetaValue{
		"keep":  {Kind: MetaString, String: "yes"},
		"strip": {Kind: MetaString, String: "no"},
	}}
	got := m.Without("strip")
	if _, ok := got.Props["strip"]; ok {
		t.Errorf("Without(strip): key still present in result")
	}
	if v, ok := got.Props["keep"]; !ok || v.String != "yes" {
		t.Errorf("Without(strip): 'keep' key missing or changed: %v", got.Props)
	}
	// Original must be unchanged.
	if _, ok := m.Props["strip"]; !ok {
		t.Errorf("Without(strip): original map was mutated — 'strip' key missing")
	}
}

func TestMetadataWithout_MultiKey(t *testing.T) {
	m := Metadata{Props: map[string]MetaValue{
		"a": {Kind: MetaString, String: "1"},
		"b": {Kind: MetaString, String: "2"},
		"c": {Kind: MetaString, String: "3"},
	}}
	got := m.Without("a", "b")
	if _, ok := got.Props["a"]; ok {
		t.Errorf("Without(a,b): key 'a' still present")
	}
	if _, ok := got.Props["b"]; ok {
		t.Errorf("Without(a,b): key 'b' still present")
	}
	if v, ok := got.Props["c"]; !ok || v.String != "3" {
		t.Errorf("Without(a,b): key 'c' missing or changed: %v", got.Props)
	}
	// Original must be unchanged.
	if len(m.Props) != 3 {
		t.Errorf("Without(a,b): original Props modified (len=%d)", len(m.Props))
	}
}

func TestMetadataWithout_EmptyResult(t *testing.T) {
	m := Metadata{Props: map[string]MetaValue{
		"only": {Kind: MetaString, String: "x"},
	}}
	got := m.Without("only")
	if len(got.Props) != 0 {
		t.Errorf("Without(only): got Props=%v, want empty", got.Props)
	}
	// Original must still have the key.
	if _, ok := m.Props["only"]; !ok {
		t.Errorf("Without(only): original map was mutated")
	}
}

func TestMetadataWithout_NilProps(t *testing.T) {
	m := Metadata{}
	got := m.Without("any")
	if got.Props != nil {
		t.Errorf("Without on nil Props: want nil result Props, got %v", got.Props)
	}
}

// --- StripMetaKeys tests ---

// TestStripMetaKeys_AllMetaBearingKinds verifies that StripMetaKeys removes
// the target key from each metadata-bearing directive type and does not mutate
// the original. One table entry per directive kind that the switch covers.
func TestStripMetaKeys_AllMetaBearingKinds(t *testing.T) {
	const stripKey = "route-account"
	const keepKey = "keep-me"
	date := time.Date(2024, time.March, 15, 0, 0, 0, 0, time.UTC)

	buildMeta := func() Metadata {
		return Metadata{Props: map[string]MetaValue{
			stripKey: {Kind: MetaString, String: "val"},
			keepKey:  {Kind: MetaString, String: "yes"},
		}}
	}

	cases := []struct {
		name      string
		directive func() Directive
		getMeta   func(Directive) Metadata
	}{
		{
			name: "Transaction",
			directive: func() Directive {
				return &Transaction{Date: date, Flag: '*', Narration: "t", Meta: buildMeta(),
					Postings: []Posting{{
						Account: Account("Assets:Cash"),
						Meta:    buildMeta(),
					}},
				}
			},
			getMeta: func(d Directive) Metadata { return d.(*Transaction).Meta },
		},
		{
			name:      "Open",
			directive: func() Directive { return &Open{Date: date, Account: Account("Assets:A"), Meta: buildMeta()} },
			getMeta:   func(d Directive) Metadata { return d.(*Open).Meta },
		},
		{
			name:      "Close",
			directive: func() Directive { return &Close{Date: date, Account: Account("Assets:A"), Meta: buildMeta()} },
			getMeta:   func(d Directive) Metadata { return d.(*Close).Meta },
		},
		{
			name: "Pad",
			directive: func() Directive {
				return &Pad{Date: date, Account: Account("Assets:A"), PadAccount: Account("Equity:Opening"), Meta: buildMeta()}
			},
			getMeta: func(d Directive) Metadata { return d.(*Pad).Meta },
		},
		{
			name: "Note",
			directive: func() Directive {
				return &Note{Date: date, Account: Account("Assets:A"), Comment: "hi", Meta: buildMeta()}
			},
			getMeta: func(d Directive) Metadata { return d.(*Note).Meta },
		},
		{
			name: "Document",
			directive: func() Directive {
				return &Document{Date: date, Account: Account("Assets:A"), Path: "/p", Meta: buildMeta()}
			},
			getMeta: func(d Directive) Metadata { return d.(*Document).Meta },
		},
		{
			name: "Price",
			directive: func() Directive {
				return &Price{Date: date, Commodity: "USD", Amount: Amount{Number: cloneTestDecimal("1"), Currency: "EUR"}, Meta: buildMeta()}
			},
			getMeta: func(d Directive) Metadata { return d.(*Price).Meta },
		},
		{
			name:      "Event",
			directive: func() Directive { return &Event{Date: date, Name: "location", Value: "NYC", Meta: buildMeta()} },
			getMeta:   func(d Directive) Metadata { return d.(*Event).Meta },
		},
		{
			name:      "Query",
			directive: func() Directive { return &Query{Date: date, Name: "q", BQL: "SELECT *", Meta: buildMeta()} },
			getMeta:   func(d Directive) Metadata { return d.(*Query).Meta },
		},
		{
			name:      "Custom",
			directive: func() Directive { return &Custom{Date: date, TypeName: "mytype", Meta: buildMeta()} },
			getMeta:   func(d Directive) Metadata { return d.(*Custom).Meta },
		},
		{
			name:      "Commodity",
			directive: func() Directive { return &Commodity{Date: date, Currency: "USD", Meta: buildMeta()} },
			getMeta:   func(d Directive) Metadata { return d.(*Commodity).Meta },
		},
		{
			name: "Balance",
			directive: func() Directive {
				return &Balance{Date: date, Account: Account("Assets:A"), Amount: Amount{Number: cloneTestDecimal("0"), Currency: "USD"}, Meta: buildMeta()}
			},
			getMeta: func(d Directive) Metadata { return d.(*Balance).Meta },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := tc.directive()

			result := StripMetaKeys(orig, []string{stripKey})

			// Stripped key must be absent from result.
			got := tc.getMeta(result)
			if _, ok := got.Props[stripKey]; ok {
				t.Errorf("StripMetaKeys(%s): stripped key %q still present in result", tc.name, stripKey)
			}
			// Non-stripped key must still be present.
			if _, ok := got.Props[keepKey]; !ok {
				t.Errorf("StripMetaKeys(%s): key %q was unexpectedly removed from result", tc.name, keepKey)
			}

			// Original directive must not have been mutated.
			origMeta := tc.getMeta(orig)
			if _, ok := origMeta.Props[stripKey]; !ok {
				t.Errorf("StripMetaKeys(%s): original directive was mutated — stripped key %q missing", tc.name, stripKey)
			}

			// For Transaction, also verify posting metadata was stripped.
			if txn, ok := result.(*Transaction); ok {
				for i, p := range txn.Postings {
					if _, ok := p.Meta.Props[stripKey]; ok {
						t.Errorf("StripMetaKeys(Transaction): stripped key %q still present in posting[%d].Meta", stripKey, i)
					}
				}
			}
		})
	}
}
