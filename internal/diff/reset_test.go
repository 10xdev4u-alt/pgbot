package diff

import (
	"strings"
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

func at(t time.Time) *time.Time { return &t }

func TestStatsResetBetween(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	older := now.Add(-2 * time.Hour)
	newer := now.Add(-3 * time.Minute)

	prev := &model.Context{Window: model.Window{StatsResetAt: at(older), PostmasterStartAt: at(older)}}

	t.Run("stats_reset moved forward -> suppress", func(t *testing.T) {
		cur := &model.Context{CollectedAt: now, Window: model.Window{StatsResetAt: at(newer), PostmasterStartAt: at(older)}}
		r := StatsResetBetween(prev, cur)
		if r == "" || !strings.Contains(r, "statistics reset") {
			t.Fatalf("expected reset reason, got %q", r)
		}
		if !strings.Contains(r, "00:03:00") {
			t.Errorf("expected the 3-minute age in the reason, got %q", r)
		}
	})

	t.Run("postmaster restart -> suppress", func(t *testing.T) {
		cur := &model.Context{CollectedAt: now, Window: model.Window{StatsResetAt: at(older), PostmasterStartAt: at(newer)}}
		if r := StatsResetBetween(prev, cur); !strings.Contains(r, "server restarted") {
			t.Fatalf("expected restart reason, got %q", r)
		}
	})

	t.Run("NULL stats_reset -> value (first reset) -> suppress", func(t *testing.T) {
		prevNoReset := &model.Context{Window: model.Window{PostmasterStartAt: at(older)}} // stats_reset NULL
		cur := &model.Context{CollectedAt: now, Window: model.Window{StatsResetAt: at(newer), PostmasterStartAt: at(older)}}
		if r := StatsResetBetween(prevNoReset, cur); !strings.Contains(r, "statistics reset") {
			t.Fatalf("NULL->value must be a reset, got %q", r)
		}
	})

	t.Run("no movement -> no suppression", func(t *testing.T) {
		cur := &model.Context{CollectedAt: now, Window: model.Window{StatsResetAt: at(older), PostmasterStartAt: at(older)}}
		if r := StatsResetBetween(prev, cur); r != "" {
			t.Errorf("expected no reset, got %q", r)
		}
	})

	t.Run("nil timestamps -> no false positive", func(t *testing.T) {
		cur := &model.Context{CollectedAt: now, Window: model.Window{}}
		if r := StatsResetBetween(&model.Context{Window: model.Window{}}, cur); r != "" {
			t.Errorf("nil windows must not signal a reset, got %q", r)
		}
	})
}

func TestHMS(t *testing.T) {
	if got := hms(3*time.Minute + 12*time.Second); got != "00:03:12" {
		t.Errorf("hms = %q, want 00:03:12", got)
	}
	if got := hms(25 * time.Hour); got != "1d 01:00:00" {
		t.Errorf("hms = %q, want 1d 01:00:00", got)
	}
}
