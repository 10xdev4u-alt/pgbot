---
id: mxid_wraparound
severity: warn
critical_when: "multixact age past the critical threshold"
dimension: risk
object: cluster
scope: infra
requires: []
thresholds: []
related: [txid_wraparound]
---

# mxid_wraparound

**Severity:** warn (critical when multixact age past the critical threshold) · **Dimension:** risk · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

The oldest unfrozen **multixact-id** age in the cluster —
`max(mxid_age(datminmxid))` taken across every row of `pg_database` — has reached
at least **1,000,000,000** (the `xidWraparoundWarn` constant, shared with
[txid_wraparound](txid_wraparound.md)), so the finding fires at `warn`. It
escalates to `critical` at **1,800,000,000** (`xidWraparoundCrit`). The title's
percentage is measured against the same wall, **2,147,483,647**
(`xidWraparoundWall`). None of these are overridable in `[thresholds]`.

A multixact id is a separate counter from the ordinary transaction id. Postgres
allocates one whenever a single row must record **more than one** locker at once —
`SELECT … FOR SHARE`/`FOR UPDATE` on a row several sessions hold, and the row locks
taken by foreign-key checks. Because it is a different counter with its own freeze
machinery (`autovacuum_multixact_freeze_max_age`, default **400 million**), a
workload heavy in shared row locks can drive multixact age to the wall while
ordinary `age(datfrozenxid)` still looks perfectly healthy. Reaching one billion
here means the multixact anti-wraparound vacuum — which, like the XID one, runs even
if `autovacuum` is off — has been losing or blocked.

## Why it matters

Multixacts exhaust toward the same ~2.1-billion wall as transaction ids, and hitting
it is the same cliff: at roughly 3 million remaining, Postgres refuses to assign new
multixacts and stops accepting the commands that would need them —
`multixact "members" limit exceeded` / `database is not accepting commands to avoid
wraparound data loss`. Any workload that takes shared row locks or relies on foreign
keys then fails. A subtler trap is the multixact **members** space: a single
multixact with many members consumes entries in `pg_multixact/members`, and that
area can fill *before* the id counter does under FK-heavy or many-sharers workloads —
producing the same stop for a different reason. Both are resolved by the same
freezing work, and both are easiest to fix early.

## How to verify it yourself

```sql
-- Reproduce pgbot's number: oldest datminmxid age across the cluster,
-- and how far that is toward the 2,147,483,647 wall.
SELECT max(mxid_age(datminmxid))                                 AS max_mxid_age,
       round(100.0 * max(mxid_age(datminmxid)) / 2147483647, 1)  AS pct_to_wrap
FROM pg_database;
```

Find which database carries the oldest multixact horizon:

```sql
SELECT datname, mxid_age(datminmxid) AS mxid_age
FROM pg_database
ORDER BY mxid_age DESC;
```

Then, inside that database, the tables whose `relminmxid` a `VACUUM (FREEZE)` must
advance (these are the rows carrying old multixacts — often FK parents and
hot shared-lock targets):

```sql
SELECT c.oid::regclass                    AS relation,
       mxid_age(c.relminmxid)             AS mxid_age,
       pg_size_pretty(pg_relation_size(c.oid)) AS size
FROM pg_class c
WHERE c.relkind IN ('r', 'm', 't')        -- tables, matviews, TOAST
  AND c.relminmxid <> '0'::xid            -- skip rels that never held a multixact
ORDER BY mxid_age(c.relminmxid) DESC
LIMIT 20;
```

## How to fix it

The fix is the same shape as XID wraparound — freeze the old rows, clear whatever
blocks the freeze — and the same overriding rule applies: **do nothing that stops
the vacuum already racing the wall, and nothing that consumes the remaining
headroom.**

1. **Release what pins the horizon.** A freeze cannot advance `relminmxid` past a row
   still visible to an old snapshot. Check
   [vacuum_horizon_blocked](vacuum_horizon_blocked.md) and end any long/idle
   transaction, replication slot with an old `xmin`, standby `hot_standby_feedback`,
   or abandoned prepared transaction that pins it.
2. **Let the multixact anti-wraparound worker run.** It launches on any table past
   `autovacuum_multixact_freeze_max_age` (default 400 million) regardless of the
   `autovacuum` setting. If you see `autovacuum: VACUUM … (to prevent wraparound)`,
   leave it running.
3. **Freeze the oldest tables explicitly**, worst-first from the query above:
   ```sql
   VACUUM (FREEZE, VERBOSE) schema.oldest_table;
   ```
   A `VACUUM (FREEZE)` replaces old multixacts with a plain transaction id or a
   frozen marker, advancing `datminmxid` and freeing member space at the same time.
4. **If the cluster has already stopped accepting commands**, run `VACUUM` anyway;
   modern Postgres still permits it. The documented last resort remains single-user
   mode: `postgres --single -D <datadir> <database>` then `VACUUM;`.

**Do not** turn `autovacuum` off, cancel or kill the anti-wraparound worker, restart
or fail over hoping the age clears (it is durable and inherited by replicas), or
reach for `VACUUM FULL`/`pg_repack`/dump-restore under pressure — a plain
`VACUUM (FREEZE)` is what advances the multixact horizon, and the heavier tools only
burn time you do not have.

## When to ignore it

Almost never — it is a live countdown to a write stop. The only defensible use is a
short acknowledgement while a freeze you have already started catches up, and a
suppressed `critical` still renders and only leaves the exit code. Cluster-scoped, so
**no** `object` line:

```toml
[[ignore]]
finding = "mxid_wraparound"
reason  = "VACUUM FREEZE running on oldest FK tables to advance datminmxid; OPS-2202"
expires = "2027-01-01"
```

Keep `expires` to days. If multixact age is still climbing when it lapses, the freeze
is losing — investigate, do not renew.

## What pgbot cannot see

- It reports one cluster-wide multixact age, not the specific database or table
  nearest the wall — the per-database and per-table queries above name those.
- It reads `mxid_age(datminmxid)` only. It does **not** measure the multixact
  **members** space (`pg_multixact/members`), which a FK-heavy or many-sharers
  workload can exhaust *before* the id counter — the same VACUUM FREEZE fixes it, but
  pgbot won't show that pressure separately.
- It cannot see which application is taking the shared row locks driving multixact
  consumption. The `relminmxid` per-table query points at the rows involved, not the
  sessions locking them.
- The value is a snapshot; whether it is rising or falling between runs comes from
  comparing runs, not one reading.

## Related

- [txid_wraparound](txid_wraparound.md) — the parallel countdown on the ordinary
  transaction-id counter; same 2.1-billion wall and same freeze remedy, but a
  separate counter that can be healthy while this one is not.
