// Package findings computes deterministic, rule-based diagnoses over a
// model.Context. This is where analysis lives — NOT the LLM. Every rule is
// computable in Go from signals already in the Context; the LLM layer (a later
// slice) explains and prioritises these findings, it does not generate them.
package findings

import (
	"fmt"
	"math"
	"sort"
	"strconv"

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
	rollbackMinTxns       = 20    // below this many transactions in the window the ratio is noise
	staleStatsWarnDays    = 30    // rates computed over a very old window are near-meaningless
	seqScanTableMinRows   = 50000 // only flag seq-scan-heavy on tables big enough to matter

	// Wait-profile (ASH) thresholds. All gated on model.WaitMinSamples — below
	// that the shares are noise and no wait finding fires.
	waitLockContentionShare = 0.30 // a query with >30% of ITS samples on Lock:*
	waitLockQueryMinSamples = 5    // ignore a query seen in only a sample or two
	waitIOBoundShare        = 0.50 // >50% of the whole window on IO:*
	waitLWLockShare         = 0.30 // a single LWLock:* event dominating the window

	// Impact/confidence horizons (T9).
	shortStatsWindowSec = 7 * 24 * 3600 // < 7 days of stats: unused-index confidence capped low

	// Connection saturation (used / max_connections).
	connSaturationWarn = 0.85
	connSaturationCrit = 0.95

	// Transaction-ID wraparound: age(datfrozenxid) climbing toward the ~2.1B wall
	// past which Postgres refuses writes. Healthy clusters stay under autovacuum's
	// 200M freeze trigger, so these thresholds only fire when vacuum is failing.
	xidWraparoundWarn = 1_000_000_000
	xidWraparoundCrit = 1_800_000_000
	xidWraparoundWall = 2_147_483_647

	// Query slowdown vs the baseline (the "what changed" finding).
	querySlowdownFactor = 2.0  // at least 2× slower
	querySlowdownMinMs  = 10.0 // and the new mean ≥ 10ms, so micro-queries aren't noise
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
	connectionSaturation(c, add)
	txidWraparound(c, add)
	querySlowdown(c, add)
	waitFindings(c, add)
	highRollbacks(c, add)
	missingPgss(c, add)
	staleStatsWindow(c, add)

	// T9 ordering: risk (time-to-incident) is pinned to the top — a wraparound or
	// an invalid index outranks any storage or latency win. Within that, sort by
	// Impact.Score descending. Severity breaks a score tie and still drives the
	// exit code, but it is no longer the primary key: an 8ms query run 40k times
	// can matter more than a warn that fires once.
	sort.SliceStable(f, func(i, j int) bool {
		ri, rj := isRisk(f[i]), isRisk(f[j])
		if ri != rj {
			return ri
		}
		if f[i].Impact.Score != f[j].Impact.Score {
			return f[i].Impact.Score > f[j].Impact.Score
		}
		if sev(f[i].Severity) != sev(f[j].Severity) {
			return sev(f[i].Severity) > sev(f[j].Severity)
		}
		return f[i].ID < f[j].ID
	})
	return f
}

func isRisk(f model.Finding) bool { return f.Impact.Dimension == model.DimRisk }

// impact builds a scored Impact. score is clamped to [0,100].
func impact(dim string, score float64, estimate, basis string) model.Impact {
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return model.Impact{Score: score, Dimension: dim, Estimate: estimate, Basis: basis}
}

