# Implementation plan: fix audit findings in knowledge-tools

## Context

A full repo audit (committed as `AUDIT.md` on branch `claude/repo-audit-suggestions-0k2ysr`)
found ~60 verified defects and design issues across `service/` (TypeScript vault server),
`cli/` (Go daemon + jobs), `site/` (Quartz publisher), `.github/workflows/`, and the
skills/plugins/template prompt layer. The owner approved fixing **all verified bugs,
robustness gaps, CI/security hardening, and the six rework candidates** — but **NOT** the
new-feature ideas (`kt doctor`, notifications, digest skill, `patch_note`, etc. are out of
scope). Work is delivered as **one PR per phase**, each independently reviewable, in the
order below. This plan is written for autonomous execution by an implementing agent:
every phase lists exact files, the change, tests, and a verification gate.

**Read `AUDIT.md` at the repo root first** — it contains the full finding descriptions
with `file:line` references that this plan assumes.

---

## Global rules for the implementing agent

1. **Branching/PRs.** Base every phase branch on latest `main`
   (`git fetch origin main && git checkout -b <branch> origin/main`). One PR per phase,
   titled with the phase's conventional-commit title. Do not stack phases; if phase N+1
   depends on unmerged phase N, wait or rebase after merge.
2. **Conventional commits are load-bearing, not style.** A merged PR touching `cli/**`
   with a `feat`/`fix` prefix **auto-cuts a CLI release**; one touching `plugins/**`
   auto-cuts a skills release. Use accurate types: `fix(cli):` for CLI bug fixes,
   `fix(service):`, `fix(site):`, `ci:` for workflow-only changes (cuts nothing),
   `docs:` for doc-only, `fix(skills):` for plugin skill changes. Never use `!` or
   `BREAKING CHANGE` — nothing in this plan is breaking.
3. **Never edit `cli/internal/initvault/template/` directly.** Edit `template/` and run
   `make -C cli sync-template`; commit both trees together. CI enforces byte-identity.
4. **Skill/template markdown changes** must pass `python3 scripts/validate_skills.py`
   before push. Plugin manifest changes must pass `claude plugin validate .` if the
   `claude` CLI is available (if not, note that in the PR description).
