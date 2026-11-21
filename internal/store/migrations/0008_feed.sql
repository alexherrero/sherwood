-- 0008: the research feed: each upload from the Mac and the items it brought.
--
-- The house rules in 0001 apply: STRICT tables, no REAL columns, times as
-- 30-character UTC text.
--
-- The research job on the Mac writes a feed file, and a plain script uploads
-- it. internal/feed checks the file against its schema, drops any item that
-- breaks the rules (no source, an unknown kind, a link that is not http or
-- https, text that is too long, and the others it names) and stores what is
-- left: one row in feed_uploads and one row in feed_items per accepted item,
-- in one transaction. Both tables are append-only and kept; the app shows the
-- latest upload's candidates, the latest note on each holding, and the latest
-- explanation of each signal.
--
-- Nothing here is read by the trading path. A feed item cannot become an
-- order.

CREATE TABLE feed_uploads (
    id       INTEGER PRIMARY KEY,
    -- When the NAS took the upload in.
    at       TEXT    NOT NULL
        CHECK (length(at) = 30 AND at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    -- How many items were stored, and how many were dropped.
    accepted INTEGER NOT NULL CHECK (accepted >= 0),
    dropped  INTEGER NOT NULL CHECK (dropped >= 0)
) STRICT;

CREATE TABLE feed_items (
    id         INTEGER PRIMARY KEY,
    upload_id  INTEGER NOT NULL REFERENCES feed_uploads (id),
    -- candidate: a case for a stock you do not hold. holding_note: what is
    -- happening to a stock you hold. signal_explanation: why a rule made a
    -- signal, in plain words.
    kind       TEXT    NOT NULL
        CHECK (kind IN ('candidate', 'holding_note', 'signal_explanation')),
    symbol     TEXT    NOT NULL
        CHECK (length(symbol) BETWEEN 1 AND 10 AND symbol GLOB '[A-Z]*' AND symbol NOT GLOB '*[^A-Z0-9.-]*'),
    -- The explained signal's idempotency key (internal/signals.Key), set on
    -- explanations and only on them.
    signal_key TEXT
        CHECK (signal_key IS NULL OR (length(signal_key) = 64 AND signal_key NOT GLOB '*[^0-9a-f]*')),
    -- The item's words, shown as escaped plain text.
    text       TEXT    NOT NULL CHECK (length(trim(text)) >= 1 AND length(text) <= 2000),
    -- The item's sources as a JSON array of {"url": ..., "title": ...}, at
    -- least one.
    sources    TEXT    NOT NULL
        CHECK (json_valid(sources) AND json_type(sources) = 'array' AND json_array_length(sources) >= 1),
    CHECK ((kind = 'signal_explanation') = (signal_key IS NOT NULL))
) STRICT;

-- The candidates (and anything else) from one upload.
CREATE INDEX feed_items_upload ON feed_items (upload_id, kind);

-- The latest note on each stock.
CREATE INDEX feed_items_holding_notes ON feed_items (symbol, id)
    WHERE kind = 'holding_note';

-- The latest explanation of each signal.
CREATE INDEX feed_items_explanations ON feed_items (signal_key, id)
    WHERE kind = 'signal_explanation';

CREATE TRIGGER feed_uploads_no_update BEFORE UPDATE ON feed_uploads
BEGIN
    SELECT RAISE(ABORT, 'feed_uploads is append-only');
END;

CREATE TRIGGER feed_uploads_no_delete BEFORE DELETE ON feed_uploads
BEGIN
    SELECT RAISE(ABORT, 'feed_uploads is append-only');
END;

CREATE TRIGGER feed_items_no_update BEFORE UPDATE ON feed_items
BEGIN
    SELECT RAISE(ABORT, 'feed_items is append-only');
END;

CREATE TRIGGER feed_items_no_delete BEFORE DELETE ON feed_items
BEGIN
    SELECT RAISE(ABORT, 'feed_items is append-only');
END;
