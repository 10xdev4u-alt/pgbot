package findings

import "strings"

// Meta is the machine-readable metadata for one finding: the contract the
// docs/findings/<id>.md page must match, and the source of truth for the
// `explain-finding` command and the terminal doc references (B7). It is checked
// against what Compute actually emits (TestCatalog_matchesEmitted) so a stale
// Meta fails CI, and against every page's front-matter (the docs test) so a page
// that disagrees with the binary fails CI too.
type Meta struct {
	Severity      string   // base severity: critical | warn | info
	CriticalWhen  string   // when the severity escalates to critical ("" if fixed)
	Dimension     string   // Impact.Dimension: risk | storage | latency | throughput
	ObjectClass   string   // suppression object class: cluster|relation|index|query|slot|sub|setting|db
	Requires      []string // capabilities/versions the finding needs
	Thresholds    []string // overridable [thresholds] keys, if any
	Related       []string // finding ids that travel with this one
}

// catalog holds Meta for every finding. B7-0 seeds the two exemplars plus the
// critical review case; B7-1 completes it to all knownIDs (guarded by the docs
// coverage test). Keep entries in sync with the code — the consistency test is
// the backstop.
var catalog = map[string]Meta{
	"sequence_exhaustion": {
		Severity: "warn", CriticalWhen: "a sequence is ≥90% consumed",
		Dimension: "risk", ObjectClass: "cluster",
		Related: []string{"txid_wraparound"},
	},
	"low_hot_update_ratio": {
		Severity: "warn", Dimension: "throughput", ObjectClass: "cluster",
		Requires: []string{"track_counts (default on)"},
		Related:  []string{"table_bloat", "unused_indexes"},
	},
	"checksum_failures": {
		Severity: "critical", Dimension: "risk", ObjectClass: "cluster",
		Requires: []string{"PG12+", "data_checksums=on"},
		Related:  []string{"ignore_checksum_failure_on", "checksums_disabled"},
	},
}

// MetaFor returns the metadata for a finding id.
func MetaFor(id string) (Meta, bool) { m, ok := catalog[id]; return m, ok }

// CatalogIDs returns the ids that currently have a Meta entry.
func CatalogIDs() []string {
	ids := make([]string, 0, len(catalog))
	for id := range catalog {
		ids = append(ids, id)
	}
	return ids
}

// ObjectClass maps a finding's Object string to its suppression class — the
// value a docs page declares and `config` reasons about.
func ObjectClass(object string) string {
	switch {
	case object == "":
		return "cluster"
	case strings.HasPrefix(object, "setting:"):
		return "setting"
	case strings.HasPrefix(object, "slot:"):
		return "slot"
	case strings.HasPrefix(object, "sub:"):
		return "sub"
	case strings.HasPrefix(object, "q:"):
		return "query"
	case strings.HasPrefix(object, "db:"):
		return "db"
	case strings.Contains(object, "."):
		return "relation"
	default:
		return "cluster"
	}
}
