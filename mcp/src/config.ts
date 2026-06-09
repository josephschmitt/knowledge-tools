// Centralized config from the environment. In the container these come from compose;
// for local dev use `node --env-file=.env`.

export const PORT = Number(process.env.PORT ?? 3000);

// pino log level (fatal|error|warn|info|debug|trace|silent). Default 'info'; set
// LOG_LEVEL=debug to surface per-request and token-verification detail with no code change.
export const LOG_LEVEL = process.env.LOG_LEVEL ?? 'info';

// Public origin the server is reached at — the base for the MCP endpoint (logging, the OAuth
// resource id when auth is enabled, and the default allowed Host for DNS-rebinding protection).
export const PUBLIC_URL = (process.env.PUBLIC_URL ?? `http://localhost:${PORT}`).replace(/\/+$/, '');

// Canonical URL of the MCP endpoint — the OAuth resource id advertised in the protected-resource
// metadata when built-in auth is enabled.
export const RESOURCE_URL = new URL(`${PUBLIC_URL}/mcp`);

// Vault filesystem root (the knowledge repo). Mounted at /vault in the container.
export const VAULT_ROOT = process.env.VAULT_ROOT ?? '/vault';

// --- Optional built-in auth (OAuth 2.1 resource server) ---
// OFF by default: with these unset the server does NO auth and trusts the network it runs on —
// deploy it behind an authenticating proxy (see mcp/README.md). Set all three to make the server
// validate a JWT access token on every /mcp request and advertise its authorization server for
// client discovery (RFC 9728). Vendor-neutral: point them at ANY OIDC issuer (Cloudflare Access,
// Auth0, Keycloak, ...). The server is only a resource server — an external issuer mints tokens.
export const AUTH_ISSUER = (process.env.MCP_AUTH_ISSUER ?? '').replace(/\/+$/, '');
export const AUTH_JWKS_URL = process.env.MCP_AUTH_JWKS_URL ?? '';
export const AUTH_AUDIENCE = process.env.MCP_AUTH_AUDIENCE ?? '';
// Request header carrying the token. Default is the standard `Authorization: Bearer`; override
// for a proxy-injected header (e.g. `cf-access-jwt-assertion`).
export const AUTH_TOKEN_HEADER = (process.env.MCP_AUTH_TOKEN_HEADER ?? 'authorization').toLowerCase();
export const AUTH_ENABLED = Boolean(AUTH_ISSUER && AUTH_JWKS_URL && AUTH_AUDIENCE);

// Half-configured auth is a silent hole (validates nothing but looks intentional) — fail fast.
const authParts = [AUTH_ISSUER, AUTH_JWKS_URL, AUTH_AUDIENCE];
if (authParts.some(Boolean) && !authParts.every(Boolean)) {
  console.error(
    'FATAL: set all of MCP_AUTH_ISSUER, MCP_AUTH_JWKS_URL, MCP_AUTH_AUDIENCE to enable auth, or none to run authless.',
  );
  process.exit(1);
}

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
