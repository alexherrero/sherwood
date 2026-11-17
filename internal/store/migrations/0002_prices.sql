-- 0002: daily price bars and each trading day's cross-check.
--
-- The house rules in 0001 apply: STRICT tables, no REAL columns, prices as
-- exact decimal text with two places, trading dates as 2006-01-02.
--
-- A bar is usable for the rules only when three things hold, and the
-- usable_bars view below is the one place that says so:
--   * its trading day was cross-checked and found complete (price_days.usable),
--   * it came from that day's primary source, and
--   * its stock's two sources agreed within the strategy file's tolerance
--     (price_checks.outcome = 'agreed').
-- Everything else stays in price_bars for the record but is never usable.

-- One row per stock, trading date and source, as that source reported it,
-- split-adjusted as of the most recent fetch that covered it.
CREATE TABLE price_bars (
    id           INTEGER PRIMARY KEY,
    symbol       TEXT    NOT NULL
        CHECK (length(symbol) BETWEEN 1 AND 10 AND symbol GLOB '[A-Z]*' AND symbol NOT GLOB '*[^A-Z0-9.-]*'),
    trading_date TEXT    NOT NULL
        CHECK (length(trading_date) = 10 AND trading_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    -- The source's short name, such as tiingo or stooq.
    source       TEXT    NOT NULL
        CHECK (source <> '' AND source NOT GLOB '*[^a-z0-9_]*'),
    -- Prices are whole cents: digits, a point, two digits.
    open         TEXT    NOT NULL
        CHECK (length(open) BETWEEN 4 AND 24 AND open GLOB '[0-9]*.[0-9][0-9]' AND open NOT GLOB '*[^0-9.]*' AND instr(substr(open, 1, length(open) - 3), '.') = 0),
    high         TEXT    NOT NULL
        CHECK (length(high) BETWEEN 4 AND 24 AND high GLOB '[0-9]*.[0-9][0-9]' AND high NOT GLOB '*[^0-9.]*' AND instr(substr(high, 1, length(high) - 3), '.') = 0),
    low          TEXT    NOT NULL
        CHECK (length(low) BETWEEN 4 AND 24 AND low GLOB '[0-9]*.[0-9][0-9]' AND low NOT GLOB '*[^0-9.]*' AND instr(substr(low, 1, length(low) - 3), '.') = 0),
    close        TEXT    NOT NULL
        CHECK (length(close) BETWEEN 4 AND 24 AND close GLOB '[0-9]*.[0-9][0-9]' AND close NOT GLOB '*[^0-9.]*' AND instr(substr(close, 1, length(close) - 3), '.') = 0),
    volume       INTEGER NOT NULL CHECK (volume >= 0),
    fetched_at   TEXT    NOT NULL
        CHECK (length(fetched_at) = 30 AND fetched_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    UNIQUE (symbol, trading_date, source)
) STRICT;

CREATE INDEX price_bars_trading_date ON price_bars (trading_date, source);

-- One row per trading date whose prices were fetched and cross-checked. A
-- re-run of the same date replaces the row and, through the cascade, its
-- price_checks rows.
CREATE TABLE price_days (
    trading_date   TEXT    PRIMARY KEY
        CHECK (length(trading_date) = 10 AND trading_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    primary_source TEXT    NOT NULL CHECK (primary_source <> ''),
    check_source   TEXT    NOT NULL CHECK (check_source <> '' AND check_source <> primary_source),
    -- The strategy file's price_cross_check_tolerance in force, as written.
    tolerance      TEXT    NOT NULL
        CHECK (tolerance <> '' AND tolerance NOT GLOB '*[^0-9.]*'),
    -- 1 only when both sources gave this date's bar for every stock checked.
    -- A missing day is 0, and then no bar of that date is usable.
    usable         INTEGER NOT NULL CHECK (usable IN (0, 1)),
    -- Why the day is not usable, in words; empty when it is.
    detail         TEXT    NOT NULL DEFAULT '',
    checked_at     TEXT    NOT NULL
        CHECK (length(checked_at) = 30 AND checked_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    CHECK ((usable = 1) = (detail = ''))
) STRICT;

-- One row per held or watched stock per checked trading date.
CREATE TABLE price_checks (
    trading_date TEXT    NOT NULL REFERENCES price_days (trading_date) ON DELETE CASCADE,
    symbol       TEXT    NOT NULL
        CHECK (length(symbol) BETWEEN 1 AND 10 AND symbol GLOB '[A-Z]*' AND symbol NOT GLOB '*[^A-Z0-9.-]*'),
    -- 1 for a stock held that day, 0 for one only watched.
    held         INTEGER NOT NULL CHECK (held IN (0, 1)),
    -- agreed: both sources gave the bar and agree within the tolerance.
    -- disagreed: they differ by more than the tolerance; the stock gets no
    --   signals that day.
    -- missing: a source gave no usable bar for the date; the whole day is
    --   then not usable.
    outcome      TEXT    NOT NULL CHECK (outcome IN ('agreed', 'disagreed', 'missing')),
    -- 1 exactly when a held stock's sources disagreed: trading must halt for
    -- review. The halt itself is recorded elsewhere.
    halt         INTEGER NOT NULL CHECK (halt IN (0, 1)),
    -- What differed or what was missing, in words; empty when agreed.
    detail       TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (trading_date, symbol),
    CHECK (halt = (held = 1 AND outcome = 'disagreed')),
    CHECK ((outcome = 'agreed') = (detail = ''))
) STRICT;

-- The bars the rules may use: the primary source's bar of a usable day, for a
-- stock whose sources agreed. Nothing else is ever usable.
CREATE VIEW usable_bars AS
SELECT b.symbol, b.trading_date, b.open, b.high, b.low, b.close, b.volume
FROM price_bars AS b
JOIN price_days AS d
    ON d.trading_date = b.trading_date AND d.usable = 1 AND b.source = d.primary_source
JOIN price_checks AS c
    ON c.trading_date = b.trading_date AND c.symbol = b.symbol AND c.outcome = 'agreed';
