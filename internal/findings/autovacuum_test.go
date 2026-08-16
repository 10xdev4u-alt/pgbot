package findings

import (
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestAutovacuumHealth(t *testing.T) {
	// autovacuum_enabled=false in reloptions → critical.
	disabled := &model.Context{Tables: &model.Tables{Top: []model.TableStat{
		{Schema: "public", Name: "legacy", LiveTuples: 5000, AutovacuumDisabled: true},
	}}}
	if f := has(Compute(disabled), "autovacuum_disabled_on_table"); f == nil || f.Severity != model.SeverityCritical {
		t.Fatalf("autovacuum_enabled=false must be critical, got %+v", f)
	}
	// Above-floor table never vacuumed → warn.
	never := &model.Context{Tables: &model.Tables{Top: []model.TableStat{
		{Schema: "public", Name: "t", LiveTuples: 100_000},
	}}}
	if has(Compute(never), "table_never_vacuumed") == nil {
		t.Error("above-floor never-vacuumed table should fire")
	}
	// Dead tuples past the trigger, no autovacuum → starved.
	starved := &model.Context{Tables: &model.Tables{Top: []model.TableStat{
		{Schema: "public", Name: "big", LiveTuples: 1_000_000, DeadTuples: 500_000, LastVacuum: ptrNow()},
	}}}
	if has(Compute(starved), "autovacuum_starved") == nil {
		t.Error("500k dead on 1M rows with no autovacuum should be starved")
	}
	// Worker saturation (point sample) → warn + caveat.
	sat := &model.Context{
		Activity: &model.Activity{AutovacuumWorkers: 3},
		Settings: &model.Settings{Params: map[string]string{"autovacuum_max_workers": "3"}},
	}
	if f := has(Compute(sat), "autovacuum_saturated"); f == nil || len(f.Caveats) == 0 {
		t.Errorf("3/3 workers should warn with a point-sample caveat, got %+v", f)
	}
}

func ptrNow() *time.Time { t := time.Now(); return &t }
