---
title: Paper trading with the safety rails
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
part_slug: paper-trading
dependencies: [phone-app, prices-and-rules]
estimated_scope: L
---

# Paper trading with the safety rails

## Scope

This part lets sherwood trade on paper, with every safety rail in place. It adds the order record and its state machine with an append-only history, the idempotency key with its unique constraint, and the mandatory risk check with every limit in the design. It adds the paper broker adapter and its fill model, the execution layer and its send windows, the protective-sell path, and the Approvals and Orders views. It adds the stop switch, the automatic halts and their reset, tightening limits from the app, reconciliation, notifications for waiting buys and halts, and the nightly checked backup.

## Dependencies

- phone-app: needs the app, identity and anti-forgery checks, approval devices and web push.
- prices-and-rules: needs signals to turn into orders, and the paper fill model.

## Verification criteria

- The execution layer cannot be built without a risk check, and every limit has a passing and a refusing test.
- Running the signal job twice for the same day creates one order.
- Every allowed state transition is tested, and an illegal one halts trading.
- A buy waits for approval and expires at the end of its trading day; a protective sell skips approval and still passes its shorter risk check.
- The stop switch declines waiting buys and cancels open buys while keeping resting protective stops, unless confirmed a second time.
- A failed fake-broker call moves the order to `unknown`, halts trading and sends no retry.
- The nightly backup opens and its row counts match.

## Parent design

This part implements one slice of [Sherwood Design](../../sherwood.md) (`Status: final`). See the parent for Context, Alternatives Considered, Quality Attributes and Operations. Changes to this part's scope mid-build are recorded in the parent's Document History.
