---
id: random_page_cost_high
severity: warn
critical_when: ""
dimension: latency
object: setting
scope: infra
requires: [a managed/SSD provider]
thresholds: []
related: [seq_scan_heavy]
---

# random_page_cost_high

**Severity:** warn · **Dimension:** latency · **Object identity:** `setting:random_page_cost` (see [configuration](../configuration.md)) · **Requires:** a managed/SSD provider

## What pgbot observed

`random_page_cost` is **≥ 4** *and* pgbot detected a managed, SSD-backed provider.
The literal condition is `rpc >= 4 && cloudProvider(c)`, where `cloudProvider`
recognizes `rds`, `aurora`, `cloudsql`, `azure`, `supabase`, and `neon`. The value
**4** isn't a pgbot constant — it is Postgres's own historical default, calibrated
for a **rotational disk**. Confidence is 0.7.

pgbot only fires this on a detected managed provider precisely because it can't
otherwise be sure the underlying storage is SSD; on those platforms it always is.

## Why it matters

`random_page_cost` tells the planner how much more a random page read costs than a
sequential one (`seq_page_cost` is 1.0, so 4 means "random is 4× as expensive").
That's true for spinning disks and badly wrong for SSD/NVMe, where random reads are
nearly as cheap as sequential ones. Left at 4 on SSD storage, the planner
**over-weights the cost of index (random) access** and systematically prefers
sequential scans — skipping index scans it should use. It's a cluster-wide bias
that quietly slows a broad swath of queries.

## How to verify it yourself

```sql
-- The setting pgbot read, plus the two values it trades off against:
SELECT name, setting, source
FROM pg_settings
WHERE name IN ('random_page_cost', 'seq_page_cost', 'effective_cache_size');
```

To see the bias on a real query, compare the plan at the current value against a
lower one — session-local, so it changes nothing:

```sql
EXPLAIN SELECT … ;                       -- current plan
SET random_page_cost = 1.1;              -- this session only
EXPLAIN SELECT … ;                       -- does an index scan appear now?
RESET random_page_cost;
```

## How to fix it

1. Lower it toward SSD reality — most managed setups land at **1.1**:
   ```sql
   ALTER SYSTEM SET random_page_cost = 1.1;
   SELECT pg_reload_conf();   -- a reload, not a restart
   ```
2. Re-check the plans of your important queries afterward (`EXPLAIN`) and confirm
   index scans are now chosen where they should be.
3. While you're here, make sure `effective_cache_size` reflects the memory actually
   available for caching — it's frequently left too low on managed defaults and
   compounds the same "avoid the index" bias.

Test on a single session first (the `SET` above) before rolling it cluster-wide.

## When to ignore it

You've deliberately kept `random_page_cost` at 4 (genuinely rotational storage, or a
workload you've tuned to prefer sequential access), or provider detection is
misfiring. Scope the rule to the setting:

```toml
[[ignore]]
finding = "random_page_cost_high"
object  = "setting:random_page_cost"
reason  = "kept at 4 intentionally; workload is bulk-sequential and index scans hurt here"
expires = "2027-01-01"
```

Even though `random_page_cost` is effectively a single object, keep the `object`
line: a bare `finding = "random_page_cost_high"` over-suppresses, muting the finding
permanently — so if you later migrate providers or someone raises the value again,
the warning never returns. Scope it explicitly and let the expiry force a re-review.

## What pgbot cannot see

- **Provider detection gates the whole finding.** It fires only for the hardcoded
  managed list (`rds`, `aurora`, `cloudsql`, `azure`, `supabase`, `neon`). A
  **self-hosted** SSD/NVMe box gets *no* finding even though the same fix applies —
  pgbot can't confirm your disk is SSD, so it stays conservative rather than guess.
  Conversely, on the rare managed instance with magnetic storage, the advice would
  be wrong.
- It reads the **setting value, not query plans**. It flags the bias; it cannot
  prove any specific query is currently mis-planned because of it. `EXPLAIN`
  confirms.
- It doesn't weigh the interplay with `effective_cache_size` and
  `effective_io_concurrency`, which also shape whether an index scan wins.

## Related

- [seq_scan_heavy](seq_scan_heavy.md) — a too-high `random_page_cost` is one of the
  root causes of the sequential scans that finding reports; fixing the setting can
  clear several `seq_scan_heavy` findings at once.
