---
title: Phone app and read-only holdings
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
part_slug: phone-app
dependencies: [foundations]
estimated_scope: M
---

# Phone app and read-only holdings

## Scope

This part puts sherwood on your phone. The Go application serves an installable web app (manifest, icon, a service worker that caches only the shell) through Tailscale Serve, with the port bound to the host's loopback. It checks the Serve identity header against the allowed login, guards every change with an anti-forgery token, and supports the approval-device allowlist. It ships the Today and Portfolio views and the web push channel. The broker helper gains its one-time login command and two read-only calls, holdings and cash, so your holdings can be imported read-only.

## Dependencies

- foundations: needs the container, the database, the settings files, the secret type and the broker helper's socket.

## Verification criteria

- A request without the allowed Serve login is refused; a change without a valid anti-forgery token is refused; a GET never changes state.
- The phone app installs to the home screen and opens full-screen; the service worker never caches a data view.
- A web push message reaches the installed app and carries only a short alert.
- The broker helper offers only login, holdings and cash in this part; the Go client imports holdings into the database without creating any order.
- Only the operator runs the login command and the first real import; agents test the helper against a fake library.

## Parent design

This part implements one slice of [Sherwood Design](../../sherwood.md) (`Status: final`). See the parent for Context, Alternatives Considered, Quality Attributes and Operations. Changes to this part's scope mid-build are recorded in the parent's Document History.
