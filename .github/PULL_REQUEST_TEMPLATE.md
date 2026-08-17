## What and why

<!-- What does this change, and why? -->

## Checklist

- [ ] `scripts/gate.sh` passes (builds HEAD, not just the working tree)
- [ ] New SQL is read-only; no `EXPLAIN ANALYZE`; findings stay deterministic (computed in Go)
- [ ] No PII enters a `model.Context` / `--json` / the store
- [ ] `--json` change is additive, or `model.SchemaVersion` bumped + schema regenerated (`go run ./tools/schemagen`)
- [ ] A new finding has a `docs/findings/<id>.md` page + catalog entry
