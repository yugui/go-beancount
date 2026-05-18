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
	got := accountstate.Build(seqOf(nil), nil)
	if len(got.State) != 0 {
		t.Errorf("State = %v, want empty map", got.State)
	}
	if len(got.DuplicateOpens) != 0 {
		t.Errorf("DuplicateOpens = %v, want nil", got.DuplicateOpens)
	}
}

func TestBuild_NilSeq(t *testing.T) {
	got := accountstate.Build(nil, nil)
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
	got := accountstate.Build(seqOf([]ast.Directive{open}), nil)
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
	got := accountstate.Build(seqOf([]ast.Directive{open, close}), nil)
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
	got := accountstate.Build(seqOf([]ast.Directive{first, second}), nil)
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
	got := accountstate.Build(seqOf([]ast.Directive{close}), nil)
	if len(got.State) != 0 {
		t.Errorf("State = %v, want empty map (orphan close is not diagnosed here)", got.State)
	}
	if len(got.DuplicateOpens) != 0 {
		t.Errorf("DuplicateOpens = %v, want nil", got.DuplicateOpens)
	}
}

// parseOpts builds an *ast.OptionValues from a single option directive,
// for feeding into accountstate.Build in option-driven tests.
func parseOpts(t *testing.T, key, value string) *ast.OptionValues {
	t.Helper()
	l := &ast.Ledger{}
	l.InsertAll([]ast.Directive{&ast.Option{Key: key, Value: value}})
	opts, diags := ast.ParseOptions(l)
	if len(diags) > 0 {
		t.Fatalf("ParseOptions: %v", diags)
	}
	return opts
}

// TestBuild_OptionDrivenBookingDefault verifies that an Open without an
// explicit booking keyword picks up the resolved booking from the
// "booking_method" option, and that the result is stored in State.Booking.
func TestBuild_OptionDrivenBookingDefault(t *testing.T) {
	opts := parseOpts(t, "booking_method", "NONE")
	open := &ast.Open{
		Date:    mustDate(t, "2024-01-01"),
		Account: "Assets:Cash",
		Booking: ast.BookingDefault,
	}
	got := accountstate.Build(seqOf([]ast.Directive{open}), opts)
	st, ok := got.State["Assets:Cash"]
	if !ok {
		t.Fatalf("State[%q] missing", "Assets:Cash")
	}
	if st.Booking != ast.BookingNone {
		t.Errorf("Booking = %v, want BookingNone (from option)", st.Booking)
	}
	if len(got.Diagnostics) != 0 {
		t.Errorf("Diagnostics = %v, want none", got.Diagnostics)
	}
}

// TestBuild_OptionDrivenBookingExplicitWins confirms that an explicit booking
// keyword on the Open directive takes precedence over the option.
func TestBuild_OptionDrivenBookingExplicitWins(t *testing.T) {
	opts := parseOpts(t, "booking_method", "NONE")
	open := &ast.Open{
		Date:    mustDate(t, "2024-01-01"),
		Account: "Assets:Investments",
		Booking: ast.BookingFIFO,
	}
	got := accountstate.Build(seqOf([]ast.Directive{open}), opts)
	st, ok := got.State["Assets:Investments"]
	if !ok {
		t.Fatalf("State[%q] missing", "Assets:Investments")
	}
	if st.Booking != ast.BookingFIFO {
		t.Errorf("Booking = %v, want BookingFIFO (explicit wins over option)", st.Booking)
	}
	if len(got.Diagnostics) != 0 {
		t.Errorf("Diagnostics = %v, want none", got.Diagnostics)
	}
}

// TestBuild_InvalidBookingMethodOption verifies that an unrecognized
// booking_method option value surfaces as a diagnostic and falls back to STRICT.
func TestBuild_InvalidBookingMethodOption(t *testing.T) {
	// parseStringOption accepts any string, so "BOGUS" is stored as-is.
	// The error surfaces at resolution time.
	opts := parseOpts(t, "booking_method", "BOGUS")
	open := &ast.Open{
		Date:    mustDate(t, "2024-01-01"),
		Account: "Assets:Cash",
		Booking: ast.BookingDefault,
	}
	got := accountstate.Build(seqOf([]ast.Directive{open}), opts)
	st, ok := got.State["Assets:Cash"]
	if !ok {
		t.Fatalf("State[%q] missing", "Assets:Cash")
	}
	if st.Booking != ast.BookingStrict {
		t.Errorf("Booking = %v, want BookingStrict (fallback)", st.Booking)
	}
	if len(got.Diagnostics) != 1 {
		t.Fatalf("len(Diagnostics) = %d, want 1", len(got.Diagnostics))
	}
	if got.Diagnostics[0].Severity != ast.Error {
		t.Errorf("Diagnostics[0].Severity = %v, want Error", got.Diagnostics[0].Severity)
	}
}

// TestBuild_InvalidBookingMethodOption_MultiOpenSpan verifies that each
// Open directive with BookingDefault and a bad booking_method option
// surfaces its own diagnostic with the Open's own span.
func TestBuild_InvalidBookingMethodOption_MultiOpenSpan(t *testing.T) {
	opts := parseOpts(t, "booking_method", "BOGUS")
	span1 := ast.Span{Start: ast.Position{Filename: "test.bean", Line: 1}}
	span2 := ast.Span{Start: ast.Position{Filename: "test.bean", Line: 5}}
	open1 := &ast.Open{
		Date:    mustDate(t, "2024-01-01"),
		Account: "Assets:Cash",
		Booking: ast.BookingDefault,
		Span:    span1,
	}
	open2 := &ast.Open{
		Date:    mustDate(t, "2024-02-01"),
		Account: "Assets:Savings",
		Booking: ast.BookingDefault,
		Span:    span2,
	}
	got := accountstate.Build(seqOf([]ast.Directive{open1, open2}), opts)
	if len(got.Diagnostics) != 2 {
		t.Fatalf("len(Diagnostics) = %d, want 2", len(got.Diagnostics))
	}
	if got.Diagnostics[0].Span != span1 {
		t.Errorf("Diagnostics[0].Span = %v, want %v", got.Diagnostics[0].Span, span1)
	}
	if got.Diagnostics[1].Span != span2 {
		t.Errorf("Diagnostics[1].Span = %v, want %v", got.Diagnostics[1].Span, span2)
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