// sizeScore maps a byte count onto 0..100 on a log scale: ~1 MiB scores near 0,
// ~100 GiB scores near 100. Storage wins are naturally logarithmic — the gap
// between 8 KB and 12 GB should dwarf the gap between 12 GB and 20 GB.
func sizeScore(bytes int64) float64 {
	if bytes <= 0 {
		return 0
	}
	l := math.Log10(float64(bytes))
	s := (l - 6) / (11 - 6) * 100 // 1e6 → 0, 1e11 → 100
	if s < 0 {
		return 0
	}
	if s > 100 {
		return 100
	}
	return s
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
	var maxWait float64
	for _, ch := range c.Locks.Chains {
		if ch.WaitSeconds > maxWait {
			maxWait = ch.WaitSeconds
		}
	}
	add(model.Finding{
		ID: "blocking_chains", Severity: model.SeverityCritical,
		Title:       fmt.Sprintf("%d session(s) blocked on locks right now", c.Locks.BlockedCount),
		Detail:      "One or more sessions are waiting on locks held by others. Sustained blocking stalls throughput and can cascade.",
		Evidence:    ev,
		Remediation: "Find the lock holder and let it commit, or terminate it with pg_terminate_backend() if it's stuck.",
		Impact: impact(model.DimRisk, math.Min(100, 80+float64(c.Locks.BlockedCount)*3+maxWait/10),
			fmt.Sprintf("%d blocked, longest %.0fs", c.Locks.BlockedCount, maxWait),
			"live pg_locks blocked/blocking chains"),
		Confidence: 1.0, // it is happening right now
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
		Title:       fmt.Sprintf("%d invalid index(es) — failed CREATE INDEX CONCURRENTLY", len(ev)),
		Detail:      "An invalid index is never used to serve reads but is still maintained on every write. It's the leftover of a CREATE INDEX CONCURRENTLY that failed partway.",
		Evidence:    cap10(ev),
		Remediation: "Drop and recreate it: DROP INDEX CONCURRENTLY <name>; then rebuild.",
		Impact: impact(model.DimRisk, 85,
			fmt.Sprintf("%d invalid index(es)", len(ev)),
			"pg_index.indisvalid = false"),
		Confidence: 1.0, // a catalog fact
	})
}

// unusedIndex represents one flagged index with its per-index score, so the
// aggregate finding's evidence lists the biggest, write-taxing targets first.
type unusedIndex struct {
	stat      model.IndexStat
	writeHeav bool
	score     float64
}

func unusedIndexes(c *model.Context, add func(model.Finding)) {
	if c.Indexes == nil {
		return
	}
	// Cold window (serverless just woke, or stats reset < 15 min ago): index-scan
	// counts start from zero, so "unused" is meaningless and acting on it is
	// actively dangerous. Suppress entirely (T2). Constraint-backing indexes are
	// already excluded upstream in the collector (T9.3).
	if c.Window.ColdWindow() {
		return
	}
	writeTables := writeHeavyTables(c)
	var found []unusedIndex
	var total int64
	anyWriteHeavy := false
	for _, ix := range c.Indexes.Unused {
		if ix.Bytes < unusedIndexMinBytes {
			continue // below the floor: not worth a recommendation (the 8 KB case)
		}
		wh := writeTables[ix.Schema+"."+ix.Table]
		// Storage score: size (log scale), lifted when the parent table is
		// write-heavy — there the index also taxes every INSERT/UPDATE.
		sc := sizeScore(ix.Bytes)
		if wh {
			sc = math.Min(100, sc*1.3)
		}
		found = append(found, unusedIndex{stat: ix, writeHeav: wh, score: sc})
		total += ix.Bytes
		anyWriteHeavy = anyWriteHeavy || wh
	}
	if len(found) == 0 {
		return
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].score > found[j].score })

	var ev []string
	partialSeen, exprSeen := false, false
	for _, u := range found {
		tag := ""
		switch {
		case u.stat.Partial:
			tag, partialSeen = " [partial]", true
		case u.stat.Expression:
			tag, exprSeen = " [expression]", true
		}
		wh := ""
		if u.writeHeav {
			wh = " · write-heavy table"
		}
		ev = append(ev, fmt.Sprintf("%s.%s (%s%s)%s", u.stat.Table, u.stat.Name, humanBytes(u.stat.Bytes), wh, tag))
	}

	// Confidence + caveats from the counter-evidence checks (T9.3).
	confidence := 0.8
	var caveats []string
	if winSec := windowAgeSeconds(c); winSec > 0 && winSec < shortStatsWindowSec {
		confidence = math.Min(confidence, 0.4)
		caveats = append(caveats, fmt.Sprintf("stats span only %s — an index used by a less-frequent path may look unused", shortDur(winSec)))
	}
	if replicationInUse(c) {
		// NEVER optional: index stats are per-node; a primary cannot see reads a
		// replica serves. The single most likely way pgbot causes an outage.
		caveats = append(caveats, "replication is active — these scan counts are from THIS node only; a replica may be using an index that looks unused here")
	}
	if partialSeen {
		caveats = append(caveats, "one or more are partial indexes — they may serve a narrow but critical path")
	}
	if exprSeen {
		caveats = append(caveats, "one or more are expression indexes — they may serve a specific query shape")
	}
	if anyWriteHeavy {
		caveats = append(caveats, "some sit on write-heavy tables — confirm no month-end or scheduled job relies on them outside this window")
	}

	estimate := "≈" + humanBytes(total) + " reclaimable"
	basis := fmt.Sprintf("%d zero-scan index(es), %s total, size×write-rate weighted", len(found), humanBytes(total))
	rem := fmt.Sprintf("Reclaims %s of storage.", humanBytes(total))
	if anyWriteHeavy {
		rem += " Those on write-heavy tables also tax every INSERT/UPDATE."
	}
	rem += " Drop with DROP INDEX CONCURRENTLY after confirming the caveats."
	add(model.Finding{
		ID: "unused_indexes", Severity: model.SeverityWarn,
		Title:       fmt.Sprintf("%d unused index(es) · %s", len(found), humanBytes(total)),
		Detail:      "These indexes have zero scans since stats began. They cost storage and slow writes without serving reads.",
		Evidence:    cap10(ev),
		Remediation: rem,
		Impact:      impact(model.DimStorage, found[0].score, estimate, basis),
		Confidence:  confidence,
		Caveats:     caveats,
	})
}

