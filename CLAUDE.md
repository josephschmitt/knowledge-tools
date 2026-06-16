# knowledge-tools

This repo is the **tooling** for a personal "LLM wiki" — everything that operates
*on* the vault from the outside. The vault itself (the notes, plus the `CLAUDE.md`
librarian spec and `/compile-inbox` command Claude runs *inside* it) lives in a separate
repo whose location is configured by whoever sets this up (the `KNOWLEDGE_REPO` /
`VAULT_ROOT` knobs below). This repo holds none of the vault's content — only the
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
  authenticating proxy, or set `KNOWLEDGE_AUTH_*` to validate JWT access tokens against any OIDC issuer
  (the homelab uses Cloudflare Access + Managed OAuth). Reads/writes the vault via `VAULT_ROOT`.
  The judgment-call tools (`list_questions`/`get_question`/`answer_question`, in `src/review.ts`)
  dispatch to a files backend (`inbox/.review/`, in `src/vault.ts`) or a GitHub-issues backend
  (`src/github.ts`, opt-in via `KNOWLEDGE_GITHUB_TOKEN`+`KNOWLEDGE_GITHUB_REPO`) — set the container's
  `KNOWLEDGE_REVIEW_CHANNEL` to match the host's same-named `KNOWLEDGE_REVIEW_CHANNEL`. Built into a
  GHCR image by CI; deployed separately.
  See `service/README.md`. (The MCP *protocol* server name stays `knowledge-vault` — only the
  image/package is `knowledge-service`.)
- `scripts/` — host-side vault automation and the skill validator. Three vault-mutating jobs
  run as systemd *user* units; all three share one flock (`vault-lib.sh`) so they never run
  concurrently, and in each the **wrapper** owns git (Claude only edits files + runs scoped
  `gh` calls).
  - `vault-compile.sh` runs an ephemeral `/compile-inbox` pass (inbox→wiki). Cadence
    `KNOWLEDGE_COMPILE_ONCALENDAR` (default hourly); also triggered on demand via a `.path`
    unit when the MCP drops `inbox/.compile/request`.
  - `vault-job.sh <synthesize|resolve>` runs the two judgment-call jobs: `/synthesize` (heavy
    weekly whole-corpus reconcile + connect, **opens** judgment calls) and `/resolve` (light
    daily pass that applies answered calls and **closes** them). `KNOWLEDGE_REVIEW_CHANNEL`
    picks where calls live (auto-detected when unset: `github` if gh+origin are present, else
    `files`). **github** runs `/synthesize`+`/resolve`, opening/closing GitHub issues via `gh`
    from *inside* the Claude run (granted via `--allowedTools`, no skip-permissions; the service
    units put `gh` on PATH and rely on `HOME` for `~/.config/gh` auth). **files** runs
    `/synthesize-files`+`/resolve-files`, which carry calls as `inbox/.review/*.md` answered from
    chat via the MCP (`list_questions`/`get_question`/`answer_question`) — no gh, no GitHub, no
    git, so it works on a bare synced folder. Cadences: `KNOWLEDGE_SYNTHESIZE_ONCALENDAR` /
    `KNOWLEDGE_RESOLVE_ONCALENDAR`.
  - `vault-lib.sh` is sourced by all three — config, the shared lock, and the commit/push
    side effect (issue jobs commit `wiki/ index.md log.md`, plus `inbox/.review/` in the files
    channel; compile stages everything). `commit_and_push` no-ops cleanly when the vault is not
    a git repo (history left to external sync), so a bare folder works too.
  - `install.sh` generates the systemd *user* units from the `knowledge-*.in` templates
    (worker = this repo; vault = the required `KNOWLEDGE_REPO`) — re-run after changing a
    template or a cadence. Idempotent.
  - `init-vault.sh` seeds a fresh vault from `template/` (below). **One-shot scaffold, not
    `install.sh`**: strictly copy-if-absent, no `--force`, leaves git alone. Re-running only
    fills gaps — it never overwrites a tuned `CLAUDE.md` or command, because post-seed drift
    is the design (the librarian is content-coupled). Targets `KNOWLEDGE_REPO` or a path arg.
  - `load-env.sh` is sourced by the scripts to read config (e.g. `KNOWLEDGE_REPO`) from a
    repo-root `.env` (gitignored; see `.env.example`). Real env vars and the systemd
    `Environment=` override `.env`.
  - `validate_skills.py` — the skill linter CI runs (see constraints below).
