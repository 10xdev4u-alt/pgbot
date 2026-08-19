# Changelog

All notable changes to pgbot are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project aims for
[Semantic Versioning](https://semver.org/). The `--json` contract is versioned
separately by `model.SchemaVersion` (currently 1.1.0).

## [Unreleased]

### Added
- **Index/code correlation (`pgbot indexes --correlate`, MCP `index_code_correlation`).**
  pgbot grades every unused / redundant / invalid index by how the drop can be
  *proven*, and hands an agent exactly what to search for — without ever reading
  your repository:
  - `catalog_proven` — invalid or redundant/duplicate; provable from the catalog
    alone, no code check, no stats-window caveat.
  - `needs_code_check` — a zero-scan plain btree over bare columns. pgbot emits the
    identifiers to grep in every case convention (camelCase, snake_case,
    PascalCase, CONSTANT_CASE) plus the load-bearing instruction: search *filter*
    positions only (WHERE / JOIN / ORDER BY / GROUP BY / ORM filters), never SELECT
    lists — and how to read a hit vs. a miss.
  - `inconclusive` — GIN/GiST/BRIN, expression, partial, or a cold window. These
    can serve a query shape that simply hasn't run, so they keep "do not DROP INDEX
    on this evidence" and are **never** promoted to actionable by an empty code
    search. pgbot never reads the repo and never drops anything.
- **Verdict write-back (MCP `record_index_verdict`).** An agent records what its
  repo search found (`found_in_code` / `not_found_in_code` / `inconclusive`),
  stored locally per database. On a later run the same still-unused index carries
  the prior verdict forward and notes when the zero-scan window has since grown —
  a one-off grep becomes compounding evidence. New `index_verdicts` store table
  only; no existing table changes.
- **Replica-identity indexes are never reported as unused.** A `REPLICA IDENTITY
  USING INDEX` index shows zero scans on the primary but dropping it breaks logical
  replication and UPDATE/DELETE row identity — now excluded alongside PK / unique /
  exclusion / FK-backing indexes.
- **Destructive-action guards are now structured and guaranteed (`finding.safety`).**
  Every finding whose remediation involves a destructive or irreversible action
  (DROP INDEX, VACUUM FULL, REINDEX, DROP REPLICATION SLOT, a table rewrite) now
  carries machine-actionable guards — `{id, kind: prohibition|precondition, action,
  text, verify}` — instead of leaving the warning to free-form prose a summarizing
  model could drop. They are emitted deterministically in code and guaranteed in
  `--json`, SARIF, the MCP payloads, and both terminal views. Two guards that
  previously existed **only** in docs pages are now on the finding itself: the
  wraparound "don't VACUUM FULL / don't consume XIDs" guard, and the "don't drop a
  replication slot a live standby still depends on" guard (whose remediation no
  longer nudges toward the drop before the check). `pgbot ask` / `explain` reassert
  these guards from code, after the model's text, so the model cannot omit them. A
  build-failing regression test fails CI if a destructive remediation ships without
  a guard.

### Changed
- `model.IndexStat` gains `columns`, `method`, `unique`, and `primary` (additive).
  JSON contract `SchemaVersion` → **1.2.0**; a 1.1.0 consumer still parses 1.2.0
  output unchanged.

## [0.3.3] - 2026-08-18

### Changed
- **The npm wrapper is published as `@pgbot/cli`, not `pgbot`.** npm's package-name
  similarity policy blocks the bare name `pgbot` from being created (too close to
  the existing `got`/`hubot` packages), which failed 0.3.2's publish after the six
  platform packages had already gone up. The wrapper now uses the scoped name we
  own: install with `npx @pgbot/cli inspect "$DATABASE_URL"` or
  `npm i -g @pgbot/cli`. Nothing else changes — the installed command is still
  `pgbot`, the six `@pgbot/<os>-<arch>` binary packages are unchanged, and the
  Homebrew formula, `install.sh`, Docker image, and `go install` path are
  unaffected.

## [0.3.2] - 2026-08-18

### Fixed
- Re-cut of 0.3.1 to publish the npm packages — 0.3.1's npm step failed because a
  CI publish needs a 2FA-bypass/automation token. No code changes versus 0.3.1
  (the binaries, Docker image, and signatures are identical). npm is now live:
  `npx @pgbot/cli inspect "$DATABASE_URL"`.

## [0.3.1] - 2026-08-18

