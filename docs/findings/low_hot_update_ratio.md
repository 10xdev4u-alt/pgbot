---
id: low_hot_update_ratio
severity: warn
critical_when: ""
dimension: throughput
object: relation
scope: workload
requires: [track_counts (default on)]
thresholds: []
related: [table_bloat, unused_indexes]
---

# low_hot_update_ratio

**Severity:** warn · **Dimension:** throughput · **Object identity:** `schema.relation` (see [configuration](../configuration.md)) · **Requires:** `track_counts` (on by default)

## What pgbot observed

On a table with enough write volume to judge — at least **10,000** updates
(`hotUpdateMinVolume`) — fewer than **50%** (`hotUpdateRatioWarn`) of those updates
were **HOT**. The ratio is `n_tup_hot_upd / n_tup_upd` from `pg_stat_user_tables`.

## Why it matters

A **HOT** (Heap-Only Tuple) update is the cheap path: when an update changes no
indexed column *and* the new row version fits on the same page, Postgres links the
new version to the old one and **touches no index at all**. When an update can't be
HOT, the opposite happens — it inserts a new index entry into *every* index on the
table. That means more WAL, faster index bloat, and more work for vacuum, on every
single update. A low ratio on a hot table is one of the most expensive silent taxes
in Postgres.

## Worked example — the `issues` table (production error tracker)

This finding's best case study is real. A production error tracker had an `issues`
table with a `last_seen_at` timestamp, bumped on **every** occurrence of an error —
the single hottest write on the database. Its HOT ratio was **0%**: not one update
took the cheap path.

The cause wasn't fillfactor. `last_seen_at` was covered by **three overlapping
indexes** (it had accreted a plain index, a composite, and a partial over the years).
Because an update that changes an **indexed** column can never be HOT, every "seen
again" write — millions a day — rewrote three index trees instead of zero. The fix
was not to touch fillfactor at all: it was to drop the two redundant `last_seen_at`
indexes (pgbot's `redundant_indexes` finding named them), after which the column was
no longer indexed on the write path and HOT updates became possible again.

The lesson the number encodes: **a 0% HOT ratio on a high-update table usually means
an indexed column is being updated**, not that the table needs a fillfactor change.

## How to verify it yourself

```sql
-- The ratio pgbot computes, worst first:
SELECT schemaname || '.' || relname                                   AS table,
       n_tup_upd,
       n_tup_hot_upd,
       round(100.0 * n_tup_hot_upd / nullif(n_tup_upd, 0), 1)         AS hot_pct
FROM pg_stat_user_tables
WHERE n_tup_upd > 10000
ORDER BY hot_pct ASC NULLS LAST
LIMIT 20;
```

Then find which indexes on **that table** cover a frequently-updated column (the HOT
blockers). Scope it to the table — otherwise you get indexes on every table that
happens to have a column of the same name. Replace the table and column with yours:

```sql
SELECT i.indexrelid::regclass AS index, am.amname AS type
FROM pg_index i
JOIN pg_class ic     ON ic.oid = i.indexrelid
JOIN pg_am am        ON am.oid = ic.relam
JOIN pg_attribute a  ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
WHERE i.indrelid = 'public.issues'::regclass
  AND a.attname = 'last_seen_at';
```

The `type` column matters (see the BRIN note below): a **summarizing** index (BRIN)
on the hot column does **not** block HOT on PostgreSQL 16+, so it isn't a culprit.

On **PostgreSQL 16+** you can tell *which* cause you have directly — `n_tup_newpage_upd`
counts updates that failed HOT because the row didn't fit on the page (a fillfactor
problem), separating them from updates blocked by an indexed-column change:

```sql
SELECT n_tup_upd, n_tup_hot_upd, n_tup_newpage_upd
FROM pg_stat_user_tables WHERE relname = 'issues';
-- high n_tup_newpage_upd → page-full (lower fillfactor)
-- low  n_tup_newpage_upd but low HOT → an indexed column is changing
```

## How to fix it

Two independent levers — the example above is the first:

1. **Stop indexing the column that keeps changing.** If a frequently-updated column
   is indexed (especially by several overlapping indexes), dropping the indexes it
   doesn't need restores HOT. Drop them concurrently:
   ```sql
   DROP INDEX CONCURRENTLY public.index_issues_on_last_seen_at;
   ```
   Check `redundant_indexes` first — it often names exactly these. **Note:** on
   PostgreSQL 16+ a **BRIN** (summarizing) index on the hot column no longer blocks
   HOT, so don't drop one on that account — check the index `type` from the query
   above before assuming an index is the culprit.
2. **Leave room on the page.** If the column set is fine but pages are full
   (fillfactor 100), lower it so updates have somewhere to go:
   ```sql
   ALTER TABLE public.issues SET (fillfactor = 90);
   -- Applies to pages written from now on. To apply to existing pages you must
   -- rewrite the table (pg_repack for an online rewrite; VACUUM FULL takes a lock).
   ```

## When to ignore it

The updated-and-indexed column is genuinely required for a hot read path and the
write cost is understood and accepted, or the table is append-mostly and the ratio
is inherent. Scope the rule to **that table** — muting the finding wholesale would
blind the metric for every other table forever:

```toml
[[ignore]]
finding = "low_hot_update_ratio"
object  = "public.issues"
reason  = "last_seen_at index backs the dashboard's recency sort; write cost accepted"
expires = "2027-01-01"
```

## What pgbot cannot see

- It sees the **ratio**, not the cause. It cannot prove *which* index blocks HOT —
  only that HOT isn't happening. The index-to-column query above is how you confirm
  the culprit.
- pgbot reports the ratio, not the split between an indexed-column change and a
  page-full (fillfactor) problem. On **PostgreSQL 16+** that split *is* knowable via
  `n_tup_newpage_upd` (see *How to verify*), but pgbot does not yet read that column;
  on older versions it isn't available at all.
- Counts are cumulative since the last stats reset, so a recent reset can make a
  chronically-bad table look fine (and vice versa).

## Related

- [redundant_indexes](redundant_indexes.md) — the redundant index over a hot column
  is frequently the actual cause; dropping it fixes both findings.
- [table_bloat](table_bloat.md) — non-HOT updates are a leading source of the bloat
  it reports.
- [unused_indexes](unused_indexes.md) — an unused index over a hot column costs on
  every write *and* returns nothing; a prime drop candidate.
