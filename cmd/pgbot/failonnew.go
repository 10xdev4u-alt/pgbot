package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/pgrundev/pgbot/internal/model"
)

// applyFailOnNew loads a base report and marks every head finding already present
// in it as Preexisting, so the exit code, SARIF, and terminal act only on what
// this change introduced (D3-2). A file path is the only base source, which
// supports both CI patterns without pgbot knowing about either: run twice in one
// job (base branch, then head), or download an artifact main's CI produced.
func applyFailOnNew(c *model.Context, path string) error {
	base, err := loadBaseReport(path)
	if err != nil {
		return err
	}
	if err := validateBase(base, c); err != nil {
		return err
	}
	markPreexisting(c, base)
	return nil
}

func loadBaseReport(path string) (*model.Context, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, usageErrf("--fail-on-new: cannot read base report %q: %v", path, err)
	}
	var base model.Context
	if err := json.Unmarshal(raw, &base); err != nil {
		return nil, usageErrf("--fail-on-new: %q is not a pgbot JSON report: %v", path, err)
	}
	return &base, nil
}

// validateBase refuses a comparison that would be silently wrong — a wrong
// comparison is worse than none. A schema-incompatible base, or one taken under a
// different --profile (a full base vs a schema head would mark every workload
// finding "resolved" and treat every schema finding as suspect), is a hard error.
func validateBase(base, head *model.Context) error {
	if base.SchemaVersion == "" {
		return usageErrf("--fail-on-new: base report has no schema_version — not a pgbot report")
	}
	if majorVersion(base.SchemaVersion) != majorVersion(head.SchemaVersion) {
		return usageErrf("--fail-on-new: base schema_version %s is incompatible with this build's %s",
			base.SchemaVersion, head.SchemaVersion)
	}
	if base.Profile != head.Profile {
		return usageErrf("--fail-on-new: base was --profile=%s but this run is --profile=%s — compare like with like",
			profileName(base.Profile), profileName(head.Profile))
	}
	return nil
}

func majorVersion(v string) string {
	if i := strings.IndexByte(v, '.'); i >= 0 {
		return v[:i]
	}
	return v
}

func profileName(p string) string {
	if p == "" {
		return "full"
	}
	return p
}

// markPreexisting flags every head finding whose findings are all already in base
// at the same or lower severity. Identity is (id, object) — the stable Object from
// B2-0. An aggregate finding expands to one identity per Objects[] row, so a new
// evidence entry inside an otherwise-existing finding (a fourth unindexed FK on
// top of three) is correctly seen as new; a finding is preexisting only when EVERY
// one of its rows is old AND un-escalated. Cluster-scoped findings have an empty
// object, so identity is the id alone.
func markPreexisting(head, base *model.Context) {
	baseRank := map[findingKey]int{}
	for _, bf := range base.Findings {
		r := severityRank(bf.Severity)
		for _, k := range expandKeys(bf) {
			if r > baseRank[k] {
				baseRank[k] = r
			}
		}
	}
	for i := range head.Findings {
		f := &head.Findings[i]
		hr := severityRank(f.Severity)
		anyNew := false
		for _, k := range expandKeys(*f) {
			br, ok := baseRank[k]
			if !ok || hr > br { // absent from base, or severity escalated (regression)
				anyNew = true
				break
			}
		}
		f.Preexisting = !anyNew
	}
}

type findingKey struct{ id, object string }

// expandKeys is the finding's identity set: one (id, object) per aggregate row, or
// the single (id, object) for a non-aggregate finding.
func expandKeys(f model.Finding) []findingKey {
	if len(f.Objects) > 0 {
		ks := make([]findingKey, 0, len(f.Objects))
		for _, o := range f.Objects {
			ks = append(ks, findingKey{f.ID, o})
		}
		return ks
	}
	return []findingKey{{f.ID, f.Object}}
}
