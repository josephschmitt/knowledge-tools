# Knowledge Vault MCP Server

A remote [MCP](https://modelcontextprotocol.io) server that exposes this knowledge vault to
**claude.ai as a custom connector**. It serves the **Streamable HTTP** transport and is gated
by **OAuth 2.1** (with Dynamic Client Registration), because claude.ai's connector flow
assumes OAuth and offers no bearer-token field.

## Tools

| Tool | Purpose |
|---|---|
| `search_wiki(query)` | Full-text search across compiled wiki notes |
| `get_note(path)` | Return one note's markdown |
| `list_index()` | Return `index.md` (the navigation map) |
| `list_notes()` | List every wiki note |
| `append_to_inbox(text, title?)` | Capture a raw note into `inbox/` for the nightly compile |
| `compile_run()` | Trigger an on-demand compile (async, rate-limited to one/hour) |

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

## Auth model (single user)

OAuth 2.1 + PKCE + DCR, implemented with the MCP SDK's auth router. The only "login" is a
**shared passphrase** (`LOGIN_PASSPHRASE`): when you add the connector, claude.ai sends you
to an approval page; enter the passphrase to approve. Registered clients and tokens persist
in a SQLite store (`OAUTH_DB_PATH`) so a restart doesn't force re-auth.

## Local development

```sh
cd mcp
npm install
npm run build
cp .env.example .env        # set LOGIN_PASSPHRASE, point VAULT_ROOT at the repo
node --env-file=.env dist/index.js
```

Then drive it with the MCP Inspector (interactive) or curl:

```sh
npx @modelcontextprotocol/inspector      # connect to http://localhost:3000/mcp
```

The discovery + 401 contract:

```sh
curl -s localhost:3000/.well-known/oauth-protected-resource/mcp
curl -s -i -X POST localhost:3000/mcp -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'   # 401 + WWW-Authenticate
```

## Deploy (homelab — `~/example.com`)

The server runs as the `knowledge-mcp` compose service (already added to
`~/example.com/docker-compose.yaml`), behind traefik at `knowledge.mcp.example.com`.

1. **Passphrase** — add to `~/example.com/.env`:
   ```sh
   echo 'KNOWLEDGE_MCP_PASSPHRASE=<a-strong-passphrase>' >> ~/example.com/.env
   ```
2. **OAuth store dir** (owned by uid 1000):
   ```sh
   mkdir -p ~/example.com/knowledge-mcp/data
   ```
3. **TLS** — `*.mcp.example.com` was added to `traefik/traefik.yml` SANs. Restart traefik so
   it issues the wildcard via the Cloudflare DNS challenge (briefly drops ingress):
   ```sh
   cd ~/example.com && docker compose up -d traefik
   ```
4. **Build + start the service** (does not touch other services):
   ```sh
   cd ~/example.com && docker compose up -d --build knowledge-mcp
   docker compose logs -f knowledge-mcp
   ```
5. **Cloudflare Zero Trust UI** — add a **public hostname** to the existing tunnel:
   - Hostname: `knowledge.mcp.example.com`
   - Service: `https://localhost:443`, with **Origin Server Name** = `knowledge.mcp.example.com`
     (or enable *No TLS Verify*) — cloudflared is host-network and traefik terminates TLS.
   - Adding the hostname creates the DNS record automatically.
   - **Do not** attach a Cloudflare Access application to this hostname — Access blocks
     claude.ai's server-side fetch. The server's OAuth is the gate.

Verify publicly:

```sh
curl -s https://knowledge.mcp.example.com/.well-known/oauth-protected-resource/mcp
```

## Connect to claude.ai

claude.ai → **Settings → Connectors → Add custom connector** → URL:
`https://knowledge.mcp.example.com/mcp`. Approve with the passphrase. The five tools appear.

## Notes

- Vault is mounted read-only except `inbox/` (least privilege — only `append_to_inbox` writes).
- DNS-rebinding protection is off by default (OAuth is the gate; claude.ai's fetch may omit
  `Origin`). See `.env.example` to enable it with `ALLOWED_HOSTS`/`ALLOWED_ORIGINS`.
- Access-token TTL is 1h; refresh tokens are long-lived and persisted.
