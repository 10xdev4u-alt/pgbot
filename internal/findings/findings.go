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
	preparedXactWarnSec   = 300  // a prepared (2PC) transaction open > 5 min is likely abandoned
	preparedXactCritSec   = 3600 // > 1h — it never times out; blocks vacuum until resolved
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

	// Vacuum horizon: an xmin held this many transactions behind the current xid
	// is a real block on dead-tuple reclamation (not a momentary query).
	vacuumHorizonWarnXIDs = 1_000_000

	// Query slowdown vs the baseline (the "what changed" finding).
	querySlowdownFactor = 2.0  // at least 2× slower
	querySlowdownMinMs  = 10.0 // and the new mean ≥ 10ms, so micro-queries aren't noise

	// Replication-slot WAL retention. An inactive slot pins WAL from its restart
	// point; the retained log grows until the consumer reconnects or the slot is
	// dropped — a classic way to fill the data disk. Small brief-reconnect gaps
	// aren't worth flagging, so warn only once retention is material.
	slotRetainWarnBytes int64 = 512 << 20 // 512 MiB retained by an inactive slot
	slotRetainCritBytes int64 = 8 << 30   // 8 GiB → serious disk-fill risk

	// Config-tuning thresholds (the `pgbot tune` findings).
	tuneTempSpillBytesPerSec = 1 << 20 // 1 MiB/s of temp files → work_mem too small
	tuneForcedCheckpointFrac = 0.30    // >30% of checkpoints forced by WAL → max_wal_size too small
	tuneForcedCheckpointMin  = 10      // need enough checkpoints to judge the ratio
	tuneConnOverprovMax      = 200     // only flag over-provisioning above this max_connections
	tuneConnOverprovUseFrac  = 0.15    // used < 15% of max → over-provisioned
)

