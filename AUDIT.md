# knowledge-tools — full repo audit (2026-07-06)

Four parallel deep-dive reviews (service, CLI/daemon, skills/plugins/template, site/CI)
plus a cross-cutting synthesis. Every high-severity finding below was verified against
the code at `25449e6`, not just pattern-matched. Findings carry severity + `file:line`.

**Overall verdict:** the architecture is genuinely good — the dumb-capture/smart-compile
split, the two-protocol shared-core service, the harness-neutral agent drivers, the
sentinel-file host contract, and the allowlist-by-struct vault config are all
well-reasoned and clearly deliberate. The weaknesses cluster in four themes, none
architectural:

1. **Unattended-loop reliability.** This system's whole point is running while you
   sleep, and that's exactly where the gaps are: no timeout anywhere around the agent
   subprocess, on-demand compile requests that get eaten or stranded, `status.json`
   stuck at `running:true` after a crash, and the site silently dropping the last
   rebuild of a burst.
2. **Testing.** `service/` has zero tests and no pre-merge CI; `internal/daemon` — the
   riskiest Go package — tests almost nothing. Both daemon request bugs below would
   have been caught by a unit test.
3. **Drift between the layers you designed to stay in sync.** The skill contradicts the
   tools in agent-driven mode, `references/mcp-operations.md` has drifted from `mcp.ts`
   four ways, README documents a CLI invocation that doesn't exist, and the two release
   workflows implement the same version-bump with three semantic divergences.
4. **Shipping personal artifacts publicly.** The marketplace plugins are marketed
   generally but hard-code "Joe", "homelab", TaskNotes, and "this interface (claude.ai)".

---

## Top 10 fixes, ranked

1. **[CLI] Add a timeout/watchdog around agent, `git`, and `gh` subprocesses.**
   `agent.Run` (`cli/internal/agent/driver.go:70-84`) is a bare `cmd.Run()` with only
   the daemon-shutdown ctx. A claude/codex process stalled on network holds the flock,
   the daemon mutex, and every future tick — a silently dead vault until you notice.
   Same for `gh auth status` (run every compile) and `git fetch/push`. One
   `runCmd(ctx, timeout, …)` helper + `KNOWLEDGE_AGENT_TIMEOUT` (per-job override)
   fixes it coherently. Also kill the process *group* (`Setpgid`), not just the child.
2. **[CLI] Fix the two on-demand-request consumption bugs**
   (`cli/internal/daemon/daemon.go`):
   - `runJob` returns `ran=true` even on `vault.ErrLocked` (line 125-133 vs the
     doc comment at 106-110), so `handleRequest` deletes the request file for a compile
     that never ran.
   - A request skipped mid-life because a scheduled job held the mutex is kept but
     never retried — the startup drain runs once; no future fsnotify event exists.
   Rework: rename request → `.consumed` *before* running (so a mid-run rewrite
   survives as a fresh file), return tri-state from `runJob`, and add a slow-ticker
   rescan of the dir as a catch-all (~30 lines, also covers lost inotify watches).
3. **[CLI] `XDG_CONFIG_HOME` mismatch bricks the daemon in a restart loop.**
   `knowledgeConfigDir()` honors `XDG_CONFIG_HOME` (`systemd.go:19,27`) but the unit
   template hardcodes `EnvironmentFile=%h/.config/knowledge-tools/%i.env`
   (`systemd.go:45`). On any host where they differ, the required env file is missing
   and `Restart=on-failure` loops every 10s forever.
4. **[CLI] Validate cron strings early; degrade instead of crash-looping.** A bad
   schedule in the vault-committed `.knowledge-tools/config.yaml` makes `cron.AddFunc`
   fail, the daemon exit non-zero, and the unit restart-loop (`daemon.go:69-77`,
   `config.go:284`). Since the headless agent runs `acceptEdits` *inside the vault*, a
   synthesize pass touching that yaml can take down its own scheduler. Parse-validate
   at `loadVaultConfig`/`install`, warn + fall back to the default on failure.
