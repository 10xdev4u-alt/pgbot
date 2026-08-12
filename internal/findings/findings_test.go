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

func TestUnusedIndexes_belowThresholdIgnored(t *testing.T) {
	c := &model.Context{Indexes: &model.Indexes{Unused: []model.IndexStat{
		{Schema: "public", Table: "t", Name: "tiny", Bytes: 100 << 10}, // 100 KiB < 1 MiB
	}}}
	if has(Compute(c), "unused_indexes") != nil {
		t.Error("a sub-threshold unused index must not be flagged")
	}
}
