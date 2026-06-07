// Centralized config from the environment. In the container these come from compose;
// for local dev use `node --env-file=.env`.

export const PORT = Number(process.env.PORT ?? 3000);

// Public origin the server is reached at, used as the OAuth resource id (audience of the
// protected resource metadata) and the base for the MCP endpoint.
export const PUBLIC_URL = (process.env.PUBLIC_URL ?? `http://localhost:${PORT}`).replace(/\/+$/, '');

// Canonical URL of the MCP endpoint — the OAuth resource id advertised in the protected
// resource metadata and stamped on each verified token's AuthInfo.
export const RESOURCE_URL = new URL(`${PUBLIC_URL}/mcp`);

// Vault filesystem root (the knowledge repo). Mounted at /vault in the container.
export const VAULT_ROOT = process.env.VAULT_ROOT ?? '/vault';

// --- Cloudflare Access (OIDC) ---
// This server is a pure OAuth *resource server*: claude.ai authenticates the user against
// the Cloudflare Access for SaaS OIDC app directly, and we validate the bearer token it
// forwards. These values are copied from that app's config (Zero Trust → Access →
// Applications → the OIDC app). See README.

// Token issuer — the Cloudflare app's Issuer endpoint
// (https://<team>.cloudflareaccess.com/cdn-cgi/access/sso/oidc/<client_id>).
export const CF_ISSUER = (process.env.CF_ISSUER ?? '').replace(/\/+$/, '');

// The app's Client ID — also the expected token audience (`aud`).
export const CF_CLIENT_ID = process.env.CF_CLIENT_ID ?? '';

if (!CF_ISSUER || !CF_CLIENT_ID) {
  console.error('FATAL: CF_ISSUER and CF_CLIENT_ID are required — refusing to start an unprotected server.');
  process.exit(1);
}

// CF_ISSUER is guaranteed non-empty past the guard above. The JWKS / authorization / token
// endpoints all live under the issuer at Cloudflare's standard paths; override only if they
// ever differ. JWKS verifies token signatures; the other two are advertised to claude.ai via
// the resource metadata so it knows where to run the OAuth flow.
export const CF_JWKS_URL = process.env.CF_JWKS_URL || `${CF_ISSUER}/jwks`;
export const CF_AUTHORIZATION_ENDPOINT = process.env.CF_AUTHORIZATION_ENDPOINT || `${CF_ISSUER}/authorization`;
export const CF_TOKEN_ENDPOINT = process.env.CF_TOKEN_ENDPOINT || `${CF_ISSUER}/token`;

// Max characters returned in a single tool result (claude.ai caps near 150k).
export const MAX_RESULT_CHARS = Number(process.env.MAX_RESULT_CHARS ?? 140_000);

// DNS-rebinding protection guards LOCAL servers from browser attacks. This server is
// remote and OAuth-gated, and claude.ai's server-side fetch may omit an Origin header,
// so it's off by default to avoid hard-to-debug connector failures. OAuth is the gate.
export const ENABLE_DNS_REBINDING = (process.env.ENABLE_DNS_REBINDING_PROTECTION ?? 'false') === 'true';

const publicHost = new URL(PUBLIC_URL).host;
export const ALLOWED_HOSTS = (process.env.ALLOWED_HOSTS ?? `${publicHost},localhost:${PORT},127.0.0.1:${PORT}`)
  .split(',').map((s) => s.trim()).filter(Boolean);
export const ALLOWED_ORIGINS = (process.env.ALLOWED_ORIGINS ?? 'https://claude.ai')
  .split(',').map((s) => s.trim()).filter(Boolean);
