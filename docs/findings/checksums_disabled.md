---
id: checksums_disabled
severity: info
critical_when: ""
dimension: risk
object: setting
scope: infra
requires: [PG12+]
thresholds: []
related: [checksum_failures]
---

# checksums_disabled

**Severity:** info · **Dimension:** risk · **Object identity:** `setting:data_checksums` (see [configuration](../configuration.md)) · **Requires:** PG12+

## What pgbot observed

The read-only `data_checksums` GUC reads exactly `off` — pgbot's literal test is
`settingParam(c, "data_checksums") == "off"`. `data_checksums` is a preset parameter
fixed at `initdb` time, so there is no threshold to tune; the finding is `info`, a note
about a missing safety layer rather than an active problem.

## Why it matters

Data checksums let Postgres detect when a page read back from disk is not what it wrote
there — bit rot, a failing drive, a filesystem that lied about a write. With them
**off**, that class of corruption is **silent**: a bad read returns wrong data with no
error, and the [checksum_failures](checksum_failures.md) counter can never trip because
there is nothing to compare against. You lose the early-warning signal entirely. Most
managed providers enable checksums by default, and `initdb` has enabled them by default
since PostgreSQL 18.

## How to verify it yourself

```sql
SELECT setting FROM pg_settings WHERE name = 'data_checksums';   -- 'on' or 'off'
```

From the OS you can also inspect (or later verify) the cluster with `pg_checksums`,
though it requires the server to be **stopped**:

```
pg_checksums --check -D $PGDATA    # cluster must be shut down
```

## How to fix it

There is **no `ALTER SYSTEM` path** — this is the one setting-scoped finding whose fix
isn't a GUC change. Checksums are chosen at cluster creation and cannot be flipped on a
running system. Two options, both offline:

- **Rebuild:** `initdb --data-checksums` on a fresh cluster (the default on PG18+) and
  reload the data via dump/restore or logical replication.
- **Convert in place:** stop the server and run `pg_checksums --enable -D $PGDATA`,
  which rewrites every page to add checksums. The cluster is unavailable for the whole
  rewrite, so plan a maintenance window.

If neither is practical right now, note it for the next rebuild or maintenance window.

## When to ignore it

A managed provider that won't let you run `initdb` or `pg_checksums` and leaves
checksums off, or a deliberately ephemeral dev instance where the small overhead isn't
worth it. This is the **cleanest per-object suppression case** in the catalogue: mute
just this one setting on the managed provider that controls it, and every other finding
is untouched.

```toml
[[ignore]]
finding = "checksums_disabled"
object  = "setting:data_checksums"
reason  = "managed provider created the cluster without checksums; can't re-initdb"
expires = "2027-01-01"
```

## What pgbot cannot see

- It reads the cluster's checksum state, not whether the storage layer beneath does its
  **own** block-level integrity checking. Many cloud volumes and filesystems (e.g. ZFS)
  checksum independently, so `off` here doesn't always mean corruption is undetectable
  end-to-end.
- It cannot see the provider's internal integrity tooling or whether a replica holds a
  verified copy.

## Related

- [checksum_failures](checksum_failures.md) — the corruption signal you forfeit by
  running without checksums; with `data_checksums = off` there is no counter to trip.
