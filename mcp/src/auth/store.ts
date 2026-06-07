// SQLite-backed persistence for OAuth state (registered clients, auth codes, tokens).
// Persisting means claude.ai doesn't have to re-register / re-auth after a restart.
import { DatabaseSync } from 'node:sqlite';
import { mkdirSync } from 'node:fs';
import path from 'node:path';
import type { OAuthClientInformationFull } from '@modelcontextprotocol/sdk/shared/auth.js';
import { DB_PATH } from '../config.js';

mkdirSync(path.dirname(DB_PATH), { recursive: true });
const db = new DatabaseSync(DB_PATH);
db.exec('PRAGMA journal_mode = WAL;');
db.exec(`
  CREATE TABLE IF NOT EXISTS clients (
    client_id TEXT PRIMARY KEY,
    data      TEXT NOT NULL
  );
  CREATE TABLE IF NOT EXISTS auth_codes (
    code           TEXT PRIMARY KEY,
    client_id      TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    redirect_uri   TEXT NOT NULL,
    scopes         TEXT NOT NULL DEFAULT '',
    resource       TEXT,
    expires_at     INTEGER NOT NULL,
    used           INTEGER NOT NULL DEFAULT 0
  );
  CREATE TABLE IF NOT EXISTS access_tokens (
    token      TEXT PRIMARY KEY,
    client_id  TEXT NOT NULL,
    scopes     TEXT NOT NULL DEFAULT '',
    resource   TEXT,
    expires_at INTEGER NOT NULL
  );
  CREATE TABLE IF NOT EXISTS refresh_tokens (
    token     TEXT PRIMARY KEY,
    client_id TEXT NOT NULL,
    scopes    TEXT NOT NULL DEFAULT '',
    resource  TEXT
  );
`);

// --- Clients (Dynamic Client Registration) ---

export function getClient(clientId: string): OAuthClientInformationFull | undefined {
  const row = db.prepare('SELECT data FROM clients WHERE client_id = ?').get(clientId) as
    | { data: string }
    | undefined;
  return row ? (JSON.parse(row.data) as OAuthClientInformationFull) : undefined;
}

export function saveClient(client: OAuthClientInformationFull): void {
  db.prepare('INSERT OR REPLACE INTO clients (client_id, data) VALUES (?, ?)').run(
    client.client_id,
    JSON.stringify(client),
  );
}

// --- Authorization codes ---

export interface AuthCodeRecord {
  code: string;
  client_id: string;
  code_challenge: string;
  redirect_uri: string;
  scopes: string[];
  resource?: string;
  expires_at: number;
}

export function saveAuthCode(rec: AuthCodeRecord): void {
  db.prepare(
    `INSERT INTO auth_codes (code, client_id, code_challenge, redirect_uri, scopes, resource, expires_at, used)
     VALUES (?, ?, ?, ?, ?, ?, ?, 0)`,
  ).run(
    rec.code,
    rec.client_id,
    rec.code_challenge,
    rec.redirect_uri,
    rec.scopes.join(' '),
    rec.resource ?? null,
    rec.expires_at,
  );
}

export function getAuthCode(code: string): (AuthCodeRecord & { used: boolean }) | undefined {
  const row = db.prepare('SELECT * FROM auth_codes WHERE code = ?').get(code) as
    | {
        code: string;
        client_id: string;
        code_challenge: string;
        redirect_uri: string;
        scopes: string;
        resource: string | null;
        expires_at: number;
        used: number;
      }
    | undefined;
  if (!row) return undefined;
  return {
    code: row.code,
    client_id: row.client_id,
    code_challenge: row.code_challenge,
    redirect_uri: row.redirect_uri,
    scopes: row.scopes ? row.scopes.split(' ') : [],
    resource: row.resource ?? undefined,
    expires_at: row.expires_at,
    used: row.used === 1,
  };
}

export function markAuthCodeUsed(code: string): void {
  db.prepare('UPDATE auth_codes SET used = 1 WHERE code = ?').run(code);
}

// --- Access tokens ---

export interface TokenRecord {
  token: string;
  client_id: string;
  scopes: string[];
  resource?: string;
  expires_at: number;
}

export function saveAccessToken(rec: TokenRecord): void {
  db.prepare(
    'INSERT OR REPLACE INTO access_tokens (token, client_id, scopes, resource, expires_at) VALUES (?, ?, ?, ?, ?)',
  ).run(rec.token, rec.client_id, rec.scopes.join(' '), rec.resource ?? null, rec.expires_at);
}

export function getAccessToken(token: string): TokenRecord | undefined {
  const row = db.prepare('SELECT * FROM access_tokens WHERE token = ?').get(token) as
    | { token: string; client_id: string; scopes: string; resource: string | null; expires_at: number }
    | undefined;
  if (!row) return undefined;
  return {
    token: row.token,
    client_id: row.client_id,
    scopes: row.scopes ? row.scopes.split(' ') : [],
    resource: row.resource ?? undefined,
    expires_at: row.expires_at,
  };
}

export function deleteAccessToken(token: string): void {
  db.prepare('DELETE FROM access_tokens WHERE token = ?').run(token);
}

// --- Refresh tokens ---

export function saveRefreshToken(rec: Omit<TokenRecord, 'expires_at'>): void {
  db.prepare(
    'INSERT OR REPLACE INTO refresh_tokens (token, client_id, scopes, resource) VALUES (?, ?, ?, ?)',
  ).run(rec.token, rec.client_id, rec.scopes.join(' '), rec.resource ?? null);
}

export function getRefreshToken(token: string): Omit<TokenRecord, 'expires_at'> | undefined {
  const row = db.prepare('SELECT * FROM refresh_tokens WHERE token = ?').get(token) as
    | { token: string; client_id: string; scopes: string; resource: string | null }
    | undefined;
  if (!row) return undefined;
  return {
    token: row.token,
    client_id: row.client_id,
    scopes: row.scopes ? row.scopes.split(' ') : [],
    resource: row.resource ?? undefined,
  };
}

export function deleteRefreshToken(token: string): void {
  db.prepare('DELETE FROM refresh_tokens WHERE token = ?').run(token);
}