func bloatedTables(c *model.Context, add func(model.Finding)) {
	if c.Tables == nil {
		return
	}
	var ev []string
	var worstDeadBytes float64
	autovacKeepingPace := true
	for _, t := range c.Tables.Top {
		if t.DeadRatio >= deadRatioWarn && t.LiveTuples+t.DeadTuples >= deadRatioTableMinRows {
			ev = append(ev, fmt.Sprintf("%s.%s %.0f%% dead (%d rows)", t.Schema, t.Name, t.DeadRatio*100, t.DeadTuples))
			if db := t.DeadRatio * float64(t.TotalBytes); db > worstDeadBytes {
				worstDeadBytes = db
			}
			if t.LastAutovac == nil { // no recent autovacuum on a bloated table → not keeping pace
				autovacKeepingPace = false
			}
		}
	}
	if len(ev) == 0 {
		return
	}
	score := sizeScore(int64(worstDeadBytes))
	if autovacKeepingPace {
		score *= 0.6 // autovacuum has run recently — the bloat is likely transient
	}
	add(model.Finding{
		ID: "table_bloat", Severity: model.SeverityWarn,
		Title:       fmt.Sprintf("%d table(s) with high dead-tuple ratio", len(ev)),
		Detail:      "Dead tuples inflate table size and slow scans until autovacuum reclaims them. A persistently high ratio suggests vacuum isn't keeping up.",
		Evidence:    cap10(ev),
		Remediation: "VACUUM the worst tables, and tune autovacuum (scale factor / cost limit) if the ratio stays high.",
		Impact: impact(model.DimStorage, score,
			"≈"+humanBytes(int64(worstDeadBytes))+" dead in the worst table",
			"max(dead_ratio × table size)"+map[bool]string{true: ", discounted (autovacuum recent)", false: ""}[autovacKeepingPace]),
		Confidence: 0.7,
	})
}

