package qlog

import (
	"testing"
	"time"
)

func TestParseAround(t *testing.T) {
	now := time.Date(2026, 10, 18, 15, 0, 0, 0, testZone)
	tests := []struct {
		name      string
		now       time.Time
		hhmm      string
		window    time.Duration
		wantSince time.Time
		wantUntil time.Time
	}{
		{"earlier today", now, "14:32", 30 * time.Second,
			time.Date(2026, 10, 18, 14, 31, 30, 0, testZone), time.Date(2026, 10, 18, 14, 32, 30, 0, testZone)},
		{"later than now means yesterday", time.Date(2026, 10, 18, 10, 0, 0, 0, testZone), "14:32", time.Minute,
			time.Date(2026, 10, 17, 14, 31, 0, 0, testZone), time.Date(2026, 10, 17, 14, 33, 0, 0, testZone)},
		{"exactly now is today", now, "15:00", 10 * time.Second,
			time.Date(2026, 10, 18, 14, 59, 50, 0, testZone), time.Date(2026, 10, 18, 15, 0, 10, 0, testZone)},
		{"one digit hour", now, "9:05", time.Minute,
			time.Date(2026, 10, 18, 9, 4, 0, 0, testZone), time.Date(2026, 10, 18, 9, 6, 0, 0, testZone)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			since, until, err := ParseAround(tt.now, tt.hhmm, tt.window)
			if err != nil {
				t.Fatalf("ParseAround: %v", err)
			}
			if !since.Equal(tt.wantSince) || !until.Equal(tt.wantUntil) {
				t.Errorf("window = %v .. %v, want %v .. %v", since, until, tt.wantSince, tt.wantUntil)
			}
		})
	}
}

func TestParseAround_Rejects(t *testing.T) {
	now := time.Date(2026, 10, 18, 15, 0, 0, 0, testZone)
	tests := []struct {
		name   string
		hhmm   string
		window time.Duration
	}{
		{"empty", "", time.Minute},
		{"no colon", "1432", time.Minute},
		{"hour out of range", "25:00", time.Minute},
		{"minute out of range", "12:60", time.Minute},
		{"words", "noon", time.Minute},
		{"seconds not accepted", "12:00:00", time.Minute},
		{"zero window", "12:00", 0},
		{"negative window", "12:00", -time.Minute},
		{"window over a day", "12:00", 25 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := ParseAround(now, tt.hhmm, tt.window); err == nil {
				t.Errorf("ParseAround(%q, %v) accepted bad input", tt.hhmm, tt.window)
			}
		})
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 10, 18, 15, 0, 0, 0, testZone)
	tests := []struct {
		in   string
		want time.Time
	}{
		{"10m", now.Add(-10 * time.Minute)},
		{"2h", now.Add(-2 * time.Hour)},
		{"90s", now.Add(-90 * time.Second)},
		{"1h30m", now.Add(-90 * time.Minute)},
		{"1d", now.Add(-24 * time.Hour)},
		{"3d", now.Add(-72 * time.Hour)},
	}
	for _, tt := range tests {
		got, err := ParseSince(now, tt.in)
		if err != nil {
			t.Errorf("ParseSince(%q): %v", tt.in, err)
			continue
		}
		if !got.Equal(tt.want) {
			t.Errorf("ParseSince(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
	for _, bad := range []string{"", "abc", "-5m", "0", "0s", "1.5d", "d", "999999999999d", "10"} {
		if _, err := ParseSince(now, bad); err == nil {
			t.Errorf("ParseSince(%q) accepted bad input", bad)
		}
	}
}
