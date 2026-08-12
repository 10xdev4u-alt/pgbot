// Package findings computes deterministic, rule-based diagnoses over a
// model.Context. This is where analysis lives — NOT the LLM. Every rule is
// computable in Go from signals already in the Context; the LLM layer (a later
// slice) explains and prioritises these findings, it does not generate them.
package findings

import (
	"fmt"
	"sort"

	"github.com/pgrundev/pgbot/internal/model"
)

// Thresholds for the rules, in one place so they read like the docs.
const (
	unusedIndexMinBytes   = 1 << 20 // 1 MiB: below this an unused index isn't worth flagging
	deadRatioWarn         = 0.20    // 20% dead tuples on a sizable table
	deadRatioTableMinRows = 10000
	cacheHitWarn          = 0.90 // sustained cache hit below this is disk-bound
	longXactWarnSec       = 300  // a transaction open > 5 min
	idleInTxnWarnSec      = 60
	rollbackRatioWarn     = 0.10  // >10% of transactions rolling back
	staleStatsWarnDays    = 30    // rates computed over a very old window are near-meaningless
	seqScanTableMinRows   = 50000 // only flag seq-scan-heavy on tables big enough to matter

	// Wait-profile (ASH) thresholds. All gated on model.WaitMinSamples — below
	// that the shares are noise and no wait finding fires.
	waitLockContentionShare = 0.30 // a query with >30% of ITS samples on Lock:*
	waitLockQueryMinSamples = 5    // ignore a query seen in only a sample or two
	waitIOBoundShare        = 0.50 // >50% of the whole window on IO:*
	waitLWLockShare         = 0.30 // a single LWLock:* event dominating the window
)

// Compute returns findings sorted most-severe first. Order among equal
// severities is stable by ID for deterministic output.
func Compute(c *model.Context) []model.Finding {
	var f []model.Finding
	add := func(x model.Finding) { f = append(f, x) }

	blockingChains(c, add)
	invalidIndexes(c, add)
	unusedIndexes(c, add)
	seqScanHeavy(c, add)
	bloatedTables(c, add)
	lowCacheHit(c, add)
	idleInTransaction(c, add)
	longRunningXact(c, add)
	waitFindings(c, add)
	highRollbacks(c, add)
	missingPgss(c, add)
	staleStatsWindow(c, add)

	sort.SliceStable(f, func(i, j int) bool {
		if sev(f[i].Severity) != sev(f[j].Severity) {
			return sev(f[i].Severity) > sev(f[j].Severity)
		}
		return f[i].ID < f[j].ID
	})
	return f
}

func sev(s string) int {
	switch s {
	case model.SeverityCritical:
		return 3
	case model.SeverityWarn:
		return 2
	default:
		return 1
	}
}

func blockingChains(c *model.Context, add func(model.Finding)) {
	if c.Locks == nil || c.Locks.BlockedCount == 0 {
		return
	}
	ev := make([]string, 0, len(c.Locks.Chains))
	for _, ch := range c.Locks.Chains {
		ev = append(ev, fmt.Sprintf("pid %d blocked %.0fs", ch.BlockedPID, ch.WaitSeconds))
	}
	add(model.Finding{
		ID: "blocking_chains", Severity: model.SeverityCritical,
		Title:    fmt.Sprintf("%d session(s) blocked on locks right now", c.Locks.BlockedCount),
		Detail:   "One or more sessions are waiting on locks held by others. Sustained blocking stalls throughput and can cascade.",
		Evidence: ev, Impact: "Queries queue behind the lock holder; latency spikes until it commits or is terminated.",
	})
}

// invalidIndexes flags indexes with indisvalid=false — a failed CREATE INDEX
// CONCURRENTLY. It costs write throughput, serves no reads, and almost nobody
// notices. This is a gauge (valid immediately, even on a cold window).
func invalidIndexes(c *model.Context, add func(model.Finding)) {
	if c.Schema == nil {
		return
	}
	var ev []string
	for _, o := range c.Schema.Objects {
		if o.Kind == "index" && o.Invalid {
			ev = append(ev, o.Identity)
		}
	}
	if len(ev) == 0 {
		return
	}
	add(model.Finding{
		ID: "index_invalid", Severity: model.SeverityCritical,
		Title:    fmt.Sprintf("%d invalid index(es) — failed CREATE INDEX CONCURRENTLY", len(ev)),
		Detail:   "An invalid index is never used to serve reads but is still maintained on every write. It's the leftover of a CREATE INDEX CONCURRENTLY that failed partway.",
		Evidence: cap10(ev), Impact: "Drop and recreate it: DROP INDEX CONCURRENTLY <name>; then rebuild.",
	})
}