### Fixed
- **pgbot no longer measures its own footprint as the database's** (from external
  PR #1 by @mishafyi, measured against a real remote PG18). Several places where
  the read path counted, timed, or reported pgbot's own sessions and session pins:
  - Settings reported pgbot's own session pins (`statement_timeout=15s`, etc.) as
    the server's non-default parameters; now reads the server's real values via a
    transaction-local unpin.
  - Connection count now counts only client backends (not autovacuum/checkpointer/
    walwriter/IO workers), and never pgbot's own pool.
  - The Aurora probe called `aurora_version()`, which errored and booked a rollback
    on every non-Aurora server each run; now detected from `pg_proc`.
  - pg_stat_statements reads no longer spill to temp files (transaction-local
    `work_mem`), so pgbot doesn't report its own `temp_bytes`.
  - The wait sampler's per-poll deadline was too short for a remote link (every
    poll timed out); a fixed budget makes the wait profile work over the internet.
  - `low_cache_hit` requires enough block traffic before grading (a thin sample was
    flipping the finding and the exit code on noise); `vacuum` grades "due?" against
    the actual autovacuum knobs and per-table reloptions; the real index count is
    reported (not the LIMIT-200 scan); idle `Client` waits aren't counted as
    "waiting"; and TPS excludes pgbot's own transactions.

### Added
- **npm distribution is live**: `npx @pgbot/cli inspect "$DATABASE_URL"`.
- Release self-checks: the published image must be anonymously pullable and the
  cosign signature must verify, both asserted after every release.

## [0.3.0] - 2026-08-17

### Added
- **Schema profile for CI (`--profile=schema`, `pgbot lint`).** Runs only the
  findings derivable from the catalog alone — invalid/redundant indexes, unindexed
  foreign keys, a narrow identity column, autovacuum disabled on a table — so it's
  safe against an empty, freshly-migrated database, where the full profile would
  fire `unused_indexes` and `stale_statistics` on everything. A schema report says
  so in its header and makes no claim about a running database's health.
- **`--fail-on-new <base.json>`.** Compare a run against a base report and act only
  on findings the change introduced — new findings, escalated severities, and new
  rows inside an existing aggregate (a fourth unindexed FK on top of three).
  Pre-existing findings are marked `preexisting: true` in `--json`, excluded from
  SARIF and the exit code. This is the migration-PR check: schema profile + base
  vs. head, only regressions fail. The GitHub Action gains `profile` and
  `base-report` inputs.
- **New finding `int4_identity_column`.** A sequence-backed `int4`/`serial` (or
  identity) column wraps at 2.1 billion — `int2` at 32767 — regardless of its
  current value, after which the next insert errors. Detected structurally, so it
  fires on the migration PR while the fix is still free, where the value-based
  `sequence_exhaustion` cannot. **Note:** this is a new finding ID, so anyone with
  a `.pgbot.toml` will see it for the first time and it will fire on serial primary
  keys immediately, some deliberately — scope an `[[ignore]]` to the bounded tables
  you've reasoned about. Its severity is not yet weighted by production table size
  (planned), so read it as "will wrap eventually", not "wraps soon".
- **npm distribution**: `npx @pgbot/cli inspect "$DATABASE_URL"` runs with no prior
  install. The prebuilt binary ships as a per-platform `optionalDependency`
  (`@pgbot/<os>-<arch>`), so it lands in the lockfile with an integrity hash,
  needs no network beyond the registry, and works with `npm ci --ignore-scripts`
  — no `postinstall` download. The wrapper passes argv, stdio, signals, and the
  exit code through verbatim, published from the release tag with npm provenance.

### Changed
- Releases now sign the checksums into a self-contained **cosign bundle**
  (`checksums.txt.cosign.bundle`), and `install.sh` verifies it with
  `cosign verify-blob --bundle` — no longer relying on the `--certificate` /
  `--signature` flags cosign v3 has deprecated. The detached `.sig`/`.pem` are kept
  this release as a fallback.

### Fixed
- The GitHub Action's default `version: latest` no longer 404s. `install.sh`
  treated `latest` as a literal release tag (`pgbot_latest_..._.tar.gz`, a 404);
  it now resolves `latest` via the releases API like an empty value, and the
  Action passes an empty version rather than the literal string. The Action also
  installs into the same `~/.local/bin` it adds to `PATH` instead of disagreeing
  with the installer's default.

## [0.2.1] - 2026-08-17

### Fixed
- **pgbot no longer counts its own connections as findings.** pgbot samples
  through a small connection pool; between short READ ONLY samples each
  connection is briefly idle in a transaction and holds an xmin. The
  pg_stat_activity queries excluded only the single querying backend, so sibling
  pool connections were intermittently counted — a flaky false positive on an
  otherwise-quiet database (`N session(s) idle in transaction` with nothing
  actually idle, a self-pinned vacuum horizon, connection-saturation slots pgbot
  was itself consuming, wait-profile noise, and pgbot listed in its own
  connection breakdown). Every pg_stat_activity query now excludes all of
  pgbot's own backend PIDs — captured when the pool warms, so the exclusion is
  unspoofable (a session can't hide by naming itself `pgbot`) and never affects a
  user service that happens to be named `pgbot`.
- Installer: `PGBOT_INSTALL_DIR` is created if it doesn't exist (a custom path
  like `~/.local/bin`), instead of falling through to an unexpected `sudo`
  prompt.

