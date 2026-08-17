// Command pgbot — in-database observability for PostgreSQL. Slice 1 ships the
// inspect one-shot (health report + --json) with a local baseline store that
// makes it temporal from the third run onward. No daemon, no external service,
// no write privilege anywhere in the path.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// version is set by the linker at release time (-X main.version=...).
var version = "dev"

func main() {
	// A SIGINT/SIGTERM cancels the run's context instead of killing the process
	// mid-flight: collectors abort at the next round trip, the pgx pool closes,
	// and the store finishes its write. cmd.Context() in every handler is this ctx.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := &cobra.Command{
		Use:           "pgbot",
		Short:         "In-database observability for PostgreSQL",
		Long:          "pgbot connects read-only to a PostgreSQL database and reports its health,\nqueries, tables, indexes and what changed since last time — pure SQL, no agent required.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.AddCommand(newInspectCmd())
	root.AddCommand(newBaselinesCmd())
	root.AddCommand(newExplainCmd())
	root.AddCommand(newAskCmd())
	root.AddCommand(newIndexesCmd())
	root.AddCommand(newQueriesCmd())
	root.AddCommand(newVacuumCmd())
	root.AddCommand(newTablesCmd())
	root.AddCommand(newTuneCmd())
	root.AddCommand(newMCPCmd())
	root.AddCommand(newConfigCmd())
	root.AddCommand(newAdviseCmd())
	root.AddCommand(newExplainFindingCmd())
	root.AddCommand(newDiffCmd())

	// enteredRun distinguishes a malformed invocation (bad flags/args/unknown
	// command — cobra fails before PersistentPreRun) from an execution failure
	// (a handler ran and returned an error). B5's --fail-on makes exit codes a
	// public interface, so the two must not share code 3.
	enteredRun := false
	root.PersistentPreRun = func(*cobra.Command, []string) { enteredRun = true }

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "pgbot: "+err.Error())
		if !enteredRun {
			os.Exit(exitUsage) // 64 — bad flags/args/command; the request was malformed
		}
		os.Exit(exitFailure) // 3 — connection/execution failure. Finding-based codes
		// (1 warn, 2 critical) are set by handlers via os.Exit directly.
	}
}
