// Centralized config from the environment. In the container these come from compose;
// for local dev use `node --env-file=.env`.

export const PORT = Number(process.env.PORT ?? 3000);

// pino log level (fatal|error|warn|info|debug|trace|silent). Default 'info'; set
// LOG_LEVEL=debug to surface per-request and token-verification detail with no code change.
export const LOG_LEVEL = process.env.LOG_LEVEL ?? 'info';

// Public origin the server is reached at — the base for the MCP endpoint (logging, and the
// default allowed Host for DNS-rebinding protection).
export const PUBLIC_URL = (process.env.PUBLIC_URL ?? `http://localhost:${PORT}`).replace(/\/+$/, '');

// Vault filesystem root (the knowledge repo). Mounted at /vault in the container.
export const VAULT_ROOT = process.env.VAULT_ROOT ?? '/vault';

// This server performs NO authentication of its own — it is a plain MCP server that trusts the
// network it is deployed on. Authentication is a deployment concern: run it behind an
// authenticating reverse proxy / identity-aware proxy (e.g. Cloudflare Access, oauth2-proxy,
// Authelia, Tailscale) and ensure only that proxy can reach it. See mcp/README.md.

// Max characters returned in a single tool result (claude.ai caps near 150k).
export const MAX_RESULT_CHARS = Number(process.env.MAX_RESULT_CHARS ?? 140_000);

// DNS-rebinding protection guards LOCAL servers from browser attacks. This server sits behind a
// proxy, and claude.ai's server-side fetch may omit an Origin header, so it's off by default to
// avoid hard-to-debug connector failures. Enable it (and tune the lists below) if appropriate.
export const ENABLE_DNS_REBINDING = (process.env.ENABLE_DNS_REBINDING_PROTECTION ?? 'false') === 'true';

const publicHost = new URL(PUBLIC_URL).host;
export const ALLOWED_HOSTS = (process.env.ALLOWED_HOSTS ?? `${publicHost},localhost:${PORT},127.0.0.1:${PORT}`)
  .split(',').map((s) => s.trim()).filter(Boolean);
export const ALLOWED_ORIGINS = (process.env.ALLOWED_ORIGINS ?? 'https://claude.ai')
  .split(',').map((s) => s.trim()).filter(Boolean);
