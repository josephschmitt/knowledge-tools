package vault

import (
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// The wrapper owns all git — Claude only edits files. These port vault-lib.sh's git discipline:
// sync_from_origin (pull-before) and commit_and_push (commit-and-push-after), shelling out to the
// git CLI to preserve the exact behavior the scripts relied on.

// git runs a git subcommand in repo, streaming its output to the log file (not stdout), and
// returns combined output for the caller to inspect on error.
func git(repo string, log *Logger, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if log != nil && len(out) > 0 {
		_, _ = log.File().Write(out)
	}
	return string(out), err
}

// gitOK reports whether a git subcommand exits zero (output discarded). Used for the quiet probes
// (remote get-url, rev-parse) the bash did with `>/dev/null 2>&1`.
func gitOK(repo string, args ...string) bool {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	return cmd.Run() == nil
}

// gitOut returns trimmed stdout of a git subcommand, or "" on error.
func gitOut(repo string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// IsGitRepo reports whether repo is inside a git work tree.
func IsGitRepo(repo string) bool {
	return gitOK(repo, "rev-parse", "--is-inside-work-tree")
}

// HasOrigin reports whether an origin remote is configured.
func HasOrigin(repo string) bool {
	return gitOK(repo, "remote", "get-url", "origin")
}

// SyncFromOrigin brings the checked-out branch up to date with origin BEFORE the job commits, so
// the later push fast-forwards. Ports vault-lib.sh:sync_from_origin exactly:
//   - no origin / detached HEAD / no origin branch yet  → no-op, nil (nothing to reconcile)
//   - fetch fails                                       → WARN, nil (proceed on local state)
//   - local up to date or ahead                         → nil
//   - origin moved ahead                                → fast-forward, nil
//   - histories diverged                                → rebase local commits onto origin
//   - rebase hits a real conflict                       → abort, return error (human reconcile)
func SyncFromOrigin(repo string, log *Logger) error {
	if !HasOrigin(repo) {
		log.Logf("no origin remote — skipping sync.")
		return nil
	}
	branch := gitOut(repo, "symbolic-ref", "--short", "-q", "HEAD")
	if branch == "" {
		log.Logf("detached HEAD — skipping sync.")
		return nil
	}
	if _, err := git(repo, log, "fetch", "origin"); err != nil {
		log.Logf("WARNING: git fetch failed — proceeding on local state (a later push may fail).")
		return nil
	}
	if !gitOK(repo, "rev-parse", "--verify", "-q", "origin/"+branch) {
		log.Logf("origin/%s does not exist yet — skipping sync (push will create it).", branch)
		return nil
	}
	if _, err := git(repo, log, "merge", "--ff-only", "origin/"+branch); err == nil {
		log.Logf("synced: %s is up to date with origin/%s.", branch, branch)
		return nil
	}
	log.Logf("local '%s' diverged from origin/%s — rebasing local commits on top.", branch, branch)
	if _, err := git(repo, log, "rebase", "origin/"+branch); err == nil {
		log.Logf("reconciled: rebased local commits onto origin/%s.", branch)
		return nil
	}
	_, _ = git(repo, log, "rebase", "--abort")
	log.Logf("ERROR: rebase onto origin/%s failed — tree restored, no commits added (see log for cause).", branch)
	log.Logf("       Likely a real conflict (shouldn't happen given disjoint edit paths) or a dirty tree from a")
	log.Logf("       crashed prior run; reconcile by hand, then re-run.")
	return &SyncConflictError{Branch: branch}
}

// SyncConflictError is returned by SyncFromOrigin when a rebase hit a genuine conflict and was
// aborted (the tree is clean, no commits added). Jobs surface this as a failure.
type SyncConflictError struct{ Branch string }

func (e *SyncConflictError) Error() string {
	return "rebase onto origin/" + e.Branch + " failed (real conflict or dirty tree); reconcile by hand"
}

// CommitAndPush stages the given pathspecs (everything if none given), commits only if that
// produced staged changes, and pushes only if an origin remote exists. Ports
// vault-lib.sh:commit_and_push. Returns nil cleanly when there's nothing to commit, the vault
// isn't a git repo, or there's no origin (commit kept local). Returns a non-nil error ONLY when
// the commit fails or the push fails — the latter so callers can surface it loudly (the commit is
// preserved locally; origin is behind).
//
// When a commit lands and rebuild is configured, it fires a best-effort POST to trigger the
// knowledge-site container (see SiteRebuild) — independent of push success, because a bind-mounted
// site reads the local files, not origin. Pass nil to disable.
func CommitAndPush(repo, msg string, pathspecs []string, rebuild *SiteRebuild, log *Logger) error {
	if !IsGitRepo(repo) {
		log.Logf("not a git repo — skipping commit (history left to external sync).")
		return nil
	}
	addArgs := []string{"add", "-A"}
	if len(pathspecs) > 0 {
		addArgs = append(addArgs, "--")
		addArgs = append(addArgs, pathspecs...)
	}
	if _, err := git(repo, log, addArgs...); err != nil {
		log.Logf("ERROR: git add failed — see log.")
		return err
	}
	// `git diff --cached --quiet` exits non-zero when there ARE staged changes.
	if gitOK(repo, "diff", "--cached", "--quiet") {
		log.Logf("no changes to commit.")
		return nil
	}
	if _, err := git(repo, log, "commit", "-m", msg); err != nil {
		log.Logf("ERROR: git commit failed — see log.")
		return err
	}
	log.Logf("committed.")
	// A commit means the vault content changed — tell the site to rebuild (best-effort, before the
	// push, since the bind-mounted site serves local files regardless of whether origin updates).
	rebuild.trigger(log)
	if !HasOrigin(repo) {
		log.Logf("no origin remote — commit kept local.")
		return nil
	}
	if _, err := git(repo, log, "push"); err != nil {
		log.Logf("ERROR: git push failed — origin is NOT updated; local commit(s) are ahead. Resolve and re-run.")
		return &PushError{}
	}
	log.Logf("pushed.")
	return nil
}

// PushError is returned by CommitAndPush when the commit succeeded locally but the push failed.
type PushError struct{}

func (e *PushError) Error() string { return "git push failed; origin is behind local commits" }

// SiteRebuild points CommitAndPush at a running knowledge-site container's POST /rebuild endpoint.
// URL is KNOWLEDGE_SITE_REBUILD_URL; Token (optional) is the shared bearer secret it checks.
type SiteRebuild struct {
	URL   string
	Token string
}

// trigger fires a best-effort POST to the site's /rebuild endpoint. It is deliberately
// non-fatal: a down or misconfigured site must never fail a content job, so every failure is
// logged and swallowed. A nil receiver or empty URL is a no-op (the trigger is unconfigured).
func (r *SiteRebuild) trigger(log *Logger) {
	if r == nil || r.URL == "" {
		return
	}
	req, err := http.NewRequest(http.MethodPost, r.URL, nil)
	if err != nil {
		log.Logf("site rebuild: bad URL %q — skipping (%v).", r.URL, err)
		return
	}
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Logf("site rebuild: POST %s failed (non-fatal): %v", r.URL, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	log.Logf("site rebuild: POST %s -> %s", r.URL, resp.Status)
}
