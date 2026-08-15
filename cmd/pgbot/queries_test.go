package main

import "testing"

func TestDurFromMs(t *testing.T) {
	cases := []struct {
		ms   float64
		want string
	}{
		{0, "0ms"},
		{45, "45ms"},
		{810.79, "811ms"},
		{1200, "1.2s"},
		{59_000, "59.0s"},
		{90_000, "1m30s"},
		{3_600_000, "1h0m"},
		{5_400_000, "1h30m"},
		{90_000_000, "1d1h"},     // 25 hours
		{3 * 86_400_000, "3d0h"}, // 3 days
	}
	for _, c := range cases {
		if got := durFromMs(c.ms); got != c.want {
			t.Errorf("durFromMs(%g) = %q, want %q", c.ms, got, c.want)
		}
	}
}

func TestHumanCount(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{60, "60"},
		{999, "999"},
		{1000, "1.0k"},
		{812394, "812.4k"},
		{4_821_004, "4.8M"},
		{3_100_000_000, "3.1B"},
	}
	for _, c := range cases {
		if got := humanCount(c.n); got != c.want {
			t.Errorf("humanCount(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestTruncStr(t *testing.T) {
	if got := truncStr("short", 60); got != "short" {
		t.Errorf("short string should pass through, got %q", got)
	}
	long := "SELECT * FROM a_very_long_table_name_that_exceeds_the_limit_easily"
	got := truncStr(long, 20)
	if len([]rune(got)) != 20 {
		t.Errorf("truncated length = %d runes, want 20 (%q)", len([]rune(got)), got)
	}
	if got[len(got)-len("…"):] != "…" {
		t.Errorf("truncated string should end with ellipsis, got %q", got)
	}
}
