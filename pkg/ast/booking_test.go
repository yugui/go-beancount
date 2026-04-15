package ast

import (
	"testing"
)

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

func TestOpenResolveBookingMethod(t *testing.T) {
	tests := []struct {
		name    string
		booking string
		want    BookingMethod
		wantErr bool
	}{
		{name: "fifo", booking: "FIFO", want: BookingFIFO},
		{name: "empty", booking: "", want: BookingDefault},
		{name: "strict", booking: "STRICT", want: BookingStrict},
		{name: "invalid", booking: "bogus", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Open{Account: Assets, Booking: tt.booking}
			got, err := o.ResolveBookingMethod()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveBookingMethod() = %v, nil; want error", got)
				}
				if got != 0 {
					t.Errorf("ResolveBookingMethod() returned %v on error; want zero value", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveBookingMethod() returned unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveBookingMethod() = %v; want %v", got, tt.want)
			}
		})
	}
}
