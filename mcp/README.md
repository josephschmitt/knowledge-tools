# Knowledge Vault MCP Server

A remote [MCP](https://modelcontextprotocol.io) server that exposes this knowledge vault to
**claude.ai as a custom connector** over the **Streamable HTTP** transport. Authentication is
**optional and off by default**: out of the box the server does no auth and trusts its network
(run it behind an authenticating proxy), or you can switch on built-in OAuth token validation
pointed at any OIDC issuer. See [Authentication](#authentication).

## Tools

| Tool | Purpose |
|---|---|
| `search_wiki(query)` | Full-text search across compiled wiki notes |
| `get_note(path)` | Return one note's markdown |
| `list_index()` | Return `index.md` (the navigation map) |
| `list_notes()` | List every wiki note |
| `append_to_inbox(text, title?)` | Capture a raw note into `inbox/` for the scheduled compile |
| `compile_run()` | Trigger an on-demand compile (async, rate-limited to one/hour) |
| `vault_status()` | Pollable JSON: last successful compile time, pending inbox count, manual-compile cooldown, running flag |

`append_to_inbox` is the capture path: drop a thought from claude.ai, and the scheduled
compile (`scripts/vault-compile.sh`) folds it into the wiki.

### Manual compile (`compile_run`)

The server can't compile in-process — the vault is read-only here except `inbox/`, and
synthesis needs the `claude` CLI + git on the host. So `compile_run` *triggers* the host
compile and reports status; it doesn't wait for the result. It writes a sentinel to
`inbox/.compile/request` (the one writable path), which a systemd `.path` unit
(`scripts/knowledge-compile.path.in`) watches to start the same `knowledge-compile.service`
the scheduled timer uses — so systemd runs one compile at a time (the shared lock). The
host writes `inbox/.compile/status.json`, which the server reads to return `triggered` /
`throttled` (refused within the one-hour cooldown) / `busy` / `empty`. The scheduled
run is never throttled and doesn't consume the manual cooldown.

Because `compile_run` returns before the compile finishes, `vault_status` is the completion
signal: the host records `last_compiled_at` at the *end* of every successful compile (both
scheduled and on-demand), so a `last_compiled_at` newer than your trigger time means the run
finished. It also reports `pending_inbox_count` and `manual_compile_available_at` (when the
cooldown next clears) — poll it after a `compile_run` to know when the wiki is caught up.

## Authentication

Two models, and you can combine them:

**1. Auth at a proxy (default — server does nothing).** Out of the box the server performs no
auth and trusts its network. Put an authenticating reverse proxy in front of `/mcp` and make
sure the origin can't be reached *around* it (don't publish the container port; keep untrusted
workloads off its network). Portable — bring whatever identity layer you already run.

**2. Built-in token validation (optional).** Set the `MCP_AUTH_*` env and the server becomes an
OAuth 2.1 *resource server*: it validates a JWT access token on every `/mcp` request and
advertises its authorization server for client discovery (RFC 9728). Vendor-neutral — point it
at any OIDC issuer. It validates tokens but never issues them, so you still need an authorization
server (the issuer). This is what lets the origin protect *itself*, so it's safe even if
something can reach it directly.

```sh
MCP_AUTH_ISSUER=https://your-idp.example.com          # the OIDC issuer (authorization server)
MCP_AUTH_JWKS_URL=https://your-idp.example.com/jwks    # its signing keys
MCP_AUTH_AUDIENCE=https://knowledge.example.com/mcp    # expected `aud` claim
# MCP_AUTH_TOKEN_HEADER=authorization                  # or e.g. cf-access-jwt-assertion
```

Set all three to enable; set none to stay authless. (Half-set → the server refuses to start.)

> **Which clients can reach it?** claude.ai's connector fetches **server-side from Anthropic's
> cloud**, so it needs a **publicly reachable** endpoint whose proxy *or* the server's own
> discovery speaks OAuth. The Claude Code CLI runs **on your machine**, so a private network is
> enough for it. Pick a deployment option (below) accordingly.

## Local development

```sh
cd mcp
npm install
npm run build
cp .env.example .env        # point VAULT_ROOT at a vault; no auth config needed
node --env-file=.env dist/index.js
```

There is no gate locally, so `/mcp` is reachable directly — drive it with the MCP Inspector or
curl:

```sh
npx @modelcontextprotocol/inspector      # connect to http://localhost:3000/mcp
curl -s localhost:3000/healthz           # {"ok":true}
```

## Image (CI)

Pushes to `main` that touch `mcp/**` trigger `.github/workflows/build-mcp.yml`, which builds
the image (linux/amd64 + arm64) and pushes it to
**`ghcr.io/josephschmitt/knowledge-mcp:latest`** (also tagged with the commit SHA). The
homelab pulls this published image — it does not build from a source checkout. The package
is **public** (set once in the GHCR package settings after the first push), so the host
pulls without authenticating; the image carries only server code, no vault content.

## Deploying behind auth

Run the container (only required config is `VAULT_ROOT`), then choose how it's protected.

### Cloudflare Access + Managed OAuth  *(public; works with claude.ai)*

Cloudflare runs the whole OAuth flow at the edge and forwards only authenticated requests. This
is how the homelab runs it.

1. Expose the container at a public hostname through a reverse proxy / tunnel (e.g. traefik + a
   Cloudflare Tunnel public hostname → `knowledge.example.com`). Keep the MCP port internal.
2. **Zero Trust → Access → Applications → Add → Self-hosted**: hostname `knowledge.example.com`,
   path `/mcp`. Enable **Managed OAuth**, and add a **policy** allowing only your identity (email).
3. claude.ai → **Settings → Connectors → Add custom connector** → `https://knowledge.example.com/mcp`.
   Managed OAuth serves discovery + dynamic registration, so there are no client credentials to
   paste — complete the Cloudflare login + consent and the tools appear.

**Recommended: also turn on built-in validation.** Cloudflare injects a `Cf-Access-Jwt-Assertion`
JWT on every request it forwards. Point the server's built-in auth at it so the origin protects
*itself* — then you don't need network isolation to stop LAN/sibling-container access:

```sh
MCP_AUTH_ISSUER=https://<team>.cloudflareaccess.com
MCP_AUTH_JWKS_URL=https://<team>.cloudflareaccess.com/cdn-cgi/access/certs
MCP_AUTH_AUDIENCE=<your Access application's AUD tag>
MCP_AUTH_TOKEN_HEADER=cf-access-jwt-assertion
```

<details><summary>Homelab specifics (traefik + cloudflared)</summary>

- **Single-level subdomain.** Free Universal SSL covers `example.com` and `*.example.com` only;
  a deeper host like `knowledge.mcp.example.com` has no edge cert. Keep the host at
  `knowledge.example.com` with the endpoint at the `/mcp` path.
- **TLS.** The traefik router requests the origin cert via the `cf-dns` resolver; the public
  edge cert is Universal SSL `*.example.com`.
- **Tunnel.** Add a public hostname `knowledge.example.com` → `https://localhost:443`; set
  cloudflared's **Origin Server Name** to the host (or **No TLS Verify**), since traefik
  terminates TLS on a loopback hop. Adding the hostname creates the DNS record.
- **Start it.** `cd ~/example.com && docker compose pull knowledge-mcp && docker compose up -d knowledge-mcp`
- **The bypass.** Without built-in validation, Access guards only the *public* path — a LAN
  client or neighbouring container could hit the origin directly. Either enable built-in
  validation (above) or keep the port off the host and the origin off shared networks.

</details>

### Your own OIDC issuer (Auth0 / Keycloak / Authentik / oauth2-proxy)  *(public; works with claude.ai)*

Two ways to use your own IdP:

- **Built-in validation** — set `MCP_AUTH_ISSUER`/`MCP_AUTH_JWKS_URL`/`MCP_AUTH_AUDIENCE` to the
  IdP's values; the server validates tokens and advertises the issuer for discovery itself. For
  the claude.ai connector the IdP must support dynamic client registration (Auth0/Keycloak do)
  or let you preconfigure claude.ai's client.
- **A gateway** (oauth2-proxy / Authelia) terminating OAuth/OIDC in front of `/mcp` and
  forwarding only authenticated requests — leave the server authless behind it. The gateway must
  serve the discovery claude.ai expects (`/.well-known/oauth-protected-resource` + a 401 challenge).

Either way, restrict the policy to your account.

### Private network (Tailscale / WireGuard / VPN)  *(Claude Code only)*

Put the endpoint on a private network and skip a public proxy entirely — simplest and fully
private. **claude.ai can't reach it** (its fetch comes from Anthropic's cloud), so use this with
the **Claude Code** CLI on a machine that's on the same tailnet/VPN: run `/mcp` to point Claude
Code at the private URL.

## Notes

- Vault is mounted read-only except `inbox/` (least privilege — only `append_to_inbox` writes).
- DNS-rebinding protection is off by default (the proxy is the gate; claude.ai's fetch may omit
  `Origin`). See `.env.example` to enable it with `ALLOWED_HOSTS`/`ALLOWED_ORIGINS`.
- The server never issues tokens or runs login/MFA/policy — that's the issuer's job. With
  built-in validation it only *verifies* tokens per request; with auth off, even that lives in
  the proxy you deploy it behind.
- Logging is [pino](https://getpino.io) (line-delimited JSON). Default level is `info`; set
  `LOG_LEVEL=debug` to see per-request lines.
