// Filesystem helpers over the vault. Every path is resolved and confined to VAULT_ROOT,
// so externally-supplied note paths can't escape the vault (path traversal).
import { promises as fs } from 'node:fs';
import path from 'node:path';
import { VAULT_ROOT, MAX_RESULT_CHARS } from './config.js';

const WIKI_DIR = path.join(VAULT_ROOT, 'wiki');
const INBOX_DIR = path.join(VAULT_ROOT, 'inbox');
const INDEX_FILE = path.join(VAULT_ROOT, 'index.md');

/** Resolve `rel` under `base`, throwing if it escapes `base`. */
function confine(base: string, rel: string): string {
  const resolved = path.resolve(base, rel);
  const baseWithSep = base.endsWith(path.sep) ? base : base + path.sep;
  if (resolved !== base && !resolved.startsWith(baseWithSep)) {
    throw new Error(`Path escapes the allowed directory: ${rel}`);
  }
  return resolved;
}

function cap(text: string): string {
  if (text.length <= MAX_RESULT_CHARS) return text;
  return text.slice(0, MAX_RESULT_CHARS) + `\n\n…[truncated — result exceeded ${MAX_RESULT_CHARS} characters]`;
}

async function walkMarkdown(dir: string): Promise<string[]> {
  const out: string[] = [];
  let entries;
  try {
    entries = await fs.readdir(dir, { withFileTypes: true });
  } catch {
    return out;
  }
  for (const e of entries) {
    const full = path.join(dir, e.name);
    if (e.isDirectory()) {
      out.push(...(await walkMarkdown(full)));
    } else if (e.isFile() && e.name.toLowerCase().endsWith('.md')) {
      out.push(full);
    }
  }
  return out;
}

/** Relative-to-wiki paths of every note, sorted. */
export async function listNotes(): Promise<string[]> {
  const files = await walkMarkdown(WIKI_DIR);
  return files.map((f) => path.relative(WIKI_DIR, f)).sort();
}

/** The navigation index. */
export async function readIndex(): Promise<string> {
  try {
    return cap(await fs.readFile(INDEX_FILE, 'utf8'));
  } catch {
    return '(index.md not found)';
  }
}

/** Raw markdown of one note. Accepts `name` or `name.md`, relative to wiki/. */
export async function getNote(notePath: string): Promise<string> {
  let rel = notePath.trim();
  if (!rel.toLowerCase().endsWith('.md')) rel += '.md';
  const full = confine(WIKI_DIR, rel);
  return cap(await fs.readFile(full, 'utf8'));
}

export interface SearchHit {
  note: string;
  snippets: string[];
}

/** Case-insensitive substring search across all wiki notes, with line-context snippets. */
export async function searchWiki(query: string, maxHits = 20): Promise<SearchHit[]> {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  const files = await walkMarkdown(WIKI_DIR);
  const hits: SearchHit[] = [];
  for (const file of files) {
    let content: string;
    try {
      content = await fs.readFile(file, 'utf8');
    } catch {
      continue;
    }
    if (!content.toLowerCase().includes(q)) continue;
    const lines = content.split('\n');
    const snippets: string[] = [];
    for (let i = 0; i < lines.length && snippets.length < 5; i++) {
      if (lines[i].toLowerCase().includes(q)) {
        const start = Math.max(0, i - 1);
        const end = Math.min(lines.length, i + 2);
        snippets.push(lines.slice(start, end).join('\n').trim());
      }
    }
    hits.push({ note: path.relative(WIKI_DIR, file), snippets });
    if (hits.length >= maxHits) break;
  }
  return hits;
}

function slugify(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 60) || 'capture';
}

/** Append a raw capture to inbox/. Returns the repo-relative path written. */
export async function appendToInbox(text: string, title?: string): Promise<string> {
  await fs.mkdir(INBOX_DIR, { recursive: true });
  const stamp = new Date().toISOString().replace(/[:.]/g, '-');
  const slug = slugify(title ?? text.split('\n')[0] ?? 'capture');
  const filename = `${stamp}-${slug}.md`;
  const full = confine(INBOX_DIR, filename);
  const body = title ? `# ${title}\n\n${text}\n` : `${text}\n`;
  await fs.writeFile(full, body, { flag: 'wx' });
  return path.relative(VAULT_ROOT, full);
}
