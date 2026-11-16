---
title: The research feed
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
part_slug: research-feed
dependencies: [phone-app, prices-and-rules]
estimated_scope: M
---

# The research feed

## Scope

This part brings in the AI research. The Go application publishes the research export (symbols, prices and signals, with no balances or account details) behind the feed token, and accepts uploaded feeds at an endpoint that can only add feed rows after schema validation. On the Mac, a launchd job runs a plain script that fetches the export, runs Claude headless with the research skill and only read, web and own-folder write tools, then validates and uploads the feed. The app gains the Feed view with sources, the "Add to watchlist" tap, AI notes on holdings and signal explanations on approval cards.

## Dependencies

- phone-app: needs the app and its views.
- prices-and-rules: needs signals and their reasons to explain.

## Verification criteria

- The feed endpoint rejects anything outside the schema and items without a source.
- A test checks that the feed code has no import path to the execution layer.
- Feed text renders escaped; a feed item containing markup shows as plain text.
- The research session's tool list contains no shell and no tool that can send to the NAS.
- A stale feed shows a quiet note and changes nothing about trading.

## Parent design

This part implements one slice of [Sherwood Design](../../sherwood.md) (`Status: final`). See the parent for Context, Alternatives Considered, Quality Attributes and Operations. Changes to this part's scope mid-build are recorded in the parent's Document History.
