---
id: never_analyzed
severity: warn
critical_when: ""
dimension: latency
object: relation
scope: workload
requires: []
thresholds: []
related: [stale_statistics, table_never_vacuumed]
---

# never_analyzed

**Severity:** warn · **Dimension:** latency · **Object identity:** `schema.relation` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

A table with at least **10,000** live tuples (`deadRatioTableMinRows`) has **no
statistics at all** — both `last_analyze` and `last_autoanalyze` in
`pg_stat_user_tables` are null. Neither a manual `ANALYZE` nor an autoanalyze pass has
ever run on it. This is the stronger sibling of [stale_statistics](stale_statistics.md):
that finding is about stats that have drifted; this one is about stats that were never
gathered in the first place. The size floor keeps it off trivial tables.

## Why it matters

With no statistics, the planner isn't working from a stale picture — it's working from
**no picture**. It falls back to hardcoded default selectivities and a row estimate
derived from the table's physical size (on PG 14+ `reltuples` is literally `-1` until
the first analyze). On a 10,000-row-plus table that produces badly wrong estimates on
anything non-trivial: wrong join orders, sequential scans where an index would win,
nested loops over huge inputs. The queries don't fail — they're just slow in ways that
are hard to explain, because the SQL and the indexes all look correct. A single
`ANALYZE` usually transforms the plans.

## How to verify it yourself

```sql
-- Tables above the 10k-row floor that have never been analyzed by hand or by
-- autoanalyze — exactly pgbot's condition:
SELECT schemaname || '.' || relname AS table,
       n_live_tup,
       last_analyze,
       last_autoanalyze
FROM pg_stat_user_tables
WHERE n_live_tup >= 10000
  AND last_analyze     IS NULL
  AND last_autoanalyze IS NULL
ORDER BY n_live_tup DESC;
```

Confirm the planner really has nothing to go on — on PG 14+ `reltuples = -1` is the
tell-tale of a never-analyzed table:

```sql
SELECT relname, reltuples, relpages
FROM pg_class WHERE relname = 'orders';
```

## How to fix it

1. **Analyze it once:**
   ```sql
   ANALYZE (VERBOSE) public.orders;
   ```
   Cheap and non-blocking (a sample scan under a `SHARE UPDATE EXCLUSIVE` lock).
2. **Then make sure autoanalyze will keep it current** — a never-analyzed table above
   the floor usually means autovacuum/autoanalyze isn't reaching it:
   - `SHOW autovacuum;` must be `on` (it drives autoanalyze too).
   - Check the table isn't excluded: `SELECT reloptions FROM pg_class WHERE oid =
     'public.orders'::regclass;` — look for `autovacuum_enabled=false`, which disables
     autoanalyze for the table as well.
   - If the table is simply newer than the last autoanalyze cycle, the initial
     `ANALYZE` plus a healthy autovacuum will keep it maintained from here.

## When to ignore it

The table is brand new and autoanalyze simply hasn't reached its first cycle yet (a
one-time `ANALYZE` closes the gap immediately), or it's a staging/scratch table whose
queries are trivial enough that plan quality doesn't matter. Scope the rule to that
table; a bare `finding = "never_analyzed"` mutes it for every table and hides the next
one the planner is flying blind on:

```toml
[[ignore]]
finding = "never_analyzed"
object  = "public.orders"
reason  = "scratch table, trivial queries only; plan quality doesn't matter here"
expires = "2027-01-01"
```

## What pgbot cannot see

- `last_analyze` and `last_autoanalyze` are reset by a **statistics reset**
  (`pg_stat_reset()`), which makes a long-analyzed table look never-analyzed. A recent
  reset is the most common false positive.
- It cannot tell a table that will get analyzed on the next autoanalyze cycle (young,
  healthy cluster) from one autoanalyze can never reach (disabled, or a broken
  autovacuum) — both show the same two nulls.
- It cannot judge whether the missing stats actually *hurt* — a table only queried by
  primary-key lookups may plan fine even with none — only that the planner has nothing
  to work from.

## Related

- [stale_statistics](stale_statistics.md) — the milder case of the same problem:
  statistics exist but have drifted far behind the data.
- [table_never_vacuumed](table_never_vacuumed.md) — the vacuum counterpart; a table
  that has never been analyzed has very often never been vacuumed either, and the same
  broken autovacuum explains both.
