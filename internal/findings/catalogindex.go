package findings

import (
	"fmt"
	"sort"
	"strings"
)

// dimensionOrder groups the index the way people arrive at it — by symptom, not
// alphabetically. Risk (time-to-incident) first.
var dimensionOrder = []struct{ key, title, blurb string }{
	{"risk", "Risk — time to incident", "Lost durability, corruption, wraparound, replication — things that end in an outage or data loss."},
	{"storage", "Storage", "Disk wasted by bloat, dead tuples, and indexes that earn nothing."},
	{"latency", "Latency", "Individual queries made slow by missing indexes or stale plans."},
	{"throughput", "Throughput", "Whole-database capacity lost to waits, cache misses, and write amplification."},
	{"cost", "Cost & visibility", "Over-provisioning and missing instrumentation."},
}

var severityRank = map[string]int{"critical": 0, "warn": 1, "info": 2}

// CatalogIndexMarkdown renders docs/findings/README.md from the catalog — the
// by-dimension index of every finding. Generated (never hand-edited) so it can't
// drift: `go run ./tools/schemagen` rewrites it and TestCatalogIndex_matches
// diffs it.
func CatalogIndexMarkdown() []byte {
	var b strings.Builder
	b.WriteString("# pgbot findings\n\n")
	b.WriteString("Every finding pgbot can emit, grouped by the kind of trouble it points at. Each\n")
	b.WriteString("page explains what pgbot observed, why it matters, how to verify it yourself, how\n")
	b.WriteString("to fix it, when to ignore it, and what pgbot cannot see.\n\n")
	b.WriteString("Read a page offline with `pgbot explain-finding <id>`. Suppress or tune any\n")
	b.WriteString("finding via [`.pgbot.toml`](../configuration.md).\n")

	for _, d := range dimensionOrder {
		var ids []string
		for id, m := range catalog {
			if m.Dimension == d.key {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			continue
		}
		sort.Slice(ids, func(i, j int) bool {
			mi, mj := catalog[ids[i]], catalog[ids[j]]
			if severityRank[mi.Severity] != severityRank[mj.Severity] {
				return severityRank[mi.Severity] < severityRank[mj.Severity]
			}
			return ids[i] < ids[j]
		})
		fmt.Fprintf(&b, "\n## %s\n\n", d.title)
		fmt.Fprintf(&b, "%s\n\n", d.blurb)
		for _, id := range ids {
			m := catalog[id]
			badge := strings.ToUpper(m.Severity[:1]) + m.Severity[1:]
			fmt.Fprintf(&b, "- **[%s](%s.md)** · %s — %s\n", id, id, badge, summaries[id])
		}
	}
	out := b.String()
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return []byte(out)
}
