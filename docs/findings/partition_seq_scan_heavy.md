---
id: partition_seq_scan_heavy
severity: warn
critical_when: ""
dimension: latency
object: relation
scope: workload
requires: []
thresholds: []
related: [seq_scan_heavy]
---

# partition_seq_scan_heavy

**Severity:** warn · **Dimension:** latency · **Object identity:** `schema.relation` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

A **partitioned** table is being read end-to-end. Rolling the scan counts up across
all of its partitions (`c.Tables.Partitioned`), pgbot flags the parent when:

- the aggregate live rows are at least **50,000** (`seqScanTableMinRows = 50000`);
- the aggregate sequential scans are at least **1,000**
  (`partitionSeqScanMin = 1000`); and
- sequential scans **dominate** — it is *not* the healthy case of
  `idx_scan > 0 && seq_scan < idx_scan`, i.e. `seq_scan >= idx_scan` (or there are
  no index scans at all).

The reason this rule exists separately from `seq_scan_heavy` (which is
per-relation) is in the numbers: each individual partition's own seq-scan count can
look harmless, so a per-table view misses it — only the **roll-up** across
partitions reveals the parent is being scanned end to end. Like the per-table rule,
it is **suppressed on a cold window** (stats younger than 15 minutes,
`ColdWindowThresholdSeconds = 900`). Confidence is 0.7.

## Why it matters

Scanning a partitioned table end-to-end means the planner is **not pruning** — it
reads every partition instead of the one or two the query actually needs. The cost
multiplies by the partition count: a query that should touch one month's partition
instead reads all of them. It's almost always a missing index (that should exist on
every partition) or a query that omits the partition key, so partition pruning can't
kick in.

## How to verify it yourself

```sql
-- Roll partition scan counts up to the parent (single-level partitioning):
SELECT parent.oid::regclass                       AS partitioned_table,
       count(*)                                    AS partitions,
       sum(st.seq_scan)                            AS seq_scans,
       sum(coalesce(st.idx_scan, 0))               AS idx_scans,
       sum(st.n_live_tup)                          AS live_rows
FROM pg_inherits i
JOIN pg_class parent        ON parent.oid = i.inhparent
JOIN pg_stat_user_tables st ON st.relid   = i.inhrelid
GROUP BY parent.oid
HAVING sum(st.n_live_tup) >= 50000
   AND sum(st.seq_scan)   >= 1000
   AND sum(st.seq_scan)   >= sum(coalesce(st.idx_scan, 0))   -- seq dominates
ORDER BY sum(st.seq_scan) DESC;
```

Then confirm the planner isn't pruning by inspecting a representative query:

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT … FROM your_partitioned_table WHERE …;
-- Look for many partitions scanned, or the absence of "Subplans Removed: N".
```

## How to fix it

**Add the missing index** — define it on the parent so it propagates, but do it
online so the build doesn't lock writes. You cannot run `CREATE INDEX CONCURRENTLY`
directly on a partitioned parent, so use the attach pattern:

```sql
-- 1. Build the index CONCURRENTLY on each partition:
CREATE INDEX CONCURRENTLY idx_p_2026_01 ON schema.tbl_2026_01 (predicate_col);
-- … one per partition …

-- 2. Create the parent index without building (ONLY = metadata, invalid until attached):
CREATE INDEX idx_parent ON ONLY schema.partitioned_table (predicate_col);

-- 3. Attach each partition's index; when all are attached the parent index goes valid:
ALTER INDEX idx_parent ATTACH PARTITION idx_p_2026_01;
```

**Enable pruning.** Make sure queries filter on the **partition key** so the planner
can eliminate partitions. If the workload can't include the key, the design itself
may need revisiting.

## When to ignore it

The partitioned table is an analytics/warehouse target that is scanned broadly by
design, or the queries intentionally span all partitions. Scope the rule to that
table:

```toml
[[ignore]]
finding = "partition_seq_scan_heavy"
object  = "public.orders"
reason  = "reporting rollup deliberately scans all partitions nightly"
expires = "2027-01-01"
```

Do **not** omit `object` — a bare `finding = "partition_seq_scan_heavy"` mutes the
check for *every* partitioned table, including ones you add later, hiding a future
partition set that stops pruning. Scope it to the one relation.

## What pgbot cannot see

- It rolls up **counts**, not queries. It cannot tell **which** query fails to
  prune, nor whether pruning is even possible (the predicate may not reference the
  partition key at all). `EXPLAIN` is how you confirm.
- Counters are **cumulative since the last stats reset** and **per-node**; a
  replica's scans aren't included and a recent reset skews the roll-up.
- The roll-up is single-level. A deeply **sub-partitioned** hierarchy may aggregate
  differently than this parent-child view suggests.
- It can't distinguish a legitimately broad analytic scan from a missing index —
  both look like end-to-end reads.

## Related

- [seq_scan_heavy](seq_scan_heavy.md) — the same disease at the single-table level.
  This is the partitioned-parent variant that a per-relation view structurally
  cannot catch.
