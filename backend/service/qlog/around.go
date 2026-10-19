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
		return time.Time{}, time.Time{}, fmt.Errorf("waktu %q harus berbentuk JJ:MM, misalnya 14:32", hhmm)
	}
	if window <= 0 || window > maxWindow {
		return time.Time{}, time.Time{}, fmt.Errorf("jendela %v harus lebih dari 0 dan paling lama %v", window, maxWindow)
	}
	center := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
	if center.After(now) {
		center = center.AddDate(0, 0, -1)
	}
	return center.Add(-window), center.Add(window), nil
}

func ParseSince(now time.Time, text string) (time.Time, error) {
	if text == "" {
		return time.Time{}, errors.New("lama waktu kosong, contoh: 10m, 2h, 1d")
	}
	if digits, ok := strings.CutSuffix(text, "d"); ok {
		days, err := strconv.Atoi(digits)
		if err != nil || days < 1 || days > maxSinceDays {
			return time.Time{}, fmt.Errorf("lama waktu %q harus berupa 1 sampai %d hari, contoh: 3d", text, maxSinceDays)
		}
		return now.Add(-time.Duration(days) * day), nil
	}
	d, err := time.ParseDuration(text)
	if err != nil || d <= 0 {
		return time.Time{}, fmt.Errorf("lama waktu %q tidak sah, contoh: 10m, 2h, 1d", text)
	}
	return now.Add(-d), nil
}
