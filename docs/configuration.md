# Configuration — `.pgbot.toml`

pgbot reads an optional `.pgbot.toml` to override detection thresholds, remap a
finding's severity, and **suppress** specific findings. The point of suppression
is that noise never trains people to ignore the severity column: if you can't
silence `checksums_disabled` on a provider that will never let you change it, you
learn to skip the severity column — and then you miss `checksum_failures`.

The file is meant to be **committed to your repository** and reviewed in PRs.
Because of that, pgbot **refuses to read any credential-shaped key** from it
(`dsn`, `conn`, `connection_string`, `url`, `password`, `user`) — with a hard
error. Connection strings come from `--dsn` / `PGBOT_DSN` / `DATABASE_URL`, never
this file.

## Discovery

The config is resolved by precedence — first hit wins:

1. `--config <path>` — a given path that can't be read is a hard error (never a
   silent fall-through).
2. `PGBOT_CONFIG` environment variable.
3. `.pgbot.toml`, searched from the current directory **upward** to the
   filesystem root — so it can live at a repo root and apply from any subdir.
4. `$XDG_CONFIG_HOME/pgbot/config.toml`, then `~/.config/pgbot/config.toml`.
5. No config — every default applies.

## Schema

```toml
schema = 1

[thresholds]
unused_index_min_size_mb = 100
dead_ratio_warn          = 0.30
replica_lag_warn_seconds = 30

[severity]
# remap, do not delete — the finding still exists, just at a different level
checksums_disabled = "info"

[[ignore]]
finding = "unused_indexes"
object  = "public.idx_legacy_audit_*"   # glob, optional; omitted = every object
reason  = "backs the quarterly export job"
expires = "2026-12-31"                    # optional, YYYY-MM-DD
```

Unknown finding ids, unknown threshold keys, malformed globs, and bad expiry
dates are **warnings, not errors** — a config written for a newer pgbot still
loads on an older binary. But a typo'd rule that silently doesn't apply is the
exact failure this feature prevents, so warnings appear in `--json`
(`config_warnings[]`), at the top of the report, and make `pgbot config check`
exit non-zero.

### Overridable thresholds

Only these keys are wired; any other `[thresholds]` key is a warning.

| Key | Meaning | Default |
|---|---|---|
| `unused_index_min_size_mb` | floor below which an unused index isn't flagged | 1 |
| `dead_ratio_warn` | dead-tuple ratio that trips `table_bloat` (0–1] | 0.20 |
| `replica_lag_warn_seconds` | replica replay lag that trips `replica_lag_time` | 60 |

A raised threshold is applied **before** the finding is computed, so the finding
is never produced at all — it won't appear anywhere, suppressed or not.

## Suppression semantics

Precedence, applied in order:

1. **`[thresholds]`** — change whether a finding is produced.
2. **`[severity]`** — remap a produced finding's severity (the original is kept
   in `severity_remapped` in `--json`).
3. **`[[ignore]]`** — mark a produced finding *suppressed*. The most specific
   rule wins: an exact `object` beats a glob beats an omitted `object`.

A suppressed finding is **never deleted**:

- `--json` keeps it, with `suppressed: true`, `suppression_reason`, and
  `suppression_rule` — so an agent can explain why it isn't surfacing it.
- The default report hides suppressed **non-criticals** behind a footer count;
  `--full` lists them in a dimmed section with the reason inline.
- **Suppressed findings never affect the exit code** — the whole point of an
  exit-code suppression is to stop a muted issue from failing CI forever.

**One hard exception:** a suppressed **critical** still renders in the report,
visibly marked with its reason. It still doesn't affect the exit code, but a
config must not be able to make `checksum_failures` or `archiving_failing` vanish
from the screen. Muting is for silencing *noise*, not for hiding *danger*.

## Object identity (the suppression contract)

`[[ignore]]` keys on `(finding, object)`. Every finding exposes a stable,
human-writable `object` (visible as `object` in `--json`):

| Finding class | `object` | Example |
|---|---|---|
| Relation-scoped | schema-qualified relation | `public.issues` |
| Index-scoped | schema-qualified index | `public.index_issues_on_last_seen_at` |
| Query-scoped | `q:` + queryid | `q:8213498112345` |
| Slot | `slot:` + name | `slot:wal2json_prod` |
| Subscription | `sub:` + name | `sub:orders_sync` |
| Setting-scoped | `setting:` + parameter | `setting:track_io_timing` |
| Database-scoped | `db:` + name | `db:analytics` |
| Cluster-scoped | *(empty)* | — |

**Ephemeral identifiers are never suppressible by object.** A rule keyed on a
PID, LSN, or lock address would match a different session tomorrow and silence
the wrong thing, so findings scoped to those — `idle_in_transaction`,
`blocking_chains`, `long_running_transaction`, `vacuum_horizon_blocked` — are
**cluster-scoped**: suppress them wholesale (omit `object`) or not at all.

> **queryid caveat.** `q:<queryid>` is stable across restarts and
> `pg_stat_statements` resets, but **changes across a major-version upgrade**
> (the parse-tree hash changes). After a major upgrade, a `q:`-scoped rule stops
> matching — pgbot's dead-rule detector (below) will flag it.

Summary findings that aggregate many objects into one line
(`unused_indexes`, `redundant_indexes`, `table_bloat`, `stale_statistics`, …)
are cluster-scoped: an `[[ignore]]` with no `object` mutes the whole category,
and `[thresholds]` is the per-object knob (raise the floor). Per-object findings
(`slot:`, `sub:`, `setting:`) suppress individually.

## Keeping suppressions honest

An ignore list that only grows becomes a second, invisible config. Two
mechanisms push back, both as `info` findings (they never block anything):

- **`suppression_expired`** — a rule past its `expires` date has stopped
  applying; the muted finding will resurface. Renew or delete the rule.
- **`suppression_unused`** — a rule that has matched **nothing** across the last
  several runs (tracked per database in the baseline store). The index was
  dropped, the setting was fixed, or a queryid changed after a major upgrade.

`pgbot config check` additionally warns on any rule with **no `expires`**.

## Commands

```
pgbot config check                       # validate + print resolved config; non-zero on any warning
pgbot config explain <finding> [object]  # which rule fires for this finding, and why
pgbot config init [dsn] [-o .pgbot.toml] # scaffold a commented config from a live run
pgbot inspect --ignore finding[:object]  # one-off suppression for a single run (repeatable)
```

`config init` seeds every finding from the current run as a **commented-out**
ignore rule with its `object` filled in — uncomment the ones you've reviewed.
The generated file loads cleanly under `config check` (it's inert until edited).