func unusedIndexes(c *model.Context, add func(model.Finding)) {
	if c.Indexes == nil {
		return
	}
	// Cold window (serverless just woke): index-scan counts start from zero, so
	// "unused" is meaningless and acting on it is actively dangerous. Suppress.
	if c.Window.ColdWindow() {
		return
	}
	var ev []string
	var total int64
	onWrite := 0
	writeTables := writeHeavyTables(c)
	for _, ix := range c.Indexes.Unused {
		if ix.Bytes < unusedIndexMinBytes {
			continue
		}
		ev = append(ev, fmt.Sprintf("%s.%s (%s)", ix.Table, ix.Name, humanBytes(ix.Bytes)))
		total += ix.Bytes
		if writeTables[ix.Schema+"."+ix.Table] {
			onWrite++
		}
	}
	if len(ev) == 0 {
		return
	}
	impact := fmt.Sprintf("Reclaims %s of storage.", humanBytes(total))
	if onWrite > 0 {
		impact += fmt.Sprintf(" %d are on write-heavy tables, where they also tax every INSERT/UPDATE.", onWrite)
	}
	add(model.Finding{
		ID: "unused_indexes", Severity: model.SeverityWarn,
		Title:    fmt.Sprintf("%d unused index(es) · %s", len(ev), humanBytes(total)),
		Detail:   "These indexes have zero scans since stats began. They cost storage and slow writes without serving reads.",
		Evidence: cap10(ev), Impact: impact,
	})
}

func bloatedTables(c *model.Context, add func(model.Finding)) {
	if c.Tables == nil {
		return
	}
	var ev []string
	for _, t := range c.Tables.Top {
		if t.DeadRatio >= deadRatioWarn && t.LiveTuples+t.DeadTuples >= deadRatioTableMinRows {
			ev = append(ev, fmt.Sprintf("%s.%s %.0f%% dead (%d rows)", t.Schema, t.Name, t.DeadRatio*100, t.DeadTuples))
		}
	}
	if len(ev) == 0 {
		return
	}
	add(model.Finding{
		ID: "table_bloat", Severity: model.SeverityWarn,
		Title:    fmt.Sprintf("%d table(s) with high dead-tuple ratio", len(ev)),
		Detail:   "Dead tuples inflate table size and slow scans until autovacuum reclaims them. A persistently high ratio suggests vacuum isn't keeping up.",
		Evidence: cap10(ev), Impact: "Wasted storage and slower sequential scans; consider tuning autovacuum for these tables.",
	})
}

// seqScanHeavy flags a large table doing far more sequential scans than index
// scans — often a query that lost (or never had) an index path.
func seqScanHeavy(c *model.Context, add func(model.Finding)) {
	if c.Tables == nil || c.Window.ColdWindow() { // scan counts are cold-window-sensitive
		return
	}
	var ev []string
	for _, t := range c.Tables.Top {
		total := t.SeqScans + t.IndexScans
		if t.LiveTuples >= seqScanTableMinRows && total >= 100 && t.SeqScans > t.IndexScans*2 {
			ev = append(ev, fmt.Sprintf("%s.%s %s seq scans vs %s index (%s rows)",
				t.Schema, t.Name, human(t.SeqScans), human(t.IndexScans), human(t.LiveTuples)))
		}
	}
	if len(ev) == 0 {
		return
	}
	add(model.Finding{
		ID: "seq_scan_heavy", Severity: model.SeverityWarn,
		Title:    fmt.Sprintf("%d table(s) sequential-scanning heavily", len(ev)),
		Detail:   "These tables are read mostly by full scans rather than index lookups. On a large table that's CPU and IO the database repeats on every query.",
		Evidence: cap10(ev), Impact: "Add an index for the hot predicate, or confirm the full scans are intended (small lookup tables, analytics).",
	})
}

