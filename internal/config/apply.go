package config

import (
	"path"
	"strings"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

// Apply enforces the config over already-computed findings, in the precedence
// the spec fixes: [thresholds] were already applied before Compute (they change
// whether a finding is produced at all); here we remap [severity], then mark
// [[ignore]] matches suppressed. A suppressed finding is NEVER deleted — the
// renderer and exit-code logic read the Suppressed flag and decide visibility.
// now is used to skip expired ignore rules (B2-3).
func (c *Config) Apply(fs []model.Finding, now time.Time) []model.Finding {
	for i := range fs {
		f := &fs[i]
		if sv, ok := c.Severity[f.ID]; ok && sv != f.Severity {
			f.SeverityRemapped = f.Severity
			f.Severity = sv
		}
		if rule, ok := c.matchIgnore(f.ID, f.Object, now); ok {
			f.Suppressed = true
			f.SuppressionReason = rule.Reason
			f.SuppressionRule = rule.String()
		}
	}
	return fs
}

// matchIgnore returns the most specific active ignore rule for (id, object).
// Specificity: an exact object beats a glob beats an omitted object; ties break
// to the earliest rule in the file (stable).
func (c *Config) matchIgnore(id, object string, now time.Time) (IgnoreRule, bool) {
	best := -1
	bestSpec := 0
	for i, r := range c.Ignore {
		if r.Finding != id || expired(r, now) {
			continue
		}
		spec, ok := matchObject(r.Object, object)
		if ok && spec > bestSpec {
			bestSpec, best = spec, i
		}
	}
	if best < 0 {
		return IgnoreRule{}, false
	}
	return c.Ignore[best], true
}

// matchObject scores how specifically pattern matches object:
// exact=3, glob=2, omitted (match every object of the finding)=1, no match=0.
func matchObject(pattern, object string) (int, bool) {
	switch {
	case pattern == "":
		return 1, true
	case pattern == object:
		return 3, true
	}
	if ok, _ := path.Match(pattern, object); ok {
		return 2, true
	}
	return 0, false
}

// expired reports whether an ignore rule's expiry date has passed. The rule is
// active through the whole of its `expires` day (UTC); expired the next day.
func expired(r IgnoreRule, now time.Time) bool {
	if r.Expires == "" {
		return false
	}
	d, err := time.Parse(expiryLayout, r.Expires)
	if err != nil {
		return false // malformed → already warned at load; treat as no expiry
	}
	n := now.UTC()
	nowDay := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	return nowDay.After(d)
}

// AddInlineIgnores appends one-off ignore rules from repeated --ignore flags,
// each "finding[:object]" (B2-4). They carry a fixed reason so they're
// distinguishable from file rules in the report and in --json.
func (c *Config) AddInlineIgnores(specs []string) {
	for _, s := range specs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		finding, object := s, ""
		if i := strings.IndexByte(s, ':'); i >= 0 {
			finding, object = s[:i], s[i+1:]
		}
		if finding == "" {
			continue
		}
		c.Ignore = append(c.Ignore, IgnoreRule{Finding: finding, Object: object, Reason: "--ignore flag"})
	}
}

// MatchedRules reports, for a set of findings, which ignore rules actually fired.
// Used by `config check`/`explain` and the dead-rule detector (B2-3).
func (c *Config) MatchedRules(fs []model.Finding, now time.Time) map[string]bool {
	hit := map[string]bool{}
	for _, f := range fs {
		if r, ok := c.matchIgnore(f.ID, f.Object, now); ok {
			hit[r.String()] = true
		}
	}
	return hit
}
