---
title: Live trading
status: draft
visibility: published
author: repository owner
contributors: []
created: 2026-11-15
updated: 2026-11-15
last_major_revision: 2026-11-15
prd:
project:
parent_design: ../../sherwood.md
part_slug: live-trading
dependencies: [paper-trading, research-feed]
estimated_scope: M
---

# Live trading

## Scope

This part connects sherwood to the real account. The broker helper gains order calls (place, cancel, look up, list open orders) through the pinned unofficial library, and the Go Robinhood adapter passes them through. It adds resting protective stop orders and their after-close updates, `unknown`-state handling by reconciliation, session-expiry halts, the intraday loss-limit check from the account value, and the switch to live mode, which needs both the setting and a confirmation in the app. It finishes with the operational runbook. Live trading follows the research feed so that the full system runs on paper before real money.

## Dependencies

- paper-trading: needs the order states, the risk check, the execution layer, the stop switch and reconciliation.
- research-feed: sequenced after it so the whole system has a paper record before going live.

## Verification criteria

- The broker helper's order calls are tested only against a fake library; no agent calls Robinhood.
- The Robinhood adapter is built only in live mode with the in-app confirmation.
- A helper error or silence halts trading and is never retried.
- Resting stops are placed when a holding opens and raised by trailing rules after close.
- The first live session is run by the operator with a small amount, after the minimum paper period.

## Parent design

This part implements one slice of [Sherwood Design](../../sherwood.md) (`Status: final`). See the parent for Context, Alternatives Considered, Quality Attributes and Operations. Changes to this part's scope mid-build are recorded in the parent's Document History.