// seqScanHeavy flags a large table doing far more sequential scans than index
// scans — often a query that lost (or never had) an index path.
func seqScanHeavy(c *model.Context, add func(model.Finding)) {
	if c.Tables == nil || c.Window.ColdWindow() { // scan counts are cold-window-sensitive
		return
	}
	var ev []string
	var worstScanRows int64
	for _, t := range c.Tables.Top {
		total := t.SeqScans + t.IndexScans
		if t.LiveTuples >= seqScanTableMinRows && total >= 100 && t.SeqScans > t.IndexScans*2 {
			ev = append(ev, fmt.Sprintf("%s.%s %s seq scans vs %s index (%s rows)",
				t.Schema, t.Name, human(t.SeqScans), human(t.IndexScans), human(t.LiveTuples)))
			if r := t.SeqScans * t.LiveTuples; r > worstScanRows {
				worstScanRows = r
			}
		}
	}
	if len(ev) == 0 {
		return
	}
	add(model.Finding{
		ID: "seq_scan_heavy", Severity: model.SeverityWarn,
		Title:       fmt.Sprintf("%d table(s) sequential-scanning heavily", len(ev)),
		Detail:      "These tables are read mostly by full scans rather than index lookups. On a large table that's CPU and IO the database repeats on every query.",
		Evidence:    cap10(ev),
		Remediation: "Add an index for the hot predicate, or confirm the full scans are intended (small lookup tables, analytics).",
		Impact: impact(model.DimThroughput, math.Min(90, 40+math.Log10(float64(worstScanRows)+1)*8),
			fmt.Sprintf("%d table(s) scanning full", len(ev)),
			"seq_scans ≫ index_scans on tables ≥ 50k rows"),
		Confidence: 0.6,
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
	miss := 1 - *c.Health.CacheHitRatio
	add(model.Finding{
		ID: "low_cache_hit", Severity: model.SeverityWarn,
		Title:       fmt.Sprintf("Cache hit ratio %.1f%% over the sample window", *c.Health.CacheHitRatio*100),
		Detail:      "A low buffer cache hit ratio means many reads are hitting disk. Sustained, it points to an undersized shared_buffers or a working set larger than RAM.",
		Remediation: "Confirm over a longer window, then consider raising shared_buffers or adding RAM.",
		Impact: impact(model.DimThroughput, math.Min(85, miss*300),
			fmt.Sprintf("%.0f%% of reads miss cache", miss*100),
			"1 − blks_hit/(blks_hit+blks_read) over the window"),
		Confidence: 0.6,
	})
}

func idleInTransaction(c *model.Context, add func(model.Finding)) {
	if c.Activity == nil || c.Activity.IdleInTransaction == 0 {
		return
	}
	sevr := model.SeverityInfo
	score := 30.0
	if c.Activity.LongestXactSec >= idleInTxnWarnSec {
		sevr = model.SeverityWarn
		score = math.Min(80, 50+c.Activity.LongestXactSec/30)
	}
	add(model.Finding{
		ID: "idle_in_transaction", Severity: sevr,
		Title:       fmt.Sprintf("%d session(s) idle in transaction", c.Activity.IdleInTransaction),
		Detail:      "Idle-in-transaction sessions hold locks and pin the xmin horizon, blocking vacuum. Long-lived ones are a common source of bloat and lock waits.",
		Remediation: "Find the session and fix the app's transaction handling; consider idle_in_transaction_session_timeout.",
		Impact: impact(model.DimRisk, score,
			fmt.Sprintf("%d session(s), longest %.0fs", c.Activity.IdleInTransaction, c.Activity.LongestXactSec),
			"pg_stat_activity idle-in-transaction count + age"),
		Confidence: 0.7,
	})
}

func longRunningXact(c *model.Context, add func(model.Finding)) {
	if c.Activity == nil || c.Activity.LongestXactSec < longXactWarnSec {
		return
	}
	add(model.Finding{
		ID: "long_running_transaction", Severity: model.SeverityWarn,
		Title:       fmt.Sprintf("Longest transaction open %.0fs", c.Activity.LongestXactSec),
		Detail:      "A long-running transaction holds back the xmin horizon so autovacuum can't remove dead rows created since it started.",
		Remediation: "Identify and end the transaction; long-lived read transactions should use a shorter scope.",
		Impact: impact(model.DimRisk, math.Min(85, 55+c.Activity.LongestXactSec/60),
			fmt.Sprintf("open %.0fs", c.Activity.LongestXactSec),
			"pg_stat_activity longest xact_start age"),
		Confidence: 0.8,
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

	// Confidence scales with how many samples backed the profile — 20 samples is
	// suggestive, 200 is solid.
	conf := math.Min(0.9, 0.4+float64(w.Samples)/400)

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
				Title:       title,
				Detail:      "This query is mostly blocked on locks held by other sessions, not doing work. Look for a conflicting long transaction, hot-row updates, or a coarse lock.",
				Evidence:    ev,
				Remediation: "Reduce contention: shorten the holding transaction, avoid hot-row updates, or lower lock granularity.",
				Impact: impact(model.DimLatency, math.Min(95, q.LockShare*100*q.Share+40),
					fmt.Sprintf("%.0f%% of query %s on locks", q.LockShare*100, queryTag(q.QueryID)),
					fmt.Sprintf("ASH: %d/%d samples of this query on Lock:*", int(q.LockShare*float64(q.Count)+0.5), q.Count)),
				Confidence: conf,
			})
		}
	}

	// IO-bound: the whole window dominated by storage reads/writes.
	if io := share("IO"); io > waitIOBoundShare {
		add(model.Finding{
			ID: "wait_io_bound", Severity: model.SeverityWarn,
			Title:       fmt.Sprintf("%.0f%% of active time was spent waiting on IO", io*100),
			Detail:      "Most active samples were waiting on the storage layer, not on CPU or locks. The working set may not fit in cache, or a few queries are scanning far more than they return.",
			Evidence:    []string{ioEvidence(w)},
			Remediation: "Add RAM/shared_buffers or better indexes; check for large scans returning few rows.",
			Impact: impact(model.DimThroughput, math.Min(90, io*100),
				fmt.Sprintf("%.0f%% of active time on IO", io*100),
				"ASH: share of samples with wait_event_type = IO"),
			Confidence: conf,
		})
	}

	// LWLock pressure: a single lightweight-lock event concentrating the window
	// (e.g. BufferMapping, WALWrite) — an internal-contention smell.
	if typ, ev, sh := dominantLWLock(w); sh > waitLWLockShare {
		add(model.Finding{
			ID: "wait_lwlock_pressure", Severity: model.SeverityWarn,
			Title:       fmt.Sprintf("%.0f%% of active time on a single lightweight lock (%s)", sh*100, ev),
			Detail:      "Concentration on one LWLock points at internal contention (buffer mapping, WAL, lock manager) rather than user locks. Often a sign of an undersized buffer cache or very high write concurrency.",
			Evidence:    []string{fmt.Sprintf("%s:%s · %.0f%% of the window", typ, ev, sh*100)},
			Remediation: "The fix depends on the lock — buffer mapping points at cache size, WAL locks at write concurrency.",
			Impact: impact(model.DimThroughput, math.Min(88, sh*100),
				fmt.Sprintf("%.0f%% on %s", sh*100, ev),
				"ASH: share of samples on a single LWLock event"),
			Confidence: conf,
		})
	}
}