5. **[CI] The embed-drift guard misses *added* template files.**
   `git diff --quiet -- internal/initvault/template` (`cli-ci.yml:84`) doesn't see
   untracked files, so a new `template/.agents/skills/<x>/SKILL.md` merged without
   `make sync-template` passes CI and the next CLI release seeds vaults *without* it.
   Also check `git status --porcelain` output.
6. **[CI] SHA-pin actions in the release workflow (and everywhere).** `cli-release.yml`
   uses tag-pinned actions + `version: latest` goreleaser while holding
   `HOMEBREW_TAP_GITHUB_TOKEN`. A compromised action tag = malicious cask pushed to the
   tap = RCE on every `brew install` (the cask even strips quarantine post-install).
   SHA-pin + Dependabot/Renovate; confirm the tap token is a fine-grained PAT scoped to
   `homebrew-tap`/contents-only.
7. **[service] REST trigger routes ignore agent-driven mode.**
   `POST /api/v1/compile|synthesize|resolve` (`rest.ts:142-163`) call
   `triggerCompile`/`triggerJob` unconditionally — `rest.ts` never imports
   `AGENT_DRIVEN`. On the documented `KNOWLEDGE_AGENT_DRIVEN=true` deployment a script
   gets `{status:"triggered"}` and writes a sentinel no daemon will ever consume.
   Return the instruction payload or an explicit 409.
8. **[service] Cap `search_notes`/`list_notes` output.** `cap()` wraps `getNote`,
   `readIndex`, `getQuestion`, `skillInstruction` (`vault.ts:164,174,611,727`) but the
   search formatter and notes list return raw (`mcp.ts:130-141,186-190`). One pasted
   minified-JSON line in a note → multi-megabyte tool result the connector rejects.
9. **[skills] De-personalize the shipped plugins.** `plugins/vault/skills/knowledge-vault/SKILL.md`
   and `auto-capture` name "Joe", "homelab", TaskNotes, and assert "This interface
   (claude.ai)" — while the README markets `/plugin install vault@knowledge-tools`
   generally. Use "the user"/"the vault host"; the server's `vault_name` already exists
   for labeling.
10. **[CLI] Startup reconciliation.** After a crash/SIGKILL mid-job, `status.json`
    stays `running:true` with nothing to clear it — `vault_status` reports a phantom
    in-flight compile for up to an hour (or a week for synthesize). At daemon start:
    `running:true` + no live PID → rewrite as `"interrupted"`; also verify the agent
    binary exists (the default `~/.local/bin/claude` silently 127s today).

---

## service/ (TypeScript)

### Correctness

- **HIGH — no tests, no pre-merge CI.** No test files, no `test` script, and
  `build-service.yml` has no `pull_request` trigger — `tsc` runs only inside the
  post-merge Docker build. A type error merges green, the image build fails, and the
  homelab keeps pulling stale `:latest` silently. The core (`confine`, `splitArea`,
  frontmatter parsers, `replaceSection`, cooldown math, GitHub status mapping) is pure
  and trivially unit-testable. Highest-leverage single improvement in the repo.
