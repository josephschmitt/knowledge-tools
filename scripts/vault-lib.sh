#!/usr/bin/env bash
# Shared helpers for the vault-mutating host jobs: compile, synthesize, resolve.
# SOURCE this (don't execute it). It provides config (REPO, CLAUDE_BIN), the single
# cross-job lock, and the "commit-if-dirty / push-if-origin" side effect — so all three
# jobs behave identically and never run concurrently.
#
# Callers must, before using the helpers below: define a log() function and a $LOG file,
# and cd into "$REPO". (This mirrors how vault-compile.sh is already structured.)

# Load config from the repo-root .env (KNOWLEDGE_REPO etc.); real env vars take precedence.
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/load-env.sh"
: "${KNOWLEDGE_REPO:?set KNOWLEDGE_REPO to the vault repo path (in .env or the environment)}"
REPO="$KNOWLEDGE_REPO"
CLAUDE_BIN="${CLAUDE_BIN:-$HOME/.local/bin/claude}"

# One lockfile every vault-mutating job acquires, so compile / synthesize / resolve never run
# at the same time — they all edit wiki/ and commit, and (say) a resolve commit must not race
# a compile commit. Kept OUTSIDE the repo on purpose: it's runtime state, not vault content,
# and synthesize/resolve must not write into inbox/ (where compile keeps its own state). The
# path is fixed (not XDG-derived) so every job computes the *same* file regardless of how each
# unit's environment is set up. Override with KNOWLEDGE_VAULT_LOCK if you must.
VAULT_LOCK="${KNOWLEDGE_VAULT_LOCK:-$HOME/.local/state/knowledge-tools/vault.lock}"

# acquire_vault_lock — take the shared lock on fd 9, or exit 0 if another job holds it.
# The lock is held for the life of the process (released when it exits). Requires log().
acquire_vault_lock() {
  mkdir -p "$(dirname "$VAULT_LOCK")"
  exec 9>"$VAULT_LOCK"
  if ! flock -n 9; then
    log "another vault job holds the lock ($VAULT_LOCK) — exiting."
    exit 0
  fi
}

# commit_and_push <message> [pathspec...] — stage the given pathspec (everything if none
# given), commit only if that produced staged changes, and push only if an origin remote
# exists. Requires cwd=$REPO, $LOG, and log(). This is the wrapper's git discipline: Claude
# never runs git; the wrapper owns it. Handles "nothing changed" cleanly (no empty commit).
#
# Scoping the pathspec matters: compile owns inbox/ (archives processed captures) so it stages
# everything, but the issue jobs must commit ONLY wiki/ + index.md + log.md — never sweeping up
# the raw inbox/ captures compile hasn't processed yet.
commit_and_push() {
  local msg="$1"; shift
  if [ "$#" -eq 0 ]; then
    git add -A
  else
    git add -A -- "$@"
  fi
  if git diff --cached --quiet; then
    log "no changes to commit."
    return 0
  fi
  git commit -m "$msg" >>"$LOG" 2>&1
  log "committed."
  if git remote get-url origin >/dev/null 2>&1; then
    git push >>"$LOG" 2>&1 && log "pushed." || log "push failed (non-fatal)."
  fi
}
