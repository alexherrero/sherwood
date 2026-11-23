# Broker helper

The broker helper is the small Python process inside the Sherwood container
that talks to the broker. It's the only process that will make broker calls and
the only one that reads the broker secrets, so the Go application never holds
them. The Go application starts the helper, watches it, restarts it if it
exits, and sends it calls over a Unix socket.

The helper offers eight operations: `ping`; the broker reads `holdings`,
`cash`, `account`, `lookup` and `open_orders`; and the two calls that change
something at the broker, `place` and `cancel`. A test holds the list to
exactly these eight, and nothing here moves money. It also has a one-time
login command that you run yourself on the NAS (see below).

Every order reaches the helper only after the Go application's risk check has
passed it. The helper checks that the request is well formed and sends it
once. Any error goes back to the Go application, which halts trading; the
helper never retries a call.

## How a call works

Each call is one connection. The Go side connects, sends one JSON object on one
line, and reads one JSON reply on one line; then the helper closes the
connection.

```text
-> {"id":"17","op":"ping","args":{}}
<- {"id":"17","ok":true,"result":{"version":"0.1.0","pid":42}}

-> {"id":"18","op":"withdraw","args":{}}
<- {"id":"18","ok":false,"error":{"code":"unknown_op","message":"unknown op: withdraw"}}
```

- `id` is a non-empty string the caller picks; the reply echoes it.
- `args` is an object and can be left out when it's empty. No other request
  fields are allowed.
- `error.code` is one of the codes below. `error.message` is for people, and
  never quotes a secret or a value the broker sent.
- Money, prices and share counts travel as strings. The helper refuses JSON
  numbers with a fraction or exponent, so a decimal is never turned into a
  float on the way in.

The helper runs every call exactly once and never retries one. Every error goes
back to the Go application, which decides what happens next. A request that
never reaches its closing newline is never run, and a client that goes quiet is
dropped after the read timeout.

The helper prints `ready` on standard output once it's listening. The socket is
readable and writable by its own user only.

## Operations

| Op | Result |
|---|---|
| `ping` | `{"version": "0.1.0", "pid": 42}` |
| `holdings` | `{"as_of": "2026-11-17T21:05:00Z", "holdings": [{"symbol": "ABC", "quantity": "10", "average_cost": "12.34", "opened_on": "2025-03-04"}]}` |
| `cash` | `{"as_of": "2026-11-17T21:05:00Z", "cash": "1234.56", "buying_power": "1200"}` |
| `account` | `{"as_of": "2026-11-17T21:05:00Z", "cash": "1234.56", "buying_power": "1200", "value": "5678.9"}` |
| `place` | an order report (below) |
| `cancel` | an order report |
| `lookup` | an order report |
| `open_orders` | `{"as_of": "2026-11-17T21:05:00Z", "orders": [<order report>, ...]}` |

`place` takes `{"ref", "symbol", "side", "type", "quantity", "price"}`;
`cancel` and `lookup` take `{"ref"}`; the others take no arguments. Every
amount and share count is an exact decimal string with no exponent and no
trailing zeros. Holdings are sorted by symbol; `opened_on` is the date in New
York that the broker opened the position, and `as_of` is when the helper read
it. `cash` may be negative. `account`'s `value` is the account's total value
(the portfolio's `equity`), which the loss limits are measured against.
`holdings` makes one request per page of positions plus one per position whose
record has no symbol, and `lookup`, `cancel` and `open_orders` read the
account's order list page by page, so give them a longer deadline than `ping`.

### Orders

`place` sends one order:

| `type` | `side` | `price` | Sent to the broker as |
|---|---|---|---|
| `day_limit_buy` | `buy` | the limit | a limit buy, good for the day |
| `stop` | `sell` | the stop | a stop sell that becomes a market sell at the stop, good until cancelled |
| `sell_at_open` | `sell` | none | a market sell, good for the day |

`ref` is the order's idempotency key. The broker wants a UUID as the order's
client reference, so the helper sends the version-5 UUID of the key in a fixed
namespace (`client_ref` in `library.py`; the namespace is the version-5 UUID
of `https://github.com/alexherrero/sherwood/broker/client-ref` in the URL
namespace, `b2c9bd9c-91ea-56fa-8a48-f93a5fccfc5f`). The same key always gives
the same reference, so a second send of one order carries the reference of the
first. A price must already be in the broker's steps (whole cents at a dollar
and above, four places below): the helper refuses a price it would have to
round, and never rounds one itself. Shares may have up to six places.

An order report is:

```json
{"ref": "<key>", "client_ref": "<uuid>", "broker_id": "<uuid>",
 "status": "accepted", "filled": "0", "fill_price": null, "detail": ""}
```

`status` is one of `accepted`, `rejected`, `partially_filled`, `filled`,
`cancelled` and `expired`. `filled` is the shares filled so far and
`fill_price` their average price, or null when the broker doesn't say.
`detail` is the broker's reason for a refusal. If the broker lists no
tradeable stock for the symbol, `place` sends nothing and answers `rejected`
with a null `broker_id`.

`lookup` and `cancel` find the order by its client reference. `cancel` asks
the broker once to call off an open order and answers `cancelled`; an order
that has already finished is answered with its status and nothing is sent.
`open_orders` can't turn a client reference back into a key, so its reports
have a null `ref`: match `client_ref` against the reference of each key you
hold. Orders placed in the broker's own app appear there too.

