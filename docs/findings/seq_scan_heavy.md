---
id: seq_scan_heavy
severity: warn
critical_when: ""
dimension: throughput
object: relation
scope: workload
requires: []
thresholds: []
related: [fk_unindexed, stale_statistics]
---

# seq_scan_heavy

**Severity:** warn · **Dimension:** throughput · **Object identity:** `schema.relation` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

A table is being read mostly by full scans. pgbot flags a table in
`pg_stat_user_tables` when **all** of these hold:

- it has at least **50,000** live rows (`seqScanTableMinRows = 50000`) — small
  tables are cheap to scan and excluded;
- there have been at least **100** scans total (`seq_scan + idx_scan >= 100`) —
  an activity floor so a barely-touched table isn't judged on noise;
- **`seq_scan > idx_scan * 2`** — more than twice as many sequential scans as index
  scans.

pgbot **suppresses this on a cold window** (stats younger than 15 minutes,
`ColdWindowThresholdSeconds = 900`), because scan counters start from zero and a
fresh window would mislabel a healthy table. Confidence is 0.6, and the worst
offender is ranked by `seq_scans × live_rows`.

## Why it matters

A sequential scan reads every page of the table. On a table of 50,000+ rows that's
CPU and IO the database repeats on **every** query that takes the scan — and it
grows linearly as the table does. A high seq-to-index ratio usually means a missing
index, or a query that *lost* its index path (a function wrapped around the column,
a type mismatch between column and parameter, or stale planner statistics).

## How to verify it yourself

```sql
-- pgbot's exact condition, worst first (seq_scans × live_rows):
SELECT schemaname || '.' || relname                              AS table,
       seq_scan,
       idx_scan,
       n_live_tup                                                AS live_rows,
       round(seq_scan::numeric / nullif(idx_scan, 0), 1)         AS seq_to_idx
FROM pg_stat_user_tables
WHERE n_live_tup >= 50000
  AND (seq_scan + coalesce(idx_scan, 0)) >= 100
  AND seq_scan > coalesce(idx_scan, 0) * 2
ORDER BY seq_scan * n_live_tup DESC
LIMIT 20;
```

## How to fix it

1. Find the query doing the scanning — `pg_stat_statements` ordered by total time,
   or `EXPLAIN (ANALYZE, BUFFERS)` on the suspect statement.
2. Diagnose **why** the planner chose a seq scan:
   - **Missing index** → add one for the hot predicate:
     `CREATE INDEX CONCURRENTLY ON schema.table (predicate_col);`
   - **Function on the column** (`WHERE lower(email) = …`) → an **expression** index.
   - **Type mismatch** (column `text`, parameter `bigint`) → align the types so the
     existing index is usable.
   - **Stale statistics** → `ANALYZE schema.table;` (see the related finding).
   - **Cost misconfiguration** → `random_page_cost` too high makes the planner shun
     index scans (see related).
3. Sometimes the scan is **correct** — the query genuinely returns most of the
   table. Then leave it; an index would only add write cost.

If the `hypopg` extension is installed, `pgbot advise` proposes candidate indexes
and reports only the ones the planner confirms it would use — nothing is built.

## When to ignore it

The table is a small lookup/dimension read fully by design, an analytics table
where full scans are expected, or a case where the full scan is genuinely the
cheapest plan (the query returns most rows). Scope the rule to that table:

```toml
[[ignore]]
finding = "seq_scan_heavy"
object  = "public.orders"
reason  = "nightly export reads the whole table; full scan is the intended plan"
expires = "2027-01-01"
```

Do **not** omit `object` — a bare `finding = "seq_scan_heavy"` mutes the check for
*every* table, including ones you add later, so a real missing-index regression on
a new table goes unseen. Scope it to the one relation you've verified.

## What pgbot cannot see

- It sees the **ratio, not the query**. It cannot tell which statement scans, or
  whether the scan reflects a missing index, a bad plan, or an intentional
  full-table read. `pg_stat_statements` and `EXPLAIN` supply that.
- Scan counters are **cumulative since the last stats reset** and **per-node** — a
  replica's scans are not counted here, and a recent reset skews the ratio.
- A seq scan on a table that fits in cache is far cheaper than the raw ratio
  suggests. pgbot weights by table size but cannot see cache residency.
- It cannot distinguish a "good" full scan (returns 90% of rows) from a "bad" one
  (returns 3 rows the long way).

## Related

- [fk_unindexed](fk_unindexed.md) — an unindexed foreign key forces a full child
  scan on every parent delete, which surfaces here as seq-scan pressure.
- [stale_statistics](stale_statistics.md) — stale planner statistics make the
  planner mis-estimate and pick a sequential scan where an index scan would win.
