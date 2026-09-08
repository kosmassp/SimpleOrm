package core

import "testing"

func TestDecimal_ParsesCanonicalLiterals(t *testing.T) {
	cases := []struct{ input, text string }{
		{"19.99", "19.99"}, {" 7 ", "7"}, {"-0.50", "-0.50"}, {"-0", "0"}, {"-0.00", "0.00"}, {"0", "0"},
	}
	for _, c := range cases {
		d, err := ParseDecimal(c.input)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", c.input, err)
		}
		if d.String() != c.text {
			t.Errorf("ParseDecimal(%q).String() = %q, want %q", c.input, d.String(), c.text)
		}
	}
}

func TestDecimal_RefusesNonLiterals(t *testing.T) {
	for _, input := range []string{"", "1e5", "1,000", "abc", "1.", ".5", "+1"} {
		if _, err := ParseDecimal(input); err == nil {
			t.Errorf("ParseDecimal(%q) accepted", input)
		} else if CodeOf(err) != "MAP-031" {
			t.Errorf("ParseDecimal(%q): code %q, want MAP-031", input, CodeOf(err))
		}
	}
}

func TestDecimal_EqualIgnoresTrailingFractionZeros(t *testing.T) {
	if !MustDecimal("19.90").Equal(MustDecimal("19.9")) {
		t.Error("19.90 should equal 19.9")
	}
	if MustDecimal("19.90").Equal(MustDecimal("19.91")) {
		t.Error("19.90 should not equal 19.91")
	}
	var zero Decimal
	if zero.String() != "0" || !zero.IsZero() || !zero.Equal(MustDecimal("0.000")) {
		t.Error("the zero value is 0")
	}
	if DecimalOf(-12).String() != "-12" {
		t.Error("DecimalOf keeps the sign")
	}
}
