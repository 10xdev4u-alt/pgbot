package diff

import (
	"fmt"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

// StatsResetBetween reports whether the cumulative statistics were reset (or the
// server restarted) between the previous snapshot and the current one. When they
// were, every cross-run delta is fiction — a counter dropping from 40M to 12k is
// a wake, not a −99.97% change — so the caller must suppress the Deltas section
// entirely. Returns a human reason, or "" when no reset happened.
func StatsResetBetween(prev, cur *model.Context) string {
	// A moved-forward stats_reset is the strongest signal (covers pg_stat_reset()
	// and the stats loss on a serverless wake). A NULL→value transition also
	// counts: on a never-reset database stats_reset is NULL, so the first reset
	// makes it appear — that IS the reset we must catch.
	if resetMoved(prev.Window.StatsResetAt, cur.Window.StatsResetAt) {
		return fmt.Sprintf("statistics reset %s ago (likely a compute restart or pg_stat_reset); no comparison available",
			hms(ageSince(cur, cur.Window.StatsResetAt)))
	}
	// Else a moved-forward postmaster start means the server itself restarted.
	// (postmaster_start is always readable, so require both sides present here.)
	if prev.Window.PostmasterStartAt != nil && cur.Window.PostmasterStartAt != nil &&
		cur.Window.PostmasterStartAt.After(*prev.Window.PostmasterStartAt) {
		return fmt.Sprintf("server restarted %s ago; no comparison available",
			hms(ageSince(cur, cur.Window.PostmasterStartAt)))
	}
	return ""
}

// resetMoved reports a stats-reset event: cur appeared where prev had none, or
// cur is strictly later than prev.
func resetMoved(prev, cur *time.Time) bool {
	if cur == nil {
		return false // stats_reset never un-sets
	}
	return prev == nil || cur.After(*prev)
}

func ageSince(c *model.Context, at *time.Time) time.Duration {
	if at == nil {
		return 0
	}
	return c.CollectedAt.Sub(*at)
}

// hms formats a duration as H:MM:SS (or D d H:MM:SS past a day).
func hms(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int64(d.Seconds())
	days := total / 86400
	h := (total % 86400) / 3600
	m := (total % 3600) / 60
	s := total % 60
	if days > 0 {
		return fmt.Sprintf("%dd %02d:%02d:%02d", days, h, m, s)
	}
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
