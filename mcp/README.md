# Knowledge Vault MCP Server

A remote [MCP](https://modelcontextprotocol.io) server that exposes this knowledge vault to
**claude.ai as a custom connector**. It serves the **Streamable HTTP** transport and is an
**OAuth resource server**: it issues no tokens itself, but validates the token claude.ai
obtains from a **Cloudflare Access for SaaS OIDC** app and forwards on each request.

## Tools

| Tool | Purpose |
|---|---|
| `search_wiki(query)` | Full-text search across compiled wiki notes |
| `get_note(path)` | Return one note's markdown |
| `list_index()` | Return `index.md` (the navigation map) |
| `list_notes()` | List every wiki note |
| `append_to_inbox(text, title?)` | Capture a raw note into `inbox/` for the nightly compile |
| `compile_run()` | Trigger an on-demand compile (async, rate-limited to one/hour) |
| `vault_status()` | Pollable JSON: last successful compile time, pending inbox count, manual-compile cooldown, running flag |

`append_to_inbox` is the capture path: drop a thought from claude.ai, and the nightly
compile (`scripts/nightly-compile.sh`) folds it into the wiki.

### Manual compile (`compile_run`)

The server can't compile in-process — the vault is read-only here except `inbox/`, and
synthesis needs the `claude` CLI + git on the host. So `compile_run` *triggers* the host
compile and reports status; it doesn't wait for the result. It writes a sentinel to
`inbox/.compile/request` (the one writable path), which a systemd `.path` unit
(`deploy/knowledge-compile.path`) watches to start the same `knowledge-compile.service`
the nightly timer uses — so systemd runs one compile at a time (the shared lock). The
host writes `inbox/.compile/status.json`, which the server reads to return `triggered` /
`throttled` (refused within the one-hour cooldown) / `busy` / `empty`. The scheduled
nightly run is never throttled and doesn't consume the manual cooldown.

Because `compile_run` returns before the compile finishes, `vault_status` is the completion
signal: the host records `last_compiled_at` at the *end* of every successful compile (both
nightly and on-demand), so a `last_compiled_at` newer than your trigger time means the run
finished. It also reports `pending_inbox_count` and `manual_compile_available_at` (when the
cooldown next clears) — poll it after a `compile_run` to know when the wiki is caught up.

## Auth model

claude.ai authenticates the user **directly against Cloudflare Access** (a SaaS OIDC app),
then sends the resulting token as a bearer on every `/mcp` request. This server is purely a
**resource server**: it advertises Cloudflare as its authorization server via
`/.well-known/oauth-protected-resource`, and on each request verifies the token's signature
against Cloudflare's JWKS (Key) endpoint plus its issuer and audience (`auth/verifier.ts`).
No passphrase, no token store, no dynamic client registration on our side — Cloudflare owns
login, MFA, policy, and token lifecycle.

> **Audience quirk:** Cloudflare Access SaaS OIDC stamps the access token's `aud` with the
> app's **redirect URL** (`https://claude.ai/api/mcp/auth_callback`), *not* the client ID. The
> verifier therefore validates `aud` against `CF_AUDIENCE` (default: that redirect URL), while
> `CF_CLIENT_ID` only identifies the client. Checking `aud` against the client ID rejects every
> otherwise-valid token.

Because Cloudflare's app is a *statically registered* OAuth client (fixed client ID +
secret), claude.ai uses its **Advanced settings** to supply those credentials instead of
registering dynamically (supported since July 2025). See "Connect to claude.ai" below.

> Cloudflare Access is used only as the **identity provider** the OAuth login redirects to.
> Do **not** put a Cloudflare Access *application in front of* `/mcp` — Access blocks
> claude.ai's server-side fetch. The bearer-token check in this server is the gate.

## Local development

```sh
cd mcp
npm install
npm run build
cp .env.example .env        # set CF_ISSUER + CF_CLIENT_ID, point VAULT_ROOT at the repo
node --env-file=.env dist/index.js
```

Then drive it with the MCP Inspector (interactive) or curl:

```sh
npx @modelcontextprotocol/inspector      # connect to http://localhost:3000/mcp
```

The discovery + 401 contract:

```sh
# protected-resource metadata: authorization_servers should list the Cloudflare issuer
curl -s localhost:3000/.well-known/oauth-protected-resource/mcp
curl -s -i -X POST localhost:3000/mcp -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'   # 401 + WWW-Authenticate
```

## Image (CI)

