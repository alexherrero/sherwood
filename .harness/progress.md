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
