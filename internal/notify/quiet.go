package notify

import (
	"fmt"
	"time"
)

// NextAllowedTime evaluates quiet hours in the user's IANA timezone. It
// returns the input unchanged when quiet hours are disabled or incomplete.
func NextAllowedTime(now time.Time, timezone string, enabled bool, start, end *string) (time.Time, error) {
	if !enabled || start == nil || end == nil || *start == "" || *end == "" {
		return now, nil
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timezone %q: %w", timezone, err)
	}
	parse := func(value string) (time.Duration, error) {
		parsed, err := time.Parse("15:04", value)
		if err != nil {
			return 0, fmt.Errorf("invalid quiet-hour time %q: %w", value, err)
		}
		return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute, nil
	}
	startAt, err := parse(*start)
	if err != nil {
		return time.Time{}, err
	}
	endAt, err := parse(*end)
	if err != nil {
		return time.Time{}, err
	}
	local := now.In(loc)
	minute := time.Duration(local.Hour())*time.Hour + time.Duration(local.Minute())*time.Minute
	inQuiet := false
	if startAt == endAt {
		inQuiet = true
	} else if startAt < endAt {
		inQuiet = minute >= startAt && minute < endAt
	} else {
		inQuiet = minute >= startAt || minute < endAt
	}
	if !inQuiet {
		return now, nil
	}
	base := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	if startAt < endAt {
		return base.Add(endAt).UTC(), nil
	}
	// A cross-midnight quiet range ends on the same local date when the
	// current time is after midnight, otherwise it ends tomorrow.
	if minute < endAt {
		return base.Add(endAt).UTC(), nil
	}
	return base.Add(24*time.Hour + endAt).UTC(), nil
}
