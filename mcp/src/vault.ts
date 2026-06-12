// Filesystem helpers over the vault. Every path is resolved and confined to VAULT_ROOT,
// so externally-supplied note paths can't escape the vault (path traversal).
import { promises as fs } from 'node:fs';
import path from 'node:path';
import { VAULT_ROOT, MAX_RESULT_CHARS } from './config.js';

const WIKI_DIR = path.join(VAULT_ROOT, 'wiki');
const INBOX_DIR = path.join(VAULT_ROOT, 'inbox');
const INDEX_FILE = path.join(VAULT_ROOT, 'index.md');

// Compile coordination lives under inbox/.compile/ — the one mount the container can
// write. It's a subdir, so the host's capture snapshot (top-level files only) ignores it.
const COMPILE_DIR = path.join(INBOX_DIR, '.compile');
const COMPILE_REQUEST = path.join(COMPILE_DIR, 'request');
const COMPILE_STATUS = path.join(COMPILE_DIR, 'status.json');

// The judgment-call review queue, the GitHub-issue-free Q&A channel. Also under inbox/ —
// the one mount the container can write — so the human can answer questions from chat.
// Each open question is one markdown file with a `status` go-signal (open|answered|applied);
// the host's synthesize/resolve jobs are the producer/consumer (see template/.claude/commands).
const REVIEW_DIR = path.join(INBOX_DIR, '.review');

/** Resolve `rel` under `base`, throwing if it escapes `base`. */
function confine(base: string, rel: string): string {
  const resolved = path.resolve(base, rel);
  const baseWithSep = base.endsWith(path.sep) ? base : base + path.sep;
  if (resolved !== base && !resolved.startsWith(baseWithSep)) {
    throw new Error(`Path escapes the allowed directory: ${rel}`);
  }
  return resolved;
}

