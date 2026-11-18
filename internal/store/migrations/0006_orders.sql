-- 0006: orders, every change to an order's state, and its fills.
--
-- The house rules in 0001 apply: STRICT tables, no REAL columns, prices as
-- exact decimal text with two places, share counts as exact decimal text
-- with at most six places, times as 30-character UTC text, trading dates as
-- 2006-01-02.
--
-- An order is made from a signal and moves through the design's states. The
-- allowed moves live in one place, internal/orders (transitions.go); the
-- schema does not repeat that table. What the schema does hold is the shape
-- of the record:
--
--   * an order starts in 'proposed', and only its state, its risk verdict and
--     its updated time may ever change;
--   * every change of state is a row in order_history, and the order's state
--     always equals the newest history row's, so the history of any order
--     replays to its current state;
--   * order_history and fills are append-only, and orders cannot be deleted.

CREATE TABLE orders (
    id              INTEGER PRIMARY KEY,
    -- signals.Key(strategy, symbol, side, kind, trading_date): re-running the
    -- signal job, restarting or replaying a day finds this order instead of
    -- making a second one. Passed to the broker as the client reference
    -- where the route accepts one.
    idempotency_key TEXT    NOT NULL UNIQUE
        CHECK (length(idempotency_key) = 64 AND idempotency_key NOT GLOB '*[^0-9a-f]*'),
    strategy        TEXT    NOT NULL CHECK (strategy <> ''),
    symbol          TEXT    NOT NULL
        CHECK (length(symbol) BETWEEN 1 AND 10 AND symbol GLOB '[A-Z]*' AND symbol NOT GLOB '*[^A-Z0-9.-]*'),
    -- The trading date the order was made for.
    trading_date    TEXT    NOT NULL
        CHECK (length(trading_date) = 10 AND trading_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    side            TEXT    NOT NULL CHECK (side IN ('buy', 'sell')),
    -- The signal kind, as in signals.kind. stop_sell is the protective sell.
    kind            TEXT    NOT NULL CHECK (kind IN ('buy', 'add', 'trim', 'sell', 'stop_sell')),
    -- day_limit_buy: a day order with a limit. stop: a protective stop
    -- resting at the broker. sell_at_open: a sell made at the next open.
    order_type      TEXT    NOT NULL CHECK (order_type IN ('day_limit_buy', 'stop', 'sell_at_open')),
    quantity        TEXT    NOT NULL
        CHECK (length(quantity) BETWEEN 1 AND 64 AND quantity GLOB '[0-9]*' AND quantity NOT GLOB '*[^0-9.]*'
               AND quantity NOT GLOB '*.*.*' AND quantity NOT GLOB '*.' AND quantity GLOB '*[1-9]*'
               AND quantity NOT GLOB '*.[0-9][0-9][0-9][0-9][0-9][0-9][0-9]*'),
    -- The limit of a day_limit_buy or the stop of a stop; NULL for a
    -- sell_at_open.
    price           TEXT
        CHECK (price IS NULL OR (length(price) BETWEEN 4 AND 24 AND price GLOB '[0-9]*.[0-9][0-9]' AND price NOT GLOB '*[^0-9.]*' AND instr(substr(price, 1, length(price) - 3), '.') = 0)),
    state           TEXT    NOT NULL
        CHECK (state IN ('proposed', 'awaiting_approval', 'approved', 'declined', 'blocked', 'submitted',
                         'accepted', 'partially_filled', 'filled', 'cancelled', 'rejected', 'expired', 'unknown')),
    -- The rule's reason, "<rule type>: <reason>", from the signal.
    reason          TEXT    NOT NULL CHECK (reason <> ''),
    -- The newest risk check's verdict in plain words, or NULL before the
    -- first check. Each verdict is also kept on the history row it came with.
    risk_verdict    TEXT    CHECK (risk_verdict IS NULL OR risk_verdict <> ''),
    strategy_hash   TEXT    NOT NULL
        CHECK (length(strategy_hash) = 64 AND strategy_hash NOT GLOB '*[^0-9a-f]*'),
    created_at      TEXT    NOT NULL
        CHECK (length(created_at) = 30 AND created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    updated_at      TEXT    NOT NULL
        CHECK (length(updated_at) = 30 AND updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    CHECK ((side = 'buy') = (kind IN ('buy', 'add'))),
    -- Each kind has one order type: buys and adds are day limit buys, trims
    -- and ordinary exits sell at the open, protective sells are stops.
    CHECK (order_type = CASE WHEN kind IN ('buy', 'add') THEN 'day_limit_buy'
                             WHEN kind = 'stop_sell' THEN 'stop'
                             ELSE 'sell_at_open' END),
    CHECK ((price IS NULL) = (order_type = 'sell_at_open')),
    CHECK (updated_at >= created_at)
) STRICT;

CREATE INDEX orders_trading_date ON orders (trading_date);
CREATE INDEX orders_state ON orders (state);

-- One row per change of an order's state, the first being its creation
-- (from_state NULL, to_state 'proposed').
CREATE TABLE order_history (
    id           INTEGER PRIMARY KEY,
    order_id     INTEGER NOT NULL REFERENCES orders (id),
    from_state   TEXT
        CHECK (from_state IS NULL OR from_state IN ('proposed', 'awaiting_approval', 'approved', 'declined', 'blocked', 'submitted',
                         'accepted', 'partially_filled', 'filled', 'cancelled', 'rejected', 'expired', 'unknown')),
    to_state     TEXT    NOT NULL
        CHECK (to_state IN ('proposed', 'awaiting_approval', 'approved', 'declined', 'blocked', 'submitted',
                         'accepted', 'partially_filled', 'filled', 'cancelled', 'rejected', 'expired', 'unknown')),
    -- Who or what made the change, such as proposer, risk, operator,
    -- stop_switch, execution, broker, reconciliation or expiry.
    actor        TEXT    NOT NULL
        CHECK (length(actor) BETWEEN 1 AND 32 AND actor GLOB '[a-z]*' AND actor NOT GLOB '*[^a-z_]*'),
    reason       TEXT    NOT NULL CHECK (reason <> ''),
    -- The risk verdict that came with this change, if a check ran.
    risk_verdict TEXT    CHECK (risk_verdict IS NULL OR risk_verdict <> ''),
    at           TEXT    NOT NULL
        CHECK (length(at) = 30 AND at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    CHECK (from_state IS NOT to_state),
    CHECK (from_state IS NOT NULL OR to_state = 'proposed')
) STRICT;

CREATE INDEX order_history_order ON order_history (order_id, id);

-- One row per fill. An order's fills add up to at most its quantity
-- (checked by internal/orders, since text decimals can't be summed exactly
-- here).
CREATE TABLE fills (
    id           INTEGER PRIMARY KEY,
    order_id     INTEGER NOT NULL REFERENCES orders (id),
    -- The trading date the shares filled on.
    trading_date TEXT    NOT NULL
        CHECK (length(trading_date) = 10 AND trading_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    quantity     TEXT    NOT NULL
        CHECK (length(quantity) BETWEEN 1 AND 64 AND quantity GLOB '[0-9]*' AND quantity NOT GLOB '*[^0-9.]*'
               AND quantity NOT GLOB '*.*.*' AND quantity NOT GLOB '*.' AND quantity GLOB '*[1-9]*'
               AND quantity NOT GLOB '*.[0-9][0-9][0-9][0-9][0-9][0-9][0-9]*'),
    price        TEXT    NOT NULL
        CHECK (length(price) BETWEEN 4 AND 24 AND price GLOB '[0-9]*.[0-9][0-9]' AND price NOT GLOB '*[^0-9.]*' AND instr(substr(price, 1, length(price) - 3), '.') = 0),
    -- quantity × price, exactly.
    value        TEXT    NOT NULL
        CHECK (length(value) BETWEEN 4 AND 64 AND value GLOB '[0-9]*.[0-9][0-9]*' AND value NOT GLOB '*[^0-9.]*' AND value NOT GLOB '*.*.*'),
    -- Why it filled at that price, such as the paper fill model's reason.
    reason       TEXT    NOT NULL CHECK (reason <> ''),
    filled_at    TEXT    NOT NULL
        CHECK (length(filled_at) = 30 AND filled_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z')
) STRICT;

CREATE INDEX fills_order ON fills (order_id);

-- An order is born proposed.
CREATE TRIGGER orders_start_proposed BEFORE INSERT ON orders
WHEN NEW.state <> 'proposed'
BEGIN
    SELECT RAISE(ABORT, 'orders must start in proposed');
END;

-- Only the state, the risk verdict and the updated time may change, and the
-- state must match the newest history row, so no state changes unrecorded.
CREATE TRIGGER orders_fixed_fields BEFORE UPDATE ON orders
WHEN NEW.id IS NOT OLD.id
  OR NEW.idempotency_key IS NOT OLD.idempotency_key
  OR NEW.strategy IS NOT OLD.strategy
  OR NEW.symbol IS NOT OLD.symbol
  OR NEW.trading_date IS NOT OLD.trading_date
  OR NEW.side IS NOT OLD.side
  OR NEW.kind IS NOT OLD.kind
  OR NEW.order_type IS NOT OLD.order_type
  OR NEW.quantity IS NOT OLD.quantity
  OR NEW.price IS NOT OLD.price
  OR NEW.reason IS NOT OLD.reason
  OR NEW.strategy_hash IS NOT OLD.strategy_hash
  OR NEW.created_at IS NOT OLD.created_at
BEGIN
    SELECT RAISE(ABORT, 'orders: only state, risk_verdict and updated_at may change');
END;

CREATE TRIGGER orders_state_recorded BEFORE UPDATE ON orders
WHEN NEW.state IS NOT (SELECT to_state FROM order_history WHERE order_id = NEW.id ORDER BY id DESC LIMIT 1)
BEGIN
    SELECT RAISE(ABORT, 'orders: state must match the newest order_history row');
END;

CREATE TRIGGER orders_updated_forward BEFORE UPDATE ON orders
WHEN NEW.updated_at < OLD.updated_at
BEGIN
    SELECT RAISE(ABORT, 'orders: updated_at may not go back');
END;

CREATE TRIGGER orders_no_delete BEFORE DELETE ON orders
BEGIN
    SELECT RAISE(ABORT, 'orders cannot be deleted');
END;

-- Each history row continues the chain: it starts where the newest row
-- ended (NULL for the first) and from the order's current state, and its
-- time is not before the newest row's.
CREATE TRIGGER order_history_chain BEFORE INSERT ON order_history
WHEN NEW.from_state IS NOT (SELECT to_state FROM order_history WHERE order_id = NEW.order_id ORDER BY id DESC LIMIT 1)
  OR NEW.from_state IS NOT NULL AND NEW.from_state IS NOT (SELECT state FROM orders WHERE id = NEW.order_id)
BEGIN
    SELECT RAISE(ABORT, 'order_history: from_state must be the order''s current state');
END;

CREATE TRIGGER order_history_in_time BEFORE INSERT ON order_history
WHEN NEW.at < (SELECT max(at) FROM order_history WHERE order_id = NEW.order_id)
BEGIN
    SELECT RAISE(ABORT, 'order_history: at is before the newest change');
END;

CREATE TRIGGER order_history_no_update BEFORE UPDATE ON order_history
BEGIN
    SELECT RAISE(ABORT, 'order_history is append-only');
END;

CREATE TRIGGER order_history_no_delete BEFORE DELETE ON order_history
BEGIN
    SELECT RAISE(ABORT, 'order_history is append-only');
END;

CREATE TRIGGER fills_no_update BEFORE UPDATE ON fills
BEGIN
    SELECT RAISE(ABORT, 'fills is append-only');
END;

CREATE TRIGGER fills_no_delete BEFORE DELETE ON fills
BEGIN
    SELECT RAISE(ABORT, 'fills is append-only');
END;