5. **Respect the instruction-altitude policy** in the repo `CLAUDE.md` ("Where
   instructions live"): tool descriptions get terse rules only; rationale goes in the
   skill; `references/mcp-operations.md` gets I/O shapes only. When you change a rule,
   update the tool description and the skill together.
6. **Do not** refactor beyond what a phase specifies, bump dependencies wholesale,
   change the `inbox/.compile/*` file contract (paths/schemas the service and CLI
   share), or add the out-of-scope features. If a fix seems to require a contract
   change, stop and flag it in the PR instead of improvising.
7. **Verification gates** (run before every push, scoped to what the phase touched):
   - Go: `cd cli && go build ./... && go vet ./... && go test ./...`
     (+ `golangci-lint run` if installed; CI runs it regardless)
   - Service: `cd service && npm ci && npm run build` (+ `npm test` once Phase 3 adds it)
   - Skills: `python3 scripts/validate_skills.py`
   - Template sync: `make -C cli sync-template && git diff --exit-code cli/internal/initvault/template && test -z "$(git status --porcelain cli/internal/initvault/template)"`
   - Workflows: validate YAML parses (`python3 -c "import yaml,sys; yaml.safe_load(open(sys.argv[1]))" <file>`); use `actionlint` if available.
8. In each PR description, list which AUDIT.md findings it closes.

---

## Phase 1 — CI & supply-chain hardening (PR: `ci: harden workflows — SHA-pin actions, fix drift guard, scope version bumps`)

Workflow-only. Do this first: it protects every later PR. Files: all six under
`.github/workflows/`, plus `cli/Makefile`.

1. **Fix the embed-drift guard** (`cli-ci.yml:82-88`): after `make sync-embed`, fail on
   untracked files too. Replace the check with:
   ```sh
   git add -A internal/initvault/template
   if ! git diff --cached --quiet -- internal/initvault/template; then
     echo "::error::embedded template out of sync — run 'make sync-template' and commit"; git status --porcelain; exit 1
   fi
   ```
   Also make the error message say `make sync-template` (the name CLAUDE.md uses).
2. **SHA-pin every action** in all six workflows (`actions/checkout`, `actions/setup-go`,
   `actions/setup-python`, `goreleaser/goreleaser-action`, `docker/*`, devbox action,
   etc.): replace `@vN` with `@<full-40-char-sha> # vN.x.y`, resolving each SHA via
   `mcp__github__get_tag`/`list_tags` against the action repo (or the GitHub API). Pin
   goreleaser itself: replace `version: latest` (`cli-release.yml:129`, `cli-ci.yml:102`)
   with the current stable version string.
3. **Scope the skills version bump** (`package-skills.yml:73-77`): add `-- plugins/` to
   both `git log` invocations so only skill commits drive the bump.
4. **Skip empty skills releases**: in `package-skills.yml:79-88`, mirror
   `cli-release.yml:79-82` — if no `feat`/`fix`/breaking commit matched, set a
   `skip=true` output and gate the packaging/release steps on it (docs/chore-only merges
   cut nothing).
5. **Add a `concurrency` group to `package-skills.yml`**
   (`group: skills-release, cancel-in-progress: false`) and a
   `git fetch --force --tags` immediately before the version compute, mirroring
   `cli-release.yml:26-28,39`. Add `concurrency` groups to `build-service.yml` and
   `build-site.yml` (`group: <name>-${{ github.ref }}`, `cancel-in-progress: true` only
   for PR refs).
6. **`validate-skills.yml`**: add `template/**` to the PR paths filter; add a
   `permissions: contents: read` block.
7. **`build-site.yml`**: drop `packages: write` for PR runs (move permissions to job
   level: PR job gets `contents: read`, push job keeps `packages: write`).
8. **Anchor the breaking-change grep** in both release workflows:
   `grep -qE '^BREAKING[ -]CHANGE:'` instead of `grep -q 'BREAKING CHANGE'`.
9. **Release notes ranges**: add `--notes-start-tag "$latest"` (the previous same-prefix
   tag computed in the plan step) to `gh release create` in both `cli-release.yml:172-175`
   and `package-skills.yml:115-119`, guarded for the first-release case (no previous tag →
   omit the flag).
10. **SIGPIPE flake**: in `package-skills.yml:59,62` and `cli-release.yml:65`, capture
    `git tag -l --sort=-v:refname` into a variable first, then take the first line —
    no `| head` in a pipeline under `set -o pipefail`.
11. **Scheduled image rebuilds**: add `schedule: - cron: '17 5 * * 1'` (weekly) triggers
    to `build-service.yml` and `build-site.yml` so base-image CVE fixes ship.
12. **Service pre-merge CI**: add a `pull_request` job (new workflow `service-ci.yml` or
    extend `build-service.yml`) that runs `npm ci && npm run build` in `service/` on
    `service/**` PR paths. (Phase 3 adds `npm test`; wire the step now as
    `npm test --if-present`.)
13. **`cli-ci.yml`**: remove `site/**` from its paths filters (vestigial — site code is
    not Go; if lint complains, confirm nothing in `cli/` imports site files first).
14. **`cli/Makefile:3`**: change version stamping to
    `git describe --tags --abbrev=0 --match 'cli/v*' 2>/dev/null | sed 's|^cli/||'` with
    a `0.0.0-dev` fallback. Also fix `release-local` (`Makefile:37`): export
    `CLI_VERSION ?= 0.0.0-local` before invoking goreleaser.
15. **`cli-release.yml` recovery path**: in the plan step, if the computed next tag
    already exists but `gh release view <tag>` fails (tag pushed, release creation
    failed), proceed to re-cut the release for that tag instead of skipping.

**Verify**: YAML parses for all changed workflows; `actionlint` clean if available;
grep confirms zero remaining `uses: .*@v[0-9]` lines. Do NOT test by pushing tags.

---

## Phase 2 — CLI daemon & job reliability (PR: `fix(cli): daemon request handling, subprocess timeouts, crash reconciliation`)

The highest-impact phase. Files: `cli/internal/daemon/daemon.go`,
`cli/internal/agent/driver.go`, `cli/internal/vault/git.go`, `cli/internal/jobs/*.go`,
`cli/internal/config/config.go`, `cli/internal/service/systemd.go`, plus tests.

### 2a. Request-sentinel rework (fixes eaten/stranded requests)
Current flow (`daemon.go:214-232`): watcher event → `handleRequest` stats the file →
`runJob` → delete on `ran==true`. Three bugs: `runJob` returns `true` on
`vault.ErrLocked` (`daemon.go:125-133`); a busy-mutex skip is never retried mid-life;
a rewrite during a run is deleted unseen. Rework:

1. Change `runJob` to return a tri-state: `type runResult int` with `ranOK`,
   `ranButLockedElsewhere`, `skippedBusy`. Map: mutex busy → `skippedBusy`; job returned
   `vault.ErrLocked` → `ranButLockedElsewhere`; else → `ranOK` (job errors still count
   as ran — they're logged and shouldn't loop).
2. In `handleRequest`: **consume before running.** Atomically
   `os.Rename(request, request+".consumed")`; if rename fails with `IsNotExist`, return
   (another goroutine took it). Parse overrides from the renamed file
   (`readRequestOverrides`). Run the job. On `skippedBusy` or `ranButLockedElsewhere`,
   rename `.consumed` back to the original name (so it is retried); on `ranOK`, delete
   `.consumed`. This way a fresh request written mid-run survives as a new file.
3. Add a **rescan ticker**: in `watchRequests`'s select loop, add a
   `time.NewTicker(60 * time.Second)` case that iterates `requestJobs` and calls
   `handleRequest` for any request file that exists (also handles a
   leftover `.consumed` older than ~10 minutes: rename it back to the live name first —
   covers a daemon killed mid-request). This is also the safety net for lost inotify
   watches.
4. `watchRequests` currently returns `nil` when the Events/Errors channels close
   (`daemon.go:194-196, 206-208`) — the daemon then exits 0 and systemd's
   `Restart=on-failure` won't restart it. Return an explicit error
   (`errors.New("fsnotify channel closed")`) in both spots.

### 2b. Subprocess supervision (timeouts)
1. New file `cli/internal/vault/execx.go` (or `internal/execx` package): one helper
   `RunCmd(ctx context.Context, timeout time.Duration, name string, args ...string)`
   that wraps `exec.CommandContext` with a derived `context.WithTimeout`, sets
   `SysProcAttr{Setpgid: true}` on unix (guard with build tags; no-op on Windows) and on
   kill signals the process group (`syscall.Kill(-pid, SIGKILL)`), and returns a
   distinguishable timeout error.
2. Thread it through: `agent.Run` (`internal/agent/driver.go:70-84`) — agent runs get
   the timeout from new config knobs `KNOWLEDGE_AGENT_TIMEOUT` (default `2h`) with
   per-job `KNOWLEDGE_COMPILE_TIMEOUT` / `KNOWLEDGE_SYNTHESIZE_TIMEOUT` /
   `KNOWLEDGE_RESOLVE_TIMEOUT` overrides. Add these to `internal/config` following the
   exact pattern of `JobModel`/`JobEffort` (`config.go:305-364`): env > vault yaml
   (`defaults.timeout`, `jobs.<job>.timeout` — extend the yaml decode struct; it is the
   allowlist, keep it minimal) > default. Parse with `time.ParseDuration`; on parse
   failure log a warning and use the default (never fail the job).
3. Give `git` and `gh` invocations (`internal/vault/git.go`, `jobs.go:110-112`,
   `job.go:274`) a fixed 5-minute timeout via the same helper.
4. Document the new knobs in `.env.example` and the README's automation section.

### 2c. Startup reconciliation & crash hygiene
1. New `daemon.reconcile()` called at the top of `Run`, before scheduling:
   - For each job status file (`inbox/.compile/status.json` and the synthesize/resolve
     status files — find their writers in `internal/jobs/compile.go:119` and
     `job.go:135` and reuse those read/write helpers): if `running: true` and the
     recorded PID (add a `pid` field to the status write if absent) is not alive,
     rewrite with `running: false` and a message like `"interrupted (daemon restart)"`.
   - Log an explicit error at startup if the resolved agent binary does not exist /
     is not executable (`exec.LookPath` on the driver's binary) — do not exit; jobs
     will fail with a clear message instead of an opaque 127 an hour later.
2. **Throttled manual compile writes a status** (`compile.go:94-100`): before the early
   return, write status `running:false` with a `"throttled (cooldown active, retry
   after <ts>)"` message so `vault_status` reflects it.

### 2d. Cron validation with graceful degradation
1. In `daemon.Run` (`daemon.go:69-77`): when `cron.AddFunc` fails for a schedule, log
   `"<job>: invalid schedule %q (%v) — falling back to default %q"`, and re-add with
   the job's built-in default (`@hourly` / the two `CRON_TZ=America/Detroit` defaults —
   pull them from `internal/config`'s default constants rather than duplicating). Only
   return an error if the *default* also fails to parse (impossible unless the constant
   is broken).
2. In `InstallCmd.Run` (`cmd/knowledge-tools/main.go:150-173`): parse-validate any
   schedule flags/env being baked into the unit with `jobs.CronParser.Parse` and fail
   the install with a clear message on a bad one (install is interactive; fail-fast is
   right there, unlike the daemon).

### 2e. Unit-file fixes (`internal/service/systemd.go`)
1. **XDG mismatch**: `knowledgeConfigDir()` honors `XDG_CONFIG_HOME` (`:19,27`) but the
   unit template hardcodes `EnvironmentFile=%h/.config/...` (`:45-46`). Render the
   resolved absolute config-dir path into the template (both the `%i.env` and `gh.env`
   lines; keep `%i` for the instance name). Escape `%` as `%%` in the rendered path.
2. **Escaping**: quote `ExecStart` binary path per systemd quoting rules (wrap in
   double quotes, escape embedded `"` and `\`, double `%` → `%%`); in
   `instanceEnvContents`, write values systemd-safe (wrap values containing whitespace,
   `"`, or `\` in double quotes with `\`-escapes — mirror what `util.go:39-48` does for
   launchd XML). Add table-driven tests beside `service_test.go:107`'s launchd ones.
3. **Shared-template ExecStart conflict** (`systemd.go:90`): on install, if the shared
   `knowledge-tools-daemon@.service` already exists with a *different* `ExecStart`
   binary path, print a prominent warning naming both paths before overwriting (full
   per-instance units are out of scope; the warning closes the silent-surprise part).

### 2f. Tests (required — this package was near-untested)
Add to `cli/internal/daemon/daemon_test.go`:
- `handleRequest` consumes on success, retains on busy mutex, retains on `ErrLocked`
  (fake job funcs — if `runJob` calls `jobs.Compile` directly, introduce a small
  function-field seam on the `daemon` struct so tests can stub it).
- Rescan ticker picks up a pre-existing request file and a stale `.consumed`.
- `overdue` cases: zero lastRun, missed tick, future tick, bad schedule.
- Cron-fallback: bad vault schedule logs + registers default (assert via the cron
  entries count).
Add to `agent` tests: `RunCmd` kills a `sleep 60` child (and its subprocess) at timeout.

**Verify**: full Go gate (rule 7). Manual smoke: `go run ./cmd/knowledge-tools daemon`
against a scratch vault (`kt init /tmp/vault-test`), touch
`/tmp/vault-test/inbox/.compile/request`, confirm consume/run/log cycle; write an
invalid schedule into `.knowledge-tools/config.yaml`, restart, confirm fallback log
instead of exit.

---

## Phase 3 — Service correctness + first test suite (PR: `fix(service): result caps, agent-driven REST, session lifecycle, typed errors — and a test suite`)

Files: everything under `service/src/`, `service/package.json`, `service/Dockerfile`.

### 3a. Test harness first
1. Add `vitest` as a devDependency; `"test": "vitest run"` script. (Node's built-in
   `node:test` is acceptable if you prefer zero deps; vitest has better TS ergonomics.
   Pick one and stay consistent.)
2. Write unit tests against a temp-dir vault fixture (create `library/`, `notebook/`,
   `inbox/`, `inbox/.review/`, sample notes in `beforeEach`) for the pure core:
   `confine`/`splitArea` traversal cases, `searchNotes` scoping, `appendToInbox`
   slug/collision, frontmatter parse/strip, `replaceSection`, cooldown math in
   `getVaultStatus`, files-backend review CRUD. Set `VAULT_ROOT` via env before
   importing `config.js` (or refactor config reads minimally if import-order makes this
   impossible — prefer `vi.stubEnv` + dynamic `import()`).
3. Every fix below lands with a test reproducing the bug first.

### 3b. Fixes (each references AUDIT.md for full context)
1. **Cap search/list output**: in `mcp.ts:130-141,186-190` wrap the formatted
   `search_notes` result and the `list_notes` result in `cap()` (exported from
   `vault.ts:99`). Additionally truncate each search snippet line to ~500 chars at the
   formatter so one minified-JSON line can't dominate.
2. **REST agent-driven**: `rest.ts` never imports `AGENT_DRIVEN` (`config.ts:45`). In
   the three trigger routes (`rest.ts:142-163`), when agent-driven is on, respond
   `409 { error: "agent-driven deployment: no daemon consumes triggers; use the MCP
   tools, which return the procedure" }`. (Returning the skill body over REST is a
   contract change — don't.)
3. **Session manager**: replace the bare `transports` record (`index.ts:66-101`) with a
   small class: max 50 sessions, per-session `lastSeen`, sweep on an interval evicting
   sessions idle > 30 min (call `transport.close()`), evict-oldest at cap. When auth is
   enabled, record the token `sub` at initialize and reject other subs with 403.
   Keep the existing 404-reinitialize semantics exactly (the comments at
   `index.ts:88-97` explain why — preserve them).
4. **Typed review errors (rework of the seam)**: define
   `class QuestionNotFoundError extends Error` in a new `service/src/errors.ts`. Throw
   it from the files backend (`vault.ts` question fns) and the GitHub backend
   (`github.ts` — only when the *issue* fetch 404s, not the comment/label calls; a
   label/comment failure after a successful comment POST must surface as a distinct
   `Error` naming the failed step). Replace both `-> 404` regex heuristics
   (`mcp.ts:37-43`, `rest.ts:41-48`) with `instanceof` checks. Delete the regexes.
5. **GitHub `listQuestions` parity** (`github.ts:71`): with no status filter, fetch
   `state=all` and map closed→`applied` so it matches the files backend and the tool
   description ("omit to list all").
6. **REST error handler** (`rest.ts:214-216`): honor `err.status ?? err.statusCode`
   when 4xx (body-parser errors), else 500; always `logger.error({ err }, ...)`.
7. **Host-file validation**: add zod schemas for `status.json`, `schedules.json`,
   `daemon.json` parses (`vault.ts:279,333,348,419-427`) with `.catch()`/safeParse —
   on mismatch, treat as absent + log a warning; never throw from `getVaultStatus`.
8. **`get_note` error fidelity** (`mcp.ts:159-165`): only map `ENOENT`/traversal to
   "Note not found"; other errors return a generic failure message (and log). Mirror
   `rest.ts:88-96`'s classification.
9. **Inbox collision retry** (`vault.ts:230-238`): on `EEXIST`, retry with `-2`, `-3`
   … suffix (max 5) before failing.
10. **Skip dot-dirs in `walkMarkdown`** (`vault.ts:104-138`): skip entries starting
    with `.` (both dirs and files).
11. **Numeric env guards** (`config.ts:4,126`): helper `numFromEnv(name, def)` that
    falls back + warns on NaN/non-positive.
12. **MCP `isError`**: in the shared tool-result helpers (`mcp.ts:29-31,43,51-57`),
    set `isError: true` on failure results.
13. **Ops hygiene**: SIGTERM/SIGINT handler closing the HTTP server + live transports;
    `HEALTHCHECK CMD wget -qO- http://127.0.0.1:${PORT}/healthz || exit 1` in the
    Dockerfile (install nothing — node one-liner via `node -e` is fine); startup
    warning when auth is off (`logger.warn` naming the bind address); read the MCP
    server version from `package.json` instead of hardcoded `'0.1.0'` (`mcp.ts:93`);
    remove the stale `data` line from `.dockerignore` and the stale sqlite comment in
    the Dockerfile.

**Verify**: `npm run build && npm test` green; manual smoke: `VAULT_ROOT=/tmp/vault-test
node dist/index.js`, exercise `/healthz`, `POST /api/v1/inbox`, `GET /api/v1/search`,
and an MCP initialize + `search_notes` via `curl` (see `service/README.md` for shapes);
with `KNOWLEDGE_AGENT_DRIVEN=true`, confirm `POST /api/v1/compile` → 409.

---

## Phase 4 — CLI correctness batch 2 (PR: `fix(cli): lock keying, push retry, catch-up semantics, init/install edge cases`)

Files: `cli/internal/config/config.go`, `cli/internal/vault/git.go`,
`cli/internal/jobs/*.go`, `cli/internal/initvault/initvault.go`,
`cli/cmd/knowledge-tools/main.go`, `template/.gitignore` (+ sync).

1. **Lock keyed by vault path** (`config.go:400-403`): incorporate a short hash of the
   canonical vault path: `vault-<instance>-<first 8 hex of sha256(resolvedRepoPath)>.lock`.
   Keep `KNOWLEDGE_VAULT_LOCK` override behavior. Note in the PR: after upgrade, one
   stale old-name lockfile may remain; harmless.
2. **Push retry when ahead** (`git.go:130-133`): before the "no changes to commit"
   early return, if an `origin` remote exists and
   `git rev-list --count origin/<branch>..<branch>` > 0, attempt the push (reuse the
   existing push + `PushError` code path). Cover with a test beside
   `git_rebuild_test.go`'s fixtures.
3. **Commit-vs-push error fidelity** (`job.go:152-156`): use
   `errors.As(err, &vault.PushError{})` like `compile.go:177-182`; report plain commit
   errors as commit failures in the log + status.
4. **Success-only catch-up** (`jobs.go:83`, `daemon.go:150`): write a per-job
   `last-success-epoch` alongside the existing lock-time `recordRun` stamp
   (compile already has `last-compiled-epoch` — replicate for synthesize/resolve), and
   make `overdue` use last-success. Keep `recordRun` for scheduling snapshots.
5. **First-install storm** (`daemon.go:160-162`): in `overdue`, `lastRun.IsZero()`
   returns true only for `JobCompile`; for synthesize/resolve, a zero lastRun seeds the
   epoch to "now" (write it) and returns false — a fresh vault shouldn't open with a
   whole-corpus opus pass.
6. **Archive rename errors** (`compile.go:168`): log failures
   (`log.Printf("archive %s: %v", ...)`) instead of discarding.
7. **`--cooldown 0`** (`main.go:180`): use kong's `IsSet("cooldown")` (or a pointer
   type) so an explicit 0 disables the throttle.
8. **`errors.Is` for `ErrLocked`** (`main.go:371` `ignoreLocked`).
9. **`cron.WithLocation`**: pass the system local location explicitly
   (`cron.New(cron.WithLocation(time.Local), ...)`) and document in README that
   unprefixed schedules use the daemon's local TZ — recommend `CRON_TZ=` prefixes.
10. **Windows-safe init** (`initvault.go:115`): wrap the `.claude/skills` symlink in a
    best-effort: on error (any OS), print
    `"note: could not create .claude/skills symlink (%v) — link it manually or use .agents/skills"`
    and return nil.
11. **Template `.gitignore`**: change `outputs/compile-logs/` to `outputs/*-logs/`
    (synthesize/resolve transcripts are being committed to vaults today). Update the
    stale "Nightly compile" comment to "Job run logs". Run `make -C cli sync-template`.
12. **Install transparency**: in `InstallCmd.Run`, print each `KNOWLEDGE_*` value being
    baked into the unit env and its source (flag vs environment), so ambient exported
    vars aren't silently persisted.

Tests: lock-name derivation; overdue seeding behavior; push-when-ahead (temp git repos —
follow existing patterns in `vault_test.go` / `git_rebuild_test.go`); `--cooldown 0`.

**Verify**: full Go gate + template-sync gate.

---

## Phase 5 — Site publishing & server hardening (PR: `fix(site): symlink-flip publishing, queued rebuilds, Dockerfile + serve hardening`)

Files: `site/build.sh`, `site/serve.mjs`, `site/Dockerfile`, `site/README.md`.

1. **Symlink-flip publish** (`build.sh:43-46`): build into `/srv/builds/site-$$.tmp`,
   then atomically repoint: create symlink `/srv/builds/current -> site-<n>` via
   `ln -sfn` + `mv -T` of a temp symlink; `SITE_OUT` (what `serve.mjs` reads) becomes
   the symlink path. Keep exactly one previous build dir for rollback; delete older.
   Update `entrypoint.sh` if it pre-creates `/srv/site`. In `serve.mjs`, resolve the
   symlink per-request (`realpath` the root once per request or per-build event) and
   keep the containment check against the *resolved* root.
2. **Queued rebuilds** (`serve.mjs:106`): add a `pending` flag. If a `POST /rebuild`
   arrives while `building`, set `pending = true`, respond 202
   `"queued behind in-progress build"`. When a build finishes, if `pending`, clear it
   and run one more build.
3. **serve.mjs hardening**:
   - Wrap `decodeURIComponent` (`:70`) in try/catch → 400.
   - Use `lstat`-based resolution or verify `realpath(file)` stays under the resolved
     root before serving (closes symlink escape, `:81-90`).
   - Cache headers: `Cache-Control: no-cache` on `.html` (and `/`),
     `Cache-Control: public, max-age=31536000, immutable` on hashed `static/` assets,
     `Last-Modified` from the stat you already have.
   - `X-Content-Type-Options: nosniff` on all responses.
   - `req.resume()` before responding on `/rebuild`.
4. **Dockerfile**:
   - Reorder: `npm ci` BEFORE `COPY quartz.config.ts quartz.layout.ts` (`:24-25`) so
     config tweaks don't re-run the install under QEMU.
   - Pin Quartz to a commit SHA: `git init + git fetch --depth 1 origin <sha> +
     git checkout FETCH_HEAD` with the tag noted in a comment (`:11,22`). Resolve the
     SHA for the currently-pinned `v4.5.2` tag.
   - `chown` only the Quartz cache dir (find where `.quartz-cache` lands; set it
     explicitly if configurable) + `/srv`, not all of `/opt/quartz` (`:33`).
   - Add `HEALTHCHECK` hitting `/` (or add a `/healthz` route in serve.mjs first —
     do that: `GET /healthz` returning `{ok, lastBuild}`).
5. **Base-URL warning**: in `build.sh`, if `KNOWLEDGE_SITE_BASE_URL` is unset, log a
   prominent warning that RSS/sitemap URLs will be wrong (`quartz.config.ts:29`
   falls back to `example.com`).
6. **README**: document the existing `draft: true` per-note privacy
   (`Plugin.RemoveDrafts`), the new healthz, and the queued-rebuild semantics.

**Verify**: `docker build site/` succeeds (or, without docker, `bash -n` both scripts +
`node --check serve.mjs`); if docker available, run the container against
`/tmp/vault-test`, hit `/`, POST two rapid `/rebuild`s and confirm the second is queued
and a second build runs.

---

## Phase 6 — Skills, plugins, template, docs (PR: `fix(skills): de-personalize plugins, reconcile agent-driven + task-capture contradictions`)

⚠️ This PR cuts a skills release on merge, and the `mcp.ts` edits ship only when the
service image is rebuilt/redeployed — say both in the PR description. Files:
`plugins/vault/skills/knowledge-vault/SKILL.md`,
`plugins/vault/skills/knowledge-vault/references/mcp-operations.md`,
`plugins/auto-capture/skills/auto-capture/SKILL.md`, both `plugin.json`s,
`service/src/mcp.ts`, `template/CLAUDE.md`, `template/.agents/skills/*/SKILL.md`,
`template/.claude/settings.json`, `README.md`, `scripts/validate_skills.py`.

1. **De-personalize both plugin skills**: replace every "Joe"/"Joe's" with "the user"/
   "the vault owner", "homelab" with "the vault host", drop the TaskNotes/`tasks/index`
   references (see item 3), and change "This interface (claude.ai)" to "This interface
   (the chat surface this skill is loaded in)". Keep meaning, keep the skills'
   trigger-matching descriptions intact. Also `mcp-operations.md:3`: "connected as a
   custom MCP connector (claude.ai, Claude Code, or stdio)".
2. **Agent-driven awareness**: add one short section to `knowledge-vault/SKILL.md` near
   the compile/synthesize guidance: *"If `compile_run`/`synthesize_run`/`resolve_run`
   returns a procedure instead of a confirmation, this deployment has no host daemon —
   you are the runner: follow the returned procedure now."* Rewrite the "Never run the
   synthesis yourself" sentence (`:152-153`) to be conditional on a daemon-backed
   deployment. Add the agent-driven output shapes of the three `*_run` tools and
   `vault_status`'s null-timing caveat to `mcp-operations.md` (shapes only — copy the
   exact strings from `mcp.ts:224-230,269-274,296-301,332-335`).
3. **Close the TODO/task gap**: in `mcp.ts:201-202` (`append_to_inbox` description),
   soften the promise to match reality: "lead the text with `TODO:` so the compiler can
   file it as actionable (how it's filed depends on the vault's compile skill)". In
   `template/.agents/skills/compile-inbox/SKILL.md` + `template/CLAUDE.md`, add a
   minimal rule: a capture starting `TODO:` is filed as a checklist item in a
   `notebook/actions.md` note (create if absent) rather than a library note. Trim the
   TaskNotes-specific promises from `knowledge-vault/SKILL.md:96-102,136-138` to the
   same neutral behavior.
4. **Sensitivity carve-out at the canonical altitude**: add one clause to
   `append_to_inbox`'s description (`mcp.ts:198-200`): "Exception: for secrets or
   clearly sensitive personal data, ask before capturing." Keep the auto-capture
   skill's longer treatment (rationale) as-is, now deferring to the tool rule.
5. **Altitude dedup**: remove the notebook-tentative sentence from the server
   `instructions` (`mcp.ts:99-101`) — it stays in the `search_notes` description only.
   Fix `mcp-operations.md`'s exact shapes: `list_questions` line format incl. the `- `
   bullet, `N question(s):` header, trailing get_question hint (`mcp.ts:368-371`);
   `search_notes` `N match(es) for "…":` header (`mcp.ts:140`); delete the
   `## Tasks` block claim (`:41`) and the restated never-searches-tasks rule (`:32`).
6. **Single-source the commit/archive contract**: add a short "Who runs git / who
   archives" table to `template/CLAUDE.md` (scheduled run: wrapper commits+pushes
   immediately and archives; interactive run: the agent archives processed captures
   itself, commit left to the user). Replace the four divergent closing paragraphs in
   `template/.agents/skills/{synthesize,resolve,synthesize-files,resolve-files}/SKILL.md`
   with one consistent sentence referring to that section, and fix
   `compile-inbox/SKILL.md:58`'s unconditional "do not move anything in inbox/" to
   defer to the same table. Remove the false "so I can review first" rationale.
