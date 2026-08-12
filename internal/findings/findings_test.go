package findings

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func ptr(v float64) *float64 { return &v }

func has(fs []model.Finding, id string) *model.Finding {
	for i := range fs {
		if fs[i].ID == id {
			return &fs[i]
		}
	}
	return nil
}

func TestCompute_flagsRealIssues(t *testing.T) {
	c := &model.Context{
		Window:   model.Window{StatsWindowDays: ptr(120)},
		Health:   &model.Health{CacheHitRatio: ptr(0.80), RollbackRatio: ptr(0.15)},
		Activity: &model.Activity{IdleInTransaction: 2, LongestXactSec: 400},
		Locks:    &model.Locks{BlockedCount: 1, Chains: []model.BlockingRow{{BlockedPID: 42, WaitSeconds: 30}}},
		Indexes: &model.Indexes{Unused: []model.IndexStat{
			{Schema: "public", Table: "orders", Name: "big_idx", Bytes: 5 << 20},
		}},
		Tables: &model.Tables{Top: []model.TableStat{
			{Schema: "public", Name: "orders", DeadRatio: 0.35, LiveTuples: 90000, DeadTuples: 48000, ModsSinceAnalyze: 5000},
		}},
		Queries: &model.Queries{Enabled: true},
	}
	fs := Compute(c)

	for _, id := range []string{"blocking_chains", "unused_indexes", "table_bloat", "low_cache_hit", "idle_in_transaction", "long_running_transaction", "high_rollback_ratio", "stale_stats_window"} {
		if has(fs, id) == nil {
			t.Errorf("expected finding %q", id)
		}
	}
	// critical must sort first.
	if len(fs) == 0 || fs[0].Severity != model.SeverityCritical {
		t.Errorf("expected a critical finding first, got %+v", fs)
	}
}

func TestCompute_cleanDatabaseHasNoFalsePositives(t *testing.T) {
	c := &model.Context{
		Health:   &model.Health{CacheHitRatio: ptr(0.999), RollbackRatio: ptr(0.001)},
		Activity: &model.Activity{IdleInTransaction: 0},
		Locks:    &model.Locks{BlockedCount: 0},
		Indexes:  &model.Indexes{},
		Tables:   &model.Tables{},
		Queries:  &model.Queries{Enabled: true},
	}
	if fs := Compute(c); len(fs) != 0 {
		t.Errorf("clean database should have no findings, got %+v", fs)
	}
}

func TestCompute_missingPgssIsInfo(t *testing.T) {
	c := &model.Context{Queries: &model.Queries{Enabled: false}}
	f := has(Compute(c), "pg_stat_statements_missing")
	if f == nil || f.Severity != model.SeverityInfo {
		t.Fatalf("expected info finding for missing pgss, got %+v", f)
	}
}

func TestColdWindow_suppressesCounterFindings_keepsGauges(t *testing.T) {
	cold := int64(120) // 2 min — below the 900s threshold
	c := &model.Context{
		Window: model.Window{WindowAgeSeconds: &cold},
		Health: &model.Health{CacheHitRatio: ptr(0.50)}, // would fire low_cache_hit on a warm window
		Indexes: &model.Indexes{Unused: []model.IndexStat{
			{Schema: "public", Table: "orders", Name: "big_idx", Bytes: 50 << 20},
		}},
		Tables: &model.Tables{Top: []model.TableStat{
			{Schema: "public", Name: "orders", LiveTuples: 1_000_000, SeqScans: 9000, IndexScans: 10},
		}},
		// Gauges — must still fire on a cold window:
		Locks:   &model.Locks{BlockedCount: 1, Chains: []model.BlockingRow{{BlockedPID: 7, WaitSeconds: 12}}},
		Queries: &model.Queries{Enabled: true},
	}
	fs := Compute(c)
	for _, suppressed := range []string{"unused_indexes", "low_cache_hit", "seq_scan_heavy"} {
		if has(fs, suppressed) != nil {
			t.Errorf("cold window must suppress %q", suppressed)
		}
	}
	if has(fs, "blocking_chains") == nil {
		t.Error("blocking chains is a gauge and must still fire on a cold window")
	}
}

