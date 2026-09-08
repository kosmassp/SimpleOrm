package core

import (
	"regexp"
	"strings"
	"time"
)

// The C# "o" round-trip layouts (§7.9): seven fractional digits, a literal Z
// for UTC or the explicit offset for datetimeoffset.
const (
	iso8601UTCLayout    = "2006-01-02T15:04:05.0000000Z"
	iso8601OffsetLayout = "2006-01-02T15:04:05.0000000-07:00"
	dateLayout          = "2006-01-02"
	timeLayout          = "15:04:05.0000000"
)

var offsetSuffix = regexp.MustCompile(`[+-][0-9]{2}:[0-9]{2}$`)

// FormatUTC is ISO-8601 UTC with C#'s "o" precision — the one place the stored
// datetime shape is produced (CODING-STANDARD §8): temporal binding, the JSON
// handler, the runner's applied_at, and snapshot generatedAt all call this.
// Any location converts to UTC first.
func FormatUTC(value time.Time) string { return value.UTC().Format(iso8601UTCLayout) }

// FormatOffset is the datetimeoffset form: "o" with the value's own offset kept.
func FormatOffset(value time.Time) string { return value.Format(iso8601OffsetLayout) }

// FormatDate is the `date` token's storage form (C# DateOnly "O").
func FormatDate(value time.Time) string { return value.Format(dateLayout) }

// FormatTime is the `time` token's storage form (C# TimeOnly "O").
func FormatTime(value time.Time) string { return value.Format(timeLayout) }

// HasUTCMarker is the §7.9 read rule: a stored datetime must end with Z or an explicit ±hh:mm offset.
func HasUTCMarker(text string) bool {
	trimmed := strings.TrimRight(text, " \t\r\n")
	return strings.HasSuffix(trimmed, "Z") || strings.HasSuffix(trimmed, "z") || offsetSuffix.MatchString(trimmed)
}

// ParseMarked reads a stored datetime under the UTC rule (VAL-020 when the
// marker is missing) and normalizes it to UTC. A space separator is accepted
// beside T; fractional seconds up to nine digits.
func ParseMarked(text string, context string) (time.Time, error) {
	trimmed := strings.TrimSpace(text)
	if !HasUTCMarker(trimmed) {
		return time.Time{}, Errorf("VAL-020", context,
			"stored datetime '%s' has no UTC/offset marker; the convention is ISO-8601 UTC with a trailing Z", text)
	}
	normalized := trimmed
	if len(normalized) > 10 && normalized[10] == ' ' {
		normalized = normalized[:10] + "T" + normalized[11:]
	}
	parsed, err := time.Parse(time.RFC3339Nano, normalized)
	if err != nil {
		return time.Time{}, Errorf("MAP-031", context, "cannot convert '%s' to a datetime: %s", text, err)
	}
	return parsed.UTC(), nil
}

// ParseOffset reads a stored datetimeoffset, keeping its offset (VAL-020 when unmarked).
func ParseOffset(text string, context string) (time.Time, error) {
	trimmed := strings.TrimSpace(text)
	if !HasUTCMarker(trimmed) {
		return time.Time{}, Errorf("VAL-020", context,
			"stored datetime '%s' has no UTC/offset marker; the convention is ISO-8601 UTC with a trailing Z", text)
	}
	normalized := trimmed
	if len(normalized) > 10 && normalized[10] == ' ' {
		normalized = normalized[:10] + "T" + normalized[11:]
	}
	parsed, err := time.Parse(time.RFC3339Nano, normalized)
	if err != nil {
		return time.Time{}, Errorf("MAP-031", context, "cannot convert '%s' to a datetimeoffset: %s", text, err)
	}
	return parsed, nil
}

// ParseDate reads the `date` storage form (a leading date of a longer ISO string is accepted).
func ParseDate(text string, context string) (time.Time, error) {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > len(dateLayout) {
		trimmed = trimmed[:len(dateLayout)]
	}
	parsed, err := time.ParseInLocation(dateLayout, trimmed, time.UTC)
	if err != nil {
		return time.Time{}, Errorf("MAP-031", context, "cannot convert '%s' to a date: %s", text, err)
	}
	return parsed, nil
}

// ParseTime reads the `time` storage form with up to nine fractional digits.
func ParseTime(text string, context string) (time.Time, error) {
	trimmed := strings.TrimSpace(text)
	for _, layout := range []string{"15:04:05.999999999", "15:04:05", "15:04"} {
		if parsed, err := time.ParseInLocation(layout, trimmed, time.UTC); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, Errorf("MAP-031", context, "cannot convert '%s' to a time", text)
}
