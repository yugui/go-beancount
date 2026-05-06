package ast

import (
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
