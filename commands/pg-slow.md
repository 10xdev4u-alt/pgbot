---
description: Find the queries eating your PostgreSQL database (pgbot)
argument-hint: "[connection-string]"
---

Identify the queries consuming the most database time.

Use the pgbot `top_queries` MCP tool if it's connected, otherwise run `pgbot queries "$ARGUMENTS"` (fall back to `$DATABASE_URL`). Also run `pgbot tables "$ARGUMENTS"` to spot a large table taking heavy sequential scans — that's a missing-index signal, and it often correlates with a hot query filtering the same table.

Report the top consumers by `share` (percent of total execution time). For the worst one, suggest a safe next step — a covering or partial index for the exact predicate, a rollup/materialized counter, or a query rewrite. A single query above ~30% of total time is the real hot path; index-dropping won't touch it. Never `EXPLAIN ANALYZE`; `EXPLAIN` (no ANALYZE) is fine.
