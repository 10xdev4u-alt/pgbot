# Changelog

All notable changes to pgbot are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project aims for
[Semantic Versioning](https://semver.org/). The `--json` contract is versioned
separately by `model.SchemaVersion` (currently 1.1.0).

## [Unreleased]

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
- **Recoverability findings**: WAL archiving health, data-checksum failures,
  synchronous-replication degradation, replica lag, stale statistics, and
  autovacuum health.
- **JSON Schema** for the `--json` contracts, published as release assets.
- **Windows** builds (amd64, arm64) and per-artifact CycloneDX SBOMs.

### Changed
- Exit codes are precise and documented: `0` clean · `1` warn · `2` critical ·
  `3` connection/execution failure · `64` usage error. Suppressed findings never
  contribute.

### Security
- Updated pgx to v5.9.2 (fixes a SQL-injection advisory), the Go toolchain to
  1.25.13, and golang.org/x/text to v0.39.0; `govulncheck` now runs in CI and
  reports no vulnerabilities.
