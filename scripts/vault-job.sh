#!/usr/bin/env bash
# Parameterized runner for the vault's judgment-call jobs: /synthesize and /resolve.
#
#   synthesize — heavy, INFREQUENT whole-corpus pass. Reconciles drift + finds connections
#                in wiki/ and OPENS judgment calls. Producer only. (synthesize.timer)
#   resolve    — light, MORE FREQUENT pass. Reads my answers, applies them to wiki/ and
#                CLOSES the calls. Consumer only; often a no-op. (resolve.timer)
#
# Two interchangeable channels carry the judgment calls, chosen by KNOWLEDGE_REVIEW_CHANNEL
# (auto-detected when unset — see below):
#
#   github — the original: synthesize/resolve run `gh issue ...` from INSIDE the Claude run,
#            so the run is granted exactly the gh subcommands each command needs via
#            --allowedTools. Needs git + a GitHub remote + an authed gh.
#   files  — git/GitHub-free: calls live as files in inbox/.review/, answered from chat via
#            the MCP connector. The run only edits files (acceptEdits, no gh, no network),
#            exactly like compile. Works on a bare folder synced by Dropbox/Syncthing.
#
# Either way Claude only edits files (+ gh calls in the github channel); this WRAPPER owns git
# (commit wiki/ + index.md + log.md — and inbox/.review/ in the files channel — push if origin
# exists) and the shared lock that serializes against compile. The commands self-declare model
# (opus) + effort in frontmatter, so we do NOT pass --model. Neither job touches top-level
# inbox/ captures.
#
# Usage: vault-job.sh <synthesize|resolve>   (normally invoked by the systemd unit)
set -euo pipefail

JOB="${1:?usage: vault-job.sh <synthesize|resolve>}"

case "$JOB" in
  synthesize | resolve) COMMIT_MSG_PREFIX="Vault $JOB" ;;
  *)
    echo "vault-job.sh: unknown job '$JOB' (expected 'synthesize' or 'resolve')" >&2
    exit 2
    ;;
esac

# Shared config (REPO, CLAUDE_BIN, KNOWLEDGE_REVIEW_CHANNEL via load-env.sh), the cross-job
# lock, and the commit/push side effect.
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/vault-lib.sh"
cd "$REPO"

# Pick the judgment-call channel. When unset, prefer github only if it's actually usable here
# (gh authed AND an origin remote exists); otherwise fall back to the portable file queue.
CHANNEL="${KNOWLEDGE_REVIEW_CHANNEL:-}"
if [ -z "$CHANNEL" ]; then
  if gh auth status >/dev/null 2>&1 && git remote get-url origin >/dev/null 2>&1; then
    CHANNEL=github
  else
    CHANNEL=files
  fi
fi

# Per-channel config: the slash command, the gh grants (github only — files needs none), and
# the commit pathspec. GH_TOOLS must match the command's frontmatter allowed-tools exactly;
# it's passed to claude as belt-and-suspenders so the headless run can't stall on a permission
# prompt it has no way to answer (and so it can do nothing *but* those gh calls + edits).
case "$CHANNEL" in
  github)
    SLASH="/$JOB"
    COMMIT_PATHS=(wiki index.md log.md)
    if [ "$JOB" = synthesize ]; then
      GH_TOOLS=(
        "Bash(gh issue list:*)"
        "Bash(gh issue view:*)"
        "Bash(gh issue create:*)"
        "Bash(gh search issues:*)"
      )
    else
      GH_TOOLS=(
        "Bash(gh issue list:*)"
        "Bash(gh issue view:*)"
        "Bash(gh issue comment:*)"
        "Bash(gh issue edit:*)"
        "Bash(gh issue close:*)"
      )
    fi
    ;;
  files)
    SLASH="/$JOB-files"
    GH_TOOLS=()
    # The files channel writes/updates question files under inbox/.review/, so commit that
    # subdir too — never the raw top-level inbox/ captures compile hasn't processed yet.
    COMMIT_PATHS=(wiki index.md log.md inbox/.review)
    ;;
  *)
    echo "vault-job.sh: unknown KNOWLEDGE_REVIEW_CHANNEL '$CHANNEL' (expected 'github' or 'files')" >&2
    exit 2
    ;;
esac

LOG_DIR="$REPO/outputs/$JOB-logs"
mkdir -p "$LOG_DIR"
STAMP="$(date +%Y-%m-%d_%H%M%S)"
LOG="$LOG_DIR/$STAMP.log"

log() { printf '%s %s\n' "$(now_iso)" "$*" | tee -a "$LOG"; }

# Serialize against compile + the other issue job — they all edit wiki/ and commit.
acquire_vault_lock

# Refresh the schedule snapshot (last/next run of all three jobs) on every exit path now that we
# hold the lock — so the status surface stays current whether this run does work, finds nothing
# to resolve, or fails. (The "couldn't get the lock" exit above is before this trap on purpose.)
trap 'refresh_schedules' EXIT

# Catch up to origin before we edit + commit, so the push at the end fast-forwards. Aborts
# (loudly) only if origin has diverged in a way we must not commit on top of.
sync_from_origin || exit 1

log "$JOB starting ($SLASH, channel=$CHANNEL)"

# resolve is the consumer side and acts ONLY on calls I've marked answered (the go-signal). If
# nothing is answered there's nothing to apply, so skip the (opus) run entirely — the same
# short-circuit compile does on an empty inbox.
if [ "$JOB" = resolve ]; then
  if [ "$CHANNEL" = github ]; then
    n=$(gh issue list --state open --label "vault:answered" --json number -q 'length' 2>>"$LOG" || echo 0)
  else
    n=$(grep -rl '^status: answered' "$REPO/inbox/.review" 2>/dev/null | wc -l | tr -d ' ' || true)
  fi
  if [ "$n" -eq 0 ]; then
    log "nothing answered — nothing to resolve."
    exit 0
  fi
  log "answered calls: $n"
fi

# Fresh, headless run. acceptEdits auto-applies Claude's wiki/ edits. In the github channel
# --allowedTools grants exactly the gh issue subcommands the command needs (and nothing else);
# the files channel passes no tool grants at all (file edits only). Claude never runs git.
CLAUDE_ARGS=(-p "$SLASH" --permission-mode acceptEdits)
if [ "${#GH_TOOLS[@]}" -gt 0 ]; then
  CLAUDE_ARGS+=(--allowedTools "${GH_TOOLS[@]}")
fi
if ! "$CLAUDE_BIN" "${CLAUDE_ARGS[@]}" >>"$LOG" 2>&1; then
  log "claude exited non-zero — leaving the vault as-is for inspection."
  exit 1
fi

# The wrapper owns git: commit the scoped pathspec and push — never the raw top-level inbox/
# captures compile hasn't processed. Stage only paths that actually exist: `git add` errors on
# a missing pathspec, and inbox/.review/ (files channel, before the first question) or a
# freshly-seeded log.md may not be there yet. resolve is often a no-op (calls read but no edit
# applied) — commit_and_push handles "nothing staged" cleanly. A push failure is surfaced as a
# non-zero exit (the commit is preserved locally) so systemd flags the unit instead of rotting
# silently.
STAGE=()
for p in "${COMMIT_PATHS[@]}"; do
  [ -e "$p" ] && STAGE+=("$p")
done
if [ "${#STAGE[@]}" -eq 0 ]; then
  log "no tracked paths present to commit."
elif ! commit_and_push "$COMMIT_MSG_PREFIX ($STAMP)" "${STAGE[@]}"; then
  log "$JOB done (with push failure)."
  exit 1
fi

log "$JOB done."
