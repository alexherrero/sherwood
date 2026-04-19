# Sherwood

An automated trading platform.

## v2 (in progress)

v2 is currently being designed and built. See the design docs in `docs/` once scaffolding is complete.

## v1 (archived)

The original proof-of-concept Go backend is preserved in [`v1/`](./v1). It includes:

- REST API (chi router) with 5 built-in trading strategies
- SQLite-backed order persistence
- Paper trading engine with backtesting
- WebSocket real-time updates
- CI/CD pipelines and wiki (see `v1/.github/` and `v1/wiki/`)

Refer to [`v1/README.md`](./v1/README.md) for the original documentation.
