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

# Portable date helpers. GNU coreutils `date` (Linux) understands --version, -Is, and -d "@epoch";
# BSD `date` (macOS) understands none of those, so fall back to an explicit strftime format and -r
# for the epoch conversion. Detect once. Sourced before the workers define log()/write_status, so
# both can route every timestamp through these instead of calling `date -Is` / `date -d` directly.
#
# Offset format: GNU's -Is emits the tz offset with a colon (+04:00); BSD's %z emits it without
# (+0400) and has no %:z. Normalize the BSD form (_iso_colon) so timestamps read identically on
# Linux and macOS — Linux output is left exactly as -Is produced it.
if date --version >/dev/null 2>&1; then GNU_DATE=1; else GNU_DATE=0; fi
_iso_colon() { sed -E 's/([+-][0-9][0-9])([0-9][0-9])$/\1:\2/'; }
now_iso() {  # current time as ISO-8601 with offset, e.g. 2026-06-18T13:45:00-04:00
  if [ "$GNU_DATE" = 1 ]; then date -Is; else date "+%Y-%m-%dT%H:%M:%S%z" | _iso_colon; fi
}
epoch_iso() {  # <epoch-seconds> -> ISO-8601; empty input -> empty string
  [ -n "${1:-}" ] || { printf ''; return 0; }
  if [ "$GNU_DATE" = 1 ]; then date -d "@$1" -Is; else date -r "$1" "+%Y-%m-%dT%H:%M:%S%z" | _iso_colon; fi
}

# One lockfile every vault-mutating job acquires, so compile / synthesize / resolve never run
# at the same time — they all edit wiki/ and commit, and (say) a resolve commit must not race
# a compile commit. Kept OUTSIDE the repo on purpose: it's runtime state, not vault content,
# and synthesize/resolve must not write into inbox/ (where compile keeps its own state).
#
# The lock is keyed by KNOWLEDGE_INSTANCE (the systemd template instance; "default" when unset,
# i.e. a single-vault host). This is exactly the isolation a multi-vault host needs: jobs for
# DIFFERENT vaults (different instances → different lock files) run concurrently, while the three
# jobs WITHIN one vault (same instance → same lock file) still serialize. The path is otherwise
# fixed (not XDG-derived) so every job for a given instance computes the *same* file regardless
# of how each unit's environment is set up. Override with KNOWLEDGE_VAULT_LOCK if you must.
VAULT_LOCK="${KNOWLEDGE_VAULT_LOCK:-$HOME/.local/state/knowledge-tools/vault-${KNOWLEDGE_INSTANCE:-default}.lock}"

# acquire_vault_lock — take the shared lock, or exit 0 if another job holds it. The lock is held
# for the life of the process (released when it exits). Requires log().
#
# Linux has flock; macOS ships none, so fall back to an atomic mkdir lock with the SAME
# non-blocking semantics (bail, don't queue). Both key off $VAULT_LOCK (per-instance), so the
# multi-vault isolation is identical either way: same instance → same lock → serialized; different
# instances → different locks → concurrent.
acquire_vault_lock() {
  mkdir -p "$(dirname "$VAULT_LOCK")"
  if command -v flock >/dev/null 2>&1; then
    exec 9>"$VAULT_LOCK"
    if ! flock -n 9; then
      log "another vault job holds the lock ($VAULT_LOCK) — exiting."
      exit 0
    fi
    return 0
  fi
  # No flock (macOS): emulate `flock -n` with an atomic mkdir of $VAULT_LOCK.d — exactly one racer
  # wins the mkdir. A crashed holder can leave the dir behind, so reclaim it once if it has no
  # valid living PID. "No valid living PID" deliberately includes an EMPTY/missing pid file: a
  # holder SIGKILL'd between winning the mkdir and writing its pid would otherwise wedge the lock
  # forever (every later caller reads "" and bails). Released by an EXIT trap (workers set no other).
  local lockdir="$VAULT_LOCK.d"
  if ! mkdir "$lockdir" 2>/dev/null; then
    local holder; holder="$(cat "$lockdir/pid" 2>/dev/null || true)"
    if [ -z "$holder" ] || ! kill -0 "$holder" 2>/dev/null; then
      log "stale vault lock ($lockdir, pid '${holder:-none}' gone) — reclaiming."
      rm -rf "$lockdir"
      mkdir "$lockdir" 2>/dev/null || { log "another vault job holds the lock ($lockdir) — exiting."; exit 0; }
    else
      log "another vault job holds the lock ($lockdir) — exiting."
      exit 0
    fi
  fi
  echo "$$" >"$lockdir/pid"
  # Reference the GLOBAL $VAULT_LOCK, not $lockdir (a local that's out of scope when EXIT fires).
  trap 'rm -rf "$VAULT_LOCK.d"' EXIT
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
#   - histories DIVERGED                                         → REBASE local (unpushed) commits
#     onto origin/<branch> and return 0. Divergence is now routine: the phone commits task-file
#     edits to origin (Working Copy auto-push) while the host may carry an unpushed commit from
#     a prior failed push. The agent and the phone touch DISJOINT paths (agent: inbox/, wiki/,
#     index.md, log.md, new tasks/*.md, tasks/_dashboard.md, tasks/_completed.md; the human: the
#     lifecycle frontmatter of EXISTING tasks/*.md), so the rebase replays cleanly. Only a GENUINE
#     content conflict aborts the rebase (leaving the tree clean) and returns 1 for a human to
#     reconcile — that stays a real-race alarm rather than a routine block. Rewriting the local
#     commits is safe because they're unpushed (that's why local diverged in the first place).
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
  log "local '$branch' diverged from origin/$branch — rebasing local commits on top."
  if git rebase "origin/$branch" >>"$LOG" 2>&1; then
    log "reconciled: rebased local commits onto origin/$branch."
    return 0
  fi
  git rebase --abort >>"$LOG" 2>&1 || true
  log "ERROR: rebase onto origin/$branch failed — tree restored, no commits added (see log for cause)."
  log "       Likely a real conflict (shouldn't happen given disjoint edit paths) or a dirty tree from a"
  log "       crashed prior run; reconcile by hand, then re-run."
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
  # The vault need not be a git repo — it can be a plain folder synced by Dropbox/Syncthing,
  # which carries history itself. If there's no work tree, there's nothing to commit: log it
  # and bail cleanly. (Mirrors the origin-remote gate below: degrade, don't fail.)
  if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    log "not a git repo — skipping commit (history left to external sync)."
    return 0
  fi
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
