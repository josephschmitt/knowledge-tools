# knowledge-tools

This repo is the **tooling** for a personal "LLM wiki" — everything that operates
*on* the vault from the outside. The vault itself (the notes, plus the `CLAUDE.md`
librarian spec and the `compile-inbox` skill the agent runs *inside* it) lives in a separate
repo whose location is configured by whoever sets this up (the `KNOWLEDGE_REPO` /
`VAULT_ROOT` knobs below). The agent harness is configurable (`KNOWLEDGE_AGENT`: claude by
default, codex, opencode, or custom); the vault's procedures ship as harness-neutral skills. This repo holds none of the vault's content — only the
generic *starting point* of its librarian (see `template/`), which a fresh vault is
seeded from once and then tunes on its own as the corpus grows.

History: this was split out of a combined repo, so if you need context on how something
got here, that original repo holds the full history.

See `README.md` for the human-facing overview; this file is the operational guide for
working in the repo.

## Layout

- `plugins/` — Claude Code plugins, one per `plugins/<plugin>/`, each bundling a single skill
  (`plugins/<plugin>/skills/<name>/SKILL.md`) and its own `.claude-plugin/plugin.json`. Two of
  them: `vault` (skill `knowledge-vault`), the conversational front-door skill
  (capture-on-request + query via the MCP connector, whose config the plugin carries); and
  `auto-capture` (skill `auto-capture`), an **optional, opt-in** always-on skill that
  proactively captures capture-worthy material to the inbox *without* being asked (it reuses
  the `vault` plugin's `append_to_inbox` connector; it never queries or compiles). Shipped two
  ways: zipped per-skill for claude.ai, and as the Claude Code plugins listed in the
  marketplace at `.claude-plugin/marketplace.json` — one plugin per skill, so each installs
  independently (see below).
- `service/` — the vault service (TypeScript). One Express app exposing the vault over **two
  protocols** off a shared in-process core (`src/vault.ts` / `src/review.ts`): a Streamable-HTTP
  **MCP** endpoint at `/mcp` (the claude.ai connector, `src/mcp.ts`) and a **REST API** at
  `/api/v1` (`src/rest.ts`) that mirrors the MCP tools 1:1 for scripts/automation. Auth is
  **optional, off by default** (`src/auth.ts`) and gates both surfaces: run it authless behind an
  authenticating proxy, or set `KNOWLEDGE_AUTH_*` to validate JWT access tokens against any OIDC
  issuer. Reads/writes the vault via `VAULT_ROOT`.
  The judgment-call tools (`list_questions`/`get_question`/`answer_question`, in `src/review.ts`)
  dispatch to a files backend (`inbox/.review/`, in `src/vault.ts`) or a GitHub-issues backend
  (`src/github.ts`, opt-in via `KNOWLEDGE_GITHUB_TOKEN`+`KNOWLEDGE_GITHUB_REPO`) — set the container's
  `KNOWLEDGE_REVIEW_CHANNEL` to match the host's same-named `KNOWLEDGE_REVIEW_CHANNEL`. Built into a
  GHCR image by CI; deployed separately.
  See `service/README.md`. (The MCP *protocol* server name stays `knowledge-vault` — only the
  image/package is `knowledge-service`.)
- `cli/` — the host-side CLI (**Go + kong**, binary `knowledge-tools`, short alias `kt`). This
  replaced the old bash jobs + systemd/launchd install machinery. **One self-managed daemon per
  vault instance** (`KNOWLEDGE_INSTANCE`, default `default`) owns scheduling internally — install
  registers just *one* OS autostart unit to keep the daemon alive (multi-vault is still N
  deployments; see issue #15). The wrapper owns git (Claude only edits files + runs scoped `gh`
  calls). Built/released by goreleaser under a distinct **`cli/vX.Y.Z`** tag prefix (skill releases
  use a `skills/vX.Y.Z` prefix); devbox (`go@1.23`) is the toolchain.
  - Commands: `install` / `uninstall` (register/remove the daemon autostart unit, per-instance,
    idempotent), `daemon` (the long-running scheduler + compile watcher), `compile` /
    `synthesize` / `resolve` (one-shot jobs, also what the daemon runs on schedule), `init`
    (scaffold a vault from the embedded template — copy-if-absent), `status` (print the compile +
    schedule snapshots and the daemon unit state). (Static-site *building* is out of scope here —
    that's the standalone `knowledge-site` image; the CLI only *triggers* its rebuild after a
    commit, see `commit_and_push` below and `site/`.)
  - `internal/config` ports `load-env.sh` (a repo-root `.env`; real env wins) + the `KNOWLEDGE_*`
    knobs. **Schedules moved from systemd OnCalendar to cron expressions** (robfig/cron grammar):
    `KNOWLEDGE_COMPILE_SCHEDULE` (default `@hourly`), `KNOWLEDGE_SYNTHESIZE_SCHEDULE`
    (`CRON_TZ=America/Detroit 30 4 * * 0`), `KNOWLEDGE_RESOLVE_SCHEDULE`
    (`CRON_TZ=America/Detroit 30 3 * * *`). Also the **agent-harness knobs**: `KNOWLEDGE_AGENT`
    (claude | codex | opencode | custom), `KNOWLEDGE_AGENT_BIN` (deprecated `CLAUDE_BIN` is a
    fallback), `KNOWLEDGE_AGENT_CMD` (custom template), and per-job `*_MODEL`/`*_EFFORT` overrides
    with an `KNOWLEDGE_AGENT_MODEL`/`_EFFORT` fallback (`JobModel`/`JobEffort`; only the claude
    agent defaults a model — opus — which the old slash-command frontmatter used to declare).
  - `internal/agent` (new): the headless-agent abstraction that replaced `vault.RunClaude`. A
    `Driver` (selected by `KNOWLEDGE_AGENT`) turns a harness-neutral `Invocation` (prompt, model,
    effort, neutral shell-grant prefixes) into one harness's argv: `claude -p … --permission-mode
    acceptEdits [--model] [--allowedTools Bash(<grant>:*)]` (reproduces the old argv), `codex exec
    … --full-auto`, `opencode run …` (materializes an ephemeral permission config), or a `custom`
    argv-tokenized template. `SupportsShellGrants()` reports whether a driver can scope unattended
    shell to the gh allowlist — codex/grant-less custom can't, so `RunIssueJob` downgrades an
    auto-detected `github` channel to the grant-free `files` channel rather than over-grant.
  - `internal/vault` ports `vault-lib.sh`: the per-instance lock (now `flock(2)` on **both** Linux
    and macOS — no mkdir fallback), `sync_from_origin` + `commit_and_push` git discipline (shells
    out to `git`; issue jobs commit `library/ notebook/ index.md log.md` [+ `inbox/.review/` in files], compile
    stages everything; no-ops cleanly when not a git repo), and RFC3339 dates (no GNU/BSD
    branching). (The headless agent invocation moved to `internal/agent`; the jobs feed it a skill
    body read from `<repo>/.agents/skills/<name>/SKILL.md` as the prompt — falling back to the legacy
    `<repo>/.claude/commands/<name>.md` body so a vault seeded before the skills migration keeps
    working untouched, with a one-line migration nudge in the job log.) After a commit lands, `commit_and_push` fires a
    best-effort `POST` to the `knowledge-site` container's `/rebuild` when `KNOWLEDGE_SITE_REBUILD_URL`
    is set (bearer `KNOWLEDGE_SITE_REBUILD_TOKEN`; non-fatal — a down site never fails a job).
  - `internal/jobs` ports `vault-compile.sh` + `vault-job.sh`: compile snapshot/archive/
    `status.json`/cooldown; synthesize/resolve channel auto-detect (`github` if gh+origin, else
    `files`), per-channel slash command + `--allowedTools` gh grants + commit pathspecs, and the
    resolve short-circuit. Also writes `inbox/.compile/schedules.json` (the daemon is the source of
    truth for `next_run_at`, from the cron entries — so it never degrades to all-null like the old
    `systemctl`-querying `refresh_schedules` did off systemd).
  - `internal/daemon` (new): robfig/cron scheduler + fsnotify watcher on
    `inbox/.compile/request` (uniform on both OSes — no systemd `.path` unit, no macOS WatchPaths
    hack) + startup catch-up for ticks missed while down (replaces systemd `Persistent=true`).
  - `internal/service` (new): the autostart unit lifecycle. Linux writes one
    `knowledge-tools-daemon@.service` (+ per-instance `~/.config/knowledge-tools/<inst>.env`,
    linger); macOS writes one `com.knowledge-tools.daemon.<inst>.plist` (KeepAlive). Both clean up
    the old per-job bash-era units on (re)install/uninstall; uninstall is idempotent, needs no
    `KNOWLEDGE_REPO`, and never touches the vault or linger.
  - `internal/initvault` ports `init-vault.sh`. The vault `template/` is **embedded** (the binary
    is standalone) as a committed copy under `cli/internal/initvault/template/`; keep it in sync
    with the repo-root `template/` via `make sync-embed` (CI guards drift). (`vault-site.sh` was
    **not** ported into the CLI — its build recipe moved into the `knowledge-site` image; see `site/`.)
  - The MCP service contract is unchanged: `inbox/.compile/{request,status.json,
    last-compiled-epoch,last-manual-epoch,schedules.json}` keep their paths + schemas, so
    `service/` needs no changes.
- `scripts/` — only `validate_skills.py` remains (the skill linter CI runs; see constraints
  below). The vault job/install scripts (`vault-{compile,job,lib}.sh`, `{in,un}install.sh`,
  `init-vault.sh`, `load-env.sh`) moved into `cli/`; `vault-site.sh`'s build recipe moved into the
  `knowledge-site` image (`site/`).
- `site/` — the source of the self-contained **`knowledge-site`** image
  (`ghcr.io/josephschmitt/knowledge-site`, built by `.github/workflows/build-site.yml`): a
  `Dockerfile` that bakes a pinned Quartz checkout + the config overlay (`quartz.config.ts` /
  `quartz.layout.ts`, read via `KNOWLEDGE_SITE_TITLE` / `KNOWLEDGE_SITE_BASE_URL`) + deps, plus
  `build.sh` (allowlist-stage `index.md`+`library/` → `quartz build` → atomic swap), `entrypoint.sh`,
  and `serve.mjs` (zero-dep static server + token-gated `POST /rebuild`). It builds the render
  **inside its own container** from a bind-mounted `VAULT_ROOT` and serves it on its **own URL**
  (browser auth at the proxy), rebuilding when a content job POSTs after a commit. The `service/`
  image no longer serves the site. See `site/README.md`.
- `template/` — the **starting point** of a vault's own librarian, mirroring the vault layout:
  `CLAUDE.md` (the librarian spec), the `.agents/skills/{compile-inbox,synthesize,resolve}/SKILL.md`
  skills plus the git/GitHub-free `{synthesize,resolve}-files` variants (at the cross-client
  standard `.agents/skills/` path; `knowledge-tools init` symlinks `.claude/skills → ../.agents/skills`
  so Claude Code discovers them too — the symlink isn't committed in either template tree because
  Go's `embed` can't hold one, so `init` creates it post-extract), `.claude/settings.json`,
  `.gitignore`, the folder skeleton (`inbox/`, `inbox/archive/`, `library/`, `notebook/`,
  `outputs/`), and empty `index.md`/`log.md`. The jobs feed each skill's **body** (frontmatter
  stripped, `$ARGUMENTS` substituted) to the agent as the prompt — deterministic across harnesses,
  not reliant on slash-command resolution or auto-activation; model/effort live in the CLI config,
  and the gh grant list lives in Go (`channelConfig`), not skill frontmatter. `knowledge-tools init`
  copies these into a new vault (from its embedded copy — keep `cli/internal/initvault/template/` in
  sync via `make sync-template`). The seed deliberately scopes to the **library + notebook**
  knowledge areas and **defers `tasks/`** (the live vault's third area): the task workflow is coupled
  to the TaskNotes Obsidian plugin and its `.obsidian/` config, which a generic seed can't ship — a
  seeder layers that on per-vault. This is a seed, **not** a source of truth: the skills and `CLAUDE.md`
  belong to the vault once seeded and are *expected* to diverge as the content grows — the tooling
  only schedules them.
- `.claude-plugin/` — the Claude Code plugin marketplace (`marketplace.json`) and plugin
  manifest (`plugin.json`).

## Skills

Every `plugins/<plugin>/skills/<name>/SKILL.md` **and** every vault skill at
`template/.agents/skills/<name>/SKILL.md` must satisfy `scripts/validate_skills.py` (CI gate):

- YAML frontmatter with `name` and `description`.
- `name`: lowercase `[a-z0-9-]`, ≤64 chars, and **must equal the directory name**.
- `description`: non-empty, ≤1024 chars. (Mind the unquoted-colon YAML trap — a `key: value`
  shape inside a description, like `` `status: answered` ``, must be quoted; `resolve-files` is.)
- No two skills share a `name`.

The vault skills are the procedures the CLI jobs feed to the agent (the plugin skills are the
claude.ai/Claude-Code connector front-door — different surface, same SKILL.md format). Only the
repo-root `template/` is linted; the embedded `cli/internal/initvault/template/` mirror is skipped
(the drift guard keeps it byte-identical, so linting it too would trip the duplicate-name check).

Run it before pushing a skill change:

```sh
python3 scripts/validate_skills.py
```

## Where instructions live (MCP vs skill)

Agent-facing guidance is split across three layers by **who is guaranteed to see it**,
and additions must keep each fact at one altitude. The MCP layers reach *every* caller
(claude.ai, the Claude Code plugin, headless server-side agents, any future client) and sit
in context on every turn, so they must stay terse; the skill is lazy-loaded and only
exists on skill-aware surfaces, so it can afford length — but can't be relied on to be
present.

- **Tool descriptions + field schemas** (`service/src/mcp.ts`) — per-tool invariants any
  caller must obey: what the tool does, hard usage rules (e.g. capture takes zero
  decisions), field-level facts (e.g. no separate source-URL field — fold it into
  `text`), and one-clause pointers to companion tools (`search_notes` → `get_note`,
  `compile_run` → `vault_status`). Rules only, no rationale.
- **Server `instructions`** (same file) — cross-tool policy and architecture only: the
  dumb-capture/smart-compile split, prefer-the-vault-over-general-knowledge, which tools
  serve which side. Never restate a per-tool rule here.
- **The skill** (`plugins/vault/skills/knowledge-vault/SKILL.md`) — everything tools can't express:
  *when* to invoke (intent-matching in the frontmatter description), multi-tool
  choreography, conversational policy and voice, examples, and the **why** behind the
  tool-level rules.

Duplication across altitudes is deliberate (rule in the tool, rationale in the skill);
duplication at the same altitude is drift waiting to happen. When a *rule* changes,
update the tool description and the skill's treatment of it together — and remember the
tool-description change only ships when the MCP image is rebuilt and redeployed, while
the skill ships via the plugin/zip path. `references/mcp-operations.md` mirrors the
server's exact I/O shapes (inputs/outputs/return format only — no rules, no rationale);
keep it in sync when shapes change.

Two practical corollaries:

- **Context budget.** The MCP layers have no progressive disclosure — every token sits in
  *every* turn's context — so keep them lean and push prose *down* into the lazy-loaded skill
  rather than *up* into a tool description. A new hard rule earns a place in a tool only if
  it's short and every caller needs it; the *why*, the craft, and examples belong in the skill.
- **Two skills, one rule.** There are now two skills — `knowledge-vault` (capture-on-request +
  query + judgment calls) and `auto-capture` (proactive capture) — and each must stand alone,
  since either can load without the other. They can't share prose by reference, but they don't
  need to duplicate *rules*: a connector-driven skill always runs with the tool descriptions
  co-present, so both defer hard capture rules to the tool (one canonical home) and carry only
  their own concern + rationale. Minimal, rule-deferring overlap between the two skills is fine;
  restating a tool's rule verbatim in either is not.

## Shipping skills

**Claude Code (plugin marketplace).** `.claude-plugin/marketplace.json` defines a
marketplace named `knowledge-tools` with **one plugin per skill**, each `source`d at its own
plugin directory: `vault` → `./plugins/vault`, `auto-capture` → `./plugins/auto-capture`.
Each plugin dir holds its own `.claude-plugin/plugin.json` and the skill it ships under
`skills/<name>/` (a plugin can only bundle skills inside its own `source` tree, which is why
the skill dirs live under the plugin rather than at the repo root). Splitting them into
separate plugins is deliberate — it makes `auto-capture` (the always-on proactive skill) a
separate, opt-in install rather than something that activates the moment you install the core
skill. Users install with:

```text
/plugin marketplace add josephschmitt/knowledge-tools
/plugin install vault@knowledge-tools          # core: capture-on-request + query
/plugin install auto-capture@knowledge-tools   # optional: autonomous capture
```

The skills are then invocable as `/vault:knowledge-vault` and `/auto-capture:auto-capture`.
After editing the manifests, validate locally:

```sh
claude plugin validate .
```

Because each plugin's `source` is its own plugin dir (not the repo root), **adding a skill
now requires a marketplace entry** — create `plugins/<plugin>/` with the skill at
`skills/<name>/SKILL.md` and a `.claude-plugin/plugin.json`, then add a `plugins[]` entry
pointing at it. A plugin's `plugin.json` lives at `<source>/.claude-plugin/plugin.json`; the
`marketplace.json` stays at the repo root. Plugins pull from the repo's default branch, so
`/plugin update` tracks `main`.

The `knowledge-vault` skill drives the vault through its **MCP connector**, declared in the
`vault` plugin's `plugin.json` (`plugins/vault/.claude-plugin/plugin.json`): a
`userConfig.mcp_url` field prompts the user for their self-hosted MCP endpoint, and an inline
`mcpServers` entry wires `${user_config.mcp_url}` into a remote HTTP server named
`knowledge-vault`. OAuth is negotiated against whatever authenticating proxy/IdP fronts the
endpoint (RFC 9728 protected-resource metadata + a 401 challenge), so no secret lives in the
manifest. **No-DCR IdPs:** some self-hosted IdPs (e.g. **Authelia**) front the endpoint with no
DCR, so Claude Code can't self-register and fails with *"Incompatible auth server:
does not support dynamic client registration."* The manifest **cannot** carry the client ID to
fix this: Claude Code interpolates `${user_config.*}` into a server's `url` but **not** into the
nested `oauth` block, so a userConfig-supplied client ID reaches the IdP as the literal string
`${user_config.oauth_client_id}` and is rejected as `invalid_client` (verified against the
shipped CLI and live). The supported path is a **`.mcp.json`** entry — project-scoped, or
`~/.mcp.json` to cover every project (Claude reads `.mcp.json` from the cwd up to the filesystem
root) — that defines `knowledge-vault` with a *literal* `oauth.clientId` + `oauth.callbackPort`
(47832); a `.mcp.json` server **overrides** the plugin's same-named one. So the manifest ships
only the bare DCR-capable server: DCR-capable proxies/IdPs auto-register and need nothing;
no-DCR IdPs add the `.mcp.json` override (a public+PKCE client
with loopback redirects `http://127.0.0.1:47832/callback` + `http://localhost:47832/callback`,
its client ID kept out of the repo). The
`auto-capture` plugin declares **no** MCP config of its own — it reuses the `knowledge-vault`
server the `vault` plugin connects, so it depends on `vault` being installed too (this also
avoids a duplicate `mcp_url` prompt). Keeping each plugin's MCP config inside its own
plugin-dir `plugin.json` (rather than a repo-root `.mcp.json`) avoids that file also acting as
this repo's *project* MCP config.