- `template/` — the **starting point** of a vault's own librarian, mirroring the vault layout:
  `CLAUDE.md` (the librarian spec), `.claude/commands/{compile-inbox,synthesize,resolve}.md`
  plus the git/GitHub-free `{synthesize,resolve}-files.md` variants,
  `.claude/settings.json`, `.gitignore`, the folder skeleton (`inbox/`, `inbox/archive/`,
  `wiki/`, `outputs/`), and empty `index.md`/`log.md`. `scripts/init-vault.sh` copies these
  into a new vault. This is a seed, **not** a source of truth: the commands and `CLAUDE.md`
  belong to the vault once seeded and are *expected* to diverge as the content grows — the
  tooling only schedules them.
- `.claude-plugin/` — the Claude Code plugin marketplace (`marketplace.json`) and plugin
  manifest (`plugin.json`).

## Skills

Every `plugins/<plugin>/skills/<name>/SKILL.md` must satisfy `scripts/validate_skills.py`
(CI gate):

- YAML frontmatter with `name` and `description`.
- `name`: lowercase `[a-z0-9-]`, ≤64 chars, and **must equal the directory name**.
- `description`: non-empty, ≤1024 chars.
- No two skills share a `name`.

Run it before pushing a skill change:

```sh
python3 scripts/validate_skills.py
```

## Where instructions live (MCP vs skill)

Agent-facing guidance is split across three layers by **who is guaranteed to see it**,
and additions must keep each fact at one altitude. The MCP layers reach *every* caller
(claude.ai, the Claude Code plugin, headless homelab agents, any future client) and sit
in context on every turn, so they must stay terse; the skill is lazy-loaded and only
exists on skill-aware surfaces, so it can afford length — but can't be relied on to be
present.

- **Tool descriptions + field schemas** (`service/src/mcp.ts`) — per-tool invariants any
  caller must obey: what the tool does, hard usage rules (e.g. capture takes zero
  decisions), field-level facts (e.g. no separate source-URL field — fold it into
  `text`), and one-clause pointers to companion tools (`search_wiki` → `get_note`,
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
manifest. **No-DCR IdPs:** the homelab fronts the endpoint with a self-hosted **Authelia** IdP,
which has no DCR, so Claude Code can't self-register and fails with *"Incompatible auth server:
does not support dynamic client registration."* The manifest **cannot** carry the client ID to
fix this: Claude Code interpolates `${user_config.*}` into a server's `url` but **not** into the
nested `oauth` block, so a userConfig-supplied client ID reaches the IdP as the literal string
`${user_config.oauth_client_id}` and is rejected as `invalid_client` (verified against the
shipped CLI and live). The supported path is a **`.mcp.json`** entry — project-scoped, or
`~/.mcp.json` to cover every project (Claude reads `.mcp.json` from the cwd up to the filesystem
root) — that defines `knowledge-vault` with a *literal* `oauth.clientId` + `oauth.callbackPort`
(47832); a `.mcp.json` server **overrides** the plugin's same-named one. So the manifest ships
only the bare DCR-capable server: DCR-capable proxies (e.g. Cloudflare Access Managed OAuth)
auto-register and need nothing; no-DCR IdPs add the `.mcp.json` override (the homelab's is
`claude-code-knowledge-vault`, public+PKCE, loopback redirects `http://127.0.0.1:47832/callback`
+ `http://localhost:47832/callback`, kept out of the repo). The
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
style — `package-skills.yml` derives the next release version from the landed commits' prefixes:
`feat` → minor, `fix` → patch, and a `!` or `BREAKING CHANGE` → major. Write commit titles
accordingly when touching `plugins/`.

## CI

- `validate-skills.yml` — runs `validate_skills.py` on skill/script changes and pushes.
- `package-skills.yml` — builds per-skill zips and cuts/updates releases (above).
- `build-service.yml` — builds and pushes the multi-arch `ghcr.io/josephschmitt/knowledge-service`
  image on `service/**` changes.
