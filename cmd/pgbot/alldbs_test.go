package main

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func ctxWith(db string, ids ...string) *model.Context {
	c := &model.Context{Server: model.ServerInfo{Database: db}, Fingerprint: db}
	for _, id := range ids {
		c.Findings = append(c.Findings, model.Finding{ID: id})
	}
	return c
}

// Cluster-wide findings are marked on the first database and dropped from the
// rest; per-database findings stay on every database (B3).
func TestDedupeClusterWide(t *testing.T) {
	dbs := []*model.Context{
		ctxWith("analytics", "fsync_off", "unused_indexes", "pg_stat_statements_missing"),
		ctxWith("app", "fsync_off", "table_bloat", "pg_stat_statements_missing"),
	}
	dedupeClusterWide(dbs)

	// fsync_off is cluster-wide: marked on the first, gone from the second.
	if f := has(dbs[0].Findings, "fsync_off"); f == nil || !f.ClusterScoped {
		t.Error("fsync_off should be present and cluster_scoped on the first database")
	}
	if has(dbs[1].Findings, "fsync_off") != nil {
		t.Error("fsync_off should be dropped from the second database")
	}
	// Per-database findings survive on each — including pg_stat_statements_missing,
	// which is per-db because the extension is installed per-database.
	if has(dbs[0].Findings, "unused_indexes") == nil || has(dbs[1].Findings, "table_bloat") == nil {
		t.Error("per-database findings must be kept")
	}
	for _, c := range dbs {
		if has(c.Findings, "pg_stat_statements_missing") == nil {
			t.Errorf("%s: pg_stat_statements_missing is per-db and must be kept", c.Server.Database)
		}
	}
}

func TestMergeContexts_tagsByDatabase(t *testing.T) {
	dbs := []*model.Context{
		ctxWith("app", "table_bloat"),
		ctxWith("analytics", "table_bloat"),
	}
	dedupeClusterWide(dbs) // table_bloat is per-db, so both survive
	merged := mergeContexts(dbs)
	if len(merged.Findings) != 2 {
		t.Fatalf("expected 2 findings merged, got %d", len(merged.Findings))
	}
	// Each per-db finding's object is tagged with its database so they stay distinct.
	objs := map[string]bool{}
	for _, f := range merged.Findings {
		objs[f.Object] = true
	}
	if !objs["db:app"] || !objs["db:analytics"] {
		t.Errorf("per-db findings should be tagged db:<name>, got %v", objs)
	}
}

func TestDuplicateFingerprint(t *testing.T) {
	if duplicateFingerprint([]*model.Context{ctxWith("a"), ctxWith("b")}) != "" {
		t.Error("distinct fingerprints should report no duplicate")
	}
	dup := []*model.Context{{Fingerprint: "x"}, {Fingerprint: "x"}}
	if duplicateFingerprint(dup) != "x" {
		t.Error("a shared fingerprint must be detected (P0-1 guard)")
	}
}

// has returns the finding with the given id, or nil.
func has(fs []model.Finding, id string) *model.Finding {
	for i := range fs {
		if fs[i].ID == id {
			return &fs[i]
		}
	}
	return nil
}