7. **Template fixes**: `template/.claude/settings.json:3` `"defaultMode": "auto"` →
   `"acceptEdits"`. Fix the CommonMark numbering in `template/CLAUDE.md:74-114`
   (indent sub-bullets under their numbered steps, mirroring `compile-inbox/SKILL.md`).
8. **README**: fix `knowledge-tools {compile,synthesize,resolve}` →
   `knowledge-tools job {compile,synthesize,resolve}` (`README.md:208-209`); mention the
   `synthesize-files`/`resolve-files` variants where the skills are enumerated
   (`README.md:52-53,139-141`).
9. **Soft de-hardcoding**: "hourly" → "on a schedule (hourly by default)" in
   `knowledge-vault/SKILL.md:149`; `mcp.ts:232-233` "Rate-limited to one manual compile
   per hour" → "cooldown-throttled". Remove "The tool enforces the rules" overclaim
   (`SKILL.md:75-76`) → "the tool description carries the rules".
10. **auto-capture degradation line**: add to `auto-capture/SKILL.md`: "If the
    `append_to_inbox` tool is not available (vault plugin not installed or connector
    down), tell the user once and stop attempting captures." Check description length —
    it is at 1022/1024 chars; if your edits push it over, shorten elsewhere in the
    description first.
11. **`plugin.json`s**: add a `"version"` field to both (start `"0.1.0"`).
12. **Validator upgrades** (`scripts/validate_skills.py`): add (a) warn when
    description > 900 chars; (b) error on unknown frontmatter keys outside an allowlist
    (`name, description, allowed-tools, model, disable-model-invocation, argument-hint,
    license, metadata`); (c) error on empty body after frontmatter; (d) strip a UTF-8
    BOM before the frontmatter regex; (e) verify that relative paths referenced in a
    SKILL.md body (regex for `](./...)` / `references/...`) exist under the skill dir;
    (f) validate `.claude-plugin/marketplace.json` + each plugin's `plugin.json`: valid
    JSON, `source` dirs exist, plugin `name` matches marketplace entry, every
    `skills/<name>/SKILL.md` under a plugin parses. Keep exit codes: warnings don't
    fail CI, errors do.
