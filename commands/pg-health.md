---
description: Read-only PostgreSQL health check with pgbot, then a prioritized diagnosis
argument-hint: "[connection-string]"
---

Run a read-only pgbot health inspection and diagnose the results.

Use the pgbot `inspect` MCP tool if it's connected (pass `connection_string` = `$ARGUMENTS` when given); otherwise run `pgbot inspect "$ARGUMENTS"` in the shell, falling back to `$DATABASE_URL` when no argument is given. Add `--full` if the user wants the subsystem detail.

Then give a prioritized, worst-first diagnosis following the `postgres-diagnostics` skill:

- The findings are facts — don't invent numbers, tables, or query ids.
- Carry every caveat into the recommendation. Especially: "unused index" scan counts are per-node, so if a replica is connected an index that looks unused may still serve reads there.
- Never recommend running a query to time it (no `EXPLAIN ANALYZE`).
- Prioritize by impact — risk (time-to-incident) first, then the biggest latency/storage win.
- Hand over exact statements for the user to run themselves; pgbot never writes.
