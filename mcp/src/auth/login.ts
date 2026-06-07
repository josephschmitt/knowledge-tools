// The single-user approval page. claude.ai sends the user here during the OAuth flow;
// they enter the shared passphrase to approve the connection.
import type { AuthorizationParams } from '@modelcontextprotocol/sdk/server/auth/provider.js';

function esc(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]!));
}

/** Render the passphrase form. Hidden fields carry the OAuth request to /approve. */
export function loginPage(clientId: string, params: AuthorizationParams, opts: { error?: boolean } = {}): string {
  const hidden = (name: string, value: string | undefined) =>
    value === undefined ? '' : `<input type="hidden" name="${name}" value="${esc(value)}">`;
  const errorBanner = opts.error
    ? '<p class="err">Incorrect passphrase. Try again.</p>'
    : '';
  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Knowledge Vault — Authorize</title>
  <style>
    body { font: 16px/1.5 system-ui, sans-serif; background: #faf9f6; color: #222;
           display: grid; place-items: center; min-height: 100vh; margin: 0; }
    form { background: #fff; padding: 2rem; border-radius: 12px; box-shadow: 0 2px 16px rgba(0,0,0,.08);
           width: min(92vw, 360px); }
    h1 { font-size: 1.1rem; margin: 0 0 .25rem; }
    p.sub { color: #666; font-size: .9rem; margin: 0 0 1.25rem; }
    label { display: block; font-size: .85rem; color: #444; margin-bottom: .35rem; }
    input[type=password] { width: 100%; padding: .6rem .7rem; border: 1px solid #ccc; border-radius: 8px;
           box-sizing: border-box; font-size: 1rem; }
    button { margin-top: 1rem; width: 100%; padding: .65rem; border: 0; border-radius: 8px;
             background: #c4623a; color: #fff; font-size: 1rem; cursor: pointer; }
    .err { color: #b00020; font-size: .85rem; margin: 0 0 1rem; }
  </style>
</head>
<body>
  <form method="POST" action="/approve">
    <h1>Authorize access to your knowledge vault</h1>
    <p class="sub">Claude is requesting access. Enter your passphrase to approve.</p>
    ${errorBanner}
    <label for="passphrase">Passphrase</label>
    <input id="passphrase" name="passphrase" type="password" autofocus required>
    ${hidden('client_id', clientId)}
    ${hidden('redirect_uri', params.redirectUri)}
    ${hidden('code_challenge', params.codeChallenge)}
    ${hidden('state', params.state)}
    ${hidden('scope', (params.scopes ?? []).join(' '))}
    ${hidden('resource', params.resource?.toString())}
    <button type="submit">Approve</button>
  </form>
</body>
</html>`;
}
