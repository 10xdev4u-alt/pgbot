---
id: autovacuum_disabled_on_table
severity: critical
critical_when: ""
dimension: risk
object: relation
scope: schema
requires: []
thresholds: []
related: [table_never_vacuumed, autovacuum_starved, txid_wraparound]
---

# autovacuum_disabled_on_table

**Severity:** critical · **Dimension:** risk · **Object identity:** `schema.relation` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

At least one table carries `autovacuum_enabled = false` in its per-table
reloptions (`pg_class.reloptions`; the collector surfaces it as
`t.AutovacuumDisabled`). This is a **relation-level** switch, independent of the
global `autovacuum` GUC — autovacuum can be on for the whole cluster and still be
turned off for this one table. pgbot reports it with confidence **1.0**: it is not
inferred from behaviour, it is read straight from the catalog. There is no size
floor — any table with the reloption set is flagged.

## Why it matters

This is almost always a **"temporarily disabled and forgotten"** table: autovacuum
gets switched off during a bulk load, a migration, or a one-off backfill to keep it
from competing for I/O, and then nobody turns it back on. From that moment the table
is invisible to every global setting and dashboard. Dead tuples accumulate with no
one reclaiming them (bloat grows unbounded), and — crucially — routine freezing
stops, so transaction-id age climbs toward wraparound. The one thing that still runs
is **emergency anti-wraparound autovacuum**: Postgres overrides `autovacuum_enabled
= false` once the table's age crosses `autovacuum_freeze_max_age` and forces an
aggressive, throttle-ignoring vacuum. So the failure mode isn't silent forever — it
ends in a disruptive forced vacuum on a badly bloated table, or in the write-blocking
wall of [txid_wraparound](txid_wraparound.md) if even that can't keep up.

## How to verify it yourself

```sql
-- Every table with autovacuum explicitly disabled in its reloptions:
SELECT n.nspname || '.' || c.relname AS table,
       c.reloptions,
       age(c.relfrozenxid)           AS xid_age   -- how close to wraparound
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p', 'm', 't')
  AND c.reloptions @> ARRAY['autovacuum_enabled=false']
ORDER BY age(c.relfrozenxid) DESC;
```

Cross-check that the table really isn't being maintained — `last_autovacuum` will
be null or stale, and dead tuples will be climbing:

```sql
SELECT relname, n_dead_tup, last_vacuum, last_autovacuum
FROM pg_stat_user_tables
WHERE relname = 'orders';
```

## How to fix it

**Re-enable it, then vacuum to catch up:**

```sql
ALTER TABLE public.orders SET (autovacuum_enabled = true);
VACUUM (VERBOSE) public.orders;   -- pay down the backlog the outage created
```

If age is already high, the catch-up VACUUM may run long — that is the cost of the
disabled window, and it is far cheaper than letting an emergency anti-wraparound
vacuum do it under duress. To clear the reloption entirely (returning the table to
the cluster defaults) rather than pinning it to `true`:

```sql
ALTER TABLE public.orders RESET (autovacuum_enabled);
```

If autovacuum was disabled to control load during a *recurring* batch job, do not
leave it off — instead tune the table (`autovacuum_vacuum_cost_delay`,
`autovacuum_vacuum_cost_limit`) so autovacuum coexists with the job, or schedule an
explicit `VACUUM` after each run.

## When to ignore it

Rarely, and never for long. A legitimate case is a table you vacuum yourself on a
strict external schedule (a cron `VACUUM`, or a partitioned ingest table that is
`VACUUM`ed once at the end of loading and then dropped) — where autovacuum is off
*by design* and the freezing is handled. Even then, a suppressed `critical` still
renders in the report and only drops out of the exit code. Scope it to the specific
table; omitting `object` mutes this critical for every table, which is exactly how
the next forgotten one hides:

```toml
[[ignore]]
finding = "autovacuum_disabled_on_table"
object  = "public.orders"
reason  = "vacuumed by the nightly maintenance job; freezing handled there (OPS-1234)"
expires = "2027-01-01"
```

## What pgbot cannot see

- It reads the **reloption**, not the **intent**. It cannot tell a deliberate
  "we vacuum this ourselves" from a migration that forgot to flip the switch back —
  only that the switch is off.
- It cannot see *who* disabled it or *when*, nor whether an external job is in fact
  keeping the table maintained. Confirm that yourself before ignoring.
- It cannot predict how close the table is to triggering an emergency
  anti-wraparound vacuum; check `age(relfrozenxid)` against `autovacuum_freeze_max_age`
  (see the verification query).

## Related

- [table_never_vacuumed](table_never_vacuumed.md) — a table that has *never* been
  vacuumed; disabling autovacuum is one way a table ends up there.
- [autovacuum_starved](autovacuum_starved.md) — where autovacuum is enabled but
  can't keep up; disabling it is the extreme version of the same outcome.
- [txid_wraparound](txid_wraparound.md) — the hard write-stop this finding is on the
  road to; a disabled table's `relfrozenxid` age is a leading contributor.