func lowCacheHit(c *model.Context, add func(model.Finding)) {
	if c.Health == nil || c.Health.CacheHitRatio == nil {
		return
	}
	// Cache-hit over a cold window is dominated by cold-cache misses at wake and
	// says nothing about steady state. Suppress.
	if c.Window.ColdWindow() {
		return
	}
	if *c.Health.CacheHitRatio >= cacheHitWarn {
		return
	}
	add(model.Finding{
		ID: "low_cache_hit", Severity: model.SeverityWarn,
		Title:  fmt.Sprintf("Cache hit ratio %.1f%% over the sample window", *c.Health.CacheHitRatio*100),
		Detail: "A low buffer cache hit ratio means many reads are hitting disk. Sustained, it points to an undersized shared_buffers or a working set larger than RAM.",
		Impact: "Higher read latency and IO load. Confirm over a longer window before resizing.",
	})
}

func idleInTransaction(c *model.Context, add func(model.Finding)) {
	if c.Activity == nil || c.Activity.IdleInTransaction == 0 {
		return
	}
	sevr := model.SeverityInfo
	if c.Activity.LongestXactSec >= idleInTxnWarnSec {
		sevr = model.SeverityWarn
	}
	add(model.Finding{
		ID: "idle_in_transaction", Severity: sevr,
		Title:  fmt.Sprintf("%d session(s) idle in transaction", c.Activity.IdleInTransaction),
		Detail: "Idle-in-transaction sessions hold locks and pin the xmin horizon, blocking vacuum. Long-lived ones are a common source of bloat and lock waits.",
		Impact: "Prevents cleanup of dead rows and can block other writers.",
	})
}

func longRunningXact(c *model.Context, add func(model.Finding)) {
	if c.Activity == nil || c.Activity.LongestXactSec < longXactWarnSec {
		return
	}
	add(model.Finding{
		ID: "long_running_transaction", Severity: model.SeverityWarn,
		Title:  fmt.Sprintf("Longest transaction open %.0fs", c.Activity.LongestXactSec),
		Detail: "A long-running transaction holds back the xmin horizon so autovacuum can't remove dead rows created since it started.",
		Impact: "Table and index bloat accumulates until it ends.",
	})
}

// waitFindings reads the ASH profile (T8). It attributes TIME, not events, and
// works even when the cumulative counters reset minutes ago. Everything here is
// gated on a minimum sample count: a thin profile is noise, so nothing fires.
func waitFindings(c *model.Context, add func(model.Finding)) {
	w := c.WaitProfile
	if w == nil || !w.Available || w.Thin() {
		return
	}
	share := func(typ string) float64 {
		for _, b := range w.Buckets {
			if b.Type == typ {
				return b.Share
			}
		}
		return 0
	}

	// Per-query lock contention: a query spending most of its time waiting on
	// row/transaction locks. Names the query_id so it's actionable.
	for _, q := range w.ByQuery {
		if q.Count >= waitLockQueryMinSamples && q.LockShare > waitLockContentionShare {
			title := fmt.Sprintf("Query %s spends %.0f%% of its time waiting on locks", queryTag(q.QueryID), q.LockShare*100)
			ev := []string{fmt.Sprintf("query_id %d · %d samples · top wait %s", q.QueryID, q.Count, orNone(q.TopEvent))}
			if q.SampleText != "" {
				ev = append(ev, truncate(q.SampleText, 80))
			}
			add(model.Finding{
				ID: "wait_lock_contention", Severity: model.SeverityWarn,
				Title:    title,
				Detail:   "This query is mostly blocked on locks held by other sessions, not doing work. Look for a conflicting long transaction, hot-row updates, or a coarse lock.",
				Evidence: ev, Impact: "Its latency is dominated by lock waits; reducing contention frees it directly.",
			})
		}
	}

	// IO-bound: the whole window dominated by storage reads/writes.
	if io := share("IO"); io > waitIOBoundShare {
		add(model.Finding{
			ID: "wait_io_bound", Severity: model.SeverityWarn,
			Title:    fmt.Sprintf("%.0f%% of active time was spent waiting on IO", io*100),
			Detail:   "Most active samples were waiting on the storage layer, not on CPU or locks. The working set may not fit in cache, or a few queries are scanning far more than they return.",
			Evidence: []string{ioEvidence(w)},
			Impact:   "Throughput is capped by disk latency; more RAM/shared_buffers or better indexes usually helps.",
		})
	}

	// LWLock pressure: a single lightweight-lock event concentrating the window
	// (e.g. BufferMapping, WALWrite) — an internal-contention smell.
	if typ, ev, sh := dominantLWLock(w); sh > waitLWLockShare {
		add(model.Finding{
			ID: "wait_lwlock_pressure", Severity: model.SeverityWarn,
			Title:    fmt.Sprintf("%.0f%% of active time on a single lightweight lock (%s)", sh*100, ev),
			Detail:   "Concentration on one LWLock points at internal contention (buffer mapping, WAL, lock manager) rather than user locks. Often a sign of an undersized buffer cache or very high write concurrency.",
			Evidence: []string{fmt.Sprintf("%s:%s · %.0f%% of the window", typ, ev, sh*100)},
			Impact:   "Backends serialize on shared-memory structures; the fix depends on which lock.",
		})
	}
}

