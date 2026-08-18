---
id: archiving_disabled
severity: warn
critical_when: "downgraded to info on a managed provider"
dimension: risk
object: cluster
scope: infra
requires: [primary]
thresholds: []
related: [archiving_failing]
---

# archiving_disabled

**Severity:** warn (downgraded to info on a managed provider) · **Dimension:** risk · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** primary

## What pgbot observed

`archive_mode` is `off`, `wal_level` is **not** `minimal`, and no replication is in use
(no connected standbys in `pg_stat_replication` and this node is not itself a replica).
In other words: WAL is being written at a level that *could* feed archiving or
replication, but none is happening — so there is no point-in-time-recovery mechanism by
this route. pgbot reads only the settings and the replication state; it does **not** read
any `archive_command` value (there's nothing to run while `archive_mode = off`). This
surfaces as an informational finding, and on a detected managed provider it is likewise
`info` with a note that the provider's backups run outside `archive_command`.

## Why it matters

With `archive_mode = off` and no streaming replica, your recovery floor is your **last
base backup** — everything committed since then is unrecoverable if the primary is lost.
There is no continuous WAL archive to roll forward from, so PITR to "5 minutes before
the bad `DELETE`" is simply not available. For many small or ephemeral databases that's
a perfectly deliberate choice, which is why this is `info` rather than a warning — but
it's worth surfacing so the absence of PITR is a *decision*, not a surprise discovered
during an incident.

## How to verify it yourself

Run on the **primary**. This reproduces all three conditions pgbot checks:

```sql
-- The two settings:
SELECT name, setting
FROM   pg_settings
WHERE  name IN ('archive_mode', 'wal_level');   -- expect archive_mode=off, wal_level<>minimal

-- No replication in use (no standbys, and this node isn't a replica):
SELECT count(*) AS connected_standbys FROM pg_stat_replication;
SELECT pg_is_in_recovery() AS is_replica;
```

If `archive_mode = off`, `wal_level` is `replica` or `logical` (not `minimal`),
`connected_standbys = 0`, and `is_replica = false`, the finding is correct — there's no
archiving and no replication providing durability beyond the last backup.

## How to fix it

Only if you actually want PITR from this node — this finding is a prompt, not a demand:

1. **Turn on archiving.** Set `archive_mode = on` (requires a restart) and provide an
   `archive_command` (or use `archive_library`) that copies each completed segment to
   durable off-host storage:
   ```conf
   archive_mode = on
   archive_command = 'test ! -f /archive/%f && cp %p /archive/%f'   # replace with your real target
   ```
   Take a fresh base backup afterward so the archive has a starting point to roll
   forward from.
2. **Or use a purpose-built tool** — pgBackRest or WAL-G handle archiving, retention,
   and restore far more safely than a hand-rolled `cp`, and are the recommended path for
   real PITR.
3. **Or rely on a streaming replica** if continuous physical replication (not
   point-in-time rollback) is all you need — that also clears this finding, since
   replication then is "in use."

**On a managed provider, do none of the above.** RDS/Aurora, Cloud SQL, Azure,
Supabase and Neon run their own automated backups and WAL retention *outside*
`archive_command`, so `archive_mode = off` at the Postgres level is expected and
harmless — confirm your backup/retention window in the provider console instead.

## When to ignore it

Common and legitimate: a database where PITR genuinely isn't required — a dev/test box,
an ephemeral cache, a rebuildable derived store, or a managed instance whose backups are
handled by the platform. This is cluster-scoped, so the ignore covers the whole node:

```toml
[[ignore]]
finding = "archiving_disabled"
reason  = "ephemeral analytics rebuild target; recoverable from source, PITR intentionally not configured"
expires = "2027-01-01"
```

Since this is already `info` (it doesn't affect the exit code the way a warn/critical
does), an ignore here is mostly about keeping the report clean for a deliberate choice.

## What pgbot cannot see

- It cannot see a **managed provider's own backup mechanism** — snapshots and WAL
  shipping happen outside `archive_command`, so "archiving off" at the SQL level says
  nothing about whether you actually have backups there.
- It does not read the `archive_command` string (a credential vector) — and with
  `archive_mode = off` there's nothing to read anyway.
- It evaluates replication state at scan time; a replica or backup job that attaches
  intermittently may be missed, making PITR look absent when another mechanism covers
  it.
- It cannot tell whether an **external** tool (pgBackRest, WAL-G, a filesystem
  snapshot schedule) is protecting this database by some path Postgres doesn't expose.

## Related

- [archiving_failing](archiving_failing.md) — the escalated case: archiving *is*
  configured but broken, which breaks a PITR window you thought you had (a `critical`,
  unlike this deliberate-off `info`).
