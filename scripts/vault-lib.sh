#!/usr/bin/env bash
# Shared helpers for the vault-mutating host jobs: compile, synthesize, resolve.
# SOURCE this (don't execute it). It provides config (REPO, CLAUDE_BIN), the single
# cross-job lock, the "pull-before / commit-and-push-after" git discipline — so all three
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

# sync_from_origin — bring the checked-out branch up to date with origin BEFORE the job makes
# any new commits, so the later push can fast-forward instead of being rejected. Fetch, then a
# fast-forward-only merge of origin/<branch>. Requires cwd=$REPO, $LOG, and log(). Call it once,
# right after acquire_vault_lock, so the fetch+merge is serialized against the other jobs.
#
# Behavior by case:
#   - no origin remote / detached HEAD / no origin/<branch> yet  → no-op, return 0 (nothing to
#     reconcile against; the push step will likewise skip or create the branch).
#   - fetch fails (e.g. transient network)                       → WARN, return 0 (proceed on
#     local state; if origin really moved the push will fail loudly and next run re-syncs).
#   - local already ahead of / equal to origin (the normal case) → "already up to date", return 0.
#   - origin moved ahead and local hasn't                        → fast-forward, return 0.
#   - histories DIVERGED                                         → ERROR, return 1 WITHOUT mutating
#     anything. Adding commits on top would only deepen the split and guarantee a rejected push,
#     so callers must abort and let a human reconcile rather than pile on.
sync_from_origin() {
  git remote get-url origin >/dev/null 2>&1 || { log "no origin remote — skipping sync."; return 0; }
  local branch
  branch="$(git symbolic-ref --short -q HEAD)" || { log "detached HEAD — skipping sync."; return 0; }
  if ! git fetch origin >>"$LOG" 2>&1; then
    log "WARNING: git fetch failed — proceeding on local state (a later push may fail)."
    return 0
  fi
  if ! git rev-parse --verify -q "origin/$branch" >/dev/null; then
    log "origin/$branch does not exist yet — skipping sync (push will create it)."
    return 0
  fi
  if git merge --ff-only "origin/$branch" >>"$LOG" 2>&1; then
    log "synced: $branch is up to date with origin/$branch."
    return 0
  fi
  log "ERROR: local '$branch' has DIVERGED from origin/$branch — refusing to add commits on top."
  log "       Reconcile by hand (inspect both, e.g. 'git -C $REPO pull --rebase'), then re-run."
  return 1
}

# commit_and_push <message> [pathspec...] — stage the given pathspec (everything if none
# given), commit only if that produced staged changes, and push only if an origin remote
# exists. Requires cwd=$REPO, $LOG, and log(). This is the wrapper's git discipline: Claude
# never runs git; the wrapper owns it. Handles "nothing changed" cleanly (no empty commit).
#
# Scoping the pathspec matters: compile owns inbox/ (archives processed captures) so it stages
# everything, but the issue jobs must commit ONLY wiki/ + index.md + log.md — never sweeping up
# the raw inbox/ captures compile hasn't processed yet.
#
# Returns non-zero if the push fails: the commit is preserved locally (no work lost), but origin
# is now behind and the caller should surface that loudly (exit non-zero → systemd marks the unit
# failed) rather than swallow it — silent push failures are exactly how origin/<branch> silently
# rots while local commits pile up. Pair with sync_from_origin at the top of the run so a
# transient failure self-heals next pass and a real divergence is caught before any new commit.
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
  if ! git commit -m "$msg" >>"$LOG" 2>&1; then
    log "ERROR: git commit failed — see $LOG."
    return 1
  fi
  log "committed."
  git remote get-url origin >/dev/null 2>&1 || { log "no origin remote — commit kept local."; return 0; }
  if git push >>"$LOG" 2>&1; then
    log "pushed."
    return 0
  fi
  log "ERROR: git push failed — origin is NOT updated; local commit(s) are ahead. Resolve and re-run."
  return 1
}
