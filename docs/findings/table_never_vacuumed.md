---
id: table_never_vacuumed
severity: warn
critical_when: ""
dimension: risk
object: relation
scope: workload
requires: []
thresholds: []
related: [autovacuum_disabled_on_table, never_analyzed]
---

# table_never_vacuumed

**Severity:** warn · **Dimension:** risk · **Object identity:** `schema.relation` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

A table with at least **10,000** live tuples (`deadRatioTableMinRows`) has **no
record of ever being vacuumed** — both `last_vacuum` and `last_autovacuum` in
`pg_stat_user_tables` are null. Neither a manual `VACUUM` nor an autovacuum worker
has touched it since statistics began. The size floor keeps this off tiny lookup
tables where it wouldn't matter.

## Why it matters

Vacuum does two jobs that a never-vacuumed table has never had done: it reclaims
dead tuples (so the table doesn't bloat) and it **freezes** old row versions to hold
back transaction-id age. A table that has never been vacuumed is accumulating both
liabilities with no counter-pressure. On its own that's a warning, not a crisis —
but it's the shape of a table that will surface later as [table_bloat](table_bloat.md),
or, if freezing never happens, as [txid_wraparound](txid_wraparound.md). The most
common benign explanation is simply that the table is young or write-light and hasn't
yet crossed the autovacuum trigger; the dangerous explanation is that autovacuum is
disabled or being starved, and this is the first table to show it.

## How to verify it yourself

```sql
-- Tables above the 10k-row floor that have never been vacuumed by hand or by
-- autovacuum — exactly pgbot's condition:
SELECT schemaname || '.' || relname AS table,
       n_live_tup,
       n_dead_tup,
       last_vacuum,
       last_autovacuum
FROM pg_stat_user_tables
WHERE n_live_tup >= 10000
  AND last_vacuum     IS NULL
  AND last_autovacuum IS NULL
ORDER BY n_live_tup DESC;
```

Then check *why* — is autovacuum off globally or for this table?

```sql
SHOW autovacuum;                                   -- must be 'on'
SELECT reloptions FROM pg_class WHERE oid = 'public.orders'::regclass;
                                                   -- look for autovacuum_enabled=false
```

## How to fix it

**Vacuum it once to establish a baseline**, then make sure autovacuum will keep it
maintained going forward:

```sql
VACUUM (VERBOSE, ANALYZE) public.orders;   -- ANALYZE too: it likely has no stats either
```

- If `SHOW autovacuum` returns `off`, the whole cluster is unmaintained — that's the
  [autovacuum_off](autovacuum_off.md) setting finding, and it needs turning on.
- If the table carries `autovacuum_enabled=false`, re-enable it (see
  [autovacuum_disabled_on_table](autovacuum_disabled_on_table.md)).
- If neither is true, the table simply hasn't crossed its trigger yet. If it's an
  append-mostly table that rarely updates or deletes, it may legitimately not need
  vacuuming for dead tuples — but it *does* still need periodic freezing, which
  autovacuum handles automatically once enabled.

## When to ignore it

The table is new and hasn't accumulated enough churn to trip the autovacuum trigger,
or it's append-only and genuinely produces no dead tuples — and you've confirmed
autovacuum is enabled so freezing will still happen when age demands it. Scope the
rule to that table; a bare `finding = "table_never_vacuumed"` mutes it for every
table, hiding the next one whose null timestamps mean autovacuum is actually broken:

```toml
[[ignore]]
finding = "table_never_vacuumed"
object  = "public.orders"
reason  = "new append-only table; autovacuum enabled, freezing will run when needed"
expires = "2027-01-01"
```

## What pgbot cannot see

- `last_vacuum` and `last_autovacuum` are reset by a **statistics reset**
  (`pg_stat_reset()`), which makes a long-maintained table look never-vacuumed. A
  recent reset is the most common false positive.
- It sees *that* no vacuum is recorded, not *why*. Disabled globally, disabled
  per-table, starved for workers, or simply young — all present the same two nulls.
- It cannot distinguish an append-only table that safely never needs dead-tuple
  reclamation from one that is quietly bloating; both can sit at null until the
  first vacuum runs.

## Related

- [autovacuum_disabled_on_table](autovacuum_disabled_on_table.md) — a disabled
  reloption is one direct way a table ends up never vacuumed; check it first.
- [never_analyzed](never_analyzed.md) — the statistics counterpart; a table that has
  never been vacuumed has very often never been analyzed either.
