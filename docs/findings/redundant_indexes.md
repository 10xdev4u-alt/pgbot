---
id: redundant_indexes
severity: info
critical_when: ""
dimension: storage
object: relation
scope: schema
requires: []
thresholds: []
related: [unused_indexes, low_hot_update_ratio]
---

# redundant_indexes

**Severity:** info · **Dimension:** storage · **Object identity:** `schema.relation` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

One or more indexes whose leading columns are a **prefix of (or identical to)**
another index on the same table — so the wider index already answers the same
lookups. pgbot detects this **structurally** in the collector by `indkey`/`indclass`
containment (columns *and* operator classes must match) and reports them from
`c.Indexes.Redundant`. There is no numeric threshold; each redundant index is
listed as `table.name (size) — prefix of covering_index`. The same
constraint-index exclusions the unused-index rule uses apply here.

Because the check is structural, it is `info`, not `warn`: pgbot proves the wider
index *could* serve the narrower one's queries, not that it currently does.

## Why it matters

An index on `(a, b, c)` already supports lookups on `(a)` and `(a, b)`, so a
separate index on `(a)` or `(a, b)` earns no extra read benefit — but you pay its
full write cost (maintained on every relevant `INSERT`/`UPDATE`), its storage, its
WAL, and its vacuum overhead. It's a duplicate tax hiding behind a distinct name.

## How to verify it yourself

```sql
-- B-tree indexes on the same table whose columns are a strict leading prefix of
-- another index's columns — the structural redundancy pgbot reports:
WITH idx AS (
  SELECT i.indrelid::regclass                              AS tbl,
         (n.nspname || '.' || c.relname)                   AS index,
         (string_to_array(i.indkey::text, ' '))::int[]     AS cols,
         i.indisunique, i.indexprs, i.indpred
  FROM pg_index i
  JOIN pg_class c     ON c.oid = i.indexrelid
  JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE i.indisvalid
)
SELECT a.tbl,
       a.index AS redundant,
       b.index AS covered_by,
       a.cols  AS redundant_cols,
       b.cols  AS covering_cols
FROM idx a
JOIN idx b
  ON a.tbl = b.tbl
 AND a.index <> b.index
WHERE a.indexprs IS NULL AND a.indpred IS NULL        -- plain b-tree, not expr/partial
  AND b.indpred IS NULL
  AND NOT a.indisunique                                -- keep unique/constraint indexes
  AND array_length(a.cols, 1) < array_length(b.cols, 1)          -- a is strictly narrower
  AND a.cols = b.cols[1 : array_length(a.cols, 1)]               -- and a leading prefix of b
ORDER BY a.tbl;
```

This catches strict prefixes. Two indexes with the **identical** column list are
also redundant — spot those by comparing `cols` for equality and keeping the older
one. (The query deliberately excludes expression and partial indexes, which are not
structurally redundant even when their columns overlap.)

## How to fix it

1. Confirm the wider "covering" index truly serves the narrower one's queries —
   same operator classes, compatible sort order, and any `INCLUDE` columns you rely
   on. A different opclass or collation means they are **not** interchangeable.
2. Drop the narrower one online: `DROP INDEX CONCURRENTLY schema.narrow_index;`
   (a plain `DROP INDEX` locks the table).

## When to ignore it

The narrower index is deliberately kept because it is much smaller and hot on a
latency-critical equality path where its compactness matters, or the "covering"
index isn't truly equivalent (different opclass, sort direction, or a replica's
plans differ). Scope the rule to that table:

```toml
[[ignore]]
finding = "redundant_indexes"
object  = "public.orders"
reason  = "orders_customer_id_idx kept small for the hot lookup; composite is too wide to cache"
expires = "2027-01-01"
```

Do **not** omit `object` — a bare `finding = "redundant_indexes"` mutes the check
for *every* table, including ones you add later, hiding future duplicate indexes.
Scope it to the one relation you've reviewed.

## What pgbot cannot see

- The detection is **structural, not usage-based**. pgbot knows the wider index
  *can* serve the same lookups; it does not know whether that wider index is
  actually used, or whether the narrower one is faster for a specific hot query
  because it is smaller. That uncertainty is why the finding is `info`.
- Index usage is **per-node**; a replica's access paths can differ, so an index
  that looks redundant on the primary may be the one a replica prefers. pgbot
  raises this caveat when it detects replication.
- It compares columns and operator classes, but subtleties like `INCLUDE` payloads,
  collations, or `NULLS FIRST/LAST` ordering can make two "prefix-equal" indexes
  behave differently — confirm before dropping.

## Related

- [unused_indexes](unused_indexes.md) — a redundant index that is *also* never
  scanned is an even clearer drop; the two findings often name the same object.
- [low_hot_update_ratio](low_hot_update_ratio.md) — a redundant index over a
  frequently-updated column blocks HOT updates on every write; dropping it fixes
  both. This is the exact cause in that finding's worked example.