// connectionSaturation warns as open connections approach max_connections —
// past which new sessions are refused and the app locks out. A risk, not a
// gradual degradation.
func connectionSaturation(c *model.Context, add func(model.Finding)) {
	if c.Limits == nil || c.Limits.ConnectionsMax <= 0 {
		return
	}
	used, max := c.Limits.ConnectionsUsed, c.Limits.ConnectionsMax
	frac := float64(used) / float64(max)
	if frac < connSaturationWarn {
		return
	}
	sev := model.SeverityWarn
	if frac >= connSaturationCrit {
		sev = model.SeverityCritical
	}
	add(model.Finding{
		ID: "connection_saturation", Severity: sev,
		Title:       fmt.Sprintf("Connection usage at %.0f%% (%d/%d)", frac*100, used, max),
		Detail:      "New connections are refused once max_connections is reached. A pool leak or a traffic burst can exhaust the slots and lock the application out.",
		Remediation: "Put a pooler (PgBouncer) in front, lower per-service pool sizes, or raise max_connections.",
		Impact: impact(model.DimRisk, frac*100,
			fmt.Sprintf("%d of %d slots", used, max),
			"count(pg_stat_activity) / max_connections"),
		Confidence: 1.0,
	})
}

// txidWraparound flags the oldest transaction-id age climbing toward the ~2.1B
// wall past which Postgres stops accepting writes — i.e. autovacuum isn't
// freezing fast enough. One of the few genuine "database goes down" risks.
func txidWraparound(c *model.Context, add func(model.Finding)) {
	if c.Limits == nil || c.Limits.MaxXIDAge < xidWraparoundWarn {
		return
	}
	age := c.Limits.MaxXIDAge
	sev := model.SeverityWarn
	if age >= xidWraparoundCrit {
		sev = model.SeverityCritical
	}
	pct := float64(age) / float64(xidWraparoundWall) * 100
	add(model.Finding{
		ID: "txid_wraparound", Severity: sev,
		Title:       fmt.Sprintf("Transaction-ID age %s — %.0f%% toward wraparound", human(age), pct),
		Detail:      "The oldest unfrozen transaction id is approaching the 2.1-billion wraparound limit, past which Postgres refuses writes. It means vacuum isn't freezing fast enough — a long-running transaction, disabled autovacuum, or a stuck worker.",
		Remediation: "Clear what blocks vacuum (long transactions, replication slots, disabled autovacuum), then VACUUM (FREEZE) the oldest tables.",
		Impact: impact(model.DimRisk, pct,
			fmt.Sprintf("age %s / 2.1B", human(age)),
			"max(age(datfrozenxid)) across databases"),
		Confidence: 1.0,
	})
}

