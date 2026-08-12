package conn

import "time"

// Capabilities is built once at connect from server_version_num + a probe of
// installed extensions and the current role. Collectors declare what they need
// and are SKIPPED (not failed) when a capability is missing — a section is
// marked unavailable rather than blanking the whole run.
type Capabilities struct {
	VersionNum        int
	VersionText       string
	Database          string
	Provider          Provider  // detected managed platform, for provider-specific fixes
	StartedAt         time.Time // pg_postmaster_start_time(); zero if not readable
	SystemIdentifier  string    // pg_control_system(); "" if not readable
	HasStatStatements bool
	HasHypopg         bool
	HasPgMonitor      bool
	Extensions        []string
}

// Version-derived feature flags. Kept as methods so the thresholds live in one
// place and read like the docs.
func (c Capabilities) HasStatWAL() bool               { return c.VersionNum >= 140000 } // pg_stat_wal
func (c Capabilities) HasStatIO() bool                { return c.VersionNum >= 160000 } // pg_stat_io
func (c Capabilities) HasStatCheckpointer() bool      { return c.VersionNum >= 170000 } // pg_stat_checkpointer
func (c Capabilities) HasStatsFetchConsistency() bool { return c.VersionNum >= 150000 }
func (c Capabilities) StatStatementsTotalCol() string { // renamed in PG13
	if c.VersionNum >= 130000 {
		return "total_exec_time"
	}
	return "total_time"
}

// Satisfied returns the human-readable capability flags that hold, for
// ServerInfo.Capabilities (so a consumer can see what was and wasn't collected).
func (c Capabilities) Satisfied() []string {
	var out []string
	add := func(cond bool, name string) {
		if cond {
			out = append(out, name)
		}
	}
	add(c.HasPgMonitor, "pg_monitor")
	add(c.HasStatStatements, "pg_stat_statements")
	add(c.HasHypopg, "hypopg")
	add(c.HasStatWAL(), "pg_stat_wal")
	add(c.HasStatIO(), "pg_stat_io")
	add(c.HasStatCheckpointer(), "pg_stat_checkpointer")
	add(c.HasStatsFetchConsistency(), "stats_fetch_consistency")
	return out
}
