-- Agent code-search verdicts for indexes (index/code correlation). A verdict is
-- an agent's answer to "is this index's column referenced in query filters in the
-- repo?", recorded against a database + index so that on later runs a still-unused
-- index over a longer window carries strengthening evidence rather than starting
-- from scratch. New table only — nothing existing is touched. Idempotent, so it
-- re-runs harmlessly on every store open.
CREATE TABLE IF NOT EXISTS index_verdicts (
    fingerprint       TEXT    NOT NULL,
    index_id          TEXT    NOT NULL, -- schema.index
    verdict           TEXT    NOT NULL, -- not_found_in_code | found_in_code | inconclusive
    source            TEXT,             -- e.g. agent_repo_search
    repo_ref          TEXT,             -- optional commit sha
    checked_at        INTEGER NOT NULL, -- unix seconds
    stats_window_days REAL,             -- window length when the verdict was recorded
    PRIMARY KEY (fingerprint, index_id)
);
