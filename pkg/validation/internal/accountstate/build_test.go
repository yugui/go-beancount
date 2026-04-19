package accountstate_test

import (
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/validation/internal/accountstate"
)

// seqOf adapts a slice of directives into an iter.Seq2[int, ast.Directive]
// for feeding into accountstate.Build without needing a full ast.Ledger.
func seqOf(directives []ast.Directive) func(yield func(int, ast.Directive) bool) {
	return func(yield func(int, ast.Directive) bool) {
		for i, d := range directives {
			if !yield(i, d) {
				return
			}
		}
	}
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return d
}

func TestBuild_Empty(t *testing.T) {
	got := accountstate.Build(seqOf(nil))
	if len(got.State) != 0 {
		t.Errorf("State = %v, want empty map", got.State)
	}
	if len(got.DuplicateOpens) != 0 {
		t.Errorf("DuplicateOpens = %v, want nil", got.DuplicateOpens)
	}
}

func TestBuild_NilSeq(t *testing.T) {
	got := accountstate.Build(nil)
	if got.State == nil {
		t.Errorf("State = nil, want non-nil empty map")
	}
	if len(got.State) != 0 {
		t.Errorf("State = %v, want empty map", got.State)
	}
	if got.DuplicateOpens != nil {
		t.Errorf("DuplicateOpens = %v, want nil", got.DuplicateOpens)
	}
}

func TestBuild_SingleOpen(t *testing.T) {
	open := &ast.Open{
		Date:       mustDate(t, "2024-01-01"),
		Account:    "Assets:Cash",
		Currencies: []string{"USD", "EUR"},
		Booking:    ast.BookingStrict,
	}
	got := accountstate.Build(seqOf([]ast.Directive{open}))
	if len(got.State) != 1 {
		t.Fatalf("len(State) = %d, want 1", len(got.State))
	}
	st, ok := got.State["Assets:Cash"]
	if !ok {
		t.Fatalf("State[%q] missing", "Assets:Cash")
	}
	if !st.OpenDate.Equal(open.Date) {
		t.Errorf("OpenDate = %v, want %v", st.OpenDate, open.Date)
	}
	if got, want := len(st.Currencies), 2; got != want {
		t.Errorf("len(Currencies) = %d, want %d", got, want)
	}
	if st.Booking != ast.BookingStrict {
		t.Errorf("Booking = %v, want BookingStrict", st.Booking)
	}
	if st.Closed {
		t.Errorf("Closed = true, want false")
	}
	if len(got.DuplicateOpens) != 0 {
		t.Errorf("DuplicateOpens = %v, want nil", got.DuplicateOpens)
	}
}

func TestBuild_OpenClose(t *testing.T) {
	open := &ast.Open{
		Date:    mustDate(t, "2024-01-01"),
		Account: "Assets:Cash",
	}
	close := &ast.Close{
		Date:    mustDate(t, "2024-06-01"),
		Account: "Assets:Cash",
	}
	got := accountstate.Build(seqOf([]ast.Directive{open, close}))
	st, ok := got.State["Assets:Cash"]
	if !ok {
		t.Fatalf("State[%q] missing", "Assets:Cash")
	}
	if !st.Closed {
		t.Errorf("Closed = false, want true")
	}
	if !st.CloseDate.Equal(close.Date) {
		t.Errorf("CloseDate = %v, want %v", st.CloseDate, close.Date)
	}
	if len(got.DuplicateOpens) != 0 {
		t.Errorf("DuplicateOpens = %v, want nil", got.DuplicateOpens)
	}
}

func TestBuild_DuplicateOpen(t *testing.T) {
	first := &ast.Open{
		Date:       mustDate(t, "2024-01-01"),
		Account:    "Assets:Cash",
		Currencies: []string{"USD"},
	}
	second := &ast.Open{
		Date:       mustDate(t, "2024-02-01"),
		Account:    "Assets:Cash",
		Currencies: []string{"EUR"},
	}
	got := accountstate.Build(seqOf([]ast.Directive{first, second}))
	st, ok := got.State["Assets:Cash"]
	if !ok {
		t.Fatalf("State[%q] missing", "Assets:Cash")
	}
	// First-wins: canonical state uses the first open's fields.
	if !st.OpenDate.Equal(first.Date) {
		t.Errorf("OpenDate = %v, want %v (first-wins)", st.OpenDate, first.Date)
	}
	if len(st.Currencies) != 1 || st.Currencies[0] != "USD" {
		t.Errorf("Currencies = %v, want [USD] (first-wins)", st.Currencies)
	}
	if len(got.DuplicateOpens) != 1 {
		t.Fatalf("len(DuplicateOpens) = %d, want 1", len(got.DuplicateOpens))
	}
	if got.DuplicateOpens[0] != second {
		t.Errorf("DuplicateOpens[0] = %p, want the second *ast.Open (%p)", got.DuplicateOpens[0], second)
	}
}

func TestBuild_CloseWithoutOpen_Ignored(t *testing.T) {
	close := &ast.Close{
		Date:    mustDate(t, "2024-06-01"),
		Account: "Assets:Cash",
	}
	got := accountstate.Build(seqOf([]ast.Directive{close}))
	if len(got.State) != 0 {
		t.Errorf("State = %v, want empty map (orphan close is not diagnosed here)", got.State)
	}
	if len(got.DuplicateOpens) != 0 {
		t.Errorf("DuplicateOpens = %v, want nil", got.DuplicateOpens)
	}
}

func TestAllowsCurrency(t *testing.T) {
	tests := []struct {
		name       string
		currencies []string
		currency   string
		want       bool
	}{
		{name: "empty list allows anything", currencies: nil, currency: "USD", want: true},
		{name: "empty list allows empty", currencies: nil, currency: "", want: true},
		{name: "listed currency allowed", currencies: []string{"USD", "EUR"}, currency: "USD", want: true},
		{name: "last listed allowed", currencies: []string{"USD", "EUR"}, currency: "EUR", want: true},
		{name: "unlisted denied", currencies: []string{"USD", "EUR"}, currency: "JPY", want: false},
		{name: "empty query denied when list set", currencies: []string{"USD"}, currency: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &accountstate.State{Currencies: tt.currencies}
			if got := s.AllowsCurrency(tt.currency); got != tt.want {
				t.Errorf("AllowsCurrency(%q) with %v = %v, want %v", tt.currency, tt.currencies, got, tt.want)
			}
		})
	}
}
