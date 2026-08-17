package findings

// clusterWide is the set of findings whose SOURCE is cluster-wide, not the
// connected database — so in an --all-databases run they are identical for every
// database and should be reported once, not N times (B3). The rest read
// per-database catalogs (pg_stat_user_tables/indexes, this database's
// pg_stat_database, pg_sequences, its checksum/conflict counters) and genuinely
// differ per database.
//
// Cluster-wide sources: pg_settings; pg_stat_activity (all backends, any db);
// pg_stat_archiver; pg_stat_replication / pg_replication_slots /
// pg_stat_subscription; pg_stat_statements (tracks all databases);
// pg_stat_bgwriter / pg_stat_checkpointer; the max transaction/multixact age
// across pg_database; the xmin horizon (backend_xmin + slots).
var clusterWide = map[string]bool{
	// settings
	"fsync_off": true, "full_page_writes_off": true, "autovacuum_off": true,
	"random_page_cost_high": true, "work_mem_overcommit": true, "statement_timeout_unset": true,
	"io_timing_off": true, "work_mem_low": true, "checkpoints_forced": true,
	"connections_overprovisioned": true, "checksums_disabled": true, "ignore_checksum_failure_on": true,
	// cluster activity (pg_stat_activity)
	"blocking_chains": true, "idle_in_transaction": true, "long_running_transaction": true,
	"connection_saturation": true, "prepared_xact_abandoned": true, "vacuum_horizon_blocked": true,
	"wait_lock_contention": true, "wait_io_bound": true, "wait_lwlock_pressure": true,
	"autovacuum_saturated": true, "autovacuum_long_running": true,
	// wraparound (max across pg_database)
	"txid_wraparound": true, "mxid_wraparound": true,
	// archiving / replication
	"archiving_failing": true, "archiving_stalled": true, "archiving_disabled": true,
	"sync_rep_degraded": true, "replica_lag_time": true, "replica_disconnected": true,
	"replication_slot_inactive": true, "subscription_worker_down": true,
	// NOTE: the pg_stat_statements findings (query_slowdown, pgss_entries_evicted,
	// pg_stat_statements_missing) are deliberately NOT here. The extension is
	// created PER DATABASE, so whether it's available differs per database — a
	// database that genuinely lacks it must report pg_stat_statements_missing, and
	// deduping against a "first" database that happens to have it would hide that.
}

// ClusterWide reports whether a finding comes from a cluster-wide source and so
// need only be reported once across an --all-databases run.
func ClusterWide(id string) bool { return clusterWide[id] }
