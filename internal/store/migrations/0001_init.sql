-- 0001: the migration ledger, job runs and settings changes.
--
-- House rules for every table, in this file and the ones after it:
--   * Tables are STRICT, so SQLite enforces each column's declared type.
--   * No REAL columns. Money, prices, share counts and limits are TEXT holding
--     an exact decimal; storing them as SQLite numbers turns them into floats.
--   * Instants are TEXT in UTC as 2006-01-02T15:04:05.000000000Z (always 30
--     characters, so text order is time order). Trading dates are TEXT as
--     2006-01-02. Go writes both with store.FormatTime and store.FormatDate.

-- One row per migration applied, written in the same transaction as the
-- migration itself.
CREATE TABLE schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    applied_at TEXT    NOT NULL
        CHECK (length(applied_at) = 30 AND applied_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z')
) STRICT;

-- One row per scheduled job per trading date. The scheduler inserts the row
-- when the job starts and finishes it when the job ends. The unique key is
-- what makes a job run at most once per trading date: a catch-up or a restart
-- finds the existing row instead of running the job again, and a retry of a
-- failed run updates its row in place.
CREATE TABLE job_runs (
    id           INTEGER PRIMARY KEY,
    job_name     TEXT    NOT NULL CHECK (job_name <> ''),
    trading_date TEXT    NOT NULL
        CHECK (length(trading_date) = 10 AND trading_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    -- Hash of everything the run read (prices, holdings, the strategy file,
    -- the trading date), so the same inputs can be shown to give the same
    -- result.
    input_hash   TEXT    NOT NULL CHECK (input_hash <> ''),
    started_at   TEXT    NOT NULL
        CHECK (length(started_at) = 30 AND started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    finished_at  TEXT
        CHECK (finished_at IS NULL OR (length(finished_at) = 30 AND finished_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z')),
    -- running until it ends; then succeeded, failed, or skipped (for example
    -- when the exchange is closed that day).
    outcome      TEXT    NOT NULL DEFAULT 'running'
        CHECK (outcome IN ('running', 'succeeded', 'failed', 'skipped')),
    -- What went wrong, or a short summary of what the run did. Never a secret.
    detail       TEXT    NOT NULL DEFAULT '',
    UNIQUE (job_name, trading_date),
    -- A run has a finish time exactly when it has finished.
    CHECK ((outcome = 'running') = (finished_at IS NULL)),
    CHECK (finished_at IS NULL OR finished_at >= started_at)
) STRICT;

CREATE INDEX job_runs_trading_date ON job_runs (trading_date);

-- One row per change to a setting or limit, kept forever. Tightening a limit
-- from the phone app writes a row with source 'phone'; a change made by
-- editing the files on the NAS is found at start-up and written with source
-- 'nas'. Values are the setting's text form (a limit is its exact decimal);
-- old_value is NULL when a setting first appears and new_value is NULL when
-- one is removed.
CREATE TABLE settings_changes (
    id         INTEGER PRIMARY KEY,
    changed_at TEXT    NOT NULL
        CHECK (length(changed_at) = 30 AND changed_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    -- The setting's dotted path, such as limits.max_daily_loss.
    setting    TEXT    NOT NULL CHECK (setting <> ''),
    old_value  TEXT,
    new_value  TEXT,
    source     TEXT    NOT NULL CHECK (source IN ('phone', 'nas')),
    CHECK (old_value IS NOT new_value)
) STRICT;

CREATE INDEX settings_changes_setting ON settings_changes (setting, changed_at);

-- The history is append-only: a recorded change is never edited or removed.
CREATE TRIGGER settings_changes_no_update BEFORE UPDATE ON settings_changes
BEGIN
    SELECT RAISE(ABORT, 'settings_changes is append-only');
END;

CREATE TRIGGER settings_changes_no_delete BEFORE DELETE ON settings_changes
BEGIN
    SELECT RAISE(ABORT, 'settings_changes is append-only');
END;
