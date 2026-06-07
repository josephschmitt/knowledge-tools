# knowledge-tools

This repo is the **tooling** for Joe's personal "LLM wiki" — everything that operates
*on* the vault from the outside. The vault itself (the notes, plus the `CLAUDE.md`
librarian spec and `/compile-inbox` command Claude runs *inside* it) lives in a separate
repo whose location is configured by whoever sets this up (the `KNOWLEDGE_REPO` /
`VAULT_ROOT` knobs below). This repo holds none of the vault's content.

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
  capture/query against the vault, gated by Cloudflare Access OIDC. Reads/writes the vault
  via `VAULT_ROOT`. Built into a GHCR image by CI; deployed separately. See `mcp/README.md`.
- `scripts/` — host-side compile automation and the skill validator.
  - `nightly-compile.sh` runs an ephemeral Claude Code `/compile-inbox` pass over the vault.
  - `install.sh` generates the systemd *user* units from the `knowledge-compile.*.in`
    templates (worker = this repo; watched inbox = the vault repo, set via the required
    `KNOWLEDGE_REPO` env var). Idempotent.
  - `validate_skills.py` — the skill linter CI runs (see constraints below).
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

> The `knowledge-vault` skill drives the vault through its **MCP connector**; installing
> the plugin alone doesn't configure that server. In Claude Code the connector must be set
> up separately for the skill to function.

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