// querySlowdown surfaces the flagship "what changed": a query whose mean time
// regressed sharply versus the baseline. Temporal — needs a prior run, which is
// exactly what a stats reader can't do.
func querySlowdown(c *model.Context, add func(model.Finding)) {
	if c.Deltas == nil {
		return
	}
	var worst *model.Delta
	var worstFactor float64
	for i := range c.Deltas.Changes {
		d := &c.Deltas.Changes[i]
		if d.ID != "query.mean_ms" || d.Before <= 0 || d.After < querySlowdownMinMs {
			continue
		}
		if factor := d.After / d.Before; factor >= querySlowdownFactor && factor > worstFactor {
			worst, worstFactor = d, factor
		}
	}
	if worst == nil {
		return
	}
	add(model.Finding{
		ID: "query_slowdown", Severity: model.SeverityWarn,
		Title:       fmt.Sprintf("Query %s is %.1f× slower (%.0f → %.0f ms mean)", queryTagStr(worst.Subject), worstFactor, worst.Before, worst.After),
		Detail:      "A query's mean execution time regressed sharply versus pgbot's baseline. Often a plan flip after the table grew, a dropped or invalidated index, or stale statistics.",
		Remediation: "Check for a missing/invalid index and run ANALYZE on the table; compare the current plan against before.",
		Impact: impact(model.DimLatency, math.Min(90, 30+worstFactor*10),
			fmt.Sprintf("%.1f× slower", worstFactor),
			"pg_stat_statements mean time vs the baseline"),
		Confidence: 0.8,
	})
}

// queryTagStr renders a query_id string as the short low-4-hex handle, or a
// truncated fallback when it isn't numeric.
func queryTagStr(s string) string {
	if id, err := strconv.ParseInt(s, 10, 64); err == nil {
		return queryTag(id)
	}
	return truncate(s, 12)
}

