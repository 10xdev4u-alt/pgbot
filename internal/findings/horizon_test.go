package findings

import (
	"strings"
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestVacuumHorizonBlocked(t *testing.T) {
	// Below the threshold — a momentary query, not a block.
	small := &model.Context{Horizon: &model.VacuumHorizon{Holders: []model.HorizonHolder{
		{Source: "backend", Holder: "123", XminAge: 500, Detail: "active"},
	}}}
	if has(Compute(small), "vacuum_horizon_blocked") != nil {
		t.Error("a tiny xmin age must not fire")
	}

	// Real horizon block, storage-dimension.
	warn := &model.Context{Horizon: &model.VacuumHorizon{Holders: []model.HorizonHolder{
		{Source: "prepared_xact", Holder: "gid_abc", XminAge: 5_000_000, Detail: "prepared 3600s ago"},
	}}}
	f := has(Compute(warn), "vacuum_horizon_blocked")
	if f == nil {
		t.Fatal("a 5M-transaction horizon block must fire")
	}
	if f.Severity != model.SeverityWarn || f.Impact.Dimension != model.DimStorage {
		t.Errorf("expected warn/storage, got %s/%s", f.Severity, f.Impact.Dimension)
	}
	if !strings.Contains(f.Remediation, "PREPARED") {
		t.Errorf("prepared-xact remediation should mention COMMIT/ROLLBACK PREPARED: %q", f.Remediation)
	}

	// Old enough to threaten wraparound → critical/risk (pins to top of report).
	crit := &model.Context{Horizon: &model.VacuumHorizon{Holders: []model.HorizonHolder{
		{Source: "replication_slot", Holder: "dead_slot", XminAge: 1_500_000_000, Detail: "inactive"},
	}}}
	f = has(Compute(crit), "vacuum_horizon_blocked")
	if f == nil || f.Severity != model.SeverityCritical || f.Impact.Dimension != model.DimRisk {
		t.Errorf("a near-wraparound horizon must be critical/risk, got %+v", f)
	}
}

// The table_bloat remediation must point at vacuum_horizon_blocked when a holder
// is pinning the horizon — the cross-reference that turns a warning into a fix.
func TestTableBloat_crossReferencesHorizon(t *testing.T) {
	c := &model.Context{
		Tables: &model.Tables{Top: []model.TableStat{
			{Schema: "public", Name: "events", DeadRatio: 0.35, LiveTuples: 1_000_000, DeadTuples: 540_000, TotalBytes: 20 << 30},
		}},
		Horizon: &model.VacuumHorizon{Holders: []model.HorizonHolder{
			{Source: "backend", Holder: "999", XminAge: 3_000_000, Detail: "idle in transaction, xact 7200s"},
		}},
	}
	f := has(Compute(c), "table_bloat")
	if f == nil {
		t.Fatal("table_bloat should fire")
	}
	if !strings.Contains(f.Remediation, "vacuum_horizon_blocked") {
		t.Errorf("table_bloat remediation must cross-reference the horizon block: %q", f.Remediation)
	}
}
