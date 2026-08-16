package config

import (
	"fmt"
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

// ExpiredFindings returns an info finding for each ignore rule whose expiry has
// passed (B2-3). The rule has stopped applying, so the finding it muted will
// resurface — by design. The note prompts the user to renew or delete it.
func (c *Config) ExpiredFindings(now time.Time) []model.Finding {
	var out []model.Finding
	for _, r := range c.Ignore {
		if r.Expires == "" || !expired(r, now) {
			continue
		}
		obj := r.Object
		if obj != "" {
			obj = " (" + obj + ")"
		}
		out = append(out, model.Finding{
			ID: "suppression_expired", Severity: model.SeverityInfo,
			Title:       fmt.Sprintf("suppression for %s%s expired on %s", r.Finding, obj, r.Expires),
			Detail:      "This [[ignore]] rule's expires date has passed, so it no longer suppresses anything and the finding it muted will surface again. Suppressions are meant to be revisited, not left forever.",
			Remediation: "Confirm the underlying issue is resolved and delete the rule, or renew its `expires` date if it still applies.",
			Impact:      model.Impact{Score: 5, Basis: "ignore rule past its expires date"},
			Confidence:  1.0,
		})
	}
	return out
}

// UnusedFindings builds a single info finding naming ignore rules that have
// matched nothing across the last several runs (the store decides which — B2-3).
func UnusedFindings(ruleStrings []string) []model.Finding {
	if len(ruleStrings) == 0 {
		return nil
	}
	return []model.Finding{{
		ID: "suppression_unused", Severity: model.SeverityInfo,
		Title:       fmt.Sprintf("%d suppression rule(s) haven't matched anything recently", len(ruleStrings)),
		Detail:      "These [[ignore]] rules have suppressed nothing across the last several runs — the finding they mute may be gone (the index was dropped, the setting fixed) or its identity changed (a queryid shifts across a major-version upgrade). A stale ignore list becomes a second, invisible config nobody audits.",
		Evidence:    ruleStrings,
		Remediation: "Remove the rules you no longer need; if one should still apply, check the object name still matches (major upgrades reset queryids).",
		Impact:      model.Impact{Score: 5, Basis: "no match across recent snapshots"},
		Confidence:  0.7,
	}}
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
