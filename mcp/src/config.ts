// Centralized config from the environment. In the container these come from compose;
// for local dev use `node --env-file=.env`.

export const PORT = Number(process.env.PORT ?? 3000);

// Public origin the server is reached at, used as the OAuth issuer + resource id.
export const PUBLIC_URL = (process.env.PUBLIC_URL ?? `http://localhost:${PORT}`).replace(/\/+$/, '');

// Vault filesystem root (the knowledge repo). Mounted at /vault in the container.
export const VAULT_ROOT = process.env.VAULT_ROOT ?? '/vault';

// Single-user approval secret for the OAuth login page. Required — no default.
export const LOGIN_PASSPHRASE = process.env.LOGIN_PASSPHRASE ?? '';

// Where the OAuth client/token store lives (persisted volume in the container).
export const DB_PATH = process.env.OAUTH_DB_PATH ?? '/data/oauth.sqlite';

// Access-token lifetime (seconds). Refresh tokens are long-lived.
export const ACCESS_TOKEN_TTL = Number(process.env.ACCESS_TOKEN_TTL ?? 3600);
// Authorization-code lifetime (seconds).
export const AUTH_CODE_TTL = Number(process.env.AUTH_CODE_TTL ?? 600);

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

if (!LOGIN_PASSPHRASE) {
  console.error('FATAL: LOGIN_PASSPHRASE is not set — refusing to start an unprotected server.');
  process.exit(1);
}
