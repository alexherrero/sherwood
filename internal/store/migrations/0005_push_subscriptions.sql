-- 0005: web push subscriptions.
--
-- The house rules in 0001 apply: STRICT tables, no REAL columns, times as
-- 30-character UTC text.
--
-- One row per installed app that turned notifications on. The browser hands
-- the app an endpoint at its maker's push service and two keys, and sherwood
-- sends each alert, encrypted with those keys, to that endpoint. An endpoint
-- is a private address: anyone holding it and the keys could send the phone
-- a notification, so it is kept here and never logged. Turning notifications
-- on again on the same phone replaces its keys. A row is deleted when the
-- push service says the subscription is gone.
CREATE TABLE push_subscriptions (
    id         INTEGER PRIMARY KEY,
    -- The push service address, always https.
    endpoint   TEXT    NOT NULL UNIQUE
        CHECK (length(endpoint) BETWEEN 9 AND 2048 AND endpoint GLOB 'https://*'),
    -- The phone's public key (P-256, base64url, 65 bytes before encoding).
    p256dh     TEXT    NOT NULL CHECK (length(p256dh) BETWEEN 80 AND 100),
    -- The shared authentication secret (16 bytes before encoding).
    auth       TEXT    NOT NULL CHECK (length(auth) BETWEEN 20 AND 30),
    created_at TEXT    NOT NULL
        CHECK (length(created_at) = 30 AND created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z'),
    updated_at TEXT    NOT NULL
        CHECK (length(updated_at) = 30 AND updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]Z')
) STRICT;
