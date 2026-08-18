---
id: autovacuum_off
severity: critical
critical_when: ""
dimension: risk
object: setting
scope: infra
requires: []
thresholds: []
related: [txid_wraparound, autovacuum_disabled_on_table]
---

# autovacuum_off

**Severity:** critical · **Dimension:** risk · **Object identity:** `setting:autovacuum` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

The `autovacuum` GUC reads exactly `off` — pgbot's literal test is
`settingParam(c, "autovacuum") == "off"`, which fires this `critical`
unconditionally. It is a boolean, so there is no threshold in `[thresholds]` to tune.

## Why it matters

Autovacuum is the background process that reclaims dead tuples (from every `UPDATE`
and `DELETE`) and advances the frozen-xid horizon. With it off, dead tuples are never
cleaned up, so tables and indexes **bloat without bound** and the planner's statistics
go stale. One nuance worth knowing: even with `autovacuum = off`, Postgres still
launches *emergency* anti-wraparound autovacuums once a table crosses
`autovacuum_freeze_max_age`, so wraparound protection is not fully lost — but you have
thrown away all the routine, gentle vacuuming and left only the last-ditch emergency
path. When that path can't keep up (long transactions pinning `xmin`, a manually
cancelled worker), transaction-id or multixact age reaches the ceiling and the
database **stops accepting writes** to protect itself. Turning autovacuum off trades
steady, cheap maintenance for a cliff.

## How to verify it yourself

```sql
SHOW autovacuum;
-- confirm it, and that the counters autovacuum depends on are also on:
SELECT name, setting FROM pg_settings
WHERE name IN ('autovacuum', 'track_counts');
```

`track_counts` must also be `on` for autovacuum to function; if either is `off`,
routine vacuuming is not happening. To see the damage already accruing, check dead
tuples and the age of the oldest un-frozen transaction:

```sql
SELECT schemaname||'.'||relname AS table, n_dead_tup, last_autovacuum
FROM pg_stat_user_tables
ORDER BY n_dead_tup DESC
LIMIT 20;

SELECT max(age(datfrozenxid)) AS oldest_xid_age FROM pg_database;
```

## How to fix it

Turn it back on. `autovacuum` is reloadable (SIGHUP) — no restart:

```sql
ALTER SYSTEM SET autovacuum = on;
SELECT pg_reload_conf();
```

If autovacuum was disabled to control its I/O load, that is the wrong fix — **tune**
it instead of switching it off. Raise `autovacuum_vacuum_cost_limit` (or lower
`autovacuum_vacuum_cost_delay`) to let it work faster, or adjust the per-table
scale factors, so the work still happens but at a pace the host can absorb.

## When to ignore it

Almost never on a database that takes writes. A defensible case is a strictly
read-only replica or a load stage where you run `VACUUM` manually on a schedule and
have proven the wraparound horizon stays safe. Suppression is the clean per-object
case — scoped to this one setting, it mutes nothing else:

```toml
[[ignore]]
finding = "autovacuum_off"
object  = "setting:autovacuum"
reason  = "read-only replica; vacuuming driven from the primary, wraparound age monitored"
expires = "2027-01-01"
```

## What pgbot cannot see

- It reads the current setting, not **why** it was set, nor whether a manual `VACUUM`
  regime is compensating for the missing automation.
- It cannot see the emergency anti-wraparound workers that still run despite `off`, so
  the setting alone does not tell you how close to the wraparound cliff you actually
  are — the `age(datfrozenxid)` query above does.
- A managed provider may not expose or permit changing this GUC.

## Related

- [txid_wraparound](txid_wraparound.md) — the hard write-stop that missing vacuuming
  marches you toward; a 2.1-billion-transaction wall.
- [autovacuum_disabled_on_table](autovacuum_disabled_on_table.md) — the same hazard
  scoped to a single table via `ALTER TABLE … SET (autovacuum_enabled = false)`,
  which this cluster-wide setting doesn't cover.
