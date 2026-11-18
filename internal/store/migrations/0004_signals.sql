-- 0004: the rule runner's signals and its suggestion for each holding.
--
-- The house rules in 0001 apply: STRICT tables, no REAL columns, prices as
-- exact decimal text with two places, times as 30-character UTC text,
-- trading dates as 2006-01-02.
--
-- A rules run for one trading date writes one signal per stock whose rules
-- call for a buy, add, trim or sell, and one suggestion per holding a
-- strategy trades. Both are append-only, and both are keyed so that running
-- the same date again finds the rows already there instead of adding new
-- ones: the first run's record stands.

-- One row per signal.
CREATE TABLE signals (
    id              INTEGER PRIMARY KEY,
    -- Hex SHA-256 of the strategy, stock, side, signal kind and trading date
    -- below (internal/signals.Key). Paper-trading's orders reuse it under
    -- their own unique constraint.
    idempotency_key TEXT    NOT NULL UNIQUE
        CHECK (length(idempotency_key) = 64 AND idempotency_key NOT GLOB '*[^0-9a-f]*'),
    -- The strategy's name in the strategy file.
    strategy        TEXT    NOT NULL CHECK (strategy <> ''),
    symbol          TEXT    NOT NULL
        CHECK (length(symbol) BETWEEN 1 AND 10 AND symbol GLOB '[A-Z]*' AND symbol NOT GLOB '*[^A-Z0-9.-]*'),
    trading_date    TEXT    NOT NULL
        CHECK (length(trading_date) = 10 AND trading_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    side            TEXT    NOT NULL CHECK (side IN ('buy', 'sell')),
    -- buy: a stock not held. add: more of a holding. trim: part of a holding.
    -- sell: a rule's ordinary exit. stop_sell: a stop rule's sell, the
    -- protective sell that skips approval.
    kind            TEXT    NOT NULL CHECK (kind IN ('buy', 'add', 'trim', 'sell', 'stop_sell')),
    -- The type of the rule whose reason is kept.
    rule_type       TEXT    NOT NULL CHECK (rule_type <> ''),
    -- "<rule type>: <reason>", written by the rule itself.
    reason          TEXT    NOT NULL CHECK (reason <> ''),
    -- Hex SHA-256 of the strategy file that made the signal.
    strategy_hash   TEXT    NOT NULL
        CHECK (length(strategy_hash) = 64 AND strategy_hash NOT GLOB '*[^0-9a-f]*'),
    created_at      TEXT    NOT NULL
        CHECK (length(created_at) = 30 AND created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    UNIQUE (strategy, symbol, side, kind, trading_date),
    CHECK ((side = 'buy') = (kind IN ('buy', 'add')))
) STRICT;

CREATE INDEX signals_trading_date ON signals (trading_date);

-- One row per holding a strategy trades, per trading date: what its rules
-- suggest doing with it. A hold has the reason of the first rule that gave no
-- change, which is empty when no rule gave any decision.
CREATE TABLE holding_suggestions (
    id            INTEGER PRIMARY KEY,
    strategy      TEXT    NOT NULL CHECK (strategy <> ''),
    symbol        TEXT    NOT NULL
        CHECK (length(symbol) BETWEEN 1 AND 10 AND symbol GLOB '[A-Z]*' AND symbol NOT GLOB '*[^A-Z0-9.-]*'),
    trading_date  TEXT    NOT NULL
        CHECK (length(trading_date) = 10 AND trading_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    suggestion    TEXT    NOT NULL CHECK (suggestion IN ('hold', 'add', 'trim', 'sell')),
    reason        TEXT    NOT NULL,
    -- The highest stop the strategy's stop rules give the holding, or NULL
    -- when it has none. Paper-trading rests it at the broker.
    stop          TEXT
        CHECK (stop IS NULL OR (length(stop) BETWEEN 4 AND 24 AND stop GLOB '[0-9]*.[0-9][0-9]' AND stop NOT GLOB '*[^0-9.]*' AND instr(substr(stop, 1, length(stop) - 3), '.') = 0)),
    strategy_hash TEXT    NOT NULL
        CHECK (length(strategy_hash) = 64 AND strategy_hash NOT GLOB '*[^0-9a-f]*'),
    created_at    TEXT    NOT NULL
        CHECK (length(created_at) = 30 AND created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    UNIQUE (strategy, symbol, trading_date),
    -- Only a hold may have no reason.
    CHECK (suggestion = 'hold' OR reason <> '')
) STRICT;

CREATE INDEX holding_suggestions_trading_date ON holding_suggestions (trading_date);

-- Both tables are append-only: a recorded signal or suggestion is never
-- edited or removed.
CREATE TRIGGER signals_no_update BEFORE UPDATE ON signals
BEGIN
    SELECT RAISE(ABORT, 'signals is append-only');
END;

CREATE TRIGGER signals_no_delete BEFORE DELETE ON signals
BEGIN
    SELECT RAISE(ABORT, 'signals is append-only');
END;

CREATE TRIGGER holding_suggestions_no_update BEFORE UPDATE ON holding_suggestions
BEGIN
    SELECT RAISE(ABORT, 'holding_suggestions is append-only');
END;

CREATE TRIGGER holding_suggestions_no_delete BEFORE DELETE ON holding_suggestions
BEGIN
    SELECT RAISE(ABORT, 'holding_suggestions is append-only');
END;
