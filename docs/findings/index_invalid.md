---
id: index_invalid
severity: critical
critical_when: "an invalid index is still indisready (maintained on every write); failed-build debris (indisready = false, typically 0 bytes) is warn, and any state is warn while a CREATE INDEX CONCURRENTLY is still building"
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

An invalid index is the leftover of a `CREATE INDEX CONCURRENTLY` (or `REINDEX
CONCURRENTLY`) that failed partway — a deadlock, a `statement_timeout`, a
cancelled session, or a unique violation discovered during the build. **What it
costs depends on the rest of its `pg_index` row**, so pgbot classifies each
invalid index by `indisready`, `indislive`, and `pg_relation_size`, and the
finding's severity follows the worst class present:

| catalog state | what Postgres does with it | pgbot |
|---|---|---|
| `indisvalid = false`, `indisready = true` | never read, but **maintained on every write** — the build got past the populate phase (or a `REINDEX CONCURRENTLY` failed after the swap) | **critical**, impact 85 |
| `indisvalid = false`, `indisready = false` (typically 0 bytes) | **ignored by `INSERT`/`UPDATE`** — the build failed *before* the index was populated; this is failed-build debris with no write cost | **warn**, impact 45 |
| `indisvalid = false`, `indislive = false` | being dropped; ignored for all purposes | **warn** |

Every evidence line carries the state and size, e.g.
`public.orders.orders_idx — indisvalid = false, indisready = false: failed-build
debris, NOT maintained on writes (0 B)`.

Whatever the class, pgbot **downgrades to `warn` and halves confidence to 0.5**
when it sees a live build in `pg_stat_progress_create_index`
(`createIndexInProgress`): an index that is invalid *because it is still building*
is normal, not a failure, so pgbot caveats it rather than telling you to drop an
index that is about to become valid.

## Why it matters

The planner never uses an invalid index to serve a read. If the index is still
`indisready`, Postgres also maintains it on **every** `INSERT`, `UPDATE`, and
`DELETE` — so you pay the full write and WAL cost for zero read benefit; that is
the critical case. If it is *not* ready, there is no write cost — but the index
still occupies its name (a retry of the same `CREATE INDEX` fails), wastes
whatever pages were written before the failure, and, in either case, **the index
you meant to have does not exist**: if you believed it existed to support a
query, that query has silently been running unindexed since the build failed,
and you won't discover it from the index list alone.

## How to verify it yourself

```sql
-- Every invalid index with the state pgbot classifies on, largest first:
SELECT n.nspname || '.' || c.relname            AS index,
       i.indrelid::regclass                     AS table,
       i.indisready,                                -- false: NOT maintained on writes (failed-build debris)
       i.indislive,                                 -- false: being dropped
       pg_size_pretty(pg_relation_size(c.oid))  AS size
FROM pg_index i
JOIN pg_class c     ON c.oid = i.indexrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE NOT i.indisvalid
ORDER BY i.indisready DESC, pg_relation_size(c.oid) DESC;
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

Failed-build debris (`indisready = false`) needs the same drop-and-recreate — it
just isn't urgent for write throughput. Prioritise it as "the index I wanted is
missing", not as "an index is slowing my writes".

## When to ignore it

Effectively never for a genuinely failed build — a maintained invalid index is
pure write cost, and debris means an index you intended is missing. The one
defensible use is a known, long-running rebuild that pgbot couldn't see as
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
  pgbot, it grades on the catalog state alone — `critical` for a maintained
  index, `warn` for debris.
- `indisready = false` tells pgbot the index is not maintained on writes; it does
  not tell it whether the failed build wrote pages before dying. pgbot reports
  the relation size so you can see the storage side, but a nonzero size on a
  not-ready index is wasted space, not write cost.

## Related

- [unused_indexes](unused_indexes.md) — an invalid index is the ultimate unused
  index: never read, still written on every change. Once you rebuild it, the
  unused-index rule will track it if it stays cold.
