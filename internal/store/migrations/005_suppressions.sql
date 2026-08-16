-- Suppression-rule usage, for dead-rule detection (B2-3). One row per active
-- [[ignore]] rule per target: when it last actually matched a finding, and how
-- many consecutive runs it has matched nothing. A rule that stops matching (the
-- index was dropped, the setting fixed, the queryid changed after a major
-- upgrade) is surfaced so the ignore list doesn't become a second, invisible
-- config nobody audits.
CREATE TABLE IF NOT EXISTS suppression_rules (
    fingerprint     TEXT    NOT NULL,
    rule            TEXT    NOT NULL,   -- IgnoreRule.String(), the rule identity
    last_matched_at INTEGER,            -- unix seconds, UTC; NULL if never matched
    misses          INTEGER NOT NULL DEFAULT 0, -- consecutive runs matching nothing
    PRIMARY KEY (fingerprint, rule)
);