13. Run `make -C cli sync-template` (template changed).

**Verify**: `python3 scripts/validate_skills.py` (with the new checks); template-sync
gate; `claude plugin validate .` if available; `cd service && npm run build` (mcp.ts
touched); re-read `AUDIT.md`'s altitude-policy findings and confirm each is closed.

---

## Phase 7 — Release versioning consolidation (PR: `ci: single shared version-bump script for skills + cli releases`)

Rework of the duplicated bump engines — **shared script, not release-please** (a
release-please migration changes the whole release flow and can't be safely validated
by an autonomous agent without cutting real releases; the shared script preserves
observable behavior).

1. New `scripts/next-version.sh <tag-prefix> <pathspec> [--pre-1.0] <before> <after>`:
   - `git fetch --force --tags` first.
   - Latest tag = `git tag -l "<prefix>v*" --sort=-v:refname` first line (no pipeline
     head); validate strict `vX.Y.Z` semver, error otherwise.
   - Commits = `git log --format='%s%n%b' <before>..<after> -- <pathspec>` (fallback
     to `-n1` when before is all-zeros).
   - Bump: breaking (title `!` after type/scope, or body line `^BREAKING[ -]CHANGE:`) →
     major (or minor under `--pre-1.0`); `^feat(\(|:|!)` → minor; `^fix(\(|:|!)` →
     patch; none → print `skip` and exit 0.
   - Output `next=<prefix>vX.Y.Z` + `previous=<tag>` (for `--notes-start-tag`) to
     `$GITHUB_OUTPUT` when set, stdout otherwise.
