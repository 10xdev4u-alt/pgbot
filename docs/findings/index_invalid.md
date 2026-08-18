---
id: index_invalid
severity: critical
critical_when: "downgraded to warn while a CREATE INDEX CONCURRENTLY is still building"
dimension: risk
object: relation
scope: schema
requires: []
thresholds: []
related: [unused_indexes]
---

# index_invalid

**Severity:** critical · **Dimension:** risk · **Object identity:** `schema.relation` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

At least one index has `pg_index.indisvalid = false`. pgbot scans the schema for
objects where `Kind == "index" && Invalid` and reports the count — there is no
numeric threshold, this is a boolean gauge that trips on a single invalid index.

An invalid index is the leftover of a `CREATE INDEX CONCURRENTLY` that failed
partway (a deadlock, a `statement_timeout`, a cancelled session, or a unique
violation discovered during the second pass). The severity is normally
`critical` (impact 85), but pgbot **downgrades it to `warn` and halves confidence
to 0.5** when it sees a live build in `pg_stat_progress_create_index`
(`createIndexInProgress`): an index that is invalid *because it is still building*
is normal, not a failure, so pgbot caveats it rather than telling you to drop an
index that is about to become valid.

## Why it matters

The planner never uses an invalid index to serve a read, but Postgres still
maintains it on **every** `INSERT`, `UPDATE`, and `DELETE` — so you pay the full
write and WAL cost for zero read benefit. Worse, if you believed that index
existed to support a query, that query has silently been running unindexed since
the build failed, and you won't discover it from the index list alone.

## How to verify it yourself

```sql
-- Every invalid index, largest first (this is pgbot's exact condition):
SELECT n.nspname || '.' || c.relname            AS index,
       i.indrelid::regclass                     AS table,
       pg_size_pretty(pg_relation_size(c.oid))  AS size
FROM pg_index i
JOIN pg_class c     ON c.oid = i.indexrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE NOT i.indisvalid
ORDER BY pg_relation_size(c.oid) DESC;
```

Before you act, confirm no build is currently running — a running
`CREATE INDEX CONCURRENTLY` produces an invalid index that is *supposed* to be
invalid:

```sql
SELECT relid::regclass AS table, index_relid::regclass AS index, phase,
       blocks_done, blocks_total
FROM pg_stat_progress_create_index;
```

## How to fix it

If **no build is running**, the index is dead and must be rebuilt:

1. Drop it without locking the table: `DROP INDEX CONCURRENTLY schema.index_name;`
2. Recreate it online: `CREATE INDEX CONCURRENTLY schema.index_name ON …;`
   (a plain `CREATE INDEX` takes an `ACCESS EXCLUSIVE`-adjacent `SHARE` lock that
   blocks writes for the whole build).
3. Investigate why the first build failed — check the server log for the error.
   If it was a unique-index build that hit a duplicate, fix the data first or the
   rebuild fails the same way.

`REINDEX INDEX CONCURRENTLY schema.index_name;` (PG12+) can rebuild it in place,
but for a build that never completed, drop-and-recreate is cleaner because you
restate the definition explicitly.

If a build **is** running, do nothing — wait for it to finish. That is exactly the
case pgbot downgrades to `warn` so you don't drop an index seconds before it goes
valid.

## When to ignore it

Effectively never for a genuinely failed build — an invalid index is pure cost.
The one defensible use is a known, long-running rebuild that pgbot couldn't see as
in-progress (e.g. run from a session whose progress row isn't visible), while you
track the rebuild to completion:

```toml
[[ignore]]
finding = "index_invalid"
object  = "public.orders"
reason  = "CIC rebuild of orders_created_at_idx in progress, tracked in OPS-1234"
expires = "2027-01-01"
```

Do **not** omit `object` here — a bare `finding = "index_invalid"` mutes the check
for *every* table, including ones you add later, which is how the next failed
`CREATE INDEX CONCURRENTLY` gets hidden. Scope it to the one relation you've
handled and let everything else keep tripping.

## What pgbot cannot see

- It sees `indisvalid = false`, not **why** the build failed. The cause — deadlock,
  timeout, cancellation, or a duplicate-key violation — is only in the server log.
- It can only distinguish a stalled build from a running one when
  `pg_stat_progress_create_index` has a visible row. If that view is empty because
  the build's session is gone (the common "it failed" case) or not visible to
  pgbot, it reports `critical` — which is the safe default.
- It keys off `indisvalid`. A CIC failure can also leave `indisready` in an
  awkward state; pgbot doesn't surface that flag separately.

## Related

- [unused_indexes](unused_indexes.md) — an invalid index is the ultimate unused
  index: never read, still written on every change. Once you rebuild it, the
  unused-index rule will track it if it stays cold.
