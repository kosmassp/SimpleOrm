package core

import (
	"testing"
	"time"
)

func TestFormatUTC_WritesSevenFractionDigitsAndZ(t *testing.T) {
	value := time.Date(2026, 8, 28, 9, 30, 0, 0, time.UTC)
	if got := FormatUTC(value); got != "2026-08-28T09:30:00.0000000Z" {
		t.Errorf("FormatUTC = %q", got)
	}
	plusTwo := time.Date(2026, 8, 28, 11, 30, 0, 1234567, time.FixedZone("CEST", 2*3600))
	if got := FormatUTC(plusTwo); got != "2026-08-28T09:30:00.0012345Z" {
		t.Errorf("FormatUTC converts to UTC first: %q", got)
	}
	if got := FormatOffset(plusTwo); got != "2026-08-28T11:30:00.0012345+02:00" {
		t.Errorf("FormatOffset keeps the offset: %q", got)
	}
	if got := FormatDate(value); got != "2026-08-28" {
		t.Errorf("FormatDate = %q", got)
	}
	if got := FormatTime(plusTwo); got != "11:30:00.0012345" {
		t.Errorf("FormatTime = %q", got)
	}
}

func TestParseMarked_RequiresTheUTCMarker(t *testing.T) {
	for _, unmarked := range []string{"2026-01-01T00:00:00", "2026-01-01 00:00:00", "2026-01-01T00:00:00.000"} {
		_, err := ParseMarked(unmarked, "row")
		if CodeOf(err) != "VAL-020" {
			t.Errorf("ParseMarked(%q): code %q, want VAL-020", unmarked, CodeOf(err))
		}
	}
	got, err := ParseMarked("2026-01-01T02:00:00+02:00", "row")
	if err != nil {
		t.Fatal(err)
	}
	if got.Location() != time.UTC || !got.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("offset normalizes to UTC: %v", got)
	}
	spaced, err := ParseMarked("2026-08-28 09:30:00.0000000Z", "row")
	if err != nil || !spaced.Equal(time.Date(2026, 8, 28, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("space separator accepted: %v %v", spaced, err)
	}
	if _, err := ParseMarked("not a date Z", "row"); CodeOf(err) != "MAP-031" {
		t.Errorf("garbage with a marker is MAP-031, got %q", CodeOf(err))
	}
}

func TestParseDateAndTime(t *testing.T) {
	date, err := ParseDate("2026-08-28", "row")
	if err != nil || !date.Equal(time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("ParseDate: %v %v", date, err)
	}
	clock, err := ParseTime("11:30:00.0012345", "row")
	if err != nil || clock.Hour() != 11 || clock.Nanosecond() != 1234500 {
		t.Errorf("ParseTime: %v %v", clock, err)
	}
	if _, err := ParseTime("noon", "row"); CodeOf(err) != "MAP-031" {
		t.Errorf("ParseTime garbage: %q", CodeOf(err))
	}
}
