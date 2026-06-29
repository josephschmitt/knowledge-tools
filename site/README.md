# site/ — the `knowledge-site` image

This directory is the source of the self-contained **`knowledge-site`** Docker image
(`ghcr.io/josephschmitt/knowledge-site`): it renders the vault as a browsable
[Quartz](https://quartz.jzhao.xyz) site — full-text search, backlinks, a graph view, and
Obsidian-style `[[wikilink]]` navigation — **inside its own container**, and serves it on its own
URL (separate from the `service/` MCP/REST image).

Quartz is a clone-and-customize generator, not an npm dependency, so the image **bakes** a pinned
upstream Quartz checkout + the two config files here + `node_modules` at build time (the heavy,
cacheable layer). At runtime it stages the bind-mounted vault's public content and builds:

- **Input**: a read-only bind-mounted vault at `VAULT_ROOT` (`/vault`). A strict **allowlist** — only
  `index.md` + `library/` ever reach the build; `inbox/`, `outputs/`, and tasks never do.
- **Build + publish**: `build.sh` stages the allowlist, runs `quartz build`, and swaps the output
  into place atomically (a request never sees a half-built tree).
- **Serve**: `serve.mjs` (zero runtime deps) serves the built site with Quartz's clean URLs and a
  `404.html` fallback. **No auth on content** — a browser session is authenticated by the proxy in
  front (e.g. Authelia).
- **Rebuild**: a token-gated `POST /rebuild` re-stages and rebuilds on demand; the host's content
  jobs fire it after a commit (`KNOWLEDGE_SITE_REBUILD_URL` / `_TOKEN`; see the repo-root
  `.env.example`).

## Files

- `Dockerfile` — bakes Quartz (pinned by `KNOWLEDGE_QUARTZ_REF`) + config + deps; the entrypoint
  builds once, then serves.
- `build.sh` — allowlist staging + `quartz build` + atomic swap (ported from the retired
  `vault-site.sh`).
- `entrypoint.sh` — initial build, then `exec`s the server.
- `serve.mjs` — the static server + `POST /rebuild`.
- `quartz.config.ts` — site config: title/baseUrl from env, no analytics, Obsidian wikilinks
  enabled, private dirs in `ignorePatterns` (belt-and-suspenders on top of the staging allowlist).
- `quartz.layout.ts` — page layout (close to the Quartz default, minimal footer).

## Configuration (runtime env)

| Var | Default | Purpose |
|---|---|---|
| `VAULT_ROOT` | `/vault` | Bind-mounted vault (read-only, sole input) |
| `KNOWLEDGE_SITE_REBUILD_TOKEN` | _(unset)_ | Shared secret for `POST /rebuild`; the endpoint returns 503 until set |
| `KNOWLEDGE_SITE_PORT` | `8080` | Serve port |
| `KNOWLEDGE_SITE_TITLE` | `Knowledge Vault` | Page title |
| `KNOWLEDGE_SITE_BASE_URL` | `example.com` | Host for RSS/sitemap/404 absolute URLs (no scheme) |
| `KNOWLEDGE_QUARTZ_REF` (build arg) | `v4.5.2` | Pinned Quartz ref, baked at image build |

```sh
docker run --rm -p 8080:8080 \
  -v /path/to/vault:/vault:ro \
  -e KNOWLEDGE_SITE_BASE_URL=library.example.com \
  -e KNOWLEDGE_SITE_REBUILD_TOKEN=your-shared-secret \
  ghcr.io/josephschmitt/knowledge-site
```

## Why these files don't typecheck in this repo

Both files `import` from `./quartz/cfg` and `./quartz/components` (and use `process.env`). Those
resolve only inside the Quartz checkout, where the files are copied at build time — not here. The
TypeScript errors you'll see when opening them in this repo are expected; the service's own `tsc`
build (`service/src/**` only) never compiles them.

## Pinned version

These overlays target **Quartz v4** (`v4.5.2`). Quartz v5 changed the configuration model (YAML)
and is a deliberate future upgrade, not a drop-in — moving to it requires porting these two files.
The image's `KNOWLEDGE_QUARTZ_REF` (Dockerfile build arg) must match the version these overlays
target.

## Local preview

From a checkout that has these files overlaid, you can use Quartz's dev server:

```sh
npx quartz build --serve -d /path/to/staged/content
```
