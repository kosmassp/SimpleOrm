package conformance

// normalizeJSONNumber is the one "does this JSON-decoded float64 represent a
// whole number" check the conformance runners need (CODING-STANDARD §8):
// encoding/json decodes every JSON number as float64, and the ast, cases, and
// crud-cases fixtures all need the integral ones read back as int64 so they
// compare equal to (or bind as) the int64 values the library itself produces.
func normalizeJSONNumber(v float64) any {
	if v == float64(int64(v)) {
		return int64(v)
	}
	return v
}
