// Filesystem helpers over the vault. Every path is resolved and confined to VAULT_ROOT,
// so externally-supplied note paths can't escape the vault (path traversal).
import { promises as fs } from 'node:fs';
import path from 'node:path';
import { VAULT_ROOT, VAULT_NAME, MAX_RESULT_CHARS } from './config.js';

const LIBRARY_DIR = path.join(VAULT_ROOT, 'library');
// notebook/ is a peer area to library/ (loose, tentative thinking; plain markdown, no
// frontmatter; its own generated notebook/index.md). The read surface is area-aware so a
// query can reach it, but every notebook hit stays clearly marked tentative. tasks/ is a
// third peer area but is Obsidian-managed TaskNotes — deliberately out of the query surface.
const NOTEBOOK_DIR = path.join(VAULT_ROOT, 'notebook');
const INBOX_DIR = path.join(VAULT_ROOT, 'inbox');
const INDEX_FILE = path.join(VAULT_ROOT, 'index.md');
const NOTEBOOK_INDEX_FILE = path.join(NOTEBOOK_DIR, 'index.md');

/** A queryable compiled area. `tasks/` is intentionally absent (Obsidian-managed). */
export type Area = 'library' | 'notebook';
/** Search breadth across the queryable areas. */
export type SearchScope = Area | 'both';

/** Absolute directory backing an area. */
function areaDir(area: Area): string {
  return area === 'notebook' ? NOTEBOOK_DIR : LIBRARY_DIR;
}

/**
 * Content notes in an area (walkMarkdown minus the area's own nav index). The library index sits
 * at the vault root, so it's already outside LIBRARY_DIR; the notebook index lives *inside*
 * notebook/, so drop it here to keep both areas symmetric — the index is reached via readIndex,
 * not surfaced as a searchable/listable note.
 */
async function areaNotes(area: Area): Promise<string[]> {
  const files = await walkMarkdown(areaDir(area));
  return area === 'notebook' ? files.filter((f) => f !== NOTEBOOK_INDEX_FILE) : files;
}

/**
 * Split an externally-supplied note path into its area + area-relative remainder. A leading
 * `library/` or `notebook/` segment names the area; anything else (an unprefixed path) defaults
 * to `library/`, so callers from before the notebook existed keep working unchanged.
 */
function splitArea(notePath: string): { area: Area; rel: string } {
  const trimmed = notePath.trim().replace(/^\/+/, '');
  const m = /^(library|notebook)\/(.*)$/.exec(trimmed);
  if (m) return { area: m[1] as Area, rel: m[2] };
  return { area: 'library', rel: trimmed };
}

// Compile coordination lives under inbox/.compile/ — the one mount the container can
// write. It's a subdir, so the host's capture snapshot (top-level files only) ignores it.
const COMPILE_DIR = path.join(INBOX_DIR, '.compile');
const COMPILE_REQUEST = path.join(COMPILE_DIR, 'request');
const COMPILE_STATUS = path.join(COMPILE_DIR, 'status.json');
// Per-job schedule snapshot the host writes from systemd (last/next run of compile, synthesize,
// resolve). See scripts/vault-lib.sh:refresh_schedules.
const COMPILE_SCHEDULES = path.join(COMPILE_DIR, 'schedules.json');

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
    // walk (search_notes) but vanish from another (list_notes). Resolve the type
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

/** Area-qualified paths of every note across both queryable areas, sorted (`library/<rel>`,
 *  `notebook/<rel>`). A missing area dir contributes nothing (walkMarkdown returns []). */
export async function listNotes(): Promise<string[]> {
  const out: string[] = [];
  for (const area of ['library', 'notebook'] as Area[]) {
    const dir = areaDir(area);
    const files = await areaNotes(area);
    out.push(...files.map((f) => `${area}/${path.relative(dir, f)}`));
  }
  return out.sort();
}

/** The navigation index: the library map plus the notebook's own index, each labeled. A missing
 *  file is noted in place so the output shape stays stable whether or not the notebook exists. */
export async function readIndex(): Promise<string> {
  const read = async (file: string, missing: string): Promise<string> => {
    try {
      return await fs.readFile(file, 'utf8');
    } catch {
      return missing;
    }
  };
  const library = await read(INDEX_FILE, '(index.md not found)');
  const notebook = await read(NOTEBOOK_INDEX_FILE, '(no notebook index)');
  return cap(`# Library index\n\n${library}\n\n# Notebook index (tentative)\n\n${notebook}`);
}

/** Raw markdown of one note. Accepts `name` or `name.md`, optionally area-qualified
 *  (`notebook/name` / `library/name`); an unprefixed path resolves under library/. */
export async function getNote(notePath: string): Promise<string> {
  const { area, rel: relRaw } = splitArea(notePath);
  let rel = relRaw;
  if (!rel.toLowerCase().endsWith('.md')) rel += '.md';
  const full = confine(areaDir(area), rel);
  return cap(await fs.readFile(full, 'utf8'));
}

export interface SearchHit {
  /** Which area the note lives in — `notebook` hits are tentative, never settled fact. */
  area: Area;
  /** Path relative to the area dir (combine with `area` for an area-qualified note path). */
  note: string;
  snippets: string[];
}

/**
 * Case-insensitive substring search with line-context snippets. `scope` selects which area(s) to
 * walk: `library` (default — authoritative), `notebook` (tentative thinking), or `both`. Hits
 * carry their `area` so callers can mark notebook results tentative. `maxHits` caps the total.
 */