Pushes to `main` that touch `mcp/**` trigger `.github/workflows/build-mcp.yml`, which builds
the image (linux/amd64 + arm64) and pushes it to
**`ghcr.io/josephschmitt/knowledge-mcp:latest`** (also tagged with the commit SHA). The
homelab pulls this published image — it does not build from a source checkout. The package
is **public** (set once in the GHCR package settings after the first push), so the host
pulls without authenticating; the image carries only server code, no vault content.

## Deploy (homelab — `~/example.com`)

The server runs as the `knowledge-mcp` compose service (already in
`~/example.com/docker-compose.yaml` as `image: ghcr.io/josephschmitt/knowledge-mcp:latest`),
behind traefik at `knowledge.example.com`.

> **Use a single-level subdomain.** Cloudflare's free Universal SSL covers only `example.com`
> and `*.example.com` (one level). A deeper host like `knowledge.mcp.example.com` has no edge
> certificate and fails the TLS handshake unless you buy Advanced Certificate Manager, so the
> host is `knowledge.example.com` and the MCP endpoint stays at the `/mcp` path.

1. **Cloudflare OIDC env** — set the app's values in `~/example.com/.env`
   (issuer + client ID; neither is secret — the server validates tokens via Cloudflare's JWKS
   and needs no client secret):
   ```sh
   KNOWLEDGE_MCP_CF_ISSUER=https://example.cloudflareaccess.com/cdn-cgi/access/sso/oidc/<CLIENT_ID>
   KNOWLEDGE_MCP_CF_CLIENT_ID=<CLIENT_ID>
   ```
2. **TLS** — the compose router requests the origin cert for `knowledge.example.com`
   automatically via the `cf-dns` resolver (the token already has the `example.com` zone); no
   traefik change is needed. The public *edge* cert is Cloudflare's Universal SSL `*.example.com`.
3. **Pull + start the service** (does not touch other services):
   ```sh
   cd ~/example.com && docker compose pull knowledge-mcp && docker compose up -d knowledge-mcp
   docker compose logs -f knowledge-mcp
   ```
4. **Cloudflare Zero Trust UI** — add a **public hostname** to the existing tunnel:
   - Hostname: `knowledge.example.com`
   - Service: `https://localhost:443`. Under **TLS**, either set **Origin Server Name** =
     `knowledge.example.com` (so cloudflared's SNI matches traefik's cert) or enable
     **No TLS Verify** — cloudflared is host-network and traefik terminates TLS on a loopback hop.
   - Adding the hostname creates the DNS record automatically.
   - **Do not** attach a Cloudflare Access application to this hostname — Access blocks
     claude.ai's server-side fetch. The bearer-token check is the gate.

Verify publicly:

```sh
curl -s https://knowledge.example.com/.well-known/oauth-protected-resource/mcp
```

## Cloudflare Access app (one-time)

In Zero Trust → Access controls → Applications, the OIDC SaaS app ("Claude") must have:

- **Redirect URL** `https://claude.ai/api/mcp/auth_callback` (claude.ai's OAuth callback).
- A **policy** restricting login to your identity (e.g. your email) — this is who can reach
  the vault.
- **PKCE** enabled is recommended (claude.ai sends a code challenge); the client secret keeps
  the flow working regardless.

The **Issuer** and **Client ID** feed this server's env (`CF_ISSUER` / `CF_CLIENT_ID`) — it
validates tokens against Cloudflare's JWKS and needs no secret. The **Client Secret** is used
only by claude.ai (the OAuth client); it's shown only at creation, so use **Reset secret** if
you no longer have it.

## Connect to claude.ai

claude.ai → **Settings → Connectors → Add custom connector** → URL:
`https://knowledge.example.com/mcp`. Open **Advanced settings** and paste the Cloudflare
**Client ID** and **Client Secret**. Add it, complete the Cloudflare login, and the tools
appear.

## Notes

- Vault is mounted read-only except `inbox/` (least privilege — only `append_to_inbox` writes).
- DNS-rebinding protection is off by default (the bearer-token check is the gate; claude.ai's
  fetch may omit `Origin`). See `.env.example` to enable it with `ALLOWED_HOSTS`/`ALLOWED_ORIGINS`.
- Tokens are issued and expired by Cloudflare; this server validates them per request against
  Cloudflare's JWKS and holds no token state.
- Logging is [pino](https://getpino.io) (line-delimited JSON). Default level is `info`; set
  `LOG_LEVEL=debug` to see per-request lines and token-verification detail. Rejected tokens log
  at `warn` with jose's specific failure reason.
