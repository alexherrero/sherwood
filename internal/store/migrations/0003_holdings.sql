-- 0003: holdings and cash.
--
-- The house rules in 0001 apply: STRICT tables, no REAL columns, amounts and
-- share counts as exact decimal text, times as 30-character UTC text,
-- trading dates as 2006-01-02.
--
-- Both are kept as append-only snapshots. Each write of the holdings records
-- the whole set as it stood at one moment (from the broker import, later from
-- fills or a reviewed reconciliation), and the newest snapshot is the current
-- one. Nothing is ever updated or deleted, so reconciliation can always see
-- what sherwood believed before and after any change. The current_holdings
-- and current_cash views below are the one definition of "current".

-- One row per write of the whole set of holdings.
CREATE TABLE holding_snapshots (
    id            INTEGER PRIMARY KEY,
    -- import: read from the broker by the read-only import.
    -- fills: derived from sherwood's own fills (a later part).
    -- reconcile: the broker's figures taken as the record after you reviewed
    --   a reconciliation difference (a later part).
    source        TEXT    NOT NULL CHECK (source IN ('import', 'fills', 'reconcile')),
    -- When the holdings were as recorded: the broker's reading time for an
    -- import, the fill time for fills.
    as_of         TEXT    NOT NULL
        CHECK (length(as_of) = 30 AND as_of GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    -- How many holdings the snapshot has. Its rows can never outnumber it.
    holding_count INTEGER NOT NULL CHECK (holding_count >= 0),
    -- 1 exactly when the snapshot has more than the design's limit of 20
    -- holdings. The import records such a set rather than refusing it, and
    -- the views show the flag.
    over_limit    INTEGER NOT NULL CHECK (over_limit IN (0, 1)),
    CHECK (over_limit = (holding_count > 20))
) STRICT;

-- One row per stock in a snapshot.
CREATE TABLE snapshot_holdings (
    snapshot_id  INTEGER NOT NULL REFERENCES holding_snapshots (id),
    symbol       TEXT    NOT NULL
        CHECK (length(symbol) BETWEEN 1 AND 10 AND symbol GLOB '[A-Z]*' AND symbol NOT GLOB '*[^A-Z0-9.-]*'),
    -- Shares held: more than zero, at most six decimal places.
    quantity     TEXT    NOT NULL
        CHECK (length(quantity) BETWEEN 1 AND 64 AND quantity GLOB '[0-9]*' AND quantity NOT GLOB '*[^0-9.]*'
               AND quantity NOT GLOB '*.*.*' AND quantity NOT GLOB '*.' AND quantity GLOB '*[1-9]*'
               AND quantity NOT GLOB '*.[0-9][0-9][0-9][0-9][0-9][0-9][0-9]*'),
    -- Average cost per share, exactly as the source gave it (it may carry
    -- more than two places). Never negative.
    average_cost TEXT    NOT NULL
        CHECK (length(average_cost) BETWEEN 1 AND 64 AND average_cost GLOB '[0-9]*' AND average_cost NOT GLOB '*[^0-9.]*'
               AND average_cost NOT GLOB '*.*.*' AND average_cost NOT GLOB '*.'),
    -- The trading date the holding was opened; trailing stops measure from it.
    opened_on    TEXT    NOT NULL
        CHECK (length(opened_on) = 10 AND opened_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    PRIMARY KEY (snapshot_id, symbol)
) STRICT;

-- One row per reading of the cash balance.
CREATE TABLE cash_snapshots (
    id     INTEGER PRIMARY KEY,
    source TEXT    NOT NULL CHECK (source IN ('import', 'fills', 'reconcile')),
    as_of  TEXT    NOT NULL
        CHECK (length(as_of) = 30 AND as_of GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    -- Cash in dollars, exactly as read. A sign is allowed so that a negative
    -- balance at the broker is recorded as it is, for reconciliation to flag.
    amount TEXT    NOT NULL
        CHECK (length(amount) BETWEEN 1 AND 64 AND (amount GLOB '[0-9]*' OR amount GLOB '-[0-9]*')
               AND substr(amount, 2) NOT GLOB '*[^0-9.]*' AND amount NOT GLOB '*.*.*' AND amount NOT GLOB '*.')
) STRICT;

-- Snapshots are recorded in time order, so the newest row is also the latest
-- reading and "current" can never go backwards.
CREATE TRIGGER holding_snapshots_in_order
BEFORE INSERT ON holding_snapshots
WHEN NEW.as_of < (SELECT max(as_of) FROM holding_snapshots)
BEGIN
    SELECT RAISE(ABORT, 'holding_snapshots must be recorded in as_of order');
END;

CREATE TRIGGER cash_snapshots_in_order
BEFORE INSERT ON cash_snapshots
WHEN NEW.as_of < (SELECT max(as_of) FROM cash_snapshots)
BEGIN
    SELECT RAISE(ABORT, 'cash_snapshots must be recorded in as_of order');
END;

-- A snapshot's rows are written only while it is the newest, and never more
-- of them than its holding_count. So a 21st holding can be written only into
-- a snapshot whose count, and therefore its over_limit flag, says so.
CREATE TRIGGER snapshot_holdings_fit
BEFORE INSERT ON snapshot_holdings
BEGIN
    SELECT RAISE(ABORT, 'snapshot_holdings may be added only to the newest snapshot')
    WHERE NEW.snapshot_id <> (SELECT max(id) FROM holding_snapshots);
    SELECT RAISE(ABORT, 'snapshot_holdings would exceed the snapshot''s holding_count')
    WHERE (SELECT count(*) FROM snapshot_holdings WHERE snapshot_id = NEW.snapshot_id)
          >= (SELECT holding_count FROM holding_snapshots WHERE id = NEW.snapshot_id);
END;

CREATE TRIGGER holding_snapshots_no_update BEFORE UPDATE ON holding_snapshots
BEGIN SELECT RAISE(ABORT, 'holding_snapshots is append-only'); END;
CREATE TRIGGER holding_snapshots_no_delete BEFORE DELETE ON holding_snapshots
BEGIN SELECT RAISE(ABORT, 'holding_snapshots is append-only'); END;
CREATE TRIGGER snapshot_holdings_no_update BEFORE UPDATE ON snapshot_holdings
BEGIN SELECT RAISE(ABORT, 'snapshot_holdings is append-only'); END;
CREATE TRIGGER snapshot_holdings_no_delete BEFORE DELETE ON snapshot_holdings
BEGIN SELECT RAISE(ABORT, 'snapshot_holdings is append-only'); END;
CREATE TRIGGER cash_snapshots_no_update BEFORE UPDATE ON cash_snapshots
BEGIN SELECT RAISE(ABORT, 'cash_snapshots is append-only'); END;
CREATE TRIGGER cash_snapshots_no_delete BEFORE DELETE ON cash_snapshots
BEGIN SELECT RAISE(ABORT, 'cash_snapshots is append-only'); END;

-- The current holdings: the rows of the newest snapshot. An empty newest
-- snapshot (nothing held) gives no rows here; read holding_snapshots to tell
-- that apart from never having imported.
CREATE VIEW current_holdings AS
SELECT s.id AS snapshot_id, s.source, s.as_of, s.over_limit,
       h.symbol, h.quantity, h.average_cost, h.opened_on
FROM holding_snapshots AS s
JOIN snapshot_holdings AS h ON h.snapshot_id = s.id
WHERE s.id = (SELECT max(id) FROM holding_snapshots);

-- The current cash: the newest reading.
CREATE VIEW current_cash AS
SELECT id, source, as_of, amount
FROM cash_snapshots
WHERE id = (SELECT max(id) FROM cash_snapshots);