// TuningIDs identifies the config-recommendation findings, surfaced together by
// `pgbot tune`.
var TuningIDs = map[string]bool{
	"work_mem_low":                true,
	"checkpoints_forced":          true,
	"connections_overprovisioned": true,
	"io_timing_off":               true,
}

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
	mxidWraparound(c, add)
	replicationSlotRisk(c, add)
	subscriptionDown(c, add)
	vacuumHorizonBlocked(c, add)
	preparedXactAbandoned(c, add)
	querySlowdown(c, add)
	workMemLow(c, add)
	checkpointsForced(c, add)
	connectionsOverprovisioned(c, add)
	ioTimingOff(c, add)
	waitFindings(c, add)
	highRollbacks(c, add)
	missingPgss(c, add)
	pgssEntriesEvicted(c, add)
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
	rem := "VACUUM the worst tables, and tune autovacuum (scale factor / cost limit) if the ratio stays high."
	if c.Horizon != nil && len(c.Horizon.Holders) > 0 && c.Horizon.Holders[0].XminAge >= vacuumHorizonWarnXIDs {
		rem = "First check vacuum_horizon_blocked — an xmin holder is pinning the horizon, so VACUUM can't reclaim these until it's released. " + rem
	}
	add(model.Finding{
		ID: "table_bloat", Severity: model.SeverityWarn,
		Title:       fmt.Sprintf("%d table(s) with high dead-tuple ratio", len(ev)),
		Detail:      "Dead tuples inflate table size and slow scans until autovacuum reclaims them. A persistently high ratio suggests vacuum isn't keeping up.",
		Evidence:    cap10(ev),
		Remediation: rem,
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

// mxidWraparound mirrors txid_wraparound for MULTIXACT ids. Multixacts are
// consumed by SELECT ... FOR SHARE/FOR UPDATE and foreign-key checks and exhaust
// toward the same ~2.1B wall; they wrap independently of regular transaction ids,
// so a workload heavy in shared row locks can hit this while datfrozenxid is fine.
func mxidWraparound(c *model.Context, add func(model.Finding)) {
	if c.Limits == nil || c.Limits.MaxMXIDAge < xidWraparoundWarn {
		return
	}
	age := c.Limits.MaxMXIDAge
	sev := model.SeverityWarn
	if age >= xidWraparoundCrit {
		sev = model.SeverityCritical
	}
	pct := float64(age) / float64(xidWraparoundWall) * 100
	add(model.Finding{
		ID: "mxid_wraparound", Severity: sev,
		Title:       fmt.Sprintf("Multixact-ID age %s — %.0f%% toward wraparound", human(age), pct),
		Detail:      "The oldest unfrozen multixact id is approaching the 2.1-billion wraparound limit, past which Postgres refuses writes. Multixacts come from shared row locks (SELECT … FOR SHARE/UPDATE) and FK checks; a workload heavy in those can wrap this while transaction-id age looks fine.",
		Remediation: "Clear what blocks vacuum (long transactions, replication slots, disabled autovacuum), then VACUUM (FREEZE) the oldest tables to advance datminmxid.",
		Impact: impact(model.DimRisk, pct,
			fmt.Sprintf("mxid age %s / 2.1B", human(age)),
			"max(mxid_age(datminmxid)) across databases"),
		Confidence: 1.0,
	})
}

// vacuumHorizonBlocked names WHY dead tuples aren't being reclaimed: the oldest
// xmin still in use pins the horizon, and VACUUM frees nothing newer. It reports
// which of the four holders (open transaction, standby feedback, replication
// slot, prepared 2PC txn) is oldest and how far behind it is.
func vacuumHorizonBlocked(c *model.Context, add func(model.Finding)) {
	if c.Horizon == nil || len(c.Horizon.Holders) == 0 {
		return
	}
	top := c.Horizon.Holders[0] // collector orders oldest-xmin first
	if top.XminAge < vacuumHorizonWarnXIDs {
		return
	}
	sev := model.SeverityWarn
	dim := model.DimStorage
	score := 45.0
	if top.XminAge >= xidWraparoundWarn { // old enough to threaten wraparound
		sev = model.SeverityCritical
		dim = model.DimRisk
		score = float64(top.XminAge) / float64(xidWraparoundWall) * 100
	}
	ev := make([]string, 0, len(c.Horizon.Holders))
	for _, h := range c.Horizon.Holders {
		d := ""
		if h.Detail != "" {
			d = " — " + h.Detail
		}
		ev = append(ev, fmt.Sprintf("%s %s: xmin %s txns behind%s", h.Source, h.Holder, human(h.XminAge), d))
	}
	add(model.Finding{
		ID: "vacuum_horizon_blocked", Severity: sev,
		Title:       fmt.Sprintf("Vacuum horizon pinned by %s %s (%s transactions behind)", top.Source, top.Holder, human(top.XminAge)),
		Detail:      "Dead tuples can't be reclaimed past the oldest xmin still in use. Until this holder releases it, VACUUM frees nothing newer — bloat grows, and if it persists, transaction-id age climbs toward wraparound.",
		Evidence:    cap10(ev),
		Remediation: horizonRemediation(top.Source),
		Impact:      impact(dim, score, human(top.XminAge)+" transactions pinned", "oldest backend_xmin / slot xmin / prepared-xact age"),
		Confidence:  0.9,
	})
}

// preparedXactAbandoned flags a prepared (2PC) transaction left open. Unlike an
// idle backend it is invisible in pg_stat_activity and never times out, so it
// blocks vacuum and pins the xmin horizon indefinitely. Reads the prepared-xact
// holders the horizon collector already gathered.
func preparedXactAbandoned(c *model.Context, add func(model.Finding)) {
	if c.Horizon == nil {
		return
	}
	for _, h := range c.Horizon.Holders {
		if h.Source != "prepared_xact" || h.AgeSec < preparedXactWarnSec {
			continue
		}
		sev := model.SeverityWarn
		score := 55.0
		if h.AgeSec >= preparedXactCritSec {
			sev = model.SeverityCritical
			score = 78
		}
		add(model.Finding{
			ID: "prepared_xact_abandoned", Severity: sev,
			Title:       fmt.Sprintf("Prepared transaction %q open for %.0fs", h.Holder, h.AgeSec),
			Detail:      "An abandoned prepared (2PC) transaction is invisible in pg_stat_activity and never times out. It holds back the xmin horizon and vacuum indefinitely until it is committed or rolled back.",
			Evidence:    []string{fmt.Sprintf("gid %q, prepared %.0fs ago, xmin %s txns behind", h.Holder, h.AgeSec, human(h.XminAge))},
			Remediation: fmt.Sprintf("Confirm the transaction manager is finished, then COMMIT PREPARED '%s' or ROLLBACK PREPARED '%s'.", h.Holder, h.Holder),
			Impact:      impact(model.DimRisk, score, fmt.Sprintf("open %.0fs", h.AgeSec), "pg_prepared_xacts"),
			Confidence:  0.9,
		})
	}
}

func horizonRemediation(source string) string {
	switch source {
	case "backend":
		return "End or commit the long-open transaction (find the pid in pg_stat_activity); set idle_in_transaction_session_timeout so it can't recur."
	case "replication_slot":
		return "If the slot's consumer is gone for good, drop it: SELECT pg_drop_replication_slot('…'). Otherwise let the consumer catch up."
	case "standby_feedback":
		return "A replica's hot_standby_feedback is holding the horizon. Shorten long queries on the standby, or weigh turning hot_standby_feedback off (risks query cancellations there)."
	case "prepared_xact":
		return "An abandoned prepared (2PC) transaction blocks vacuum forever. COMMIT PREPARED / ROLLBACK PREPARED the gid once you confirm its transaction manager is done."
	default:
		return "Identify and release the holder of the oldest xmin."
	}
}

// replicationSlotRisk flags an inactive replication slot that is holding back WAL
// removal — the retained log grows until the slot's consumer reconnects or the
// slot is dropped, and unbounded growth fills the data disk and stops the primary.
// wal_status='lost' means required WAL is already gone: the slot is broken.
func replicationSlotRisk(c *model.Context, add func(model.Finding)) {
	if c.Replication == nil {
		return
	}
	for _, s := range c.Replication.Slots {
		lost := s.WALStatus == "lost"
		// An active, healthy slot is fine. Only inactive slots (or an already-lost
		// one) are a risk, and an inactive slot below the floor is just a brief gap.
		if s.Active && !lost {
			continue
		}
		if !lost && s.RetainedBytes < slotRetainWarnBytes {
			continue
		}
		sev := model.SeverityWarn
		score := 55.0
		note := "inactive — no consumer connected"
		switch {
		case lost:
			sev = model.SeverityCritical
			score = 92
			note = "wal_status=lost — required WAL has already been removed"
		case s.RetainedBytes >= slotRetainCritBytes:
			sev = model.SeverityCritical
			score = 85
		}
		ev := []string{
			fmt.Sprintf("slot %q (%s) on %s", s.Name, orText(s.Type, "unknown"), orText(s.Database, "cluster")),
			note,
			fmt.Sprintf("WAL retained: %s", humanBytes(s.RetainedBytes)),
		}
		if s.WALStatus != "" && !lost {
			ev = append(ev, "wal_status="+s.WALStatus)
		}
		add(model.Finding{
			ID: "replication_slot_inactive", Severity: sev,
			Title:       fmt.Sprintf("Inactive replication slot %q pinning %s of WAL", s.Name, humanBytes(s.RetainedBytes)),
			Detail:      "An inactive replication slot holds back WAL removal from its restart point. The retained WAL grows until the slot's consumer reconnects or the slot is dropped — a classic way to fill the data disk and take the primary down.",
			Evidence:    ev,
			Remediation: fmt.Sprintf("If the standby/subscriber is gone for good, drop it: SELECT pg_drop_replication_slot('%s'). If it should be connected, restart its consumer. Set max_slot_wal_keep_size to cap the retention.", s.Name),
			Impact:      impact(model.DimRisk, score, humanBytes(s.RetainedBytes)+" WAL retained", "pg_replication_slots"),
			Confidence:  0.9,
		})
	}
}

// subscriptionDown flags a logical subscription whose apply worker isn't running:
// changes from the publisher aren't being applied, so this subscriber is drifting.
func subscriptionDown(c *model.Context, add func(model.Finding)) {
	if c.Replication == nil {
		return
	}
	for _, s := range c.Replication.Subscriptions {
		if s.WorkerRunning {
			continue
		}
		add(model.Finding{
			ID: "subscription_worker_down", Severity: model.SeverityWarn,
			Title:       fmt.Sprintf("Logical subscription %q has no running apply worker", s.Name),
			Detail:      "The subscription exists but its apply worker isn't running, so changes from the publisher aren't being replicated. Data on this subscriber is drifting from the source.",
			Evidence:    []string{fmt.Sprintf("subscription %q: apply worker not running", s.Name)},
			Remediation: "Check the subscriber logs for apply errors, confirm the subscription is enabled (ALTER SUBSCRIPTION … ENABLE), and that the publisher's slot still exists.",
			Impact:      impact(model.DimRisk, 60, "apply worker not running", "pg_stat_subscription"),
			Confidence:  0.75,
		})
	}
}

// orText returns s, or fallback when s is empty.
func orText(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
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
	// If pg_stat_statements is evicting entries, this query may have been evicted
	// and re-entered (stats reset to zero) — the "regression" could be an artifact.
	// Carry that as a load-bearing caveat and drop confidence below the assertion line.
	conf := 0.8
	var caveats []string
	if pgssEvicting(c) {
		caveats = append(caveats, "pg_stat_statements is evicting entries — this query may have been evicted and re-entered (stats reset), so the regression could be an artifact (see pgss_entries_evicted)")
		conf = 0.4
	}
	add(model.Finding{
		ID: "query_slowdown", Severity: model.SeverityWarn,
		Title:       fmt.Sprintf("Query %s is %.1f× slower (%.0f → %.0f ms mean)", queryTagStr(worst.Subject), worstFactor, worst.Before, worst.After),
		Detail:      "A query's mean execution time regressed sharply versus pgbot's baseline. Often a plan flip after the table grew, a dropped or invalidated index, or stale statistics.",
		Remediation: "Check for a missing/invalid index and run ANALYZE on the table; compare the current plan against before.",
		Impact: impact(model.DimLatency, math.Min(90, 30+worstFactor*10),
			fmt.Sprintf("%.1f× slower", worstFactor),
			"pg_stat_statements mean time vs the baseline"),
		Confidence: conf,
		Caveats:    caveats,
	})
}

// pgssEvicting reports whether pg_stat_statements is discarding entries — which
// biases the top list and can reset a query's stats between runs.
func pgssEvicting(c *model.Context) bool {
	if c.Queries == nil {
		return false
	}
	q := c.Queries
	return q.PgssDealloc > 0 || (q.PgssMax > 0 && q.PgssCount >= q.PgssMax)
}

// pgssEntriesEvicted is a trust finding: when pg_stat_statements fills, the
// top-queries list is a biased sample and query_slowdown deltas can be fiction.
// It also stamps Queries.Section.Reason so any consumer sees the caveat.
func pgssEntriesEvicted(c *model.Context, add func(model.Finding)) {
	if c.Queries == nil || !c.Queries.Enabled || !pgssEvicting(c) {
		return
	}
	q := c.Queries
	c.Queries.Reason = fmt.Sprintf("pg_stat_statements at capacity (%d/%d entries, %s evictions) — the top-queries list is a biased sample and cross-run query deltas may compare against an evicted-and-re-entered query", q.PgssCount, q.PgssMax, human(q.PgssDealloc))
	add(model.Finding{
		ID: "pgss_entries_evicted", Severity: model.SeverityWarn,
		Title:       fmt.Sprintf("pg_stat_statements is evicting entries (%d/%d, %s discarded)", q.PgssCount, q.PgssMax, human(q.PgssDealloc)),
		Detail:      "When pg_stat_statements fills, it discards least-used entries. The top-queries view becomes a biased sample, and a query that was evicted and re-entered resets to zero — so query_slowdown deltas can be fiction.",
		Remediation: "Raise pg_stat_statements.max (needs a restart) so the workload fits, or accept that low-frequency queries won't appear.",
		Impact:      impact(model.DimThroughput, 25, fmt.Sprintf("%d/%d entries", q.PgssCount, q.PgssMax), "pg_stat_statements_info.dealloc + count vs max"),
		Confidence:  0.85,
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

// workMemLow — sorts/hashes are spilling to disk because they don't fit in
// work_mem. A config-tuning recommendation, not a defect.
func workMemLow(c *model.Context, add func(model.Finding)) {
	if c.Health == nil || c.Health.TempBytesPerSec == nil || *c.Health.TempBytesPerSec < tuneTempSpillBytesPerSec {
		return
	}
	rate := int64(*c.Health.TempBytesPerSec)
	cur := settingParam(c, "work_mem")
	add(model.Finding{
		ID: "work_mem_low", Severity: model.SeverityWarn,
		Title:       fmt.Sprintf("Queries spilling to disk — work_mem is %s", orUnknownSetting(cur)),
		Detail:      fmt.Sprintf("Sorts and hashes are writing %s/s of temporary files because they don't fit in work_mem, forcing slower on-disk operations.", humanBytes(rate)),
		Remediation: "Raise work_mem — but it's per-operation, so budget total ≈ work_mem × active connections; or add an index so the sort/hash is avoided.",
		Impact: impact(model.DimThroughput, math.Min(80, math.Log10(float64(rate))*10),
			humanBytes(rate)+"/s temp files", "pg_stat_database.temp_bytes rate"),
		Confidence: 0.7,
	})
}

// checkpointsForced — WAL is filling max_wal_size before the timed checkpoint
// interval, so checkpoints fire on volume (IO spikes, more full-page writes).
func checkpointsForced(c *model.Context, add func(model.Finding)) {
	if c.IO == nil {
		return
	}
	total := c.IO.CheckpointsReq + c.IO.CheckpointsTimed
	if total < tuneForcedCheckpointMin {
		return
	}
	frac := float64(c.IO.CheckpointsReq) / float64(total)
	if frac < tuneForcedCheckpointFrac {
		return
	}
	cur := settingParam(c, "max_wal_size")
	add(model.Finding{
		ID: "checkpoints_forced", Severity: model.SeverityWarn,
		Title:       fmt.Sprintf("%.0f%% of checkpoints forced by WAL pressure (%d of %d)", frac*100, c.IO.CheckpointsReq, total),
		Detail:      "Checkpoints are triggered by max_wal_size filling rather than the timed interval — WAL is written faster than the checkpoint spacing expects, causing IO spikes and extra full-page writes.",
		Remediation: fmt.Sprintf("Raise max_wal_size (currently %s) so checkpoints are paced by time, not WAL volume.", orUnknownSetting(cur)),
		Impact: impact(model.DimThroughput, math.Min(75, frac*100),
			fmt.Sprintf("%d of %d forced", c.IO.CheckpointsReq, total), "pg_stat_checkpointer forced vs timed"),
		Confidence: 0.7,
	})
}

// connectionsOverprovisioned — max_connections far above real usage. Each slot
// reserves backend memory whether used or not; better handled by a pooler.
func connectionsOverprovisioned(c *model.Context, add func(model.Finding)) {
	if c.Limits == nil || c.Limits.ConnectionsMax < tuneConnOverprovMax {
		return
	}
	if float64(c.Limits.ConnectionsUsed) >= float64(c.Limits.ConnectionsMax)*tuneConnOverprovUseFrac {
		return
	}
	add(model.Finding{
		ID: "connections_overprovisioned", Severity: model.SeverityInfo,
		Title:       fmt.Sprintf("max_connections is %d but only %d in use", c.Limits.ConnectionsMax, c.Limits.ConnectionsUsed),
		Detail:      "Each connection slot reserves backend memory (roughly work_mem plus overhead) whether used or not. A high max_connections with low real usage wastes RAM and invites connection storms.",
		Remediation: "Put a pooler (PgBouncer) in front and lower max_connections to match real concurrency.",
		Impact: impact(model.DimCost, 20,
			fmt.Sprintf("%d/%d slots used", c.Limits.ConnectionsUsed, c.Limits.ConnectionsMax), "max_connections vs observed usage"),
		Confidence: 0.6,
	})
}

// ioTimingOff flags track_io_timing=off. With it off, blk_read_time/blk_write_time
// are zero, so pgbot can't tell an IO-bound query from a CPU-bound one — which
// degrades query analysis and the wait profile. Measured overhead is negligible
// on modern hardware. Info severity.
func ioTimingOff(c *model.Context, add func(model.Finding)) {
	if settingParam(c, "track_io_timing") != "off" {
		return
	}
	add(model.Finding{
		ID: "io_timing_off", Severity: model.SeverityInfo,
		Title:       "track_io_timing is off — no per-query IO time",
		Detail:      "With track_io_timing off, block read/write times are zero, so pgbot (and pg_stat_statements) can't separate IO-bound queries from CPU-bound ones. That weakens query analysis and the wait profile.",
		Remediation: "Set track_io_timing = on (a reload, no restart). Overhead is negligible on modern hardware — verify with pg_test_timing if unsure.",
		Impact:      impact(model.DimThroughput, 15, "no IO timing", "track_io_timing setting"),
		Confidence:  1.0,
	})
}

func settingParam(c *model.Context, name string) string {
	if c.Settings == nil || c.Settings.Params == nil {
		return ""
	}
	return c.Settings.Params[name]
}

func orUnknownSetting(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
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
