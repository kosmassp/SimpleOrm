package core

import (
	"regexp"
	"strconv"
	"strings"
)

var decimalLiteral = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// Decimal is an exact decimal value (CODING-STANDARD §10): Go has no decimal
// type and floats would silently corrupt money. String-backed, canonical (no
// sign on zero, no exponent, no thousands separators), compared by value. The
// zero value is 0. Arithmetic is deliberately absent: the database does the
// math (§2 "real SQL first").
type Decimal struct {
	text string
}

// ParseDecimal accepts digits with an optional fraction and sign; anything else is an error.
func ParseDecimal(text string) (Decimal, error) {
	trimmed := strings.TrimSpace(text)
	if !decimalLiteral.MatchString(trimmed) {
		return Decimal{}, Errorf("MAP-031", "Decimal", "'%s' is not a decimal literal (digits with an optional fraction)", text)
	}
	return Decimal{text: canonicalDecimal(trimmed)}, nil
}

// MustDecimal is ParseDecimal for literals in code; it panics on a malformed literal.
func MustDecimal(text string) Decimal {
	d, err := ParseDecimal(text)
	if err != nil {
		panic(err)
	}
	return d
}

// DecimalOf converts an integer.
func DecimalOf(value int64) Decimal { return Decimal{text: strconv.FormatInt(value, 10)} }

// DecimalFromFloat formats a float with the shortest round-trip representation
// (invariant, no exponent) — for values the database returned as REAL.
func DecimalFromFloat(value float64) (Decimal, error) {
	return ParseDecimal(strconv.FormatFloat(value, 'f', -1, 64))
}

// String is the canonical invariant text (the conformance encoding and the stored TEXT).
func (d Decimal) String() string {
	if d.text == "" {
		return "0"
	}
	return d.text
}

// IsZero reports the zero value (0, 0.0, an unset Decimal).
func (d Decimal) IsZero() bool { return d.normalized() == "0" }

// Equal compares by value: 19.90 equals 19.9.
func (d Decimal) Equal(other Decimal) bool { return d.normalized() == other.normalized() }

// Value comparison ignores trailing fraction zeros.
func (d Decimal) normalized() string {
	text := d.String()
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "-0" || text == "" {
		return "0"
	}
	return text
}

// canonicalDecimal strips a sign from zero so equal values share one spelling.
func canonicalDecimal(text string) string {
	if strings.HasPrefix(text, "-") && strings.Trim(text[1:], "0.") == "" {
		return text[1:]
	}
	return text
}
