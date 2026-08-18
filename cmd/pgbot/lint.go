package main

import (
	"time"

	"github.com/spf13/cobra"
)

// newLintCmd is `inspect --profile=schema --no-store` under a name the CI audience
// reaches for (D3-1). Schema-only: it runs the catalog-derived findings a
// migration can introduce — invalid/redundant indexes, unindexed FKs, narrow
// identity columns, autovacuum disabled on a table — and nothing that needs a live
// workload, so it's safe against a freshly-migrated, empty database. Pair it with
// --fail-on-new in CI to fail only on regressions a PR introduces.
func newLintCmd() *cobra.Command {
	var f inspectFlags
	cmd := &cobra.Command{
		Use:   "lint <connection-string>",
		Short: "Schema-only health check (safe on an empty CI database)",
		Long: "Run only the schema-scoped findings — the ones derivable from the catalog\n" +
			"alone, valid on an empty database — and skip everything that needs query\n" +
			"traffic, history, or a real server config. An alias for\n" +
			"`inspect --profile=schema --no-store`. In CI, add --fail-on-new <base.json>\n" +
			"to report only what the PR changed.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f.profile = "schema"
			f.noStore = true
			return runInspect(cmd, args, f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.failOn, "fail-on", "warn", "exit non-zero on findings at/above this severity: critical|warn|info|none")
	fl.StringVar(&f.format, "format", "text", "output format: text|json|sarif|junit|prometheus")
	fl.StringVar(&f.config, "config", "", "path to .pgbot.toml (default: discover)")
	fl.StringArrayVar(&f.ignore, "ignore", nil, "suppress a finding for this run: finding[:object] (repeatable)")
	fl.StringVar(&f.failOnNew, "fail-on-new", "", "path to a base report (JSON); act only on findings new vs it")
	fl.DurationVar(&f.timeout, "timeout", 30*time.Second, "total wall-clock budget for the run")
	fl.BoolVar(&f.noColor, "no-color", false, "disable ANSI color")
	return cmd
}
