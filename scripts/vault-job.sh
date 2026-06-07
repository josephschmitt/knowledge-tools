#!/usr/bin/env bash
# Parameterized runner for the vault's GitHub-issue jobs: /synthesize and /resolve.
#
#   synthesize — heavy, INFREQUENT whole-corpus pass. Reconciles drift + finds connections
#                in wiki/ and OPENS judgment-call issues. Producer only. (synthesize.timer)
#   resolve    — light, MORE FREQUENT pass. Reads my answers on open issues, applies them to
#                wiki/ and CLOSES them. Consumer only; often a no-op. (resolve.timer)
#
# Unlike the compile, these run `gh issue ...` from INSIDE the Claude run — filing/closing
# issues is the whole point — so the run is granted exactly the gh subcommands each command's
# frontmatter declares, via --allowedTools (no blanket skip-permissions). Claude still only
# edits files + runs those gh calls; this WRAPPER owns git (commit wiki/ + index.md + log.md,
# push if origin exists) and the shared lock that serializes against compile. The commands
# self-declare model (opus) + effort in frontmatter, so we do NOT pass --model. Neither job
# touches inbox/.
#
# Usage: vault-job.sh <synthesize|resolve>   (normally invoked by the systemd unit)
set -euo pipefail

JOB="${1:?usage: vault-job.sh <synthesize|resolve>}"

# Per-job config. GH_TOOLS must match the command's frontmatter allowed-tools exactly; it's
# passed to claude as belt-and-suspenders so the headless run can't stall on a permission
# prompt it has no way to answer (and so it can do nothing *but* those gh calls + edits).
case "$JOB" in
  synthesize)
    SLASH="/synthesize"
    COMMIT_MSG_PREFIX="Vault synthesize"
    GH_TOOLS=(
      "Bash(gh issue list:*)"
      "Bash(gh issue view:*)"
      "Bash(gh issue create:*)"
      "Bash(gh search issues:*)"
    )
    ;;
  resolve)
    SLASH="/resolve"
    COMMIT_MSG_PREFIX="Vault resolve"
    GH_TOOLS=(
      "Bash(gh issue list:*)"
      "Bash(gh issue view:*)"
      "Bash(gh issue comment:*)"
      "Bash(gh issue close:*)"
    )
    ;;
  *)
    echo "vault-job.sh: unknown job '$JOB' (expected 'synthesize' or 'resolve')" >&2
    exit 2
    ;;
esac

# Shared config (REPO, CLAUDE_BIN), the cross-job lock, and the commit/push side effect.
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/vault-lib.sh"
cd "$REPO"

LOG_DIR="$REPO/outputs/$JOB-logs"
mkdir -p "$LOG_DIR"
STAMP="$(date +%Y-%m-%d_%H%M%S)"
LOG="$LOG_DIR/$STAMP.log"

log() { printf '%s %s\n' "$(date -Is)" "$*" | tee -a "$LOG"; }

# Serialize against compile + the other issue job — they all edit wiki/ and commit.
acquire_vault_lock

log "$JOB starting ($SLASH)"

# resolve is the consumer side: when there are no open vault issues at all, there's nothing
# to read back, so skip the (opus) run entirely — the same short-circuit compile does on an
# empty inbox. Two label queries because gh ANDs multiple --label flags; we want either.
if [ "$JOB" = resolve ]; then
  n1=$(gh issue list --state open --label "vault:judgment-call"      --json number -q 'length' 2>>"$LOG" || echo 0)
  n2=$(gh issue list --state open --label "vault:needs-verification" --json number -q 'length' 2>>"$LOG" || echo 0)
  if [ "$((n1 + n2))" -eq 0 ]; then
    log "no open vault issues — nothing to resolve."
    exit 0
  fi
  log "open vault issues: judgment-call=$n1 needs-verification=$n2"
fi

# Fresh, headless run. acceptEdits auto-applies Claude's wiki/ edits; --allowedTools grants
# exactly the gh issue subcommands the command needs (and nothing else). Claude never runs git.
if ! "$CLAUDE_BIN" -p "$SLASH" \
      --permission-mode acceptEdits \
      --allowedTools "${GH_TOOLS[@]}" \
      >>"$LOG" 2>&1; then
  log "claude exited non-zero — leaving the vault as-is for inspection."
  exit 1
fi

# The wrapper owns git: commit ONLY wiki/ + index.md + log.md and push — never the raw inbox/
# captures compile hasn't processed. resolve is often a no-op (issues read but no edit applied)
# — commit_and_push handles "nothing staged" cleanly.
commit_and_push "$COMMIT_MSG_PREFIX ($STAMP)" wiki index.md log.md

log "$JOB done."
