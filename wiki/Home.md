# Sherwood

A self-hosted automated trading platform: a single operator watches the S&P 500 (and crypto) across multiple timeframes, runs configurable strategies against live market data, backtests them against historical bars, and chats with an AI research assistant. Humans approve every position; the AI may only act protectively on its own (halt strategies, cancel pending orders).

## 🧩 Designs

- **[Sherwood v2 — System Design](sherwood-v2)** — the v2 architecture (decimal money, mandatory risk, clock injection, streaming data layer, AI-on-the-side), written against the v1 tech-debt retro as hard constraints. Build decisions are recorded in its **Amendment log**, not standalone ADRs.

> Sherwood v2 is in the design-and-build phase. v1 — the archived Go proof-of-concept (REST API, 5 strategies, SQLite persistence, paper trading + backtesting) — lives in [`v1/`](https://github.com/alexherrero/sherwood/tree/main/v1).
