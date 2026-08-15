package main

import (
	"testing"
	"time"
)

func TestExpectAutovacuum(t *testing.T) {
	cases := []struct {
		live, dead int64
		want       bool
	}{
		{0, 50, false},      // exactly at threshold — not yet past
		{0, 51, true},       // one over the base threshold of 50
		{1000, 200, false},  // 200 < 50 + 0.2*1000 = 250
		{1000, 300, true},   // 300 > 250
		{100000, 20000, false},
		{100000, 20100, true}, // 20100 > 50 + 20000
	}
	for _, c := range cases {
		if got := expectAutovacuum(c.live, c.dead); got != c.want {
			t.Errorf("expectAutovacuum(live=%d, dead=%d) = %v, want %v", c.live, c.dead, got, c.want)
		}
	}
}

func TestAgoStr(t *testing.T) {
	if got := agoStr(nil); got != "never" {
		t.Errorf("nil should be %q, got %q", "never", got)
	}
	var zero time.Time
	if got := agoStr(&zero); got != "never" {
		t.Errorf("zero time should be %q, got %q", "never", got)
	}
	// Values chosen to sit well inside their buckets so sub-second test runtime
	// can't tip them across a boundary.
	m30 := time.Now().Add(-30 * time.Minute)
	if got := agoStr(&m30); got != "30m ago" {
		t.Errorf("30 minutes ago = %q, want %q", got, "30m ago")
	}
	h5 := time.Now().Add(-5 * time.Hour)
	if got := agoStr(&h5); got != "5h ago" {
		t.Errorf("5 hours ago = %q, want %q", got, "5h ago")
	}
	d3 := time.Now().Add(-3 * 24 * time.Hour)
	if got := agoStr(&d3); got != "3d ago" {
		t.Errorf("3 days ago = %q, want %q", got, "3d ago")
	}
}
