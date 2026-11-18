# Progress

Append-only log. Newest entries at the bottom. Format: `<YYYY-MM-DD HH:MM> /<phase> — <one-line summary>`.

---

## compaction event — 2026-04-19T23:29:56Z
- trigger: auto
- branch: main
- The session was compacted at this point. To re-anchor on the
  in-flight task, read .harness/PLAN.md and the entries above
  this marker. The compaction summary alone is not enough.

2026-11-15 /design — Sherwood design final; translated into six parts and sequenced. Active plan: foundations (11 tasks) at .harness/PLAN.md. Queued: phone-app, prices-and-rules, paper-trading, research-feed, live-trading. Next: /work task 1.
2026-11-16 /work — foundations task 1 (Go module and repo layout) merged as #64. Root module, cmd/sherwood, internal/ stubs, Makefile, golangci-lint v2 config, .gitignore for the real settings files and local databases. Next: tasks 3, 4, 6 and 8 in parallel.
2026-11-16 /work — foundations task 4 (SQLite store and migrations) merged as #66 and task 8 (broker helper scaffold) merged as #67. Tasks 3 and 6 are open; task 2 (money) is next now that the store exists.
2026-11-16 /work — foundations task 6 (masked secrets and the logger) merged as #69. Task 3 is open; tasks 2 (money) and 5 (settings files) are ready to start.
2026-11-16 /work — foundations task 3 (clock and exchange calendar) merged as #71. Wave 2 is complete. Ready: tasks 2 (money), 5 (settings files) and 7 (scheduler).
2026-11-16 /work — foundations task 7 (scheduler) merged as #73 and task 5 (settings and strategy files) merged as #74. Task 2 (money) is open; tasks 9 (container and start-up) and 11 (reference page) are ready.
2026-11-16 /work — foundations task 2 (money types) merged as #76 and task 9 (container, Compose and start-up wiring) merged as #77. Task 11 (reference page) is open; task 10 (CI, including the first real docker build) is ready, with a small follow-up to move config's dollar limits onto money.Money.
2026-11-16 /work — foundations task 11 (settings and strategy file reference) merged as #79, and the follow-up moving the dollar limits onto money.Money merged as #81. Only task 10 (CI) remains in foundations.
2026-11-16 /work — foundations task 10 (CI) merged as #82: eight checks on every PR, including the first real container build and a smoke test, a path-only gitleaks allowlist, and Dependabot over both Go modules, pip, docker and actions.

## Foundations complete — 2026-11-16

Part 1 of 6 of the sherwood rewrite is done: 11 tasks and one follow-up, merged as #64, #66, #67, #69, #71, #73, #74, #76, #77, #79, #81 and #82, with plan bookkeeping in #65, #68, #70, #72, #75, #78 and #84. The container builds and starts with example settings, refuses to start with the field named when a limit is missing, and runs an empty scheduler on the NYSE calendar. Money is exact decimals stored as text, no float can reach the money package, the wall clock is read only through the injected clock, secrets print as a mask, the logger writes only allowlisted fields, the broker helper answers only ping, and CI checks all of it. Open for later parts: interval jobs and the 02:00 backup need their own job kind; a broker helper error is only logged until the halt wiring lands; the rotating log file is still to do. Operator items: make the new CI checks required if wanted, check Dependabot's pip lock detection after its first run, and run the image on the NAS with real settings. The plan is archived to .harness/PLAN.archive.20261116-foundations.md. Next: phone-app (now the active plan) and prices-and-rules run side by side.
2026-11-16 /work — phone-app task 1 (web server and identity) merged as #88; prices-and-rules task 1 (price sources, migration 0002) merged as #89 and task 2 (rule library) as #90. Design amended: approval devices are named by tailnet address. Next: phone-app 2 and 3 (migration 0003) and prices 5; then phone-app 6 and prices 3 (migration 0004).
2026-11-18 /work — phone-app task 3 (holdings and cash snapshots, migration 0003) merged as #92 and task 2 (installable app shell) as #93. Next: phone-app 4 (views) and 6 (helper login), prices 3 (runner and signals, migration 0004); prices 5 is in progress.
2026-11-18 /work — prices-and-rules task 5 (paper fill model and back-tester) merged as #95. It fixes the order in which a strategy's rules combine, which task 3's runner must follow. Next: prices 3 (runner and signals, migration 0004).
2026-11-18 /work — prices-and-rules task 3 (rule runner and signals, migration 0004) merged as #98; phone-app task 4 (Today and Portfolio views) as #99 and task 6 (broker helper login and read-only calls) as #100. The app layout redesign merged as #97 while the operator was still choosing a design; the choice stays open. Next: phone-app 5 (web push, migration 0005) and 7 (holdings import), prices 4 (16:30 job) and 6 (setup skill).
2026-11-18 /work — phone-app task 7 (read-only holdings import) merged as #102 and prices-and-rules task 4 (the after-close job with retries at 18:00 and 21:00) as #103. The operator kept design B from #97. Next: phone-app 5 (web push, migration 0005, in progress) then 8; prices 6 (setup skill) then 7.
2026-11-18 /work — phone-app task 5 (web push, migration 0005) merged as #105. Next: phone-app 8 (install and broker-login how-tos), then close out phone-app; prices 6 (setup skill) in progress, then prices 7.
