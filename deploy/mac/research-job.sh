#!/bin/bash
# sherwood's nightly research job, run on the Mac by launchd
# (deploy/mac/com.example.sherwood-research.plist).
#
# It runs three steps, in order, and stops at the first failure:
#   1. "sherwood research-export" downloads the research export from the NAS.
#   2. Claude runs headless with the research skill and writes one feed file.
#      It gets only the tools in TOOLS below: no shell, and no tool that can
#      send anything to the NAS. Writes are allowed only in its output folder.
#   3. "sherwood research-upload" checks the feed file with the same code the
#      NAS runs, then uploads it.
#
# Only steps 1 and 3 hold the feed token. Claude never sees the config file:
# it lives outside Claude's working folder, and Claude's file tools are kept
# inside that folder.
#
# Every path below is a placeholder default. Set the variables in the plist's
# EnvironmentVariables, or edit the defaults in your own copy of this file.
set -euo pipefail

# The job's own folder: the sherwood binary, the config file and the logs.
RESEARCH_DIR="${RESEARCH_DIR:-$HOME/sherwood-research}"
# The sherwood binary built for the Mac (go build -o ... ./cmd/sherwood).
SHERWOOD="${SHERWOOD:-$RESEARCH_DIR/sherwood}"
# The config file with nas_url and feed_token (deploy/mac/research.example.yaml).
# It must be readable only by you; sherwood refuses it otherwise.
CONFIG="${CONFIG:-$RESEARCH_DIR/research.yaml}"
# The Claude CLI. launchd's PATH is short, so give the full path in the plist.
CLAUDE="${CLAUDE:-claude}"
# The research skill and its example feed, from your clone of the repo.
SKILL_DIR="${SKILL_DIR:-$(cd "$(dirname "$0")/../../.claude/skills/research" && pwd)}"
# Claude's working folder, emptied of last night's files on every run. The
# export goes here, and Claude writes its feed into OUT.
RUN_DIR="${RUN_DIR:-$RESEARCH_DIR/run}"
OUT_DIR="$RUN_DIR/out"

# The research session's tools: read its inputs, search and fetch public web
# pages, and write. Nothing else. A test (cmd/sherwood/research_test.go)
# reads these lines and fails if a shell or any other tool appears.
TOOLS="Read,Write,WebFetch,WebSearch"
# What runs without asking. Write is allowed only inside OUT_DIR (Claude Code
# checks writes against Edit rules; "//" starts an absolute path).
ALLOWED_TOOLS="Read,WebFetch,WebSearch,Edit(/$OUT_DIR/**)"
# Refused outright, as a second guard: every tool that can run a command, and
# fetches to any tailnet address (Tailscale Serve names end in .ts.net).
DENIED_TOOLS="Bash,PowerShell,NotebookEdit,Agent,Task,WebFetch(domain:*.ts.net)"

log() { printf '%s research-job: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }

# Claude Code reads these lists split on spaces, so no folder may hold one.
case "$RUN_DIR" in
  *" "*) log "RUN_DIR must not contain a space: $RUN_DIR"; exit 2 ;;
esac
case "$RUN_DIR" in
  /*) ;;
  *) log "RUN_DIR must be an absolute path: $RUN_DIR"; exit 2 ;;
esac
# The token must stay out of Claude's reach.
case "$CONFIG" in
  "$RUN_DIR"/*) log "CONFIG must not be inside RUN_DIR, where Claude can read it"; exit 2 ;;
esac
if [ ! -f "$SKILL_DIR/SKILL.md" ]; then
  log "no research skill at $SKILL_DIR/SKILL.md"
  exit 2
fi

mkdir -p -m 700 "$RUN_DIR" "$OUT_DIR"
rm -f "$RUN_DIR/export.json" "$RUN_DIR/research-skill.md" "$RUN_DIR/example-feed.json" "$OUT_DIR/feed.json"

log "step 1: fetching the export"
"$SHERWOOD" research-export -config "$CONFIG" -out "$RUN_DIR/export.json"

cp "$SKILL_DIR/SKILL.md" "$RUN_DIR/research-skill.md"
cp "$SKILL_DIR/example-feed.json" "$RUN_DIR/example-feed.json"

log "step 2: research (Claude headless, tools: $TOOLS)"
PROMPT="Read research-skill.md in your working folder and follow it exactly. Tonight's export is export.json. Write the feed to out/feed.json and nothing else."
(
  cd "$RUN_DIR"
  printf '%s\n' "$PROMPT" | "$CLAUDE" -p \
    --restricted \
    --strict-mcp-config \
    --no-session-persistence \
    --permission-mode dontAsk \
    --tools "$TOOLS" \
    --allowedTools "$ALLOWED_TOOLS" \
    --disallowedTools "$DENIED_TOOLS" \
    --output-format text
)

if [ ! -s "$OUT_DIR/feed.json" ]; then
  log "the research session wrote no feed file; nothing to upload"
  exit 1
fi

log "step 3: checking and uploading the feed"
"$SHERWOOD" research-upload -config "$CONFIG" -export "$RUN_DIR/export.json" -feed "$OUT_DIR/feed.json"
log "done"
