package ast

import (
	"testing"
)

// TestResolveBookingMethod covers every row of the locked semantics matrix.
func TestResolveBookingMethod(t *testing.T) {
	span := Span{Start: Position{Filename: "test.bean", Line: 3}}

	// Helper to build an Open with a given booking method and span.
	open := func(m BookingMethod) *Open {
		return &Open{Account: "Assets:Test", Booking: m, Span: span}
	}

	// mkOpts builds an OptionValues with a specific booking_method value.
	mkOpts := func(raw string) *OptionValues {
		v := NewOptionValues()
		_ = v.set("booking_method", raw) // parseStringOption never errors
		return v
	}

	tests := []struct {
		name       string
		d          *Open
		opts       *OptionValues
		wantMethod BookingMethod
		wantDiag   bool
	}{
		// Explicit booking on directive: option is ignored.
		{
			name:       "explicit strict ignores option",
			d:          open(BookingStrict),
			opts:       mkOpts("NONE"),
			wantMethod: BookingStrict,
		},
		{
			name:       "explicit fifo ignores option",
			d:          open(BookingFIFO),
			opts:       mkOpts("LIFO"),
			wantMethod: BookingFIFO,
		},
		{
			name:       "explicit lifo ignores option",
			d:          open(BookingLIFO),
			opts:       mkOpts("STRICT"),
			wantMethod: BookingLIFO,
		},
		{
			name:       "explicit none ignores option",
			d:          open(BookingNone),
			opts:       mkOpts("FIFO"),
			wantMethod: BookingNone,
		},
		{
			name:       "explicit average ignores option",
			d:          open(BookingAverage),
			opts:       mkOpts("NONE"),
			wantMethod: BookingAverage,
		},

		// BookingDefault + option unset or empty → BookingStrict, no diagnostic.
		{
			name:       "default + nil opts → strict",
			d:          open(BookingDefault),
			opts:       nil,
			wantMethod: BookingStrict,
		},
		{
			name:       "default + empty option → strict",
			d:          open(BookingDefault),
			opts:       mkOpts(""),
			wantMethod: BookingStrict,
		},
		{
			name:       "default + default opts → strict",
			d:          open(BookingDefault),
			opts:       NewOptionValues(),
			wantMethod: BookingStrict,
		},

		// BookingDefault + recognized keyword → corresponding method.
		{
			name:       "default + STRICT",
			d:          open(BookingDefault),
			opts:       mkOpts("STRICT"),
			wantMethod: BookingStrict,
		},
		{
			name:       "default + FIFO",
			d:          open(BookingDefault),
			opts:       mkOpts("FIFO"),
			wantMethod: BookingFIFO,
		},
		{
			name:       "default + LIFO",
			d:          open(BookingDefault),
			opts:       mkOpts("LIFO"),
			wantMethod: BookingLIFO,
		},
		{
			name:       "default + NONE",
			d:          open(BookingDefault),
			opts:       mkOpts("NONE"),
			wantMethod: BookingNone,
		},
		{
			name:       "default + AVERAGE",
			d:          open(BookingDefault),
			opts:       mkOpts("AVERAGE"),
			wantMethod: BookingAverage,
		},

		// BookingDefault + unknown value → BookingStrict + Error diagnostic.
		{
			name:       "default + unknown → strict + diag",
			d:          open(BookingDefault),
			opts:       mkOpts("BOGUS"),
			wantMethod: BookingStrict,
			wantDiag:   true,
		},
		{
			name:       "default + lowercase → strict + diag",
			d:          open(BookingDefault),
			opts:       mkOpts("strict"),
			wantMethod: BookingStrict,
			wantDiag:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := ResolveBookingMethod(tt.d, tt.opts)
			if got != tt.wantMethod {
				t.Errorf("ResolveBookingMethod() method = %v, want %v", got, tt.wantMethod)
			}
			if tt.wantDiag {
				if len(diags) == 0 {
					t.Fatalf("ResolveBookingMethod() diags = nil, want non-empty")
				}
				d := diags[0]
				if d.Code != invalidOptionCode {
					t.Errorf("diag.Code = %q, want %q", d.Code, invalidOptionCode)
				}
				if d.Severity != Error {
					t.Errorf("diag.Severity = %v, want Error", d.Severity)
				}
				if d.Span != span {
					t.Errorf("diag.Span = %v, want %v", d.Span, span)
				}
			} else {
				if len(diags) != 0 {
					t.Errorf("ResolveBookingMethod() unexpected diags: %v", diags)
				}
			}
		})
	}
}

func TestParseBookingMethod(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    BookingMethod
		wantErr bool
	}{
		{name: "empty", in: "", want: BookingDefault},
		{name: "strict", in: "STRICT", want: BookingStrict},
		{name: "fifo", in: "FIFO", want: BookingFIFO},
		{name: "lifo", in: "LIFO", want: BookingLIFO},
		{name: "none", in: "NONE", want: BookingNone},
		{name: "average", in: "AVERAGE", want: BookingAverage},
		{name: "lowercase strict", in: "strict", wantErr: true},
		{name: "mixed case average", in: "Average", wantErr: true},
		{name: "unknown foo", in: "FOO", wantErr: true},
		{name: "whitespace", in: " STRICT ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseBookingMethod(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseBookingMethod(%q) = %v, nil; want error", tt.in, got)
				}
				if got != 0 {
					t.Errorf("ParseBookingMethod(%q) returned %v on error; want zero value", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseBookingMethod(%q) returned unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseBookingMethod(%q) = %v; want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseBookingMethodRoundTrip(t *testing.T) {
	// Every uppercase keyword must round-trip through String/Parse.
	// BookingDefault is excluded: ParseBookingMethod("") returns
	// BookingDefault, but BookingDefault.String() returns "DEFAULT", so the
	// round-trip is intentionally asymmetric.
	keywords := []string{"STRICT", "FIFO", "LIFO", "NONE", "AVERAGE"}
	for _, kw := range keywords {
		t.Run(kw, func(t *testing.T) {
			m, err := ParseBookingMethod(kw)
			if err != nil {
				t.Fatalf("ParseBookingMethod(%q) returned error: %v", kw, err)
			}
			if got := m.String(); got != kw {
				t.Errorf("ParseBookingMethod(%q).String() = %q; want %q", kw, got, kw)
			}
		})
	}
}

func TestBookingMethodString(t *testing.T) {
	tests := []struct {
		name string
		m    BookingMethod
		want string
	}{
		{name: "default", m: BookingDefault, want: "DEFAULT"},
		{name: "strict", m: BookingStrict, want: "STRICT"},
		{name: "fifo", m: BookingFIFO, want: "FIFO"},
		{name: "lifo", m: BookingLIFO, want: "LIFO"},
		{name: "none", m: BookingNone, want: "NONE"},
		{name: "average", m: BookingAverage, want: "AVERAGE"},
		{name: "unknown", m: BookingMethod(999), want: "BookingMethod(999)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.String(); got != tt.want {
				t.Errorf("BookingMethod(%d).String() = %q; want %q", int(tt.m), got, tt.want)
			}
		})
	}
}
