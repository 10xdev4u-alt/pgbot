package findings

import "testing"

// The cluster-wide set decides what --all-databases dedupes. Both directions
// are wrong outcomes: a cluster-wide finding missing from the set prints N
// times; a per-database finding wrongly in it gets deduplicated away. These
// two were each wrong once (checksum_failures missing, work_mem_low present) —
// pin the classification to the SQL that feeds each finding.
func TestClusterWide_classification(t *testing.T) {
	// checksum_failures reads ALL of pg_stat_database (checksums.sql has no
	// current_database() filter), so it is identical from every database.
	if !ClusterWide("checksum_failures") {
		t.Error("checksum_failures is cluster-wide (pg_stat_database, all rows) and must be deduped")
	}
	// work_mem_low is driven by THIS database's temp_bytes rate (health.sql
	// filters on current_database()), so each spilling database needs its own.
	if ClusterWide("work_mem_low") {
		t.Error("work_mem_low is per-database (this db's temp_bytes) and must NOT be deduped")
	}
	// Spot-check the stable anchors of each side so a future edit that flips
	// the whole map is caught, not just these two.
	for _, id := range []string{"fsync_off", "txid_wraparound", "archiving_stalled"} {
		if !ClusterWide(id) {
			t.Errorf("%s should be cluster-wide", id)
		}
	}
	for _, id := range []string{"table_bloat", "unused_indexes", "pg_stat_statements_missing"} {
		if ClusterWide(id) {
			t.Errorf("%s should be per-database", id)
		}
	}
}