2. Add bats-style or plain-bash self-tests: `scripts/next-version-test.sh` running the
   script against a scratch git repo covering: first release, feat, fix, chore-only
   (skip), breaking pre-1.0, out-of-scope commit ignored by pathspec. Wire it into
   `validate-skills.yml` or a tiny new workflow.
3. Replace the inline compute in `package-skills.yml` (prefix `skills/`, pathspec
   `plugins/`) and `cli-release.yml` (prefix `cli/`, pathspec `cli/`, `--pre-1.0`) with
   script calls. Keep everything downstream (goreleaser env dance, zip packaging,
   `gh release create`) unchanged.
4. Confirm Phase 1's semantics carried over (skip-on-none for both, scoping, anchored
   breaking-change grep).

**Verify**: run the self-test script locally; YAML parses; diff the workflow logic
against the pre-change behavior notes in the PR description.

---

## Phase 8 — Service search index (PR: `feat(service): indexed search behind the same tool contract`)

Do last — it's the only phase with real design freedom, and everything else is
independent of it.

1. New `service/src/search-index.ts`: an in-memory inverted index over
   `library/` + `notebook/` built by reusing `walkMarkdown` (`vault.ts:104`). Per doc:
   path, title (first `# ` heading or filename), tokenized lowercase terms (split on
   non-alphanumerics, drop length-1) from title + body.
   Query: AND across query tokens (fall back to the current substring scan when the
   query contains no indexable token, e.g. all-CJK or punctuation); rank = title-hit
   bonus + term frequency; return the same `SearchHit` shape `searchNotes` returns
   today (`vault.ts:192-223`) with snippets extracted from the stored line offsets.
