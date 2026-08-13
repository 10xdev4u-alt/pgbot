package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/pgrundev/pgbot/internal/mcp"
	"github.com/pgrundev/pgbot/internal/render"
	"github.com/spf13/cobra"
)

// newMCPCmd runs pgbot as a Model Context Protocol server over stdio, so an AI
// agent can call it as a tool. It exposes DETERMINISTIC tools only — the agent
// (the model) does the explaining over the findings pgbot returns.
func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run as an MCP server over stdio (for AI agents like Claude)",
		Long: "Speaks the Model Context Protocol on stdin/stdout. Configure it in an MCP\n" +
			"client (Claude Desktop/Code, Cursor, …) and the agent gains read-only tools:\n" +
			"  inspect         — full health findings as JSON\n" +
			"  unused_indexes  — zero-scan indexes + the replication caveat\n\n" +
			"Set $DATABASE_URL in the server's env so tools need no connection argument,\n" +
			"or pass connection_string per call. pgbot never writes to the database.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			srv := &mcp.Server{
				Name:    "pgbot",
				Version: version,
				Instructions: "pgbot gives read-only PostgreSQL health findings. Call `inspect` and " +
					"explain its findings to the user — the findings are computed deterministically; " +
					"treat them as facts and carry any caveats into your advice.",
				Tools: pgbotTools(),
			}
			fmt.Fprintln(os.Stderr, "pgbot mcp: serving on stdio (ctrl-c to stop)")
			return srv.Serve(cmd.Context(), os.Stdin, os.Stdout)
		},
	}
}

func pgbotTools() []mcp.Tool {
	dsnSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"connection_string": map[string]any{
				"type":        "string",
				"description": "postgres:// URL or libpq DSN. Optional if $DATABASE_URL is set for the server.",
			},
		},
	}
	return []mcp.Tool{
		{
			Name: "inspect",
			Description: "Run a read-only health inspection of a PostgreSQL database and return the " +
				"findings (critical/warning/note), a health score, wait-event profile, unused " +
				"indexes, and key stats as JSON. Deterministic — computed in Go, not by a model. " +
				"pgbot never writes to the database.",
			InputSchema: dsnSchema,
			Handler:     inspectTool,
		},
		{
			Name: "unused_indexes",
			Description: "List indexes with zero scans in the observed window (schema, table, name, " +
				"bytes), plus whether replication is active — because on a primary these counts are " +
				"per-node and a replica may still use an index that looks unused here. Read-only.",
			InputSchema: dsnSchema,
			Handler:     unusedIndexesTool,
		},
	}
}

// dsnFromArgs pulls connection_string from the tool arguments, falling back to
// the server's environment.
func dsnFromArgs(args json.RawMessage) (string, error) {
	var a struct {
		ConnectionString string `json:"connection_string"`
	}
	_ = json.Unmarshal(args, &a)
	dsn := firstNonEmpty(a.ConnectionString, os.Getenv("DATABASE_URL"), os.Getenv("PGBOT_DATABASE_URL"))
	if dsn == "" {
		return "", fmt.Errorf("no connection string: pass connection_string or set $DATABASE_URL for the server")
	}
	return dsn, nil
}

func inspectTool(ctx context.Context, args json.RawMessage) (string, error) {
	dsn, err := dsnFromArgs(args)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	c, _, err := gather(ctx, dsn, inspectFlags{interval: time.Second, ashHz: 10, window: 5 * time.Second})
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := render.JSON(&buf, c); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func unusedIndexesTool(ctx context.Context, args json.RawMessage) (string, error) {
	dsn, err := dsnFromArgs(args)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// No wait sampling needed for an index listing.
	c, _, err := gather(ctx, dsn, inspectFlags{interval: time.Second, ashHz: 0, noStore: true})
	if err != nil {
		return "", err
	}
	out := map[string]any{
		"replication_active": c.Replication != nil && (len(c.Replication.Replicas) > 0 || c.Replication.IsReplica),
		"cold_window":        c.Window.ColdWindow(),
		"unused":             []any{},
	}
	if c.Indexes != nil {
		out["unused"] = c.Indexes.Unused
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
