package main

import (
	"time"

	"github.com/pgrundev/pgbot/internal/config"
	"github.com/pgrundev/pgbot/internal/findings"
	"github.com/pgrundev/pgbot/internal/model"
)

// computeFindings runs the deterministic analysis under the active .pgbot.toml:
// [thresholds] feed Compute (so a raised threshold means a finding is never
// produced), then [severity]/[[ignore]] rules are applied. Suppressed findings
// are KEPT in the slice (marked) — the renderer and exit code read the Suppressed
// flag and decide visibility. A config error (unreadable --config, a
// credential-shaped key) is fatal and returned.
func computeFindings(c *model.Context, configPath string, inlineIgnores []string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	cfg.AddInlineIgnores(inlineIgnores) // --ignore finding[:object] one-offs (B2-4)
	c.Findings = findings.ComputeWithTunables(c, cfg.Tunables)
	c.Findings = cfg.Apply(c.Findings, time.Now())
	c.ConfigWarnings = cfg.Warnings
	return nil
}
