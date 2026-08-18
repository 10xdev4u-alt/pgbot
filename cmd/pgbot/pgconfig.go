package main

import (
	"github.com/pgrundev/pgbot/internal/config"
	"github.com/pgrundev/pgbot/internal/findings"
	"github.com/pgrundev/pgbot/internal/model"
	"github.com/pgrundev/pgbot/internal/store"
)

// computeFindings runs the deterministic analysis under the active .pgbot.toml:
// [thresholds] feed Compute (so a raised threshold means a finding is never
// produced), then [severity]/[[ignore]] rules are applied. Suppressed findings
// are KEPT in the slice (marked) — the renderer and exit code read the flag.
// Finally it appends the suppression-hygiene notes (B2-3): expired rules, and —
// using the baseline store — rules that have gone dead. A config error
// (unreadable --config, a credential-shaped key) is fatal and returned.
func computeFindings(c *model.Context, f inspectFlags) error {
	cfg, err := config.Load(f.config)
	if err != nil {
		return err
	}
	// Only file rules are tracked for rot; --ignore one-offs are ephemeral.
	fileRules := append([]config.IgnoreRule(nil), cfg.Ignore...)
	cfg.AddInlineIgnores(f.ignore)

	c.Findings = findings.ComputeWithTunables(c, cfg.Tunables)
	now := c.CollectedAt
	c.Findings = cfg.Apply(c.Findings, now)
	c.ConfigWarnings = cfg.Warnings

	// Rot: expired rules (no store needed).
	c.Findings = append(c.Findings, cfg.ExpiredFindings(now)...)

	// Rot: dead-rule detection needs the history store. Best-effort — a broken
	// local store must never change the health findings.
	if !f.noStore && len(fileRules) > 0 {
		if unused := deadRules(f.storePath, c, fileRules, cfg.MatchedRuleStrings()); len(unused) > 0 {
			c.Findings = append(c.Findings, config.UnusedFindings(unused)...)
		}
	}

	// --profile=schema: keep only findings derivable from the catalog alone, so a
	// PR check against an empty database stays quiet about everything the empty
	// database can't speak to (D3-1). The collectors that would feed the rest were
	// already skipped; this is the belt-and-suspenders filter over the findings.
	if f.schemaProfile() {
		c.Profile = "schema"
		kept := c.Findings[:0]
		for _, fd := range c.Findings {
			if findings.IsSchemaScoped(fd.ID) {
				kept = append(kept, fd)
			}
		}
		c.Findings = kept
	}

	// --fail-on-new: mark findings already in the base report as preexisting, so
	// only what this change introduced moves the exit code and the annotations
	// (D3-2). Runs last, over the final finding set.
	if f.failOnNew != "" {
		if err := applyFailOnNew(c, f.failOnNew); err != nil {
			return err
		}
	}
	return nil
}

// deadRules records which file ignore rules matched this run (whole-finding or
// row-level, as tracked by Apply) and returns those that have matched nothing for
// several consecutive runs (B2-3).
func deadRules(storePath string, c *model.Context, fileRules []config.IgnoreRule, matched map[string]bool) []string {
	st, err := store.Open(storePath)
	if err != nil {
		return nil
	}
	defer st.Close()

	active := make([]string, 0, len(fileRules))
	for _, r := range fileRules {
		active = append(active, r.String())
	}
	unused, _ := st.RecordSuppressionUsage(c.Fingerprint, active, matched, c.CollectedAt)
	return unused
}
