package bytes

import (
	"errors"
	"flag"
	"testing"
)

func TestCapacityString(t *testing.T) {
	tests := []struct {
		want string
		in   Capacity
	}{
		{want: "0", in: 0},
		{want: "1", in: 1},
		{want: "999", in: 999},
		{want: "1K", in: Kilobyte},
		{want: "1K", in: 1001},
		{want: "1K", in: 1040},     // .04 rounds down
		{want: "1.1K", in: 1050},   // .05 is the first hundredth that rounds up
		{want: "1.1K", in: 1100},   // .1 rounds to a tenth
		{want: "1.1K", in: 1149},   // .149 rounds to .1
		{want: "2K", in: 1990},     // .99 rounds up to the next whole unit
		{want: "1K", in: Kibibyte}, // 1024 bytes reads as 1K in base 10
		{want: "1M", in: Megabyte},
		{want: "999.9K", in: 999_949},
		{want: "1M", in: 999_950}, // would round to 1000K
		{want: "1M", in: 999_960},
		{want: "1M", in: Mebibyte}, // 1048576 -> 1.048M -> 1M
		{want: "10.4G", in: 10_400_000_000},
		{want: "1G", in: Gigabyte},
		{want: "1T", in: Terabyte},
		{want: "1P", in: Petabyte},
		{want: "1E", in: Exabyte},
		{want: "1.1G", in: Gibibyte},
		{want: "1.1T", in: Tebibyte},
		{want: "999.9P", in: 999_900_000_000_000_000}, // longest fractional output
		{want: "1E", in: 999_960_000_000_000_000},
		{want: "18.4E", in: maxCapacity}, // longest whole-unit output
	}
	for _, tt := range tests {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("Capacity(%d).String() = %q, want %q", uint64(tt.in), got, tt.want)
		}
	}
}

func TestParseCapacity(t *testing.T) {
	tests := []struct {
		in   string
		want Capacity
	}{
		{"0", 0},
		{"1", Byte},
		{"512", 512 * Byte},
		{"0G", 0},
		{"10G", 10 * Gigabyte},
		{"5T", 5 * Terabyte},
		{"1K", Kilobyte},
		{"1M", Megabyte},
		{"1P", Petabyte},
		{"1E", Exabyte},
		{"1Ki", Kibibyte},
		{"512Mi", 512 * Mebibyte},
		{"1Gi", Gibibyte},
		{"1Ti", Tebibyte},
		{"1Pi", Pebibyte},
		{"1Ei", Exbibyte},
		{"18446744073709551615", maxCapacity},
	}
	for _, tt := range tests {
		got, err := ParseCapacity(tt.in)
		if err != nil {
			t.Errorf("ParseCapacity(%q) returned error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseCapacity(%q) = %d, want %d", tt.in, uint64(got), uint64(tt.want))
		}
	}
}

func TestParseCapacityErrors(t *testing.T) {
	tests := []string{
		"",                      // empty
		"G",                     // no digits
		"-1",                    // sign is not a digit
		"10X",                   // unknown unit
		"10Xi",                  // unknown binary unit
		"10x",                   // units are case sensitive
		"10GB",                  // two-byte suffix must end in 'i'
		"10Gib",                 // suffix too long
		"10 G",                  // no internal whitespace
		"1.5G",                  // whole integers only
		"18446744073709551616",  // one past uint64
		"999999999999999999999", // far past uint64
		"18446744073709551615K", // overflows on the unit multiply
		"20E",                   // 20 exabytes exceeds uint64
	}
	for _, in := range tests {
		got, err := ParseCapacity(in)
		if err == nil {
			t.Errorf("ParseCapacity(%q) = %d, want error", in, uint64(got))
			continue
		}
		if !errors.Is(err, ErrInvalidCapacity) {
			t.Errorf("ParseCapacity(%q) error %v does not wrap ErrInvalidCapacity", in, err)
		}
		if got != 0 {
			t.Errorf("ParseCapacity(%q) = %d on error, want 0", in, uint64(got))
		}
	}
}

func TestParseCapacityRoundTrip(t *testing.T) {
	// Every value that String renders exactly must parse back to itself.
	for _, c := range []Capacity{0, 1, 999, Kilobyte, Megabyte, Gigabyte, Terabyte, Petabyte, Exabyte} {
		s := c.String()
		got, err := ParseCapacity(s)
		if err != nil {
			t.Errorf("ParseCapacity(%q): %v", s, err)
			continue
		}
		if got != c {
			t.Errorf("round trip of %d via %q = %d", uint64(c), s, uint64(got))
		}
	}
}

func TestUnitConversions(t *testing.T) {
	c := 2 * Gibibyte
	if got := c.Bytes(); got != 2*1024*1024*1024 {
		t.Errorf("Bytes() = %d", got)
	}
	if got := c.Mebibytes(); got != 2048 {
		t.Errorf("Mebibytes() = %d", got)
	}
	if got := c.Gibibytes(); got != 2 {
		t.Errorf("Gibibytes() = %d", got)
	}
	if got := Exbibyte.Exbibytes(); got != 1 {
		t.Errorf("Exbibytes() = %d", got)
	}

	d := 2 * Gigabyte
	if got := d.Kilobytes(); got != 2_000_000 {
		t.Errorf("Kilobytes() = %d", got)
	}
	if got := d.Gigabytes(); got != 2 {
		t.Errorf("Gigabytes() = %d", got)
	}
	if got := Exabyte.Exabytes(); got != 1 {
		t.Errorf("Exabytes() = %d", got)
	}
}

func TestCapacitySet(t *testing.T) {
	var c Capacity
	if err := c.Set("512Mi"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if c != 512*Mebibyte {
		t.Errorf("after Set, c = %d, want %d", uint64(c), uint64(512*Mebibyte))
	}

	// A failed Set must leave the receiver untouched.
	if err := c.Set("bogus"); err == nil {
		t.Fatal("Set(\"bogus\") = nil, want error")
	}
	if c != 512*Mebibyte {
		t.Errorf("failed Set mutated receiver to %d", uint64(c))
	}
}

func TestCapacityImplementsFlagValue(t *testing.T) {
	var c Capacity
	var _ flag.Value = &c

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Var(&c, "size", "maximum size")
	if err := fs.Parse([]string{"-size", "10G"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c != 10*Gigabyte {
		t.Errorf("flag parsed to %d, want %d", uint64(c), uint64(10*Gigabyte))
	}
}
