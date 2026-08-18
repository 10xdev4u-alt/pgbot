---
id: pg_stat_statements_missing
severity: info
critical_when: ""
dimension: cost
object: cluster
scope: infra
requires: []
thresholds: []
related: [pgss_entries_evicted]
---

# pg_stat_statements_missing

**Severity:** info · **Dimension:** cost · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

The `pg_stat_statements` extension is not available. pgbot marks its queries section
disabled when the view can't be read (`c.Queries == nil || !c.Queries.Enabled`) and emits
this `info` finding. Either the extension isn't created in this database, or the module
isn't in `shared_preload_libraries` so no query statistics are being collected at all.

## Why it matters

`pg_stat_statements` is the single most useful Postgres observability extension: it's the
only built-in source of per-query execution counts, total and mean time, rows, and buffer
usage aggregated by normalized statement. Without it, an entire class of pgbot's analysis is
blind — there is no top-queries list, no [query_slowdown](query_slowdown.md) baseline diff,
no way to point at *which* statement is expensive. You're left reasoning about the cluster
in aggregate instead of per query.

## How to verify it yourself

Check whether the extension is installed and whether the shared library is loaded:

```sql
-- READ-ONLY. Is the extension present, and is the module preloaded?
SELECT (SELECT count(*) FROM pg_extension
        WHERE extname = 'pg_stat_statements')            AS extension_installed,
       current_setting('shared_preload_libraries')       AS preload_libraries,
       (SELECT count(*) FROM pg_available_extensions
        WHERE name = 'pg_stat_statements')               AS available_to_install;
```

If `extension_installed` is 0 but `available_to_install` is 1, you just need to
`CREATE EXTENSION`. If `preload_libraries` doesn't mention `pg_stat_statements`, the module
isn't loaded and a restart is required first.

## How to fix it

1. **Preload the module.** Add it to `shared_preload_libraries` in `postgresql.conf` (or via
   `ALTER SYSTEM SET shared_preload_libraries = 'pg_stat_statements';`) and **restart** —
   this parameter only takes effect at startup.
2. **Create the extension** in the database(s) you want to observe:
   ```sql
   CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
   ```
3. On a **managed provider** it's usually a parameter-group toggle plus a reboot (RDS/Aurora
   ship it in `shared_preload_libraries` by default; you still run `CREATE EXTENSION`).
   Supabase, Cloud SQL, and Azure expose it similarly.

Once loaded, let it accumulate for a while before drawing conclusions — its counters start
empty.

## When to ignore it

You've made a deliberate decision not to run it — e.g. a locked-down or ephemeral instance
where the per-statement overhead or the restart isn't warranted:

```toml
[[ignore]]
finding = "pg_stat_statements_missing"
reason  = "…"
expires = "2027-01-01"
```

## What pgbot cannot see

- Without the extension, pgbot cannot see **any per-query statistics at all** — no counts,
  no timings, no top list. This finding is precisely the absence of that data; everything
  downstream of it is dark.
- It cannot tell whether the module is merely un-preloaded (a restart fixes it) versus
  intentionally excluded by policy — the verification query distinguishes the two.
- It observes the current database's catalog; the extension can be installed per-database,
  so absence here doesn't prove it's missing cluster-wide.

## Related

- [pgss_entries_evicted](pgss_entries_evicted.md) — the opposite failure once it *is*
  installed: it fills up and starts discarding entries, biasing the very data this finding
  is about enabling.
