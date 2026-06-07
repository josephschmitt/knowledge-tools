// Bearer-token verifier for the MCP resource server. We do not issue tokens — claude.ai
// authenticates the user against the Cloudflare Access for SaaS OIDC app and forwards the
// resulting token. Here we verify that token's signature (against Cloudflare's JWKS) and
// its issuer/audience, then hand the SDK an AuthInfo. The MCP SDK's requireBearerAuth only
// needs the single `verifyAccessToken` method (the OAuthTokenVerifier interface).
import { createRemoteJWKSet, jwtVerify, errors as joseErrors } from 'jose';
import type { OAuthTokenVerifier } from '@modelcontextprotocol/sdk/server/auth/provider.js';
import type { AuthInfo } from '@modelcontextprotocol/sdk/server/auth/types.js';
import { InvalidTokenError } from '@modelcontextprotocol/sdk/server/auth/errors.js';
import { CF_ISSUER, CF_JWKS_URL, CF_CLIENT_ID, CF_AUDIENCE, RESOURCE_URL } from '../config.js';
import { logger } from '../logger.js';

// Cached, auto-refreshing remote key set keyed on the `kid` in each token header.
const jwks = createRemoteJWKSet(new URL(CF_JWKS_URL));

/** Pull OAuth scopes out of the verified claims (Cloudflare uses `scope`; fall back to none). */
function scopesFrom(payload: Record<string, unknown>): string[] {
  const raw = payload.scope ?? payload.scp;
  if (typeof raw === 'string') return raw.split(' ').filter(Boolean);
  if (Array.isArray(raw)) return raw.filter((s): s is string => typeof s === 'string');
  return [];
}

export const verifier: OAuthTokenVerifier = {
  async verifyAccessToken(token: string): Promise<AuthInfo> {
    try {
      const { payload } = await jwtVerify(token, jwks, {
        issuer: CF_ISSUER,
        audience: CF_AUDIENCE,
      });
      const scopes = scopesFrom(payload);
      logger.debug({ sub: payload.sub, aud: payload.aud, scopes }, 'token verified');
      return {
        token,
        clientId: CF_CLIENT_ID,
        scopes,
        // `jwtVerify` already enforced exp; surface it so the SDK can too.
        ...(typeof payload.exp === 'number' ? { expiresAt: payload.exp } : {}),
        resource: RESOURCE_URL,
      };
    } catch (err) {
      // jose throws typed errors for expired/invalid/claim-mismatch — all map to 401.
      const reason =
        err instanceof joseErrors.JWTExpired
          ? 'Access token expired'
          : err instanceof joseErrors.JOSEError
            ? `Invalid access token: ${err.code}`
            : 'Invalid access token';
      // A present-but-invalid token is rare and worth surfacing (this is exactly the signal
      // that caught the audience mismatch). The message carries jose's specific failure.
      logger.warn({ reason, detail: (err as Error).message }, 'token rejected');
      throw new InvalidTokenError(reason);
    }
  },
};
