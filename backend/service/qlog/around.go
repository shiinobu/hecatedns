package qlog

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	aroundLayout = "15:04"
	maxWindow    = 24 * time.Hour
	maxSinceDays = 3650
	day          = 24 * time.Hour
)

func ParseAround(now time.Time, hhmm string, window time.Duration) (since, until time.Time, err error) {
	clock, err := time.Parse(aroundLayout, hhmm)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("time %q must look like HH:MM, for example 14:32", hhmm)
	}
	if window <= 0 || window > maxWindow {
		return time.Time{}, time.Time{}, fmt.Errorf("window %v must be greater than 0 and at most %v", window, maxWindow)
	}
	center := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
	if center.After(now) {
		center = center.AddDate(0, 0, -1)
	}
	return center.Add(-window), center.Add(window), nil
}

func ParseSince(now time.Time, text string) (time.Time, error) {
	if text == "" {
		return time.Time{}, errors.New("duration is empty, for example: 10m, 2h, 1d")
	}
	if digits, ok := strings.CutSuffix(text, "d"); ok {
		days, err := strconv.Atoi(digits)
		if err != nil || days < 1 || days > maxSinceDays {
			return time.Time{}, fmt.Errorf("duration %q must be 1 to %d days, for example: 3d", text, maxSinceDays)
		}
		return now.Add(-time.Duration(days) * day), nil
	}
	d, err := time.ParseDuration(text)
	if err != nil || d <= 0 {
		return time.Time{}, fmt.Errorf("duration %q is invalid, for example: 10m, 2h, 1d", text)
	}
	return now.Add(-d), nil
}
