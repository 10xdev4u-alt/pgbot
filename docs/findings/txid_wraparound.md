---
id: txid_wraparound
severity: warn
critical_when: "XID age past the critical threshold (approaching the 2.1B wall)"
dimension: risk
object: cluster
scope: infra
requires: []
thresholds: []
related: [autovacuum_off, vacuum_horizon_blocked, mxid_wraparound]
---

# txid_wraparound

**Severity:** warn (critical when XID age past the critical threshold (approaching the 2.1B wall)) · **Dimension:** risk · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

The oldest unfrozen transaction-id age in the cluster —
`max(age(datfrozenxid))` taken across every row of `pg_database` — has reached at
least **1,000,000,000** transactions (the `xidWraparoundWarn` constant), so the
finding fires at `warn`. It escalates to `critical` once that age reaches
**1,800,000,000** (`xidWraparoundCrit`). The percentage in the title is measured
against the wraparound wall itself, **2,147,483,647** (`xidWraparoundWall`) — the
~2.1-billion XID that Postgres will not cross.

These thresholds are deliberately far past the point where the database *should*
have acted on its own. Autovacuum launches an anti-wraparound (aggressive) vacuum
on any table whose age exceeds `autovacuum_freeze_max_age` — **200 million** by
default — and it does so *even if `autovacuum` is turned off*. Reaching a
cluster-wide age of one billion means that safety net has been running and losing,
or has been prevented from running at all. This is one of the few conditions that
ends in the database refusing writes, so it sits at the top of the report as
`risk`. None of these numbers is overridable in `[thresholds]`.

## Why it matters

Postgres has a usable transaction-id space of only ~2.1 billion (half of the
32-bit XID counter is always "the future"). As the oldest unfrozen XID falls
further behind the current one, the cluster walks toward a hard wall: at roughly
**40 million** transactions remaining it logs `database "…" must be vacuumed within
N transactions`, and at roughly **3 million** remaining it stops assigning new
XIDs entirely and refuses commands with `database is not accepting commands to
avoid wraparound data loss`. That is a full write outage for every database in the
cluster — not gradual degradation, a cliff. The only way off the cliff is to freeze
old rows, which is exactly the work that was already falling behind, so the last
stretch is the worst possible time to start.

## How to verify it yourself

```sql
-- Reproduce pgbot's number: oldest datfrozenxid age across the cluster,
-- and how far that is toward the 2,147,483,647 wall.
SELECT max(age(datfrozenxid))                                   AS max_xid_age,
       round(100.0 * max(age(datfrozenxid)) / 2147483647, 1)    AS pct_to_wrap
FROM pg_database;
```

pgbot reports a single cluster-wide age; it does **not** tell you which database or
table is the laggard. Drill down to the offending database:

```sql
SELECT datname, age(datfrozenxid) AS xid_age
FROM pg_database
ORDER BY xid_age DESC;
```

Then, connected to that database, find the specific tables holding the horizon back
(the ones a `VACUUM (FREEZE)` must reach — include TOAST tables via `relkind`):

```sql
SELECT c.oid::regclass                    AS relation,
       age(c.relfrozenxid)                AS xid_age,
       pg_size_pretty(pg_relation_size(c.oid)) AS size
FROM pg_class c
WHERE c.relkind IN ('r', 'm', 't')        -- tables, matviews, TOAST
ORDER BY age(c.relfrozenxid) DESC
LIMIT 20;
```

## How to fix it

Freeze the old rows, and remove whatever is stopping vacuum from doing it. The one
rule that overrides everything else: **do nothing that consumes the remaining XID
headroom or that stops the vacuum already trying to run.**

1. **Find and release what pins the horizon.** Vacuum cannot freeze a row that is
   still visible to some older snapshot. Check pgbot's
   [vacuum_horizon_blocked](vacuum_horizon_blocked.md) finding, or look directly for
   a long-running or idle-in-transaction backend, a replication slot with an old
   `xmin`, `hot_standby_feedback` from a lagging replica, or an abandoned prepared
   (2PC) transaction. Ending the holder lets freezing actually advance
   `datfrozenxid`.
2. **Confirm autovacuum is allowed to run.** If `autovacuum` was set off (see
   [autovacuum_off](autovacuum_off.md)), turn it back on. Note that the *anti-wraparound*
   worker runs regardless of that setting — if you see
   `autovacuum: VACUUM … (to prevent wraparound)` in `pg_stat_activity`, **leave it
   alone**; it is the thing saving you.
3. **Freeze the oldest tables explicitly** to get ahead of the automatic worker,
   worst-first from the per-table query above:
   ```sql
   VACUUM (FREEZE, VERBOSE) schema.oldest_table;
   ```
   On PostgreSQL 14+ vacuum enters *failsafe* mode automatically past
   `vacuum_failsafe_age` (default 1.6 billion), skipping index cleanup to freeze as
   fast as possible; you do not need to intervene for that to happen.
4. **If the cluster has already stopped accepting commands**, connect and run
   `VACUUM` anyway — modern Postgres still permits vacuum in this state. If it
   truly will not accept a normal connection, the documented last resort is
   single-user mode: stop the server and run `postgres --single -D <datadir>
   <database>`, then `VACUUM;` (bare `VACUUM`, all tables) at the backend prompt.

**Do not** do any of the following — each one makes wraparound *worse*:

- Do **not** turn autovacuum off, and do **not** repeatedly cancel or
  `pg_terminate_backend` the anti-wraparound autovacuum worker. Killing the vacuum
  that is racing the wall is the single most common way a recoverable situation
  becomes an outage.
- Do **not** restart the server or fail over expecting it to clear — `datfrozenxid`
  is durable state, and a promoted replica inherits the same age.
- Do **not** reach for `pg_dump`/restore or `VACUUM FULL`/`pg_repack` as the "fix"
  under time pressure. A plain `VACUUM (FREEZE)` is what advances the horizon;
  the heavier operations consume more time and resources you do not have.

## When to ignore it

Almost never — this is a genuine countdown to a write outage. A suppression here is
only defensible as a short-lived acknowledgement while a freeze you have already
launched is catching up, and even then a suppressed `critical` still renders in the
report and only drops out of the exit code. Because the finding is cluster-scoped,
the block carries **no** `object` line:

```toml
[[ignore]]
finding = "txid_wraparound"
reason  = "aggressive VACUUM FREEZE in progress on oldest tables; tracked in OPS-2201"
expires = "2027-01-01"
```

Keep `expires` close — days, not months. If the age is still rising when it lapses,
the freeze is losing and the suppression must not be renewed.

## What pgbot cannot see

- It reports one cluster-wide maximum age, **not** which database or which table is
  nearest the wall. The per-database and per-table queries above are the only way to
  name the laggard.
- It cannot see *why* vacuum is falling behind on its own — whether an xmin holder,
  a disabled autovacuum, throttled vacuum cost limits, or a worker that keeps dying.
  [vacuum_horizon_blocked](vacuum_horizon_blocked.md) covers the xmin-holder case.
- The value is a point-in-time reading. Whether the number is climbing or falling
  between runs — the thing that tells you if you are winning — comes from comparing
  runs, not from a single snapshot.

## Related

- [vacuum_horizon_blocked](vacuum_horizon_blocked.md) — the usual reason freezing
  can't advance: an old xmin pins the horizon, so vacuum frees nothing newer.
- [autovacuum_off](autovacuum_off.md) — with the automatic freeze net disabled, age
  climbs unchecked; re-enabling it is step one.
- [mxid_wraparound](mxid_wraparound.md) — the parallel countdown for multixact ids,
  which race toward the same 2.1-billion wall on a separate counter.
