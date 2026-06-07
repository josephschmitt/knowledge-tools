// OAuth 2.1 authorization server for the vault, implementing the MCP SDK's
// OAuthServerProvider. Single user: the only "login" is a shared passphrase.
// The SDK validates PKCE (via challengeForAuthorizationCode); we issue codes + tokens.
import { randomUUID, randomBytes, timingSafeEqual } from 'node:crypto';
import type { Response } from 'express';
import type {
  OAuthServerProvider,
  AuthorizationParams,
} from '@modelcontextprotocol/sdk/server/auth/provider.js';
import type { OAuthRegisteredClientsStore } from '@modelcontextprotocol/sdk/server/auth/clients.js';
import type { AuthInfo } from '@modelcontextprotocol/sdk/server/auth/types.js';
import type {
  OAuthClientInformationFull,
  OAuthTokens,
  OAuthTokenRevocationRequest,
} from '@modelcontextprotocol/sdk/shared/auth.js';
import { InvalidGrantError, InvalidTokenError } from '@modelcontextprotocol/sdk/server/auth/errors.js';
import { ACCESS_TOKEN_TTL, AUTH_CODE_TTL, LOGIN_PASSPHRASE } from '../config.js';
import * as store from './store.js';
import { loginPage } from './login.js';

const nowSec = () => Math.floor(Date.now() / 1000);
const newToken = () => randomBytes(32).toString('base64url');

/** Constant-time passphrase check for the approval page. */
export function checkPassphrase(input: string): boolean {
  const a = Buffer.from(input);
  const b = Buffer.from(LOGIN_PASSPHRASE);
  return a.length === b.length && timingSafeEqual(a, b);
}

/** Issue an authorization code after the user approves on the login page. */
export function issueAuthorizationCode(input: {
  clientId: string;
  redirectUri: string;
  codeChallenge: string;
  scopes: string[];
  resource?: string;
}): string {
  const code = newToken();
  store.saveAuthCode({
    code,
    client_id: input.clientId,
    code_challenge: input.codeChallenge,
    redirect_uri: input.redirectUri,
    scopes: input.scopes,
    resource: input.resource,
    expires_at: nowSec() + AUTH_CODE_TTL,
  });
  return code;
}

const clientsStore: OAuthRegisteredClientsStore = {
  getClient(clientId) {
    return store.getClient(clientId);
  },
  registerClient(client) {
    const usesSecret =
      client.token_endpoint_auth_method !== undefined && client.token_endpoint_auth_method !== 'none';
    const full: OAuthClientInformationFull = {
      ...client,
      client_id: randomUUID(),
      client_id_issued_at: nowSec(),
      ...(usesSecret ? { client_secret: newToken(), client_secret_expires_at: 0 } : {}),
    };
    store.saveClient(full);
    return full;
  },
};

export const provider: OAuthServerProvider = {
  get clientsStore() {
    return clientsStore;
  },

  async authorize(client: OAuthClientInformationFull, params: AuthorizationParams, res: Response) {
    // Render the approval page. The form POSTs to /approve, which calls
    // issueAuthorizationCode() and redirects back to the client's redirect_uri.
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.send(loginPage(client.client_id, params));
  },

  async challengeForAuthorizationCode(client: OAuthClientInformationFull, authorizationCode: string) {
    const rec = store.getAuthCode(authorizationCode);
    if (!rec || rec.client_id !== client.client_id) {
      throw new InvalidGrantError('Invalid authorization code');
    }
    return rec.code_challenge;
  },

  async exchangeAuthorizationCode(
    client: OAuthClientInformationFull,
    authorizationCode: string,
    _codeVerifier?: string,
    redirectUri?: string,
  ): Promise<OAuthTokens> {
    const rec = store.getAuthCode(authorizationCode);
    if (!rec || rec.client_id !== client.client_id) throw new InvalidGrantError('Invalid authorization code');
    if (rec.used) throw new InvalidGrantError('Authorization code already used');
    if (rec.expires_at < nowSec()) throw new InvalidGrantError('Authorization code expired');
    if (redirectUri !== undefined && redirectUri !== rec.redirect_uri) {
      throw new InvalidGrantError('redirect_uri mismatch');
    }
    store.markAuthCodeUsed(authorizationCode);

    const access = newToken();
    const refresh = newToken();
    store.saveAccessToken({
      token: access,
      client_id: client.client_id,
      scopes: rec.scopes,
      resource: rec.resource,
      expires_at: nowSec() + ACCESS_TOKEN_TTL,
    });
    store.saveRefreshToken({ token: refresh, client_id: client.client_id, scopes: rec.scopes, resource: rec.resource });

    return {
      access_token: access,
      token_type: 'Bearer',
      expires_in: ACCESS_TOKEN_TTL,
      refresh_token: refresh,
      ...(rec.scopes.length ? { scope: rec.scopes.join(' ') } : {}),
    };
  },

  async exchangeRefreshToken(
    client: OAuthClientInformationFull,
    refreshToken: string,
    scopes?: string[],
  ): Promise<OAuthTokens> {
    const rec = store.getRefreshToken(refreshToken);
    if (!rec || rec.client_id !== client.client_id) throw new InvalidGrantError('Invalid refresh token');
    // Requested scopes must be a subset of the originally granted scopes.
    const granted = rec.scopes;
    const effective = scopes && scopes.length ? scopes.filter((s) => granted.includes(s)) : granted;

    const access = newToken();
    store.saveAccessToken({
      token: access,
      client_id: client.client_id,
      scopes: effective,
      resource: rec.resource,
      expires_at: nowSec() + ACCESS_TOKEN_TTL,
    });

    return {
      access_token: access,
      token_type: 'Bearer',
      expires_in: ACCESS_TOKEN_TTL,
      refresh_token: refreshToken,
      ...(effective.length ? { scope: effective.join(' ') } : {}),
    };
  },

  async verifyAccessToken(token: string): Promise<AuthInfo> {
    const rec = store.getAccessToken(token);
    if (!rec) throw new InvalidTokenError('Invalid access token');
    if (rec.expires_at < nowSec()) {
      store.deleteAccessToken(token);
      throw new InvalidTokenError('Access token expired');
    }
    return {
      token,
      clientId: rec.client_id,
      scopes: rec.scopes,
      expiresAt: rec.expires_at,
      ...(rec.resource ? { resource: new URL(rec.resource) } : {}),
    };
  },

  async revokeToken(_client: OAuthClientInformationFull, request: OAuthTokenRevocationRequest) {
    store.deleteAccessToken(request.token);
    store.deleteRefreshToken(request.token);
  },
};
