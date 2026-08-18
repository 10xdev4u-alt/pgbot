---
id: unused_indexes
severity: warn
critical_when: ""
dimension: storage
object: relation
scope: workload
requires: []
thresholds: [unused_index_min_size_mb]
related: [redundant_indexes, low_hot_update_ratio]
---

# unused_indexes

**Severity:** warn · **Dimension:** storage · **Object identity:** `schema.relation` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

One or more indexes have recorded **zero index scans** since stats began
(`pg_stat_user_indexes.idx_scan = 0`, collected into `c.Indexes.Unused`) **and** are
at least the size floor. The floor is `unusedIndexMinBytes` = **1 MiB** (`1 << 20`
bytes); below that an index isn't worth a recommendation. You can raise it with
`unused_index_min_size_mb` in config (which sets `UnusedIndexMinBytes`). Indexes
that back a primary-key or unique **constraint** are excluded upstream in the
collector, because you can't drop those independently.

pgbot **suppresses this finding entirely on a cold window** — if the stats window
is younger than 15 minutes (`ColdWindowThresholdSeconds = 900`), for example a
serverless instance that just woke or a stats reset moments ago, the scan counters
start from zero and "unused" would be meaningless (and acting on it dangerous).
When the window is merely short (under 7 days, `shortStatsWindowSec`), it still
reports but caps confidence low.

## Why it matters

A zero-scan index is pure overhead. It occupies storage, and it is updated on
**every** `INSERT` and on every `UPDATE` that changes its columns — extra WAL,
extra bloat, extra vacuum work, and it can block HOT updates on the table — while
returning nothing on the read side. On a write-heavy table this tax is paid
thousands of times a second for an index no query ever consults.

## How to verify it yourself

```sql
-- pgbot's condition: zero scans, past the 1 MiB floor, constraint indexes excluded:
SELECT s.schemaname || '.' || s.relname                       AS table,
       s.indexrelname                                          AS index,
       pg_size_pretty(pg_relation_size(s.indexrelid))          AS size,
       s.idx_scan
FROM pg_stat_user_indexes s
JOIN pg_index i ON i.indexrelid = s.indexrelid
WHERE s.idx_scan = 0
  AND NOT i.indisunique                              -- approximates the constraint exclusion
  AND pg_relation_size(s.indexrelid) >= 1024 * 1024  -- the 1 MiB floor
ORDER BY pg_relation_size(s.indexrelid) DESC;
```

Check how long these counters have been accumulating before you trust a zero —
a recent reset makes a used index look unused:

```sql
SELECT stats_reset FROM pg_stat_database WHERE datname = current_database();
```

## How to fix it

1. Confirm the index is unused across your **whole** cycle, not just this window —
   month-end reports, nightly jobs, and rare admin queries can be the only readers.
2. Save the `CREATE INDEX` DDL somewhere so you can restore it if a plan regresses.
3. Drop it without locking the table: `DROP INDEX CONCURRENTLY schema.index_name;`
   (a plain `DROP INDEX` takes an `ACCESS EXCLUSIVE` lock on the table).

If you later need it back, recreate with `CREATE INDEX CONCURRENTLY`.

## When to ignore it

The index serves a rare-but-critical path that never fell inside the stats window
(a quarterly report), or a **replica** uses it while the primary does not, or it
exists to steer the planner away from a bad plan. Scope the rule to that table:

```toml
[[ignore]]
finding = "unused_indexes"
object  = "public.orders"
reason  = "orders_region_idx backs the month-end reconciliation job, run outside the stats window"
expires = "2027-01-01"
```

Do **not** omit `object` — a bare `finding = "unused_indexes"` mutes the check for
*every* table, including ones you add later, so a genuinely dead index you build
next month silently accumulates cost. Scope it to the one relation and let the rest
keep reporting.

## What pgbot cannot see

- **Scan counts are per-node.** `idx_scan` reflects reads on the node pgbot
  connected to. A read replica can be leaning on an index that looks completely
  unused on the primary — dropping it there is the single most likely way pgbot
  causes an outage. Confirm on every replica first. (pgbot raises this caveat
  automatically when it detects replication.)
- Counters are **cumulative since the last stats reset**. A recent reset makes a
  heavily-used index look unused; the cold-window guard covers the first 15 minutes,
  but a reset a few hours ago still produces a misleadingly low count.
- It cannot see a query you're about to ship that would use the index, nor a
  partial/expression index whose narrow purpose only fires occasionally.

## Related

- [redundant_indexes](redundant_indexes.md) — an index that *is* scanned but only
  because a wider index would serve the same lookups; a different reason to drop.
- [low_hot_update_ratio](low_hot_update_ratio.md) — an unused index over a
  frequently-updated column also blocks HOT updates, so dropping it fixes two
  findings at once.