- **HIGH — uncapped search/list results** (see Top 10 #8).
- **HIGH — REST ignores agent-driven mode** (see Top 10 #7).
- **MED — MCP session map leaks** (`index.ts:66-85`): `transports` grows unboundedly;
  eviction only on explicit close. Abandoned claude.ai sessions leak a transport +
  server pair each; with authless default, anyone on the LAN can spam `initialize` to
  exhaust memory. Replace with LRU + idle TTL + max count (+ bind sessions to the
  authenticated `sub` when auth is on).
- **MED — GitHub backend `listQuestions()` with no filter omits `applied`**
  (`github.ts:71` maps unfiltered → open-only) while the files backend lists all three
  and the tool description promises "omit to list all". Parity break + doc lie.
- **MED — the `-> 404` not-found regex heuristic** (`mcp.ts:37-43`, duplicated
  `rest.ts:41-48`) misclassifies partial failures: in `answerQuestion` the comment POST
  can succeed and the label POST 404 — reported as "Question not found", inviting a
  duplicate-comment retry. Replace with typed errors (`QuestionNotFound`) thrown by the
  backends.
- **MED — REST catch-all maps every error to 500** (`rest.ts:214-216`) including
  `express.json()`'s 400/413s, and never logs the error.
- **MED — unvalidated host-file parses**: a malformed `cooldown_seconds` in
  `status.json` makes `vault_status` throw `RangeError` (`vault.ts:419-427`); all
  `JSON.parse … as X` casts share this. zod is already a dependency — use it at the
  host-file and REST-body boundaries.
- **LOW —** `get_note` swallows EACCES/EIO as "Note not found" (`mcp.ts:159-165`, the
  exact thing `rest.ts:88-96`'s comment forbids); inbox filename collision throws
  EEXIST instead of suffix-retrying (`vault.ts:230-238`) — capture should never fail;
  `walkMarkdown` recurses into dot-dirs so `library/.trash` resurfaces deleted notes
  (`vault.ts:104-138`); `PORT=""` → random port and `MAX_RESULT_CHARS=abc` → NaN →
  every result truncated to empty (`config.ts:4,126`); MCP errors returned as success
  text without `isError: true`; no SIGTERM handler; no Dockerfile `HEALTHCHECK`
  despite `/healthz` existing; MCP server version hardcoded `'0.1.0'` (`mcp.ts:93`).

### Security

- **HIGH (posture) — authless default + `0.0.0.0` bind**: a bare `docker run -p` gives
  the LAN read/write vault access. At minimum log a loud startup warning when auth is
  off and the bind is non-loopback; better, default bind to `127.0.0.1`.
- **MED — no rate limiting**; and `trust proxy: true` unconditionally means future
  IP-keyed limiting is spoofable when directly reachable.
- **LOW —** `confine()` is prefix-based, no realpath — symlinks written into the vault
  escape (prompt-injection→file-disclosure chain only); GitHub PAT sent to a
  configurable `GITHUB_API_URL` with no scheme/host sanity check; auth debug log emits
  token claims; REST reflects raw internal error strings (paths) to clients.
- **Verified fine:** path traversal confinement, slug safety, fail-fast on
  half-configured auth/GitHub, read/write scope split, atomic tmp-file writes,
  comment-before-label ordering.

### Rework candidates

- **Session store** → small session manager (LRU/TTL/cap/owner) — closes three findings.
- **Review-backend seam** → an explicit `ReviewBackend` interface with typed errors,
  killing the duplicated regex heuristic and the list-all divergence.
- **Search** (`vault.ts:192-223`) → read-every-file substring scan is fine at 200
  notes, wrong at 5,000, and feature-poor (no ranking, no AND, no title boost). An
  in-memory inverted index or `node:sqlite` FTS5 rebuilt on mtime check keeps the tool
  contract and transforms retrieval — this is the vault's core read path.
- Keep: two-protocol/shared-core, sentinel contract, auth layer — all sound.

---

## cli/ (Go)

### Correctness

- **HIGH —** timeouts (Top 10 #1), request consumption (Top 10 #2), XDG mismatch
  (Top 10 #3), cron crash-loop (Top 10 #4).
- **MED — lock keyed by instance, not vault path** (`config.go:400-403`):
  `--instance foo` + the default daemon on the *same* vault → two agents + two
  concurrent `git commit`s on one repo. Key on (instance, canonical vault path).
- **MED — push never retried on no-change runs** (`git.go:130-133`): after a
  `PushError` (commit landed, push failed), every later run with no new changes exits
  at "no changes to commit" before the push — origin stays behind indefinitely. If
  local is ahead of `origin/<branch>`, push even with nothing to commit.
- **MED — `RunIssueJob` reports any commit error as "push failure"**
  (`job.go:152-156`); compile already does the `*vault.PushError` type assertion —
  mirror it.
- **MED — `Makefile:3` version stamping matches any tag** including `skills/v*` —
  `git describe --match 'cli/v*'` — and **`make release-local` is broken** (goreleaser
  templates require `CLI_VERSION`, which the target never sets).
- **MED — multi-instance installs fight over the shared systemd template's
  `ExecStart`** (`systemd.go:90`): installing instance B from a different binary path
  silently rewrites what instance A execs. Also no quoting/escaping in the unit or env
  file (paths with spaces/`%`; values with quotes/backslashes) — launchd got XML
  escaping, systemd didn't.
- **MED — `kt init` on Windows fails partway** at the `.claude/skills` symlink
  (`initvault.go:115`); make it best-effort with a printed fallback.
- **LOW —** `--cooldown 0` silently ignored (`main.go:180`); daemon exits 0 if the
  watcher channel closes (scheduling silently stops, and `Restart=on-failure` won't
  resurrect an exit-0); no `cron.WithLocation` (unprefixed schedules run in the
  process TZ — UTC under systemd); archive `os.Rename` errors swallowed
  (`compile.go:168` — re-compiled duplicates); `ignoreLocked` uses `==` not
  `errors.Is`.

### Robustness

- **HIGH — stale `running:true` after crash** (Top 10 #10).
- **MED — fresh install immediately fires a full synthesize**: `overdue` treats
  never-ran as overdue (`daemon.go:160-162`), so first install runs
  compile+resolve+synthesize back-to-back on a possibly-empty corpus. Seed last-run
  epochs at install, or treat zero-lastRun as not-overdue for the issue jobs.
- **MED — `template/.gitignore` only ignores `outputs/compile-logs/`** while
  `RunIssueJob` writes `outputs/synthesize-logs/` and `outputs/resolve-logs/`
  (`job.go:69`) — and compile's unscoped `git add -A` then commits full agent
  transcripts to the vault forever. Fix the seed to `outputs/*-logs/`. No
  rotation/pruning for logs or `inbox/archive/` — unbounded growth.
- **MED — `recordRun` stamps at lock acquisition** (`jobs.go:83`), so a job that
  crashes still counts as "ran" for catch-up — a synthesize that dies every Sunday is
  never caught up. Compile already has a success-only epoch; use the same distinction.
- **MED — throttled manual compile writes no status** (`compile.go:94-100`) — the MCP
  caller can't distinguish "throttled" from "nothing happened".
- **LOW —** no failure notification of any kind (journald only); `install` silently
  bakes ambient exported `KNOWLEDGE_*` env into the unit — print what's being
  persisted; macOS `daemonActive` via `launchctl print` reports a crash-looping agent
  as healthy.
- **Verified fine:** argv injection (none — prompts/models/grants travel as single argv
  elements, instance is regex-gated), Windows flock semantics, the embed-symlink
  workaround.

### Design / tests

- **Config layering** is centralized per-dimension (good) but the "raw `Config.*Schedule`
  holds only the operator override" invariant is enforced nowhere by type, the vault
  tier is a stringly `map[string]string` keyed by env names, and `InstallCmd.Run`
  mutates cfg as a fourth layering site. A single `Resolved(job)` struct with a
  per-field `source` would also power `kt doctor` and `--dry-run`.
- **Test gaps, ranked:** `internal/daemon` (riskiest, near-untested — both request bugs
  were catchable), `internal/service` install/uninstall flows (fake `systemctl` on
  PATH), `cmd/` (zero tests), `SyncFromOrigin` diverged/conflict branches,
  `agent.Run` cancellation.
- **Embed sync:** make `make build` depend on `sync-template` (or `//go:generate`) so
  the manual step disappears; keep the CI byte-identical guard.

---

## Skills, plugins, template

- **HIGH — personalization** (Top 10 #9).
- **HIGH — the `TODO:` task promise is false on template-seeded vaults.**
  `append_to_inbox`'s description (`mcp.ts:201-202`) and the knowledge-vault skill
  promise TODO captures become tasks, but `template/compile-inbox` and template
  CLAUDE.md have zero action-capture handling (the seed defers `tasks/`). Either make
  the tool clause conditional/configurable or add a minimal action rule to the seed
  ("a `TODO:` capture becomes a notebook 'actions' entry").
- **HIGH — the skill contradicts agent-driven mode.** `knowledge-vault/SKILL.md:152-153`
  ("Never run the synthesis yourself") vs the agent-driven tools returning the
  procedure "for YOU to carry out now". Add one paragraph: "if a `*_run` tool returns a
  procedure instead of a confirmation, you are the runner."
- **MED — altitude-policy violations** (your own rule, "Where instructions live"):
  the notebook-tentative rule is stated in *both* server instructions (`mcp.ts:99-101`)
  and the `search_notes` description (`mcp.ts:118-121`) — same-altitude duplication in
  every turn's context; `references/mcp-operations.md` drifted four ways (no
  agent-driven output shapes; asserts `list_index` includes a `## Tasks` block — that's
  your vault's content, not an invariant; wrong `list_questions`/`search_notes` line
  formats; restates a rule against its own charter).
- **MED — the sensitivity carve-out lives only in the auto-capture skill**
  (`SKILL.md:82-87`) while the canonical rule home, `append_to_inbox`'s description,
  says unconditionally "when in doubt, capture" — callers without that skill loaded are
  affirmatively told to capture secrets. One clause in the tool description.
- **MED — four synthesize/resolve skills disagree about who commits**, and
  `synthesize`'s "leave the commit to me so I can review first" is false in the
  scheduled path (the wrapper commits+pushes immediately). Archiving is specified in
  three mutually-patching places — an interactive `/compile-inbox` run (the symlink
  exists to allow it) is forbidden from archiving and no wrapper runs, so the next
  compile reprocesses everything. Single-source a "who runs git / who archives, per
  invocation mode" table in template CLAUDE.md.
- **MED — `template/.claude/settings.json:3` `"defaultMode": "auto"`** is not a
  documented permission mode (likely meant `acceptEdits`).
- **MED — README documents `knowledge-tools {compile,synthesize,resolve}`**
  (`README.md:208-209`) — the real commands are `kt job {…}`.
- **LOW —** broken CommonMark numbering in template CLAUDE.md's compile steps;
  hard-coded "hourly"/"one per hour" facts for configurable knobs; "The tool enforces
  the rules" overclaim; `mcp-operations.md:3` says "claude.ai" but ships to Claude Code
  and stdio; template `.gitignore` "Nightly compile" comment is stale; README omits the
  `-files` skill variants from its enumerations; `plugin.json`s lack `version`;
  auto-capture has no "if `append_to_inbox` isn't available, say so once and stop"
  degradation line; `auto-capture`'s description is 1022/1024 chars — one edit from CI
  failure with no warning.
- **Validator gaps:** no manifest validation in CI (`claude plugin validate` is
  manual-only — a typoed `source` ships silently on `/plugin update`); no
  description-length headroom warning; no unknown-frontmatter-key warning
  (`allowed_tools` typo ships inert); no relative-link existence check; frontmatter-only
  bodies pass; BOM yields a misleading error.

---

## site/

- **MED — rebuild coalescing drops content** (`serve.mjs:106`): a POST during a build
  returns 202 and is forgotten — compile commits, build starts, synthesize commits,
  its POST is dropped, site stays stale until the *next* commit (hours/days). Add a
  one-slot `pending` flag → run one more build after the current finishes.
- **MED — the "atomic" swap isn't** (`build.sh:43-46`): between the two `mv`s there is
  no `/srv/site` at all, and a crash there strands the site in `.prev` until the next
  rebuild. Publish via versioned dir + symlink flip (`ln -sfn` + `mv -T`) — also fixes
  old-HTML/new-assets skew and gives instant rollback.
- **MED — Dockerfile:** Quartz pinned to a *mutable tag* (`--branch v4.5.2`) — pin a
  commit SHA; config COPY precedes `npm ci` so every color tweak re-runs the full
  install under QEMU (swap the layers); `chown -R node /opt/quartz` makes the whole
  generator writable by the runtime user that spawns builds from it — chown only the
  cache dir; `node:24-slim` undigested + no scheduled rebuild = no CVE patching.
- **LOW —** symlinks in the built tree can escape `SITE_OUT` (lstat/realpath check);
  malformed %-encoding → 500 not 400; no caching headers at all (stale-HTML risk after
  rebuild + every asset re-downloads — `no-cache` on HTML, long max-age on hashed
  assets); no `HEALTHCHECK`/`/healthz`; POST body never drained; `fontOrigin:
  "googleFonts"` phones Google from a private auth-gated site — Quartz supports local
  fonts.
- **Verified fine:** path traversal containment, timing-safe token compare,
  503-before-401 ordering, concurrent-POST race (sync check-and-set).
- **Undocumented existing features:** search and RSS are already on
  (`quartz.layout.ts:32`, `quartz.config.ts:109`) but RSS/sitemap URLs are broken until
  `KNOWLEDGE_SITE_BASE_URL` is set (silent `example.com` fallback — warn at build);
  `draft: true` per-note privacy via `RemoveDrafts` exists but `site/README.md` never
  mentions it.

---

## CI / release engineering

- **HIGH — drift guard** (Top 10 #5) and **action pinning/tap-token blast radius**
  (Top 10 #6).
- **MED — two hand-rolled, divergent version-bump engines.**
  `package-skills.yml:51-91` vs `cli-release.yml:47-100` differ on three axes: the
  skills scan has **no `-- plugins/` pathspec** (a `feat(cli)!:` in the same push
  major-bumps the *skills* release), zero-conventional-commit pushes cut a **minor**
  skills release (`chore(skills): reword comment` → new release) while the CLI
  correctly skips, and pre-1.0 breaking semantics differ. Either extract one
  `scripts/next-version.sh <prefix> <pathspec>` or adopt **release-please**
  multi-component mode (native tag prefixes, per-path scoping, chore-skipping,
  idempotency) and delete ~120 lines of bash. This would also give the third,
  fully-manual release lane (`@joe-sh/knowledge-tools-mcp` npm) a home.
- **MED — no `concurrency` group on package-skills** → two quick merges race to the
  same tag; run B fails "tag exists" and its commits go unreleased (cli-release already
  has the fix — copy it, plus re-fetch tags before compute).
- **MED — `validate-skills.yml` PR paths omit `template/**`** — a broken template
  skill merges green and fails on the push run after the fact. Also no `permissions:`
  block (every other workflow has one).
- **MED — a cli-release run that fails *after* pushing the tag is unrecoverable via
  dispatch** (next runs see no new `cli/**` commits → skip); if the computed tag exists
  with no release, re-cut instead of skipping.
- **LOW —** `--generate-notes` without `--notes-start-tag` spans the *other*
  namespace's commits in the interleaved-tag repo (both workflows);
  `BREAKING CHANGE` grep matches prose anywhere (anchor as a footer);
  `git tag -l | head -1` under pipefail is a latent SIGPIPE flake;
  `build-site.yml` grants `packages: write` to PR runs; no scheduled image rebuilds
  (base-image CVEs never ship until a path change); `cli-ci.yml` runs the full Go
  matrix on `site/**` changes (vestigial or undocumented); error message says
  `make sync-embed`, CLAUDE.md says `make sync-template`; no
  provenance/SBOM attestation on published artifacts.
- **Verified fine:** no commit-message injection anywhere (env-indirection done right),
  first-release paths, force-push handling, tag-exists idempotency on the auto path.

---

## Rework-from-scratch candidates (ranked by payoff)

1. **Daemon request/subprocess handling** — consume-then-run sentinel protocol, tri-state
   run results, periodic rescan, one supervised-exec helper with timeouts, startup
   reconciliation. This is a focused rewrite of ~2 files that closes 7 findings.
2. **Release versioning** — release-please (or one shared script) replacing both bash
   engines + the manual npm lane.
3. **Service search** — indexed retrieval (FTS5/inverted index) behind the same tool
   contract.
4. **Site publishing** — symlink-flip versioned dirs + queued rebuilds + cache headers.
5. **Review backend seam** — typed-error interface, killing the `-> 404` regex.
6. **Config resolution** — one `Resolved(job)` struct with per-field provenance,
   powering `doctor`/`--dry-run`.

Explicitly **not** worth reworking: the two-protocol shared-core service, the
sentinel-file host contract, the plugin-per-skill marketplace split, the
allowlist-by-struct vault yaml, the harness driver abstraction, and the goreleaser
tag-prefix dance — all carry their weight.

---

## New feature ideas (highest leverage for the vault workflow)

**Close the loop on unattended operation (biggest gap):**
- **Failure/completion notifications** — `KNOWLEDGE_NOTIFY_URL`/`_CMD` (ntfy/webhook)
  on job error, push failure, and daemon crash-loop; plus "synthesize opened N
  judgment calls" — today the review loop depends on you remembering to ask.
  `SiteRebuild.trigger` is already the exact pattern to reuse.
- **`kt doctor`** — agent binary present, `gh` auth, origin reachable, all three
  effective schedules cron-parsed *with source tier*, lock acquirable, unit state +
  version match, XDG sanity, `.gitignore` covers `outputs/*-logs/`.
- **`kt logs [job|daemon] [-f]`** and **`kt status --json`**.
- **`kt job <x> --dry-run`** — resolved plan: channel + downgrade reason, skill path,
  driver argv, model/effort with provenance.

**Vault interaction:**
- **`patch_note` / structured edit-capture** — the biggest workflow gap: fixing a typo
  you just spotted requires a capture→compile round trip. Even shipping it as a
  structured inbox capture (`kind: edit, target: library/x.md`) that the compiler
  applies preserves the dumb-capture invariant.
- **Batch capture** (array input on `append_to_inbox` / `POST /inbox/batch`) — helps
  auto-capture and imports, and sidesteps the same-ms filename collision.
- **Long-poll/SSE job completion** (`vault_status?wait_for=compile`) — polling burns
  agent turns; an fs-watch on `status.json` makes it push.
- **`kt job synthesize --focus <topic>`** — `$ARGUMENTS` plumbing already exists
  (`job.go:236-253`); every caller passes `""`. One flag away.

**New skills (give the orphaned pieces producers):**
- **`vault-health`** — template CLAUDE.md's "Maintenance (health check)" section is
  dead prose no skill invokes; turn it into a skill with an explicit channel.
- **`weekly-digest`** — `outputs/` is defined but written by nothing; a dated briefing
  from `log.md` + recently-touched notes is a natural opt-in fourth job.

**Housekeeping:**
- Retention knobs (`KNOWLEDGE_LOG_KEEP`/`KNOWLEDGE_ARCHIVE_KEEP`) — logs and
  `inbox/archive/` grow unboundedly today.
- `kt list` — enumerate instances/vaults with daemon state and last/next runs (the
  discovery half of issue #15; the per-instance groundwork already exists).
- Vault-plugin first-run choreography — "on first use call `vault_status`; on failure
  walk the user through `/mcp` auth and the DCR section" — the no-DCR failure mode
  (issue #4) is documented three places for maintainers and zero places for the agent
  fielding "it doesn't work".
