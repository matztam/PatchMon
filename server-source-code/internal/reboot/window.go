// Package reboot implements host reboots for the matztam/PatchMon fork:
// manual reboot requests, automatic reboots through patch policies and the
// check that a host actually came back.
package reboot

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ParseClock parses "HH:MM" into minutes since midnight.
func ParseClock(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, fmt.Errorf("invalid time %q, want HH:MM", s)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 || len(m) != 2 {
		return 0, fmt.Errorf("invalid time %q, want HH:MM", s)
	}
	return hh*60 + mm, nil
}

// InWindow reports whether now falls into the maintenance window. days holds
// the weekdays (0 = Sunday) on which the window opens. A window whose end is
// before its start runs over midnight and belongs to the day it opened on.
// start == end means the whole day.
func InWindow(now time.Time, loc *time.Location, days []int32, start, end string) (bool, error) {
	startMin, err := ParseClock(start)
	if err != nil {
		return false, err
	}
	endMin, err := ParseClock(end)
	if err != nil {
		return false, err
	}
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	minute := local.Hour()*60 + local.Minute()
	today := int32(local.Weekday())
	yesterday := (today + 6) % 7

	switch {
	case startMin == endMin:
		return slices.Contains(days, today), nil
	case startMin < endMin:
		return slices.Contains(days, today) && minute >= startMin && minute < endMin, nil
	default: // over midnight
		if minute >= startMin {
			return slices.Contains(days, today), nil
		}
		if minute < endMin {
			return slices.Contains(days, yesterday), nil
		}
		return false, nil
	}
}

// ValidateDays checks that every entry is a weekday 0..6 and there is at least one.
func ValidateDays(days []int32) error {
	if len(days) == 0 {
		return fmt.Errorf("select at least one day")
	}
	for _, d := range days {
		if d < 0 || d > 6 {
			return fmt.Errorf("invalid weekday %d", d)
		}
	}
	return nil
}
