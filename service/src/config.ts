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

// --- Which surfaces to serve ---
// The server can expose the MCP endpoint (/mcp), the REST API (/api/v1), or both. Both are ON by
// default; set either to 'false' to run a single-surface deployment (e.g. a headless host that
// only wants REST, or a claude.ai connector that only wants MCP). A disabled surface's paths 404.
export const ENABLE_MCP = (process.env.KNOWLEDGE_ENABLE_MCP ?? 'true') !== 'false';
export const ENABLE_REST = (process.env.KNOWLEDGE_ENABLE_REST ?? 'true') !== 'false';

// A server serving neither surface is a misconfiguration — fail fast rather than start a process
// that only answers /healthz.
if (!ENABLE_MCP && !ENABLE_REST) {
  console.error(
    'FATAL: both KNOWLEDGE_ENABLE_MCP and KNOWLEDGE_ENABLE_REST are false — nothing to serve. Enable at least one.',
  );
  process.exit(1);
}

// --- Optional built-in auth (OAuth 2.1 resource server) ---
// OFF by default: with these unset the server does NO auth and trusts the network it runs on —
// deploy it behind an authenticating proxy (see service/README.md). Set all three to make the server
// validate a JWT access token on every /mcp and /api/v1 request and advertise its authorization server for
// client discovery (RFC 9728). Vendor-neutral: point them at ANY OIDC issuer (Cloudflare Access,
// Auth0, Keycloak, ...). The server is only a resource server — an external issuer mints tokens.
export const AUTH_ISSUER = (process.env.KNOWLEDGE_AUTH_ISSUER ?? '').replace(/\/+$/, '');
export const AUTH_JWKS_URL = process.env.KNOWLEDGE_AUTH_JWKS_URL ?? '';
export const AUTH_AUDIENCE = process.env.KNOWLEDGE_AUTH_AUDIENCE ?? '';
// Request header carrying the token. Default is the standard `Authorization: Bearer`; override
// for a proxy-injected header (e.g. `cf-access-jwt-assertion`).
export const AUTH_TOKEN_HEADER = (process.env.KNOWLEDGE_AUTH_TOKEN_HEADER ?? 'authorization').toLowerCase();
export const AUTH_ENABLED = Boolean(AUTH_ISSUER && AUTH_JWKS_URL && AUTH_AUDIENCE);

// Half-configured auth is a silent hole (validates nothing but looks intentional) — fail fast.
const authParts = [AUTH_ISSUER, AUTH_JWKS_URL, AUTH_AUDIENCE];
if (authParts.some(Boolean) && !authParts.every(Boolean)) {
  console.error(
    'FATAL: set all of KNOWLEDGE_AUTH_ISSUER, KNOWLEDGE_AUTH_JWKS_URL, KNOWLEDGE_AUTH_AUDIENCE to enable auth, or none to run authless.',
  );
  process.exit(1);
}

// --- REST API (/api/v1) least-privilege scopes ---
// Opt-in. When true (and built-in auth is on), every /api/v1 request must carry an OAuth scope:
// `vault.read` for GETs, `vault.write` for writes. /api/v1 validates against the SAME
// KNOWLEDGE_AUTH_AUDIENCE as /mcp — scopes, not a separate audience, are the least-privilege lever
// (both surfaces front the same vault, so a per-surface audience would buy little). OFF by default
// so tokens that don't carry these scopes (e.g. the interactive MCP login) keep working on REST —
// turn it on once your IdP issues the scopes to the calling client.
export const API_REQUIRE_SCOPES = (process.env.KNOWLEDGE_API_REQUIRE_SCOPES ?? 'false') === 'true';
export const API_SCOPE_READ = 'vault.read';
export const API_SCOPE_WRITE = 'vault.write';

// Scope enforcement rides on top of token validation — with built-in auth off there's no token to
// read scopes from, so the checks silently no-op. Warn (don't fail) so an operator who set this
// expecting enforcement isn't left with a false sense of least-privilege.
if (API_REQUIRE_SCOPES && !AUTH_ENABLED) {
  console.warn(
    'WARNING: KNOWLEDGE_API_REQUIRE_SCOPES=true has no effect without built-in auth — set ' +
      'KNOWLEDGE_AUTH_ISSUER/JWKS_URL/AUDIENCE, or the /api/v1 scope checks are skipped.',
  );
}

// --- Judgment-call review channel ---
// The list_questions / get_question / answer_question tools operate over one of two backends,
// matching whichever channel the host's synthesize/resolve jobs use:
//   files  — the queue under inbox/.review/ (default; needs no external creds, writes only inbox/).
//   github — the vault's GitHub issues, reached through the REST API. Set KNOWLEDGE_GITHUB_TOKEN (a PAT
//            with issues:read+write) and KNOWLEDGE_GITHUB_REPO ("owner/repo") to enable it, so the same
//            tools work — and answering one here comments + labels the issue vault:answered, which
//            is exactly what the host's /resolve then applies and closes.
// This adds outbound GitHub access + a token to the server, so it's OFF unless configured.
export const GITHUB_TOKEN = process.env.KNOWLEDGE_GITHUB_TOKEN ?? '';
export const GITHUB_REPO = process.env.KNOWLEDGE_GITHUB_REPO ?? ''; // "owner/repo"
export const GITHUB_API_URL = (process.env.KNOWLEDGE_GITHUB_API_URL ?? 'https://api.github.com').replace(/\/+$/, '');

const githubConfigured = Boolean(GITHUB_TOKEN && GITHUB_REPO);
// Half-set GitHub creds are a silent misconfig — fail fast (mirrors the auth check above).
if (Boolean(GITHUB_TOKEN) !== Boolean(GITHUB_REPO)) {
  console.error('FATAL: set both KNOWLEDGE_GITHUB_TOKEN and KNOWLEDGE_GITHUB_REPO to back the review tools with GitHub, or neither.');
  process.exit(1);
}

// 'files' | 'github'. Explicit KNOWLEDGE_REVIEW_CHANNEL wins; otherwise auto-detect from the creds.
const reviewChannelEnv = (process.env.KNOWLEDGE_REVIEW_CHANNEL ?? '').toLowerCase();
export const REVIEW_CHANNEL: 'files' | 'github' =
  reviewChannelEnv === 'github' || reviewChannelEnv === 'files'
    ? reviewChannelEnv
    : githubConfigured
      ? 'github'
      : 'files';

if (REVIEW_CHANNEL === 'github' && !githubConfigured) {
  console.error('FATAL: KNOWLEDGE_REVIEW_CHANNEL=github needs both KNOWLEDGE_GITHUB_TOKEN and KNOWLEDGE_GITHUB_REPO.');
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
