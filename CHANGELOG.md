# Changelog

All notable changes to pgbot are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project aims for
[Semantic Versioning](https://semver.org/). The `--json` contract is versioned
separately by `model.SchemaVersion` (currently 1.1.0).

## [Unreleased]

### Fixed
- The GitHub Action's default `version: latest` no longer 404s. `install.sh`
  treated `latest` as a literal release tag (`pgbot_latest_..._.tar.gz`, a 404);
  it now resolves `latest` via the releases API like an empty value, and the
  Action passes an empty version rather than the literal string. The Action also
  installs into the same `~/.local/bin` it adds to `PATH` instead of disagreeing
  with the installer's default.

### Added
- **npm distribution**: `npx pgbot inspect "$DATABASE_URL"` runs with no prior
  install. The prebuilt binary ships as a per-platform `optionalDependency`
  (`@pgbot/<os>-<arch>`), so it lands in the lockfile with an integrity hash,
  needs no network beyond the registry, and works with `npm ci --ignore-scripts`
  — no `postinstall` download. The wrapper passes argv, stdio, signals, and the
  exit code through verbatim. Published from the release tag with npm provenance;
  the README documents that npm attests provenance while `install.sh` verifies a
  cosign signature.

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

[0.2.1]: https://github.com/pgrundev/pgbot/releases/tag/v0.2.1
[0.2.0]: https://github.com/pgrundev/pgbot/releases/tag/v0.2.0