Only two addresses ever receive a POST: the order list (to send an order) and
one order's cancel link (to call it off). The wrapper refuses any other, and
nothing sends a DELETE, a PUT or a PATCH.

| Error code | Meaning |
|---|---|
| `bad_request` | The request couldn't be read, or an op was given arguments it doesn't take or a malformed order. Nothing was sent. |
| `unknown_op` | The helper doesn't offer that op. |
| `login_needed` | Nobody has logged in yet, the session has expired, or the broker refused it. Run the login command again. |
| `broker_settings` | `broker.yaml` is malformed, a link, not yours, or readable by others. |
| `broker_error` | The call to the broker failed: network, timeout, or an HTTP status other than 200 (200 or 201 for a POST). After `place` or `cancel`, the outcome is unknown. |
| `bad_broker_reply` | The broker answered in a shape the helper doesn't expect, so it won't guess. |
| `order_not_found` | The broker lists no order with that reference. |
| `internal` | Anything else; the message holds only the exception's type. |

## Logging in to the broker

Only you run this, in a terminal on the NAS. No agent runs it, and the tests
never touch the broker.

```sh
docker exec -it sherwood python3 -m brokerhelper login
```

It asks for the broker username, the password (not shown as you type) and, if
you use an authenticator app, its current code. The broker may then ask you to
approve the login in its app or send you a code by text or email; follow the
prompts. If the account list has more than one open account, it shows each by
its last four characters and asks which one sherwood should read.

When the broker issues a session, the command writes `/data/broker.yaml` with
mode 600. It writes nowhere else: the name is fixed, it refuses a data folder
that's a link or missing, and it refuses to replace anything at that path but a
regular file. The file holds the session token, the refresh token, the chosen
account and when the session expires. **It never holds your password or a
second-factor code**; those go to the broker once and aren't kept. Use
`--data-dir` only if the application-data folder isn't `/data`.

The helper reads `broker.yaml` fresh on every call, so a new login takes effect
without a restart. It refuses the file unless it's a regular file owned by the
helper's user and readable by nobody else. When the session expires, the calls
answer `login_needed` until you log in again.

## The broker library

The helper reaches Robinhood through the unofficial `robin-stocks` library,
pinned to exactly 3.4.0 in `pyproject.toml` and locked with hashes. Only
`src/brokerhelper/library.py` imports it, and only when a broker call is made:

- **Login** goes through the library, because it handles the second factor and
  device approval. The library insists on saving a session pickle; the wrapper
  points it at a private temporary folder that is deleted straight afterwards.
- **Every other call** uses only the library's HTTP session and two of its URL
  builders. The wrapper makes each request itself, because the library's own
  helpers print and swallow HTTP errors, can return a partial list without
  saying so, and send prices as floats. Every request has a 10-second timeout,
  anything but HTTP 200 (or 200 and 201 for a POST) is an error (401 and 403
  mean log in again), redirects aren't followed, the session token is sent
  only to `https://api.robinhood.com/`, and nothing is retried. The order
  routes are written out in `library.py`, copied from the library's
  `urls.py`, so the safety test can pin exactly where a POST may go.

**Before taking a new version of the library**, read its diff in full,
especially `robinhood/authentication.py`, `helper.py`, `globals.py` and
`urls.py`, then change the pin and regenerate the locks. Dependabot may
propose a bump; don't merge one unread.

## Running it

```sh
PYTHONPATH=brokerhelper/src python3 -m brokerhelper --socket /tmp/sherwood-broker.sock
```

It reads `broker.yaml` from `/data` unless you pass `--data-dir`.

## Tests

```sh
cd brokerhelper
python3 -m venv .venv
.venv/bin/pip install --require-hashes -r requirements-dev.lock
.venv/bin/python -m pytest
```

The tests replace the library with a fake (`tests/fakes.py`, made-up values in
the broker's documented shapes). `tests/guard/` refuses any import of the
library or its HTTP client during the tests, including in the helper processes
they start, and a test checks that importing the helper loads neither.
`tests/test_safety.py` pins the list of ops, the wrapper's methods, the parts
of the library it borrows and the addresses a POST may go to, so any addition
fails until that list is changed on purpose. It also checks that no secret
reaches stdout, stderr or an error reply.

The Go side has a contract test in `internal/broker` that starts this helper,
pings it, kills it and checks that the error is reported. It skips when
`python3` isn't installed.

## Dependencies

Dependencies are declared in `pyproject.toml` and locked with hashes:

- `requirements.lock` holds the runtime dependencies: the broker library, its
  own dependencies, and `tzdata`, so the helper has the New York time zone
  whatever the image holds. The container runs them on distroless Debian 13's
  Python 3.13, so CI tests the helper on 3.13 too.
- `requirements-dev.lock` holds the same plus the test tools.

Install either with `pip install --require-hashes -r <file>`. After changing
`pyproject.toml`, run `brokerhelper/lock.sh` (it needs
[uv](https://docs.astral.sh/uv/)) and read the whole diff before committing it.

There's no example `broker.yaml`: only the login command writes it, and the
real one is never committed (`.gitignore` and `.dockerignore` both exclude it).
