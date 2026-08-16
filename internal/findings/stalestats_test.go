package findings

import (
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestStaleStatistics(t *testing.T) {
	ts := time.Now()
	// 500k mods on 1M rows; default trigger 50+0.1*1M=100050, 2× = 200100 → stale.
	stale := &model.Context{Tables: &model.Tables{Top: []model.TableStat{
		{Schema: "public", Name: "events", LiveTuples: 1_000_000, ModsSinceAnalyze: 500_000, LastAnalyze: &ts},
	}}}
	if has(Compute(stale), "stale_statistics") == nil {
		t.Error("500k mods on 1M rows should be stale")
	}
	fresh := &model.Context{Tables: &model.Tables{Top: []model.TableStat{
		{Schema: "public", Name: "events", LiveTuples: 1_000_000, ModsSinceAnalyze: 1000, LastAnalyze: &ts},
	}}}
	if has(Compute(fresh), "stale_statistics") != nil {
		t.Error("1000 mods on 1M rows must not fire")
	}
	never := &model.Context{Tables: &model.Tables{Top: []model.TableStat{
		{Schema: "public", Name: "newtab", LiveTuples: 100_000},
	}}}
	if has(Compute(never), "never_analyzed") == nil {
		t.Error("above-floor never-analyzed table should fire")
	}
	// Per-table reloption tightens the threshold: 5k mods, scale 0.001 → trigger
	// 50+0.001*1M=1050, 2×=2100 → fires (with the default 0.1 it would not).
	scale := 0.001
	tight := &model.Context{Tables: &model.Tables{Top: []model.TableStat{
		{Schema: "public", Name: "t", LiveTuples: 1_000_000, ModsSinceAnalyze: 5000, LastAnalyze: &ts, AnalyzeScaleOverride: &scale},
	}}}
	if has(Compute(tight), "stale_statistics") == nil {
		t.Error("tight per-table analyze scale should make 5k mods stale")
	}
}
