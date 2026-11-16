---
title: Prices, rules and the setup skill
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
part_slug: prices-and-rules
dependencies: [foundations]
estimated_scope: L
---

# Prices, rules and the setup skill

## Scope

This part makes sherwood able to decide. It adds the free end-of-day price sources behind an interface, with a cross-check source and fail-closed handling of gaps and disagreements. It adds the rule library (moving-average cross, breakout, momentum ranking, pullback, fixed stop, trailing stop, sizing rules) as pure functions, the rule runner that turns a day's prices into signals with mechanical reasons and the strategy file's hash, and the 16:30 ET job. It adds the back-tester, which runs the same rule functions with the paper fill model, and the setup skill that writes a strategy file and back-tests it on the Mac.

## Dependencies

- foundations: needs decimals, the clock, the calendar, the scheduler, the database and strategy-file validation.

## Verification criteria

- Two runs of the rule runner on the same inputs give identical signals and identical idempotency keys.
- A disagreement between price sources beyond the configured amount gives that stock no signals; a missing day gives no signals at all.
- Each rule type has table tests for its signals, including no-change days.
- The back-tester gives the same report on every run of the same inputs.
- The setup skill writes a strategy file that passes validation, and its example output uses placeholder values only.

## Parent design

This part implements one slice of [Sherwood Design](../../sherwood.md) (`Status: final`). See the parent for Context, Alternatives Considered, Quality Attributes and Operations. Changes to this part's scope mid-build are recorded in the parent's Document History.
