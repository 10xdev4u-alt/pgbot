---
description: Review unused indexes that are safe to drop (pgbot)
argument-hint: "[connection-string]"
---

Review indexes that aren't being used and how much space they'd reclaim.

Use the pgbot `unused_indexes` MCP tool if it's connected, otherwise run `pgbot indexes "$ARGUMENTS"` (fall back to `$DATABASE_URL`).

Report the reclaimable space, but **check the replication state first**: on a primary, `pg_stat_user_indexes` scan counts are per-node, so if a replica is connected an "unused" index may still serve reads there. Only call an index safe to drop when there is no replica, or you've confirmed it's unused on every node — carry that caveat explicitly. Hand over `DROP INDEX CONCURRENTLY` statements for the user to run themselves; pgbot never writes.
