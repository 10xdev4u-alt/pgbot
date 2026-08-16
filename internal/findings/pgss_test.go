package findings

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestPgssEntriesEvicted(t *testing.T) {
	// Healthy: room to spare, no evictions.
	ok := &model.Context{Queries: &model.Queries{Enabled: true, PgssDealloc: 0, PgssCount: 100, PgssMax: 5000}}
	if has(Compute(ok), "pgss_entries_evicted") != nil {
		t.Error("healthy pg_stat_statements must not fire")
	}

	// Evicting → finding + a stamped section reason so consumers see the caveat.
	evict := &model.Context{Queries: &model.Queries{Enabled: true, PgssDealloc: 42, PgssCount: 5000, PgssMax: 5000}}
	f := has(Compute(evict), "pgss_entries_evicted")
	if f == nil || f.Severity != model.SeverityWarn {
		t.Fatalf("expected warn, got %+v", f)
	}
	if evict.Queries.Reason == "" {
		t.Error("Queries.Reason must be stamped when evicting")
	}
}

// query_slowdown must be caveated and its confidence dropped below the assertion
// line when pg_stat_statements is evicting (the delta may be an artifact).
func TestQuerySlowdown_caveatedWhenEvicting(t *testing.T) {
	c := &model.Context{
		Queries: &model.Queries{Enabled: true, PgssDealloc: 10, PgssCount: 5000, PgssMax: 5000},
		Deltas: &model.Deltas{Changes: []model.Delta{
			{ID: "query.mean_ms", Subject: "123", Before: 10, After: 50},
		}},
	}
	f := has(Compute(c), "query_slowdown")
	if f == nil {
		t.Fatal("query_slowdown should fire")
	}
	if f.Confidence >= 0.5 {
		t.Errorf("confidence should drop below 0.5 when pgss is evicting, got %v", f.Confidence)
	}
	if len(f.Caveats) == 0 {
		t.Error("expected an eviction caveat carried into query_slowdown")
	}
}