> The plugin only points Claude Code at the connector — the user still has to deploy the
> service (see `service/README.md`) and reach it at the URL they enter.

**claude.ai (zip releases).** CI (`.github/workflows/package-skills.yml`) zips each skill
as `<name>/...` for manual upload to claude.ai. Two paths trigger it: a skill change merged
to `main` auto-cuts a release (version bumped from the landed commits' conventional-commit
prefixes), and publishing a GitHub release by hand packages it. Keep this flow working when
touching skills — it's independent of the Claude Code plugin path.

## Commits

Use [Conventional Commits](https://www.conventionalcommits.org/) titles: `type(scope): summary`
(e.g. `feat(skills): add ...`, `fix(service): ...`, `docs: ...`, `chore: ...`). This isn't just
style — `package-skills.yml` derives the next skill release version (a `skills/vX.Y.Z` tag) from
the landed commits' prefixes: `feat` → minor, `fix` → patch, and a `!` or `BREAKING CHANGE` →
major. Write commit titles accordingly when touching `plugins/`. Use the `cli` scope for CLI
changes (`feat(cli): ...`): a `cli/**` merge to `main` **auto-cuts** a `cli/vX.Y.Z` release from
the same prefixes (pre-1.0 semantics — breaking → minor, `feat` → minor, `fix` → patch; a
docs/chore-only merge cuts nothing), and pushing a `cli/v*` tag by hand stays available as a
manual escape hatch (re-cuts, hotfixes).

## CI

- `validate-skills.yml` — runs `validate_skills.py` on skill/script changes and pushes.
- `package-skills.yml` — builds per-skill zips and cuts/updates releases (above).
- `build-service.yml` — builds and pushes the multi-arch `ghcr.io/josephschmitt/knowledge-service`
  image on `service/**` changes.
- `cli-ci.yml` — on `cli/**` (+ `template/**`) changes: `go test`/`vet`/`golangci-lint` (ubuntu +
  macos + a windows build), `goreleaser check`, and a drift guard that the embedded
  `cli/internal/initvault/template/` matches the repo-root `template/` (run `make sync-template`).
- `cli-release.yml` — triggers on `cli/**` merges to `main` **and** on `cli/v*` tag pushes. On a
  merge it computes the next `cli/vX.Y.Z` from the landed conventional-commit prefixes; on a tag
  push it uses that exact tag. Because OSS goreleaser (no Pro `monorepo`) parses the *raw* tag as
  semver and chokes on the `cli/` prefix, the job feeds goreleaser a clean `vX.Y.Z` via
  `GORELEASER_CURRENT_TAG` (and `CLI_VERSION`) and runs `goreleaser release --clean --skip=validate
  --skip=publish` — which builds artifacts and **generates** the Homebrew cask under `dist/` but
  pushes nothing (`release.disable: true` is belt-and-suspenders) — then **publishes the GitHub
  release itself with `gh`** on the real `cli/vX.Y.Z` tag (a GITHUB_TOKEN-created tag/release can't
  trigger a second workflow, so the build and release must share one run). The `cli/v*` prefix keeps
  the CLI out of the skill releases' `skills/vX.Y.Z` namespace. A final step then **pushes goreleaser's
  generated cask** to `josephschmitt/homebrew-tap` (`HOMEBREW_TAP_GITHUB_TOKEN` secret) — done after
  the release so the cask's assets exist and its sha256s match them (one build); a `url.template`
  override points the cask at the prefixed `cli/vX.Y.Z` release, and post-install hooks symlink `kt`
  + strip the quarantine xattr. So `brew install --cask josephschmitt/tap/knowledge-tools` works —
  alongside binaries + deb/rpm/apk/archlinux + checksums.
