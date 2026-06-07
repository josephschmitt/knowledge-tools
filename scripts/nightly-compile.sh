#!/usr/bin/env bash
# Nightly ephemeral compile: turn inbox/ captures into wiki/ knowledge as a fresh,
# short-lived Claude run, then archive the processed captures and commit.
#
# Design notes:
#   - Claude only edits files (--permission-mode acceptEdits); it never runs git or
#     deletes inbox files. This wrapper owns git and inbox archival, so the unattended
#     run needs no Bash/git permissions.
#   - Only the inbox files that existed *before* the Claude run are archived, so captures
#     that arrive mid-run are left for the next night.
set -euo pipefail

REPO="${KNOWLEDGE_REPO:-/home/USER/knowledge}"
CLAUDE_BIN="${CLAUDE_BIN:-$HOME/.local/bin/claude}"
cd "$REPO"

LOG_DIR="$REPO/outputs/compile-logs"
mkdir -p "$LOG_DIR"
STAMP="$(date +%Y-%m-%d_%H%M%S)"
LOG="$LOG_DIR/$STAMP.log"

log() { printf '%s %s\n' "$(date -Is)" "$*" | tee -a "$LOG"; }

# Snapshot the inbox items to process (top-level files, excluding .gitkeep and archive/).
mapfile -t ITEMS < <(find inbox -maxdepth 1 -type f ! -name '.gitkeep' | sort)

if [ "${#ITEMS[@]}" -eq 0 ]; then
  log "inbox empty — nothing to compile."
  exit 0
fi

log "compiling ${#ITEMS[@]} inbox item(s):"
printf '  %s\n' "${ITEMS[@]}" | tee -a "$LOG"

# Fresh, headless compile. acceptEdits auto-applies Write/Edit without prompting.
if ! "$CLAUDE_BIN" -p "/compile-inbox" --permission-mode acceptEdits >>"$LOG" 2>&1; then
  log "claude exited non-zero — leaving inbox untouched for inspection."
  exit 1
fi

# Archive the captures we processed (preserve a raw trail per CLAUDE.md).
ARCHIVE="inbox/archive/$STAMP"
mkdir -p "$ARCHIVE"
for f in "${ITEMS[@]}"; do
  [ -e "$f" ] && mv "$f" "$ARCHIVE/"
done
log "archived processed captures to $ARCHIVE"

# Commit if anything changed; push only if an origin remote exists.
if [ -n "$(git status --porcelain)" ]; then
  git add -A
  git commit -m "Nightly compile ($STAMP)" >>"$LOG" 2>&1
  log "committed."
  if git remote get-url origin >/dev/null 2>&1; then
    git push >>"$LOG" 2>&1 && log "pushed." || log "push failed (non-fatal)."
  fi
else
  log "no changes to commit."
fi

log "done."