2. Staleness: keep a per-file mtime map; on each search, `stat` the area roots' dirs is
   not enough — re-stat indexed files lazily in bulk at most once per 30s
   (`Date.now()` throttle), re-index changed/new/deleted files only. Full rebuild on
   first query after boot.
3. `searchNotes` delegates to the index; keep its signature and the scope/maxHits
   semantics identical so `mcp.ts`/`rest.ts` don't change. Existing Phase 3 tests must
   pass unchanged; add index-specific tests (ranking, AND semantics, staleness after a
   file write, fallback path).
4. Do NOT add sqlite/FTS5 or any new dependency — in-memory is sufficient at
   personal-vault scale and keeps the npx stdio path lightweight.

**Verify**: `npm test`; manual: seed 50 notes in `/tmp/vault-test`, compare result
relevance for a two-term query before/after (title matches should now rank first).

---

## Explicitly out of scope (do not implement)

`kt doctor` / `kt logs` / `kt status --json` / `--dry-run` / notification hooks /
retention knobs / `--focus` / `patch_note` / batch capture / SSE-long-poll /
vault-health + weekly-digest skills / `kt list` multi-vault / release-please migration /
config-resolution `Resolved(job)` refactor (deferred: entangled with the out-of-scope
`doctor`; the config edits in Phase 2b are the only config changes allowed) / any
`inbox/.compile/*` schema change.

## Suggested execution order & dependencies

1 (CI) → 2 (daemon) → 3 (service fixes+tests) → 4 (CLI batch 2) → 5 (site) →
6 (skills/docs) → 7 (version script; depends on Phase 1's workflow shape) → 8 (search;
depends on Phase 3's test harness). Phases 4-6 are mutually independent and can be
reordered if a PR is blocked on review.

## Final verification (after all PRs merge)

- CI green on `main`; a skills release and a CLI release were cut with correct versions
  (check the two most recent tags' semver deltas match the merged commit types).
- `kt init /tmp/fresh && kt install /tmp/fresh` on a scratch host/container: daemon
  starts, no immediate synthesize storm, `touch inbox/.compile/request` compiles once,
  `kill -9` the daemon mid-compile then restart → status shows "interrupted", not
  running.
- Service: `npm test` green; agent-driven REST returns 409; a 200k-char note line no
  longer explodes `search_notes`.
- Site container: two rapid rebuild POSTs → two builds; `readlink /srv/.../current`
  flips atomically.