func highRollbacks(c *model.Context, add func(model.Finding)) {
	if c.Health == nil || c.Health.RollbackRatio == nil || *c.Health.RollbackRatio < rollbackRatioWarn {
		return
	}
	// A ratio computed over a handful of transactions is noise (2 rollbacks out of
	// 4 reads as 50%). Require enough volume in the window to trust it — TPS × the
	// sample seconds ≈ the transactions actually observed.
	if c.Health.TPS == nil || *c.Health.TPS*c.Window.SampleSeconds < rollbackMinTxns {
		return
	}
	add(model.Finding{
		ID: "high_rollback_ratio", Severity: model.SeverityWarn,
		Title:  fmt.Sprintf("Rollback ratio %.1f%% over the sample window", *c.Health.RollbackRatio*100),
		Detail: "A high share of transactions are rolling back. Often application error handling or failed constraint checks; worth confirming it's intended.",
		Impact: impact(model.DimThroughput, math.Min(35, *c.Health.RollbackRatio*100),
			fmt.Sprintf("%.1f%% rolling back", *c.Health.RollbackRatio*100),
			"xact_rollback/(commit+rollback) over the window"),
		Confidence: 0.5,
	})
}

func missingPgss(c *model.Context, add func(model.Finding)) {
	if c.Queries != nil && c.Queries.Enabled {
		return
	}
	add(model.Finding{
		ID: "pg_stat_statements_missing", Severity: model.SeverityInfo,
		Title:       "pg_stat_statements not enabled",
		Detail:      "Without it, per-query performance analysis is unavailable. It's the single most useful Postgres monitoring extension.",
		Remediation: "Enable with: CREATE EXTENSION pg_stat_statements; (and add it to shared_preload_libraries if not already).",
		Impact:      impact(model.DimCost, 15, "no per-query stats", "pg_stat_statements not in the catalog"),
		Confidence:  1.0,
	})
}

func staleStatsWindow(c *model.Context, add func(model.Finding)) {
	if c.Window.StatsWindowDays == nil || *c.Window.StatsWindowDays < staleStatsWarnDays {
		return
	}
	add(model.Finding{
		ID: "stale_stats_window", Severity: model.SeverityInfo,
		Title:       fmt.Sprintf("Cumulative stats span %.0f days", *c.Window.StatsWindowDays),
		Detail:      "Ratios like cache hit and cumulative query totals average over the whole window since the last stats reset. Over a very long window they hide recent regressions.",
		Remediation: "Consider pg_stat_reset() to get a fresh baseline, or rely on pgbot's own deltas for recent change.",
		Impact:      impact(model.DimCost, 12, fmt.Sprintf("%.0f-day window", *c.Window.StatsWindowDays), "stats_reset age"),
		Confidence:  1.0,
	})
}

// windowAgeSeconds is how long the cumulative stats have been accumulating
// (since the last reset / restart), or 0 when unknown.
func windowAgeSeconds(c *model.Context) int64 {
	if c.Window.WindowAgeSeconds == nil {
		return 0
	}
	return *c.Window.WindowAgeSeconds
}

// replicationInUse reports whether this cluster is part of a replication setup —
// either a primary with connected standbys or a replica itself. When true,
// per-node scan counts cannot be trusted to prove an index is globally unused.
func replicationInUse(c *model.Context) bool {
	return c.Replication != nil && (len(c.Replication.Replicas) > 0 || c.Replication.IsReplica)
}

// shortDur renders a seconds count as a coarse human duration for caveats.
func shortDur(sec int64) string {
	switch {
	case sec >= 86400:
		return fmt.Sprintf("%.0fd", float64(sec)/86400)
	case sec >= 3600:
		return fmt.Sprintf("%.0fh", float64(sec)/3600)
	case sec >= 60:
		return fmt.Sprintf("%.0fm", float64(sec)/60)
	default:
		return fmt.Sprintf("%ds", sec)
	}
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
