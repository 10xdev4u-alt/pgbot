---
id: full_page_writes_off
severity: critical
critical_when: ""
dimension: risk
object: setting
scope: infra
requires: []
thresholds: []
related: [fsync_off]
---

# full_page_writes_off

**Severity:** critical · **Dimension:** risk · **Object identity:** `setting:full_page_writes` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

The `full_page_writes` GUC reads exactly `off` — pgbot's literal test is
`settingParam(c, "full_page_writes") == "off"`, which fires this `critical`
unconditionally. It is a boolean, so there is no threshold in `[thresholds]` to tune.

## Why it matters

Postgres pages are 8 KB, but the storage layer writes in smaller sectors, so a crash
mid-write can leave a **torn page** — half old, half new. `full_page_writes` defends
against this by logging a complete image of each page the first time it is modified
after a checkpoint, so recovery can overwrite a torn page wholesale. With it off,
recovery has only the incremental WAL change and **cannot reconstruct** a torn page —
the result is silent block corruption after any crash. It is safe to turn off *only*
on storage that guarantees atomic 8 KB page writes; almost nothing in commodity or
cloud storage does.

## How to verify it yourself

```sql
SHOW full_page_writes;
-- or, with the default and the source of the current value:
SELECT setting, boot_val, source FROM pg_settings WHERE name = 'full_page_writes';
```

`boot_val` is `on`. If `setting` is `off`, it was set intentionally — confirm with
whoever set it that the underlying storage really guarantees atomic page writes.

## How to fix it

Turn torn-page protection back on. `full_page_writes` is reloadable (SIGHUP) — no
restart:

```sql
ALTER SYSTEM SET full_page_writes = on;
SELECT pg_reload_conf();
```

Leave it `off` only on a **throwaway instance** or on storage you can *prove*
performs atomic 8 KB writes (a specific enterprise array or filesystem with that
documented guarantee). If you are not certain, the correct value is `on`; the WAL
volume it adds is the price of a database that survives a crash.

## When to ignore it

Only with a documented atomic-write storage guarantee, or on a disposable instance.
Suppression is the clean per-object case: scoped to this one setting, it mutes
nothing else on the cluster.

```toml
[[ignore]]
finding = "full_page_writes_off"
object  = "setting:full_page_writes"
reason  = "storage vendor guarantees atomic 8KB page writes; verified in <ticket>"
expires = "2027-01-01"
```

## What pgbot cannot see

- It reads the current setting, not **why** it was set — and specifically not whether
  your storage truly performs atomic 8 KB writes, which is the only thing that makes
  `off` safe. That guarantee lives below Postgres, where pgbot has no visibility.
- It cannot tell a deliberate, storage-justified `off` from an accidental one.
- A managed provider may set and lock this GUC; the value shown may be the provider's.

## Related

- [fsync_off](fsync_off.md) — the companion crash-durability knob; both must be `on`
  for crash recovery to be safe.
