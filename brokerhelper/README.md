# Broker helper

The broker helper is the small Python process inside the Sherwood container
that talks to the broker. It's the only process that will make broker calls and
the only one that reads the broker secrets, so the Go application never holds
them. The Go application starts the helper, watches it, restarts it if it
exits, and sends it calls over a Unix socket.

Today the helper offers one operation, `ping`. The broker operations, and the
broker library they need, arrive in a later part.

## How a call works

Each call is one connection. The Go side connects, sends one JSON object on one
line, and reads one JSON reply on one line; then the helper closes the
connection.

```text
-> {"id":"17","op":"ping","args":{}}
<- {"id":"17","ok":true,"result":{"version":"0.1.0","pid":42}}

-> {"id":"18","op":"sell","args":{}}
<- {"id":"18","ok":false,"error":{"code":"unknown_op","message":"unknown op: sell"}}
```

- `id` is a non-empty string the caller picks; the reply echoes it.
- `args` is an object and can be left out when it's empty. No other request
  fields are allowed.
- `error.code` is one of `bad_request`, `unknown_op` or `internal` today; later
  operations add their own codes. `error.message` is for people.
- Money, prices and share counts travel as strings. The helper refuses JSON
  numbers with a fraction or exponent, so a decimal is never turned into a
  float on the way in.

The helper runs every call exactly once and never retries one. Every error goes
back to the Go application, which decides what happens next. A request that
never reaches its closing newline is never run, and a client that goes quiet is
dropped after the read timeout.

The helper prints `ready` on standard output once it's listening. The socket is
readable and writable by its own user only.

## Running it

```sh
PYTHONPATH=brokerhelper/src python3 -m brokerhelper --socket /tmp/sherwood-broker.sock
```

## Tests

```sh
cd brokerhelper
python3 -m venv .venv
.venv/bin/pip install --require-hashes -r requirements-dev.lock
.venv/bin/python -m pytest
```

The Go side has a contract test in `internal/broker` that starts this helper,
pings it, kills it and checks that the error is reported. It skips when
`python3` isn't installed.

## Dependencies

Dependencies are declared in `pyproject.toml` and locked with hashes:

- `requirements.lock` holds the runtime dependencies. It's empty today; the
  broker library joins it later, pinned to one reviewed version.
- `requirements-dev.lock` holds the test tools.

Install either with `pip install --require-hashes -r <file>`. After changing
`pyproject.toml`, run `brokerhelper/lock.sh` (it needs
[uv](https://docs.astral.sh/uv/)) and read the whole diff before committing it.

There's no example settings file yet, because `ping` needs no settings. When
one arrives it will be `broker.example.yaml`; the real `broker.yaml` is never
committed.