func highRollbacks(c *model.Context, add func(model.Finding)) {
	if c.Health == nil || c.Health.RollbackRatio == nil || *c.Health.RollbackRatio < rollbackRatioWarn {
		return
	}
	add(model.Finding{
		ID: "high_rollback_ratio", Severity: model.SeverityInfo,
		Title:  fmt.Sprintf("Rollback ratio %.1f%% over the sample window", *c.Health.RollbackRatio*100),
		Detail: "A high share of transactions are rolling back. Often application error handling or failed constraint checks; worth confirming it's intended.",
	})
}

func missingPgss(c *model.Context, add func(model.Finding)) {
	if c.Queries != nil && c.Queries.Enabled {
		return
	}
	add(model.Finding{
		ID: "pg_stat_statements_missing", Severity: model.SeverityInfo,
		Title:  "pg_stat_statements not enabled",
		Detail: "Without it, per-query performance analysis is unavailable. It's the single most useful Postgres monitoring extension.",
		Impact: "Enable with: CREATE EXTENSION pg_stat_statements; (and add it to shared_preload_libraries if not already).",
	})
}

func staleStatsWindow(c *model.Context, add func(model.Finding)) {
	if c.Window.StatsWindowDays == nil || *c.Window.StatsWindowDays < staleStatsWarnDays {
		return
	}
	add(model.Finding{
		ID: "stale_stats_window", Severity: model.SeverityInfo,
		Title:  fmt.Sprintf("Cumulative stats span %.0f days", *c.Window.StatsWindowDays),
		Detail: "Ratios like cache hit and cumulative query totals average over the whole window since the last stats reset. Over a very long window they hide recent regressions.",
		Impact: "Consider pg_stat_reset() to get a fresh baseline, or rely on pgbot's own deltas for recent change.",
	})
}

// writeHeavyTables returns the set of tables with meaningful write activity,
// used to flag unused indexes that also tax writes.
func writeHeavyTables(c *model.Context) map[string]bool {
	out := map[string]bool{}
	if c.Tables == nil {
		return out
	}
	for _, t := range c.Tables.Top {
		if t.ModsSinceAnalyze > 1000 {
			out[t.Schema+"."+t.Name] = true
		}
	}
	return out
}

func cap10(s []string) []string {
	if len(s) > 10 {
		return append(s[:10:10], "…")
	}
	return s
}

func human(v int64) string {
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%.1fG", float64(v)/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%.1fM", float64(v)/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.1fk", float64(v)/1e3)
	default:
		return fmt.Sprintf("%d", v)
	}
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// queryTag is a short stable handle for a query_id (pg_stat_statements prints
// the full 64-bit id, which is unreadable). We show the low 4 hex digits.
func queryTag(id int64) string { return fmt.Sprintf("%04x", uint64(id)&0xffff) }

func orNone(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// ioEvidence names the top specific IO wait event for the finding's evidence.
func ioEvidence(w *model.WaitProfile) string {
	for _, b := range w.Buckets {
		if b.Type == "IO" && len(b.Events) > 0 {
			return fmt.Sprintf("top IO wait: %s (%.0f%% of the window)", b.Events[0].Event, b.Events[0].Share*100)
		}
	}
	return "IO waits dominate the window"
}

// dominantLWLock returns the single largest LWLock:event and its share of the
// whole window, or ("","",0) if there are no LWLock samples.
func dominantLWLock(w *model.WaitProfile) (typ, event string, share float64) {
	for _, b := range w.Buckets {
		if b.Type != "LWLock" {
			continue
		}
		if len(b.Events) > 0 {
			return "LWLock", b.Events[0].Event, b.Events[0].Share
		}
		// No specific event names (rare) — fall back to the bucket share.
		return "LWLock", "LWLock", b.Share
	}
	return "", "", 0
}
