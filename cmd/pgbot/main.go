// Command pgbot — in-database observability for PostgreSQL. Slice 1 ships the
// inspect one-shot (health report + --json) with a local baseline store that
// makes it temporal from the third run onward. No daemon, no external service,
// no write privilege anywhere in the path.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is set by the linker at release time (-X main.version=...).
var version = "dev"

func main() {
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
	root.AddCommand(newTuneCmd())
	root.AddCommand(newMCPCmd())

	if err := root.Execute(); err != nil {
		// Exit-code contract: connection/exec failure is 3. Command handlers that
		// need the finding-based codes (1 warn, 2 critical) call os.Exit directly.
		fmt.Fprintln(os.Stderr, "pgbot: "+err.Error())
		os.Exit(exitFailure)
	}
}
