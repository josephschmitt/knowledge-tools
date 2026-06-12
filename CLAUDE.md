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

- `skills/` — Claude skills, one per `skills/<name>/SKILL.md`. Currently just
  `knowledge-vault`, the conversational front-door skill (capture + query via the MCP
  connector). Shipped two ways: zipped per-skill for claude.ai, and as a Claude Code
  plugin via the marketplace in `.claude-plugin/` (see below).
- `mcp/` — the claude.ai connector. A Streamable-HTTP MCP server (TypeScript) that
  capture/query against the vault. Auth is **optional, off by default** (`src/auth.ts`): run it
  authless behind an authenticating proxy, or set `MCP_AUTH_*` to validate JWT access tokens
  against any OIDC issuer (the homelab uses Cloudflare Access + Managed OAuth). Reads/writes the
  vault via `VAULT_ROOT`. Built into a GHCR image by CI; deployed separately. See `mcp/README.md`.
- `scripts/` — host-side vault automation and the skill validator. Three vault-mutating jobs
  run as systemd *user* units; all three share one flock (`vault-lib.sh`) so they never run
  concurrently, and in each the **wrapper** owns git (Claude only edits files + runs scoped
  `gh` calls).
  - `vault-compile.sh` runs an ephemeral `/compile-inbox` pass (inbox→wiki). Cadence
    `KNOWLEDGE_COMPILE_ONCALENDAR` (default hourly); also triggered on demand via a `.path`
    unit when the MCP drops `inbox/.compile/request`.
  - `vault-job.sh <synthesize|resolve>` runs the two GitHub-issue jobs: `/synthesize` (heavy
    weekly whole-corpus reconcile + connect, **opens** judgment-call issues) and `/resolve`
    (light daily pass that applies answered issues and **closes** them). Unlike compile these
    run `gh` from *inside* the Claude run, granted via `--allowedTools` (no skip-permissions);
    the service units put `gh` on PATH and rely on `HOME` for `~/.config/gh` auth. Cadences:
    `KNOWLEDGE_SYNTHESIZE_ONCALENDAR` / `KNOWLEDGE_RESOLVE_ONCALENDAR`.
  - `vault-lib.sh` is sourced by all three — config, the shared lock, and the commit/push
    side effect (issue jobs commit only `wiki/ index.md log.md`; compile stages everything).
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
  `CLAUDE.md` (the librarian spec), `.claude/commands/{compile-inbox,synthesize,resolve}.md`,
  `.claude/settings.json`, `.gitignore`, the folder skeleton (`inbox/`, `inbox/archive/`,
  `wiki/`, `outputs/`), and empty `index.md`/`log.md`. `scripts/init-vault.sh` copies these
  into a new vault. This is a seed, **not** a source of truth: the commands and `CLAUDE.md`
  belong to the vault once seeded and are *expected* to diverge as the content grows — the
  tooling only schedules them.
- `.claude-plugin/` — the Claude Code plugin marketplace (`marketplace.json`) and plugin
  manifest (`plugin.json`).

## Skills

Every `skills/<name>/SKILL.md` must satisfy `scripts/validate_skills.py` (CI gate):

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

- **Tool descriptions + field schemas** (`mcp/src/mcp.ts`) — per-tool invariants any
  caller must obey: what the tool does, hard usage rules (e.g. capture takes zero
  decisions), field-level facts (e.g. no separate source-URL field — fold it into
  `text`), and one-clause pointers to companion tools (`search_wiki` → `get_note`,
  `compile_run` → `vault_status`). Rules only, no rationale.
- **Server `instructions`** (same file) — cross-tool policy and architecture only: the
  dumb-capture/smart-compile split, prefer-the-vault-over-general-knowledge, which tools
  serve which side. Never restate a per-tool rule here.
- **The skill** (`skills/knowledge-vault/SKILL.md`) — everything tools can't express:
  *when* to invoke (intent-matching in the frontmatter description), multi-tool
  choreography, conversational policy and voice, examples, and the **why** behind the
  tool-level rules.

Duplication across altitudes is deliberate (rule in the tool, rationale in the skill);
duplication at the same altitude is drift waiting to happen. When a *rule* changes,
update the tool description and the skill's treatment of it together — and remember the
tool-description change only ships when the MCP image is rebuilt and redeployed, while
the skill ships via the plugin/zip path. `references/mcp-operations.md` mirrors the
server's exact I/O shapes; keep it in sync when shapes change.

## Shipping skills

**Claude Code (plugin marketplace).** `.claude-plugin/marketplace.json` defines a
marketplace named `tools` with a single plugin named `knowledge` whose source is the repo
root (`"."`), so the `skills/` directory is auto-discovered. Users install with:

```text
/plugin marketplace add josephschmitt/knowledge-tools
/plugin install knowledge@tools
```

The skill is then invocable as `/knowledge:knowledge-vault`. After editing the manifests,
validate locally:

```sh
claude plugin validate .
```

Adding a new skill needs no manifest change — drop it under `skills/<name>/` and the plugin
picks it up. The plugin pulls from the repo's default branch, so `/plugin update` tracks
`main`.

The `knowledge-vault` skill drives the vault through its **MCP connector**, which the
plugin now bundles: `plugin.json` declares a `userConfig.mcp_url` field, so enabling the
plugin prompts the user for their self-hosted MCP endpoint, and an inline `mcpServers`
entry wires `${user_config.mcp_url}` into a remote HTTP server named `knowledge-vault`.
OAuth is auto-negotiated against whatever authenticating proxy fronts the endpoint (the
homelab uses Cloudflare Access, which serves `/.well-known/oauth-protected-resource` + 401),
so no secret lives in the manifest. The
config is inlined in `plugin.json` rather than a repo-root `.mcp.json` on purpose: source
is `"."`, so a root `.mcp.json` would also act as this repo's *project* MCP config.

> The plugin only points Claude Code at the connector — the user still has to deploy the
> MCP server (see `mcp/README.md`) and reach it at the URL they enter.

**claude.ai (zip releases).** CI (`.github/workflows/package-skills.yml`) zips each skill
as `<name>/...` for manual upload to claude.ai. Two paths trigger it: a skill change merged
to `main` auto-cuts a release (version bumped from the landed commits' conventional-commit
prefixes), and publishing a GitHub release by hand packages it. Keep this flow working when
touching skills — it's independent of the Claude Code plugin path.

## Commits

Use [Conventional Commits](https://www.conventionalcommits.org/) titles: `type(scope): summary`
(e.g. `feat(skills): add ...`, `fix(mcp): ...`, `docs: ...`, `chore: ...`). This isn't just
style — `package-skills.yml` derives the next release version from the landed commits' prefixes:
`feat` → minor, `fix` → patch, and a `!` or `BREAKING CHANGE` → major. Write commit titles
accordingly when touching `skills/`.

## CI

- `validate-skills.yml` — runs `validate_skills.py` on skill/script changes and pushes.
- `package-skills.yml` — builds per-skill zips and cuts/updates releases (above).
- `build-mcp.yml` — builds and pushes the multi-arch `ghcr.io/josephschmitt/knowledge-mcp`
  image on `mcp/**` changes.
