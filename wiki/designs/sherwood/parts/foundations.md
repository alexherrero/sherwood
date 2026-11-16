---
title: Foundations: container, storage and guard rails
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
part_slug: foundations
dependencies: []
estimated_scope: M
---

# Foundations: container, storage and guard rails

## Scope

This part builds the base every later part stands on. It sets up the Go application and the Python broker helper in one container, the SQLite database with numbered migrations from the first commit, exact decimal money types, an injected clock, the exchange calendar, and the deterministic scheduler's job records. It loads and validates the settings and strategy files, refusing to start if any limit is missing. It adds the masked secret type, the field-allowlist logger, and CI with credential scanning and pinned actions.

## Dependencies

None. This is the first part.

## Verification criteria

- Money, prices and share counts round-trip through SQLite and JSON as exact decimals, and no float type appears in the money path.
- A fresh database migrates to the latest version; a failing migration rolls back and stops start-up; re-running migrations changes nothing.
- The application refuses to start when the strategy file is missing any limit, and reports which one.
- Printing, logging or JSON-encoding a secret shows only a mask.
- Tests use a fake clock and never sleep; the calendar returns the published US exchange holidays and half days.
- The broker helper starts under the Go application and answers a ping over its Unix socket; a contract test checks both sides.
- CI runs vet, lint, vulnerability scan, race-enabled Go tests, Python tests, a credential scan and a container build, with every action pinned by commit.

## Parent design

This part implements one slice of [Sherwood Design](../../sherwood.md) (`Status: final`). See the parent for Context, Alternatives Considered, Quality Attributes and Operations. Changes to this part's scope mid-build are recorded in the parent's Document History.
