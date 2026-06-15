// Optional OAuth 2.1 resource-server auth. Active only when KNOWLEDGE_AUTH_* is configured (see
// config.ts); otherwise every export here is a no-op and the server runs authless behind a
// trusted proxy. When enabled the server (Tier 1) validates a JWT access token on each /mcp
// request and (Tier 2) advertises its authorization server via RFC 9728 protected-resource
// metadata so MCP clients can discover where to authenticate. This is a pure resource server —
// it never issues tokens or logs anyone in; an external authorization server (the issuer) does.
import type { Express, RequestHandler } from 'express';
import { getOAuthProtectedResourceMetadataUrl } from '@modelcontextprotocol/sdk/server/auth/router.js';
import { createRemoteJWKSet, jwtVerify, errors as joseErrors, type JWTPayload } from 'jose';
import {
  AUTH_ENABLED,
  AUTH_ISSUER,
  AUTH_JWKS_URL,
  AUTH_AUDIENCE,
  API_REQUIRE_SCOPES,
  AUTH_TOKEN_HEADER,
  RESOURCE_URL,
} from './config.js';
import { logger } from './logger.js';

// RFC 9728 metadata URL for this resource (e.g. .../.well-known/oauth-protected-resource/mcp).
// Clients reach it from the WWW-Authenticate challenge, then discover the issuer's own OAuth
// metadata from there. Build it only when enabled (the helper just formats a URL).
const prmUrl = AUTH_ENABLED ? getOAuthProtectedResourceMetadataUrl(RESOURCE_URL) : '';
const prmPath = AUTH_ENABLED ? new URL(prmUrl).pathname : '';

// Cached, auto-refreshing remote key set keyed on the `kid` in each token header.
const jwks = AUTH_ENABLED ? createRemoteJWKSet(new URL(AUTH_JWKS_URL)) : null;

function extractToken(value: string | string[] | undefined): string | undefined {
  const raw = Array.isArray(value) ? value[0] : value;
  if (!raw) return undefined;
  // A standard Authorization header carries a `Bearer ` prefix; a proxy-injected header is raw.
  return AUTH_TOKEN_HEADER === 'authorization' ? raw.replace(/^Bearer\s+/i, '') : raw;
}

/** OAuth scopes from a verified token — `scope` (space-delimited, OAuth2) or `scp` (array/string). */
function scopesFromPayload(payload: JWTPayload): Set<string> {
  const raw = (payload as Record<string, unknown>).scope ?? (payload as Record<string, unknown>).scp;
  if (typeof raw === 'string') return new Set(raw.split(/\s+/).filter(Boolean));
  if (Array.isArray(raw)) return new Set(raw.map(String));
  return new Set();
}

/**
 * Tier 1 — reject any request lacking a valid token. No-op when auth is disabled. Gates both /mcp
 * and /api/v1 against the same KNOWLEDGE_AUTH_AUDIENCE. On success it stashes the verified scopes
 * on `res.locals` so requireScope (used on /api/v1) can enforce least-privilege.
 */
export const requireToken: RequestHandler = async (req, res, next) => {
  if (!AUTH_ENABLED) return next();

  const challenge = `Bearer resource_metadata="${prmUrl}"`;
  const token = extractToken(req.headers[AUTH_TOKEN_HEADER]);
  if (!token) {
    res.setHeader('WWW-Authenticate', challenge);
    res.status(401).json({ error: 'missing access token' });
    return;
  }
  try {
    const { payload } = await jwtVerify(token, jwks!, { issuer: AUTH_ISSUER, audience: AUTH_AUDIENCE });
    res.locals.scopes = scopesFromPayload(payload);
    logger.debug({ sub: payload.sub, email: payload.email, aud: payload.aud }, 'token verified');
    next();
  } catch (err) {
    // jose throws typed errors for expired/invalid/claim-mismatch — all map to 401.
    const reason =
      err instanceof joseErrors.JWTExpired
        ? 'token expired'
        : err instanceof joseErrors.JOSEError
          ? `invalid token: ${err.code}`
          : 'invalid token';
    logger.warn({ reason, detail: (err as Error).message }, 'token rejected');
    res.setHeader('WWW-Authenticate', `${challenge}, error="invalid_token"`);
    res.status(401).json({ error: reason });
  }
};

/**
 * Require an OAuth `scope` on the request, read from the token verified by requireToken. No-op when
 * auth is disabled or KNOWLEDGE_API_REQUIRE_SCOPES is off (so deployments that don't issue these
 * scopes are unaffected). 403 when the scope is absent.
 */
export function requireScope(scope: string): RequestHandler {
  return (_req, res, next) => {
    if (!AUTH_ENABLED || !API_REQUIRE_SCOPES) return next();
    const scopes = res.locals.scopes as Set<string> | undefined;
    if (scopes?.has(scope)) return next();
    res.status(403).json({ error: `insufficient scope: ${scope} required` });
  };
}

/** Tier 2 — advertise the authorization server (RFC 9728). No-op when auth is disabled. */
export function mountAuthMetadata(app: Express): void {
  if (!AUTH_ENABLED) return;
  // Point clients at the issuer; they fetch its OAuth/OIDC metadata (and run the flow) directly.
  const metadata = {
    resource: RESOURCE_URL.href,
    authorization_servers: [AUTH_ISSUER],
    scopes_supported: ['openid', 'email', 'profile'],
    resource_name: 'Knowledge Vault',
  };
  app.get(prmPath, (_req, res) => res.json(metadata));
  logger.info({ issuer: AUTH_ISSUER, resourceMetadata: prmUrl }, 'built-in auth enabled');
}
