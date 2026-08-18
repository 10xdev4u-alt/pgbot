---
id: archiving_failing
severity: critical
critical_when: "downgraded to info on a managed provider"
dimension: risk
object: cluster
scope: infra
requires: [primary, archive_mode=on]
thresholds: []
related: [archiving_stalled, replication_slot_inactive]
---

# archiving_failing

**Severity:** critical · **Dimension:** risk · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** primary, archive_mode=on

## What pgbot observed

WAL archiving is failing, judged purely from `pg_stat_archiver` — pgbot fires when
**either**:

- the most recent attempt was a failure that no success has followed:
  `last_failed_time IS NOT NULL AND (last_archived_time IS NULL OR last_failed_time > last_archived_time)`; **or**
- the cumulative `failed_count` **increased since the last run** (the
  `archiver.failed_count` baseline delta > 0), which catches intermittent failures the
  timestamp comparison would miss.

The timestamp test is baseline-free and self-clearing: an old transient failure from
months ago (where `last_archived_time` is now newer) never fires. The lifetime
`failed_count` is only ever shown as **corroborating** evidence, never the sole trigger,
so a stale cumulative counter can't raise a false `critical`. **pgbot never reads the
`archive_command` value itself** — only the archiver's success/failure timestamps and
counts. On a detected managed provider this finding is downgraded to `info` with a note
that the provider's backup mechanism sits outside `archive_command`.

## Why it matters

A failing `archive_command` breaks point-in-time recovery **silently**. Clients see no
error, transactions commit normally, nothing in the workload changes — but from the
first failed segment onward, your PITR window has a hole, and you don't find out until
you try to restore and can't. It compounds: WAL that can't be archived **can't be
recycled**, so `pg_wal` starts growing and the same failure that broke your backups
begins filling the data disk toward a hard primary outage (pgbot cross-links the
`pg_wal` size when it's already large). Failed segments are retried, so this persists
until the underlying cause is fixed — it does not clear on its own.

## How to verify it yourself

Run on the **primary**. This reproduces both of pgbot's triggers:

```sql
SELECT archived_count,
       last_archived_wal,
       last_archived_time,
       failed_count,
       last_failed_wal,
       last_failed_time,
       stats_reset,
       (last_failed_time IS NOT NULL
         AND (last_archived_time IS NULL
              OR last_failed_time > last_archived_time)) AS pgbot_failing
FROM   pg_stat_archiver;
```

`pgbot_failing = true` is the point-in-time trigger. To catch the intermittent case,
compare `failed_count` against a previous reading — a rising count with a *newer*
`last_archived_time` still means archiving is flapping. The exact `archive_command`
error is in the **server log** (`grep -i archive $PGDATA/log/*.log`) — permission
denied, no space, unreachable target.

## How to fix it

The counter tells you archiving broke; the **server log** tells you why. Fix the cause
and archiving resumes on its own, because failed segments are retried:

1. **Read the log for the actual error** — it names the failure: destination
   unreachable, credentials rejected, disk full at the target, or a wrong path.
2. **Fix the `archive_command` target**: permissions on the archive directory/bucket,
   free space at the destination, network/DNS to a remote store, expired object-store
   credentials.
3. **Confirm recovery**: watch `last_archived_time` advance past `last_failed_time` and
   `failed_count` stop rising. The backlog of retained WAL then drains as the queued
   segments archive.
4. **Check the disk you almost filled**: if `pg_wal` grew during the outage, verify it
   recycles back down once archiving catches up.

**On a managed provider (RDS/Aurora, Cloud SQL, Azure, Supabase, Neon), this is the
provider's job, not yours.** Their backups and WAL shipping run *outside*
`archive_command`, which is why pgbot downgrades this to `info` there — the finding
can't see the real backup path. Do not start setting `archive_command` yourself;
instead verify backup health in the provider console and open a support case if their
backups are actually failing.

## When to ignore it

Almost never for a self-managed primary — a broken PITR window is a live incident. The
legitimate case is a *known, already-tracked* failure you don't want failing CI while
you close it out; the suppressed `critical` still renders in the report (marked) and
only drops from the exit code. This is cluster-scoped:

```toml
[[ignore]]
finding = "archiving_failing"
reason  = "known archive-target outage 2026-08-17; backups running via pgBackRest meanwhile, tracked in OPS-1234"
expires = "2026-09-01"
```

Keep the window tight — this is not a finding to leave muted.

## What pgbot cannot see

- It sees the archiver's **timestamps and counts**, not the relation, segment, or
  reason — that detail is only in the server log.
- It deliberately **never reads the `archive_command` string**, which routinely embeds
  credentials/secrets and is a leak vector; so it cannot tell you the command is
  misconfigured, only that it's failing.
- On a managed provider it cannot see the provider's **own** backup/WAL-shipping
  mechanism — `pg_stat_archiver` may look idle or off while backups run perfectly
  elsewhere, which is exactly why it downgrades there.
- The intermittent trigger needs a **baseline**; a first run only has the point-in-time
  timestamp comparison to go on.

## Related

- [archiving_stalled](archiving_stalled.md) — the sibling case: archiving isn't
  *failing*, it's simply not happening while WAL keeps being written.
- [replication_slot_inactive](replication_slot_inactive.md) — another way `pg_wal`
  fills the disk; combined with broken archiving it's a compound disk-fill emergency.