export function cap(text: string): string {
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
    let isDir = e.isDirectory();
    let isFile = e.isFile();
    // Container bind/overlay/network mounts often return DT_UNKNOWN from readdir,
    // so Dirent.isFile()/isDirectory() are *both* false even for a regular file —
    // and inconsistently so between calls, which is how a note can show up in one
    // walk (search_wiki) but vanish from another (list_notes). Resolve the type
    // with stat() (also follows symlinked notes) so nothing is silently dropped.
    // See issue #12.
    if (!isDir && !isFile) {
      try {
        const st = await fs.stat(full);
        isDir = st.isDirectory();
        isFile = st.isFile();
      } catch {
        continue;
      }
    }
    if (isDir) {
      out.push(...(await walkMarkdown(full)));
    } else if (isFile && e.name.toLowerCase().endsWith('.md')) {
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

/** Count top-level inbox captures, ignoring control files (.gitkeep, dotfiles, .compile/). */
export async function countInboxCaptures(): Promise<number> {
  let entries;
  try {
    entries = await fs.readdir(INBOX_DIR, { withFileTypes: true });
  } catch {
    return 0;
  }
  // Same DT_UNKNOWN caveat as walkMarkdown: a capture whose dirent type didn't
  // resolve would be missed, undercounting pending_inbox_count. Stat the unknowns.
  let count = 0;
  for (const e of entries) {
    if (e.name.startsWith('.')) continue;
    let isFile = e.isFile();
    if (!isFile && !e.isDirectory()) {
      try {
        isFile = (await fs.stat(path.join(INBOX_DIR, e.name))).isFile();
      } catch {
        continue;
      }
    }
    if (isFile) count++;
  }
  return count;
}

export interface CompileStatus {
  running?: boolean;
  started_at?: string;
  last_compiled_at?: string;
  last_manual_compile_at?: string;
  cooldown_seconds?: number;
  summary?: string;
}

/** Read the host-written compile status, or null if it doesn't exist / is unparseable. */
export async function readCompileStatus(): Promise<CompileStatus | null> {
  try {
    return JSON.parse(await fs.readFile(COMPILE_STATUS, 'utf8')) as CompileStatus;
  } catch {
    return null;
  }
}

// Mirrors the host's KNOWLEDGE_COMPILE_COOLDOWN default; only used when status.json is
// absent (before the first compile writes it).
const DEFAULT_COOLDOWN_SECONDS = 3600;

/** Treat the host's empty-string timestamps (iso_of writes "" when an epoch file is missing) as absent. */
function nonEmpty(s: string | undefined): string | null {
  return s && s.length > 0 ? s : null;
}

export interface VaultStatus {
  /** ISO time the most recent *successful* compile finished, or null if none yet. */
  last_compiled_at: string | null;
  /** Captures sitting in inbox/ not yet compiled. */
  pending_inbox_count: number;
  /**
   * ISO time the next manual compile_run is allowed (the hourly cooldown clears).
   * null when no manual compile has run yet (available now). A value in the past also
   * means available now; a future value means wait.
   */
  manual_compile_available_at: string | null;
  /** Whether a compile is in progress right now. */
  running: boolean;
}

/**
 * Pollable vault status. Reads the host-written compile status plus the inbox count.
 * A `last_compiled_at` newer than a compile_run trigger time means that run finished.
 */
export async function getVaultStatus(): Promise<VaultStatus> {
  const status = await readCompileStatus();
  const pending = await countInboxCaptures();

  let manualAvailableAt: string | null = null;
  const lastManual = nonEmpty(status?.last_manual_compile_at);
  if (lastManual) {
    const t = Date.parse(lastManual);
    if (!Number.isNaN(t)) {
      const cooldownMs = (status?.cooldown_seconds ?? DEFAULT_COOLDOWN_SECONDS) * 1000;
      manualAvailableAt = new Date(t + cooldownMs).toISOString();
    }
  }

  return {
    last_compiled_at: nonEmpty(status?.last_compiled_at),
    pending_inbox_count: pending,
    manual_compile_available_at: manualAvailableAt,
    running: status?.running ?? false,
  };
}

/**
 * Request a manual compile by dropping the sentinel the host's systemd .path unit watches.
 * Writes inbox/.compile/request with the trigger time. The host consumes it when it runs.
 */
export async function requestCompile(): Promise<void> {
  await fs.mkdir(COMPILE_DIR, { recursive: true });
  await fs.writeFile(COMPILE_REQUEST, `${new Date().toISOString()}\n`);
}

// --- Review queue (judgment-call Q&A channel) -------------------------------------------
//
// A question is one markdown file under inbox/.review/ with a small `key: value` frontmatter
// block and `## Question` / `## Answer` / `## Discussion` sections. We hand-parse the fixed
// schema rather than pull in a YAML dependency on the write path: keys are flat scalars,
// preserved in file order so a round-trip stays stable.

export type QuestionStatus = 'open' | 'answered' | 'applied';

export interface QuestionSummary {
  id: string;
  kind: string;
  status: string;
  created: string;
  /** First non-empty line of the `## Question` section, for a one-line listing. */
  title: string;
}

interface ParsedDoc {
  /** Frontmatter keys in file order. */
  fm: Record<string, string>;
  /** Everything after the closing `---` fence (leading blank line included). */
  body: string;
}

function parseFrontmatter(content: string): ParsedDoc {
  const m = /^---\n([\s\S]*?)\n---\n?([\s\S]*)$/.exec(content);
  if (!m) return { fm: {}, body: content };
  const fm: Record<string, string> = {};
  for (const line of m[1].split('\n')) {
    const i = line.indexOf(':');
    if (i === -1) continue;
    const key = line.slice(0, i).trim();
    if (key) fm[key] = line.slice(i + 1).trim();
  }
  return { fm, body: m[2] };
}

function serializeDoc(fm: Record<string, string>, body: string): string {
  const front = Object.entries(fm)
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n');
  return `---\n${front}\n---\n${body}`;
}

/** First non-empty line under `## <heading>`, or '' if the section is absent/empty. */
function sectionFirstLine(body: string, heading: string): string {
  const lines = body.split('\n');
  const start = lines.findIndex((l) => l.trim() === `## ${heading}`);
  if (start === -1) return '';
  for (let i = start + 1; i < lines.length; i++) {
    if (lines[i].startsWith('## ')) break;
    if (lines[i].trim()) return lines[i].trim();
  }
  return '';
}

/** Replace the body under `## <heading>` with `content`, appending the section if absent. */
function replaceSection(body: string, heading: string, content: string): string {
  const lines = body.split('\n');
  const start = lines.findIndex((l) => l.trim() === `## ${heading}`);
  if (start === -1) {
    return `${body.replace(/\s*$/, '')}\n\n## ${heading}\n\n${content}\n`;
  }
  let end = lines.length;
  for (let i = start + 1; i < lines.length; i++) {
    if (lines[i].startsWith('## ')) {
      end = i;
      break;
    }
  }
  return [...lines.slice(0, start + 1), '', content, '', ...lines.slice(end)].join('\n');
}

/** Resolve a question id (with or without `.md`) to its confined path under REVIEW_DIR. */
function questionPath(id: string): string {
  let rel = id.trim();
  if (!rel.toLowerCase().endsWith('.md')) rel += '.md';
  return confine(REVIEW_DIR, rel);
}

/** Open/answered/applied questions in the review queue, newest first. Optionally filtered. */
export async function listQuestions(status?: string): Promise<QuestionSummary[]> {
  const files = await walkMarkdown(REVIEW_DIR);
  const out: QuestionSummary[] = [];
  for (const file of files) {
    let content: string;
    try {
      content = await fs.readFile(file, 'utf8');
    } catch {
      continue;
    }
    const { fm, body } = parseFrontmatter(content);
    const st = fm.status ?? 'open';
    if (status && st !== status) continue;
    out.push({
      id: fm.id || path.basename(file, '.md'),
      kind: fm.kind || 'judgment-call',
      status: st,
      created: fm.created || '',
      title: sectionFirstLine(body, 'Question') || '(no question text)',
    });
  }
  return out.sort((a, b) => b.created.localeCompare(a.created));
}

/** Raw markdown of one question by id. */
export async function getQuestion(id: string): Promise<string> {
  return cap(await fs.readFile(questionPath(id), 'utf8'));
}

/**
 * Record the human's answer to a question: write it into the `## Answer` section and flip
 * `status` to `answered` (the go-signal the host's resolve job acts on). Written atomically
 * (tmp + rename) so the resolve job never reads a half-written file. Returns the new status.
 */
export async function answerQuestion(id: string, answer: string): Promise<QuestionStatus> {
  const full = questionPath(id);
  const content = await fs.readFile(full, 'utf8');
  const { fm, body } = parseFrontmatter(content);
  fm.status = 'answered';
  fm.updated = new Date().toISOString();
  const next = serializeDoc(fm, replaceSection(body, 'Answer', answer.trim()));
  const tmp = `${full}.${process.pid}.tmp`;
  await fs.writeFile(tmp, next, 'utf8');
  await fs.rename(tmp, full);
  return 'answered';
}
