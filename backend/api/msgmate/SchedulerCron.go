package msgmate

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ci#744: the cron engine behind the bot self-message scheduler.
//
// It is a small, dependency-free parser/matcher (no new cron module in the
// binary) so schedule definitions can be evaluated deterministically in tests
// and cheaply at runtime.
//
// Syntax:
//
//	second minute hour day-of-month month day-of-week   (6 fields, optional)
//	       minute hour day-of-month month day-of-week   (5 fields)
//
// Every field accepts "*", "a", "a-b", "*/step", "a-b/step", "a/step" and
// comma-separated lists of those. Month and day-of-week also accept the usual
// three-letter names (JAN..DEC, SUN..SAT); 7 is accepted as Sunday. A 5-field
// expression fires at second 0. When both day-of-month and day-of-week are
// restricted, a day matches if either field matches (the standard cron OR
// rule).
type schedulerCron struct {
	seconds uint64
	minutes uint64
	hours   uint64
	doms    uint64
	months  uint64
	dows    uint64

	domRestricted bool
	dowRestricted bool
}

var schedulerCronMonthNames = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
}

var schedulerCronWeekdayNames = map[string]int{
	"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
}

// ParseSchedulerCron parses a 5- or 6-field cron expression.
func ParseSchedulerCron(expression string) (*schedulerCron, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 && len(fields) != 6 {
		return nil, fmt.Errorf("a cron expression needs 5 or 6 fields, got %d in %q", len(fields), expression)
	}
	secondsField := "0"
	if len(fields) == 6 {
		secondsField = fields[0]
		fields = fields[1:]
	}

	cron := &schedulerCron{}
	var err error
	if cron.seconds, _, err = parseSchedulerCronField(secondsField, 0, 59, nil); err != nil {
		return nil, fmt.Errorf("invalid second field: %w", err)
	}
	if cron.minutes, _, err = parseSchedulerCronField(fields[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("invalid minute field: %w", err)
	}
	if cron.hours, _, err = parseSchedulerCronField(fields[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("invalid hour field: %w", err)
	}
	if cron.doms, cron.domRestricted, err = parseSchedulerCronField(fields[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("invalid day-of-month field: %w", err)
	}
	if cron.months, _, err = parseSchedulerCronField(fields[3], 1, 12, schedulerCronMonthNames); err != nil {
		return nil, fmt.Errorf("invalid month field: %w", err)
	}
	if cron.dows, cron.dowRestricted, err = parseSchedulerCronField(fields[4], 0, 7, schedulerCronWeekdayNames); err != nil {
		return nil, fmt.Errorf("invalid day-of-week field: %w", err)
	}
	// 7 and 0 both mean Sunday; time.Weekday() uses 0 for Sunday.
	if cron.dows&(1<<7) != 0 {
		cron.dows = cron.dows&^(1<<7) | 1<<0
	}
	return cron, nil
}

// parseSchedulerCronField turns one field into a bitmask of allowed values and
// reports whether the field restricts the schedule (a bare "*" does not).
func parseSchedulerCronField(field string, min int, max int, names map[string]int) (uint64, bool, error) {
	var mask uint64
	restricted := false
	for _, element := range strings.Split(field, ",") {
		element = strings.TrimSpace(element)
		if element == "" {
			return 0, false, fmt.Errorf("empty list element in %q", field)
		}
		base := element
		step := 1
		if index := strings.Index(element, "/"); index >= 0 {
			base = strings.TrimSpace(element[:index])
			rawStep := strings.TrimSpace(element[index+1:])
			parsedStep, err := strconv.Atoi(rawStep)
			if err != nil || parsedStep <= 0 {
				return 0, false, fmt.Errorf("invalid step %q in %q", rawStep, field)
			}
			step = parsedStep
		}

		start, end := min, max
		switch {
		case base == "*":
			// The full range; step still applies.
		case strings.Contains(base, "-"):
			bounds := strings.SplitN(base, "-", 2)
			parsedStart, err := schedulerCronValue(bounds[0], names)
			if err != nil {
				return 0, false, err
			}
			parsedEnd, err := schedulerCronValue(bounds[1], names)
			if err != nil {
				return 0, false, err
			}
			start, end = parsedStart, parsedEnd
		default:
			value, err := schedulerCronValue(base, names)
			if err != nil {
				return 0, false, err
			}
			start, end = value, value
			if step > 1 {
				end = max
			}
		}

		if start < min || start > max || end < min || end > max {
			return 0, false, fmt.Errorf("value out of range in %q (allowed %d..%d)", field, min, max)
		}
		if start > end {
			return 0, false, fmt.Errorf("inverted range in %q", field)
		}
		for value := start; value <= end; value += step {
			mask |= 1 << uint(value)
		}
		if base != "*" || step > 1 {
			restricted = true
		}
	}
	return mask, restricted, nil
}

func schedulerCronValue(raw string, names map[string]int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty cron value")
	}
	if names != nil {
		if value, ok := names[strings.ToUpper(raw)]; ok {
			return value, nil
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid cron value %q", raw)
	}
	return value, nil
}

// Next returns the first instant strictly after `after` that matches the
// expression, evaluated in loc (UTC when loc is nil).
func (cron *schedulerCron) Next(after time.Time, loc *time.Location) (time.Time, bool) {
	if cron == nil {
		return time.Time{}, false
	}
	if loc == nil {
		loc = time.UTC
	}
	cursor := after.In(loc).Truncate(time.Second).Add(time.Second)
	limit := cursor.AddDate(5, 0, 0)
	for cursor.Before(limit) {
		if cron.months&(1<<uint(cursor.Month())) == 0 {
			cursor = time.Date(cursor.Year(), cursor.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
			continue
		}
		if !cron.matchesDay(cursor) {
			cursor = time.Date(cursor.Year(), cursor.Month(), cursor.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
			continue
		}
		if cron.hours&(1<<uint(cursor.Hour())) == 0 {
			cursor = time.Date(cursor.Year(), cursor.Month(), cursor.Day(), cursor.Hour(), 0, 0, 0, loc).Add(time.Hour)
			continue
		}
		if cron.minutes&(1<<uint(cursor.Minute())) == 0 {
			cursor = cursor.Truncate(time.Minute).Add(time.Minute)
			continue
		}
		if cron.seconds&(1<<uint(cursor.Second())) == 0 {
			cursor = cursor.Add(time.Second)
			continue
		}
		return cursor, true
	}
	return time.Time{}, false
}

// matchesDay applies the month restriction and the day-of-month/day-of-week OR
// rule.
func (cron *schedulerCron) matchesDay(day time.Time) bool {
	if cron.months&(1<<uint(day.Month())) == 0 {
		return false
	}
	domMatch := cron.doms&(1<<uint(day.Day())) != 0
	dowMatch := cron.dows&(1<<uint(day.Weekday())) != 0
	switch {
	case cron.domRestricted && cron.dowRestricted:
		return domMatch || dowMatch
	case cron.domRestricted:
		return domMatch
	case cron.dowRestricted:
		return dowMatch
	default:
		return true
	}
}
