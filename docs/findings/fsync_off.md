---
id: fsync_off
severity: critical
critical_when: ""
dimension: risk
object: setting
scope: infra
requires: []
thresholds: []
related: [full_page_writes_off]
---

# fsync_off

**Severity:** critical · **Dimension:** risk · **Object identity:** `setting:fsync` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

The `fsync` GUC reads exactly `off` — pgbot's literal test is
`settingParam(c, "fsync") == "off"`, which fires this `critical` unconditionally.
There is no numeric threshold: the setting is boolean, so the finding is on or off
with nothing in `[thresholds]` to tune.

## Why it matters

With `fsync` off, Postgres never asks the OS to flush WAL and data writes to durable
storage — they sit in the kernel page cache and are written back whenever the OS
feels like it. An OS crash or power loss can then leave the cluster in a state WAL
recovery **cannot repair**: not merely the loss of a few recent transactions, but a
structurally inconsistent database that may fail to start or return garbage. This is
the single most dangerous durability knob in Postgres, which is why it is `critical`.

## How to verify it yourself

```sql
SHOW fsync;
-- or, to see the compiled-in default and where the current value came from:
SELECT setting, boot_val, source FROM pg_settings WHERE name = 'fsync';
```

`boot_val` is `on`; if `setting` is `off`, someone changed it deliberately —
`source` tells you whether it came from `postgresql.conf`, `ALTER SYSTEM`, or a
command-line flag.

## How to fix it

Turn durability back on. `fsync` is reloadable (SIGHUP context) — no restart:

```sql
ALTER SYSTEM SET fsync = on;
SELECT pg_reload_conf();
```

Only ever leave `fsync = off` on a **throwaway instance you can rebuild from
scratch** — a disposable CI database, or a one-shot bulk load where you will
recreate the whole cluster afterward if it crashes. On anything whose data you care
about, `on` is the only safe value; the speed you buy with `off` is not worth a
cluster you cannot recover.

## When to ignore it

Only when the instance really is disposable — a CI or throwaway load box you rebuild
from scratch on any failure. Suppression here is the cleanest per-object case: the
block is scoped to this one setting, so muting it says nothing about any other
finding on the cluster.

```toml
[[ignore]]
finding = "fsync_off"
object  = "setting:fsync"
reason  = "ephemeral CI instance, rebuilt from scratch every run; no durable data"
expires = "2027-01-01"
```

## What pgbot cannot see

- It reads the current setting value, not **why** it was set or whether this instance
  is genuinely disposable — only you know if the data is expendable.
- It cannot see the storage stack's own guarantees. `fsync = off` is unsafe on all
  ordinary storage regardless; no cloud volume or SAN makes it safe.
- A managed provider may not expose or permit changing `fsync` at all, in which case
  the value you see is the provider's and outside your control.

## Related

- [full_page_writes_off](full_page_writes_off.md) — the other crash-durability
  foot-gun; both must be `on` for crash recovery to be trustworthy.