### Changed
- Installer signature verification prefers a self-contained cosign bundle
  (`checksums.txt.cosign.bundle`) when present, so it no longer depends on the
  `--certificate` / `--signature` flags cosign v3 has deprecated; it falls back
  to the detached certificate + signature when no bundle is published.

## [0.2.0] - 2026-08-17

### Added
- **Index advisor** (`pgbot advise`): missing-index suggestions, each validated
  by the planner with hypopg — nothing is built. Also the MCP `suggest_indexes`
  tool. Requires hypopg + pg_stat_statements + PostgreSQL 16+.
- **Configuration & suppression** (`.pgbot.toml`): per-object `[[ignore]]` rules
  (with expiry and dead-rule detection), `[severity]` remaps, `[thresholds]`
  overrides, and `pgbot config check` / `explain` / `init`. Suppression is always
  visible and never hides a critical or affects the exit code silently.
- **Findings catalogue**: a `docs/findings/<id>.md` page for every finding, an
  offline `pgbot explain-finding <id>`, and a by-dimension index.
- **`pgbot diff`**: compare two baseline snapshots offline, honest about the
  interval it actually used and about resets/evictions between them.
- **`pgbot inspect --all-databases`**: sweep every non-template database in the
  cluster; cluster-wide findings are reported once, not once per database.
- **Recoverability findings**: WAL archiving health, data-checksum failures,
  synchronous-replication degradation, replica lag, stale statistics, and
  autovacuum health.
- **CI-pipeline output**: `--fail-on=<severity>`, `--format=sarif` (uploads to the
  GitHub Security tab), `--format=junit`, `--format=prometheus` (node_exporter
  textfile), and a `pgrundev/pgbot` GitHub Action.
- **JSON Schema** for the `--json` contracts, published as release assets.
- **Windows** builds (amd64, arm64) and per-artifact CycloneDX SBOMs.

### Changed
- **Baseline fingerprints are now per-database within a cluster.** Previously a
  baseline was keyed on the cluster-wide `system_identifier` alone, so snapshots
  from different databases on the same server were merged into one series and
  their deltas were meaningless. The key now includes the database name.
  **On upgrade:** snapshots written by v0.1.x used the old cluster-wide key and
  will not match new per-database runs — those series effectively reset. Old
  snapshots are left in place (the `system_identifier` isn't stored in a snapshot,
  so they can't be recomputed); pgbot prints a one-time notice on the first run,
  and you can clear the stale series with `pgbot baselines prune <fingerprint>`.
- Exit codes are precise and documented: `0` clean · `1` warn · `2` critical ·
  `3` connection/execution failure · `64` usage error. Suppressed findings never
  contribute.

### Security
- **Fixed an information-disclosure defect in `pg_stat_statements` handling.**
  pg_stat_statements normalizes ordinary queries but stores *utility* statements
  (e.g. `CREATE USER … PASSWORD`, `ALTER ROLE`, `DO` blocks, `COPY … FROM PROGRAM`)
  verbatim. The `queries` collector trusted that text as already-parameterized and
  did not scrub it, so a literal secret in such a statement could appear in a
  `--json` report and, through `pgbot explain` / `ask`, be sent to an external
  model. All pg_stat_statements text is now scrubbed before it leaves the process.
  **If you ran a v0.1.x `queries`/`--json`/`explain`/`ask` and shared the output,
  treat any credential in a recent utility statement as exposed and rotate it.**
- **Fixed a dropped redaction marker in query-text scrubbing.** Dollar-quoted
  spans were replaced using regex Expand semantics, so the `$REDACTED$` marker
  parsed as an empty capture-group reference: the sensitive span was removed but
  came out blank instead of marked. Scrubbing now uses literal replacement and is
  covered by a fuzz test.
- Updated pgx to v5.9.2 (fixes a SQL-injection advisory), the Go toolchain to
  1.25.13, and golang.org/x/text to v0.39.0; `govulncheck` now runs in CI and
  reports no vulnerabilities.

[0.3.3]: https://github.com/pgrundev/pgbot/releases/tag/v0.3.3
[0.3.2]: https://github.com/pgrundev/pgbot/releases/tag/v0.3.2
[0.3.1]: https://github.com/pgrundev/pgbot/releases/tag/v0.3.1
[0.3.0]: https://github.com/pgrundev/pgbot/releases/tag/v0.3.0
[0.2.1]: https://github.com/pgrundev/pgbot/releases/tag/v0.2.1
[0.2.0]: https://github.com/pgrundev/pgbot/releases/tag/v0.2.0