export async function searchNotes(query: string, scope: SearchScope = 'library', maxHits = 20): Promise<SearchHit[]> {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  const areas: Area[] = scope === 'both' ? ['library', 'notebook'] : [scope];
  const hits: SearchHit[] = [];
  for (const area of areas) {
    const dir = areaDir(area);
    const files = await areaNotes(area);
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
      hits.push({ area, note: path.relative(dir, file), snippets });
      if (hits.length >= maxHits) return hits;
    }
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

/** One scheduled host job's timing. Either field is null when systemd has no value yet (the job
 *  hasn't run / has no next elapse) or the host isn't systemd-driven. */
export interface JobSchedule {
  /** ISO time the job last ran (systemd's last trigger), or null. */
  last_run_at: string | null;
  /** ISO time the job is next scheduled to run (systemd's next elapse), or null. */
  next_run_at: string | null;
}

/** Last/next run for each scheduled host job, as written by scripts/vault-lib.sh. */
export interface JobSchedules {
  compile: JobSchedule;
  synthesize: JobSchedule;
  resolve: JobSchedule;
}

/** Shape of the host-written schedules.json (jobs map plus bookkeeping fields). */
interface SchedulesFile {
  jobs?: Partial<Record<keyof JobSchedules, Partial<JobSchedule>>>;
}

const EMPTY_JOB_SCHEDULE: JobSchedule = { last_run_at: null, next_run_at: null };

/** Read the host-written schedule snapshot, or all-null rows if it doesn't exist / is unparseable. */
export async function readJobSchedules(): Promise<JobSchedules> {
  let parsed: SchedulesFile | null = null;
  try {
    parsed = JSON.parse(await fs.readFile(COMPILE_SCHEDULES, 'utf8')) as SchedulesFile;
  } catch {
    parsed = null;
  }
  const row = (job: keyof JobSchedules): JobSchedule => {
    const r = parsed?.jobs?.[job];
    return {
      last_run_at: nonEmpty(r?.last_run_at),
      next_run_at: nonEmpty(r?.next_run_at),
    };
  };
  return parsed
    ? { compile: row('compile'), synthesize: row('synthesize'), resolve: row('resolve') }
    : { compile: { ...EMPTY_JOB_SCHEDULE }, synthesize: { ...EMPTY_JOB_SCHEDULE }, resolve: { ...EMPTY_JOB_SCHEDULE } };
}

// Mirrors the host's KNOWLEDGE_COMPILE_COOLDOWN default; only used when status.json is
// absent (before the first compile writes it).
const DEFAULT_COOLDOWN_SECONDS = 3600;

/** Treat the host's empty-string timestamps (iso_of writes "" when an epoch file is missing) as absent. */
function nonEmpty(s: string | null | undefined): string | null {
  return s && s.length > 0 ? s : null;
}

export interface VaultStatus {
  /** This vault's KNOWLEDGE_VAULT_NAME label, or null when unlabeled — lets a client that
   *  reaches several vaults tell them apart. Cosmetic; it routes nothing. */
  vault_name: string | null;
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
  /** Last/next *scheduled* run of each host job (compile/synthesize/resolve), from systemd via
   *  the host. Each timestamp is null when unknown (job not yet run, or no systemd). A job's
   *  next_run_at is its scheduled cadence — distinct from manual_compile_available_at, which is
   *  the on-demand compile cooldown. */
  jobs: JobSchedules;
}

/**
 * Pollable vault status. Reads the host-written compile status plus the inbox count.
 * A `last_compiled_at` newer than a compile_run trigger time means that run finished.
 */
export async function getVaultStatus(): Promise<VaultStatus> {
  const status = await readCompileStatus();
  const pending = await countInboxCaptures();
  const jobs = await readJobSchedules();

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
    vault_name: VAULT_NAME || null,
    last_compiled_at: nonEmpty(status?.last_compiled_at),
    pending_inbox_count: pending,
    manual_compile_available_at: manualAvailableAt,
    running: status?.running ?? false,
    jobs,
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

/** Outcome of an on-demand compile request. `throttled` carries when the next one is allowed. */
export type CompileTrigger =
  | { status: 'triggered' }
  | { status: 'empty' }
  | { status: 'busy' }
  | { status: 'throttled'; available_at: string };

/**
 * Orchestrate an on-demand compile: refuse when the inbox is empty, a compile is already
 * running, or the hourly cooldown is still active; otherwise drop the request sentinel. Shared
 * by the MCP `compile_run` tool and the REST `POST /compile` route so the guard logic lives in
 * one place. The host script remains the authoritative guard; this mirrors it so the caller can
 * refuse early with a clear outcome.
 */
export async function triggerCompile(): Promise<CompileTrigger> {
  if ((await countInboxCaptures()) === 0) return { status: 'empty' };

  const status = await readCompileStatus();
  if (status?.running) return { status: 'busy' };

  const cooldownMs = (status?.cooldown_seconds ?? DEFAULT_COOLDOWN_SECONDS) * 1000;
  const last = nonEmpty(status?.last_manual_compile_at);
  if (last) {
    const t = Date.parse(last);
    if (!Number.isNaN(t) && Date.now() - t < cooldownMs) {
      return { status: 'throttled', available_at: new Date(t + cooldownMs).toISOString() };
    }
  }

  await requestCompile();
  return { status: 'triggered' };
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