func TestSeqScanHeavy_firesOnWarmWindow(t *testing.T) {
	warm := int64(7200)
	c := &model.Context{
		Window: model.Window{WindowAgeSeconds: &warm},
		Tables: &model.Tables{Top: []model.TableStat{
			{Schema: "public", Name: "orders", LiveTuples: 1_000_000, SeqScans: 9000, IndexScans: 10},
		}},
		Queries: &model.Queries{Enabled: true},
	}
	if has(Compute(c), "seq_scan_heavy") == nil {
		t.Error("expected seq_scan_heavy on a large seq-scanned table over a warm window")
	}
}

func TestUnusedIndexes_belowThresholdIgnored(t *testing.T) {
	c := &model.Context{Indexes: &model.Indexes{Unused: []model.IndexStat{
		{Schema: "public", Table: "t", Name: "tiny", Bytes: 100 << 10}, // 100 KiB < 1 MiB
	}}}
	if has(Compute(c), "unused_indexes") != nil {
		t.Error("a sub-threshold unused index must not be flagged")
	}
}

// --- T8: wait-profile findings ---

func waitProfile(samples int, buckets []model.WaitBucket, byQuery []model.QueryWaits) *model.WaitProfile {
	return &model.WaitProfile{Available: true, Samples: samples, WindowSeconds: 5, Buckets: buckets, ByQuery: byQuery}
}

func TestWaitFindings_thinProfileFiresNothing(t *testing.T) {
	// 10 samples (< WaitMinSamples) all on locks: must NOT fire — it's noise.
	c := &model.Context{WaitProfile: waitProfile(10,
		[]model.WaitBucket{{Type: "Lock", Count: 10, Share: 1.0, Events: []model.WaitEvent{{Event: "transactionid", Count: 10, Share: 1.0}}}},
		[]model.QueryWaits{{QueryID: 5, Count: 10, Share: 1.0, LockShare: 1.0}},
	)}
	fs := Compute(c)
	if has(fs, "wait_lock_contention") != nil {
		t.Error("thin profile (<20 samples) must not fire wait findings")
	}
}

func TestWaitFindings_lockContention(t *testing.T) {
	c := &model.Context{WaitProfile: waitProfile(50,
		[]model.WaitBucket{
			{Type: "Lock", Count: 30, Share: 0.6, Events: []model.WaitEvent{{Event: "transactionid", Count: 30, Share: 0.6}}},
			{Type: "CPU", Count: 20, Share: 0.4},
		},
		[]model.QueryWaits{{QueryID: 4242, SampleText: "UPDATE orders SET ...", Count: 30, Share: 0.6, LockShare: 1.0, TopType: "Lock", TopEvent: "Lock:transactionid"}},
	)}
	f := has(Compute(c), "wait_lock_contention")
	if f == nil {
		t.Fatal("a query >30% on locks (with enough samples) should fire wait_lock_contention")
	}
	if f.Severity != model.SeverityWarn {
		t.Errorf("want warn, got %s", f.Severity)
	}
}

func TestWaitFindings_ioBound(t *testing.T) {
	c := &model.Context{WaitProfile: waitProfile(40,
		[]model.WaitBucket{
			{Type: "IO", Count: 24, Share: 0.6, Events: []model.WaitEvent{{Event: "DataFileRead", Count: 24, Share: 0.6}}},
			{Type: "CPU", Count: 16, Share: 0.4},
		}, nil,
	)}
	if has(Compute(c), "wait_io_bound") == nil {
		t.Error(">50% IO should fire wait_io_bound")
	}
}

func TestWaitFindings_idleDatabaseSilent(t *testing.T) {
	// 0 samples: an idle database. No wait findings.
	c := &model.Context{WaitProfile: waitProfile(0, nil, nil)}
	for _, id := range []string{"wait_lock_contention", "wait_io_bound", "wait_lwlock_pressure"} {
		if has(Compute(c), id) != nil {
			t.Errorf("idle database must not fire %s", id)
		}
	}
}
