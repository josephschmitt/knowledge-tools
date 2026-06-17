#!/usr/bin/env bash
# Ephemeral inbox->wiki compile: turn inbox/ captures into wiki/ knowledge as a fresh,
# short-lived Claude run, then archive the processed captures and commit.
#
# Triggered two ways, both starting the SAME per-vault systemd unit (so systemd's single-instance
# guarantee serializes them — that's the cross-process lock for this vault):
#   - knowledge-compile@<vault>.timer  → scheduled run (cadence via KNOWLEDGE_COMPILE_ONCALENDAR)
#   - knowledge-compile@<vault>.path   → manual run, when the MCP server drops inbox/.compile/request
#
# Design notes:
#   - Claude only edits files (--permission-mode acceptEdits); it never runs git or
#     deletes inbox files. This wrapper owns git and inbox archival, so the unattended
#     run needs no Bash/git permissions.
#   - Only the inbox files that existed *before* the Claude run are archived, so captures
#     that arrive mid-run are left for the next run.
#   - Manual runs are rate-limited (COOLDOWN_SECONDS). The scheduled run is never throttled
#     and does not consume the manual cooldown.
set -euo pipefail

# Shared config (REPO, CLAUDE_BIN), the cross-job lock, and git side effects. This sources
# load-env.sh and requires KNOWLEDGE_REPO; the lock it provides is what serializes compile
# against synthesize/resolve (all three edit wiki/ and commit).
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/vault-lib.sh"
COOLDOWN_SECONDS="${KNOWLEDGE_COMPILE_COOLDOWN:-3600}"
cd "$REPO"

LOG_DIR="$REPO/outputs/compile-logs"
mkdir -p "$LOG_DIR"
STAMP="$(date +%Y-%m-%d_%H%M%S)"
LOG="$LOG_DIR/$STAMP.log"

# Compile coordination state (shared with the MCP server via the inbox bind mount).
COMPILE_DIR="$REPO/inbox/.compile"
REQUEST="$COMPILE_DIR/request"
STATUS="$COMPILE_DIR/status.json"
LAST_COMPILED_FILE="$COMPILE_DIR/last-compiled-epoch"
LAST_MANUAL_FILE="$COMPILE_DIR/last-manual-epoch"
mkdir -p "$COMPILE_DIR"

log() { printf '%s %s\n' "$(date -Is)" "$*" | tee -a "$LOG"; }

iso_of() { [ -s "$1" ] && date -d "@$(cat "$1")" -Is || printf ''; }

# Write status.json for the MCP to read. Args: running(true|false) summary.
write_status() {
  cat >"$STATUS" <<EOF
{
  "running": $1,
  "started_at": "$STARTED_AT",
  "last_compiled_at": "$(iso_of "$LAST_COMPILED_FILE")",
  "last_manual_compile_at": "$(iso_of "$LAST_MANUAL_FILE")",
  "cooldown_seconds": $COOLDOWN_SECONDS,
  "summary": "$2"
}
EOF
}

# Take the shared vault lock so compile never runs concurrently with synthesize/resolve (or a
# hand-run script). systemd already serializes this service's own timer vs path triggers.
acquire_vault_lock

# Catch up to origin before we compile + commit, so the push at the end fast-forwards. Aborts
# (loudly) only if origin has diverged in a way we must not commit on top of.
if ! sync_from_origin; then
  write_status false "aborted: local diverged from origin"
  exit 1
fi

# Manual if the MCP dropped a request sentinel; consume it so the .path unit can re-arm.
if [ -f "$REQUEST" ]; then
  MODE=manual
  rm -f "$REQUEST"
else
  MODE=scheduled
fi
log "compile mode: $MODE"

NOW_EPOCH="$(date +%s)"
STARTED_AT="$(date -Is)"

# Manual runs are throttled; the scheduled run is exempt and never consumes the cooldown.
if [ "$MODE" = manual ] && [ -s "$LAST_MANUAL_FILE" ]; then
  ELAPSED=$((NOW_EPOCH - $(cat "$LAST_MANUAL_FILE")))
  if [ "$ELAPSED" -lt "$COOLDOWN_SECONDS" ]; then
    log "throttled — last manual compile was ${ELAPSED}s ago (< ${COOLDOWN_SECONDS}s). Skipping."
    exit 0
  fi
fi

# Snapshot the inbox items to process (top-level files, excluding dotfiles like .gitkeep
# and the .compile/ control dir).
mapfile -t ITEMS < <(find inbox -maxdepth 1 -type f ! -name '.*' | sort)

if [ "${#ITEMS[@]}" -eq 0 ]; then
  log "inbox empty — nothing to compile."
  write_status false "inbox empty"
  exit 0
fi

log "compiling ${#ITEMS[@]} inbox item(s):"
printf '  %s\n' "${ITEMS[@]}" | tee -a "$LOG"
write_status true "compiling ${#ITEMS[@]} item(s)"

# Fresh, headless compile. acceptEdits auto-applies Write/Edit without prompting.
if ! "$CLAUDE_BIN" -p "/compile-inbox" --permission-mode acceptEdits >>"$LOG" 2>&1; then
  log "claude exited non-zero — leaving inbox untouched for inspection."
  write_status false "compile failed"
  exit 1
fi

# Archive the captures we processed (preserve a raw trail per CLAUDE.md).
ARCHIVE="inbox/archive/$STAMP"
mkdir -p "$ARCHIVE"
for f in "${ITEMS[@]}"; do
  [ -e "$f" ] && mv "$f" "$ARCHIVE/"
done
log "archived processed captures to $ARCHIVE"

# Commit if anything changed; push only if an origin remote exists (shared with the other jobs).
# Defer a push failure so the cooldown/status bookkeeping below still runs (the compile itself
# succeeded locally); we re-raise it as a non-zero exit at the end so systemd flags the run.
PUSH_FAILED=
commit_and_push "Vault compile ($STAMP)" || PUSH_FAILED=1

# Record completion timestamps for the cooldown + status surface.
date +%s >"$LAST_COMPILED_FILE"
[ "$MODE" = manual ] && date +%s >"$LAST_MANUAL_FILE"

if [ -n "$PUSH_FAILED" ]; then
  write_status false "compiled ${#ITEMS[@]} item(s) but push failed"
  log "done (with push failure)."
  exit 1
fi
write_status false "compiled ${#ITEMS[@]} item(s)"

log "done."
