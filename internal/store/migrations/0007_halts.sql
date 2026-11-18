-- 0007: halts and their resets: the stop switch's record.
--
-- The house rules in 0001 apply: STRICT tables, no REAL columns, times as
-- 30-character UTC text.
--
-- Trading halts when anything throws the stop switch: you, from the phone
-- app, or sherwood itself on a broker error, a disagreement between the price
-- sources on a held stock, an illegal order state change, and the other
-- causes the design names. Each throw is a row in halts, with its cause and
-- the evidence. Only you can reset the switch, from an approval device, and
-- each reset is a row in halt_resets with your reason and the device.
--
-- A reset clears every halt up to and including through_halt_id, the newest
-- halt the phone showed you. The switch is on while any halt is newer than
-- the newest reset's through_halt_id. So a halt thrown while you were
-- writing your reason is never cleared by a reset you made without seeing
-- it, and a second throw while the switch is on adds a row without touching
-- the first. Both tables are append-only.

CREATE TABLE halts (
    id       INTEGER PRIMARY KEY,
    -- What threw the switch, as a short snake_case word such as operator,
    -- broker_error, price_disagreement or illegal_transition. Deliberately
    -- not a fixed list, so a later cause needs no new table; the words shown
    -- for each cause live in internal/halt.
    cause    TEXT    NOT NULL
        CHECK (length(cause) BETWEEN 1 AND 32 AND cause GLOB '[a-z]*' AND cause NOT GLOB '*[^a-z_]*'),
    -- What was seen, in words: the error, the stocks and their prices, the
    -- order and the refused move.
    evidence TEXT    NOT NULL CHECK (length(evidence) BETWEEN 1 AND 2000),
    at       TEXT    NOT NULL
        CHECK (length(at) = 30 AND at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z')
) STRICT;

CREATE TABLE halt_resets (
    id              INTEGER PRIMARY KEY,
    -- The newest halt this reset clears; every older halt is cleared too.
    through_halt_id INTEGER NOT NULL REFERENCES halts (id),
    -- Why you reset it, in your words.
    reason          TEXT    NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 500),
    -- The approval device the reset came from, as its tailnet address.
    device          TEXT    NOT NULL CHECK (length(device) BETWEEN 1 AND 64),
    at              TEXT    NOT NULL
        CHECK (length(at) = 30 AND at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z')
) STRICT;

-- A reset must clear at least one halt the last reset did not.
CREATE TRIGGER halt_resets_forward BEFORE INSERT ON halt_resets
WHEN NEW.through_halt_id <= (SELECT coalesce(max(through_halt_id), 0) FROM halt_resets)
BEGIN
    SELECT RAISE(ABORT, 'halt_resets: a reset must clear a halt newer than the last reset');
END;

CREATE TRIGGER halts_no_update BEFORE UPDATE ON halts
BEGIN
    SELECT RAISE(ABORT, 'halts is append-only');
END;

CREATE TRIGGER halts_no_delete BEFORE DELETE ON halts
BEGIN
    SELECT RAISE(ABORT, 'halts is append-only');
END;

CREATE TRIGGER halt_resets_no_update BEFORE UPDATE ON halt_resets
BEGIN
    SELECT RAISE(ABORT, 'halt_resets is append-only');
END;

CREATE TRIGGER halt_resets_no_delete BEFORE DELETE ON halt_resets
BEGIN
    SELECT RAISE(ABORT, 'halt_resets is append-only');
END;
