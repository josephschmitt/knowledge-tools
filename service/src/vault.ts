// Filesystem helpers over the vault. Every path is resolved and confined to VAULT_ROOT,
// so externally-supplied note paths can't escape the vault (path traversal).
import { promises as fs } from 'node:fs';
import { randomUUID } from 'node:crypto';
import path from 'node:path';
import { VAULT_ROOT, VAULT_NAME, MAX_RESULT_CHARS, REVIEW_CHANNEL } from './config.js';

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
// The two judgment-call maintenance jobs get their own on-demand request sentinels alongside
// compile's (the daemon watches all three). Compile keeps its original 'request' filename so the
// service contract is unchanged; synthesize/resolve mirror it as 'request-<job>'.
const SYNTHESIZE_REQUEST = path.join(COMPILE_DIR, 'request-synthesize');
const RESOLVE_REQUEST = path.join(COMPILE_DIR, 'request-resolve');
const COMPILE_STATUS = path.join(COMPILE_DIR, 'status.json');
// The two judgment-call jobs write their own compile-style live status alongside compile's, named
// status-<job>.json to mirror their request-<job> sentinels. compile keeps the original
// 'status.json' name so its contract is unchanged.
const SYNTHESIZE_STATUS = path.join(COMPILE_DIR, 'status-synthesize.json');
const RESOLVE_STATUS = path.join(COMPILE_DIR, 'status-resolve.json');
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

/**
 * Write `data` to `path` atomically (tmp file in the same dir + rename) so a concurrent reader — the
 * host's resolve/compile jobs, or the daemon's fsnotify watcher — never observes a half-written
 * file. Used for the review-queue answer files and the on-demand request sentinels.
 */
async function atomicWrite(path: string, data: string): Promise<void> {
  // Unique per call (not process.pid, which is shared by every concurrent async write) so two
  // in-flight writers to the same path can't clobber each other's tmp file and race the rename.
  const tmp = `${path}.${randomUUID()}.tmp`;
  await fs.writeFile(tmp, data, 'utf8');
  await fs.rename(tmp, path);
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
 * carry their `area` so callers can mark notebook results tentative. `maxHits` caps the hits
 * *per area* — so with `both` a flood of library matches can never starve the notebook (each area
 * gets its own budget; the total is at most `maxHits × areas`).
 */
export async function searchNotes(query: string, scope: SearchScope = 'library', maxHits = 20): Promise<SearchHit[]> {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  const areas: Area[] = scope === 'both' ? ['library', 'notebook'] : [scope];
  const hits: SearchHit[] = [];
  for (const area of areas) {
    const dir = areaDir(area);
    const files = await areaNotes(area);
    let areaHits = 0;
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
      if (++areaHits >= maxHits) break; // per-area cap — move to the next area, don't abort the search
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

/** One scheduled host job's timing plus its live run status. The timing fields are null when the
 *  job hasn't run / has no next elapse; the status fields come from the host's per-job status file
 *  (status.json for compile, status-<job>.json for synthesize/resolve) and are false/null when
 *  that file is absent (e.g. an un-upgraded host). */
export interface JobSchedule {
  /** ISO time the job last ran, or null. */
  last_run_at: string | null;
  /** ISO time the job is next scheduled to run, or null. */
  next_run_at: string | null;
  /** Whether this job is running right now — flips false when it finishes. */
  running: boolean;
  /** ISO time the in-flight (or most recent) run started, or null. */
  started_at: string | null;
  /** One-line human summary of the current/last run (e.g. "synthesizing", "resolved"), or null. */
  summary: string | null;
}

/** Last/next run for each scheduled host job, as written by scripts/vault-lib.sh. */
export interface JobSchedules {
  compile: JobSchedule;
  synthesize: JobSchedule;
  resolve: JobSchedule;
}

/** Shape of the host-written schedules.json (jobs map plus bookkeeping fields). The schedules file
 *  only carries the timing fields; the live status fields come from the per-job status files. */
interface SchedulesFile {
  jobs?: Partial<Record<keyof JobSchedules, { last_run_at?: string | null; next_run_at?: string | null }>>;
}

/** The subset of a host status file the schedule rows surface. compile's status.json is a superset
 *  (CompileStatus); synthesize/resolve write exactly these fields. */
interface JobStatusFile {
  running?: boolean;
  started_at?: string;
  summary?: string;
}

/** Where each job's live status file lives (compile shares its existing status.json). */
const JOB_STATUS_FILE: Record<keyof JobSchedules, string> = {
  compile: COMPILE_STATUS,
  synthesize: SYNTHESIZE_STATUS,
  resolve: RESOLVE_STATUS,
};

/** Read one job's live status file, or null if it doesn't exist / is unparseable. */
async function readJobStatus(job: keyof JobSchedules): Promise<JobStatusFile | null> {
  try {
    return JSON.parse(await fs.readFile(JOB_STATUS_FILE[job], 'utf8')) as JobStatusFile;
  } catch {
    return null;
  }
}

/**
 * Read the host-written schedule snapshot merged with each job's live run status. Timing comes from
 * schedules.json (all-null rows if it's missing/unparseable), the running/started_at/summary fields
 * from each per-job status file (false/null when that file is absent — back-compat for a host that
 * predates the per-job status surface).
 */
export async function readJobSchedules(): Promise<JobSchedules> {
  let parsed: SchedulesFile | null = null;
  try {
    parsed = JSON.parse(await fs.readFile(COMPILE_SCHEDULES, 'utf8')) as SchedulesFile;
  } catch {
    parsed = null;
  }
  const [compileStatus, synthesizeStatus, resolveStatus] = await Promise.all([
    readJobStatus('compile'),
    readJobStatus('synthesize'),
    readJobStatus('resolve'),
  ]);
  const statusByJob: Record<keyof JobSchedules, JobStatusFile | null> = {
    compile: compileStatus,
    synthesize: synthesizeStatus,
    resolve: resolveStatus,
  };
  const row = (job: keyof JobSchedules): JobSchedule => {
    const r = parsed?.jobs?.[job];
    const s = statusByJob[job];
    return {
      last_run_at: nonEmpty(r?.last_run_at),
      next_run_at: nonEmpty(r?.next_run_at),
      running: s?.running ?? false,
      started_at: nonEmpty(s?.started_at),
      summary: nonEmpty(s?.summary),
    };
  };
  return { compile: row('compile'), synthesize: row('synthesize'), resolve: row('resolve') };
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
  /** Whether a compile is in progress right now. Equals jobs.compile.running; kept as a top-level
   *  field for back-compat. */
  running: boolean;
  /** Per host job (compile/synthesize/resolve): last/next *scheduled* run plus the live run status
   *  (running/started_at/summary). Timestamps are null when unknown (job not yet run). A job's
   *  next_run_at is its scheduled cadence — distinct from manual_compile_available_at, the
   *  on-demand compile cooldown. Use jobs.<job>.running to tell an in-flight run from a finished
   *  one after triggering synthesize_run / resolve_run. */
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
 * Optional per-invocation overrides carried into a compile/synthesize/resolve trigger. Empty/omitted
 * fields fall back to the host's config/env chain (KNOWLEDGE_*_MODEL/_EFFORT, then harness default).
 * Values are pass-through / unvalidated (harness-specific), matching the env knobs.
 */
export type JobOverrides = { model?: string; effort?: string };

/**
 * Write a request sentinel atomically (tmp + rename) so the daemon's fsnotify watcher never reads a
 * half-written body. The body is a small JSON payload: `requested_at` for observability plus any
 * per-request model/effort override (omitted when unset). An older daemon ignores the body entirely;
 * a newer daemon parses it and falls back to config/env when a field is absent.
 */
async function writeRequestFile(path: string, ov?: JobOverrides): Promise<void> {
  await fs.mkdir(COMPILE_DIR, { recursive: true });
  const payload: { requested_at: string; model?: string; effort?: string } = {
    requested_at: new Date().toISOString(),
  };
  if (ov?.model) payload.model = ov.model;
  if (ov?.effort) payload.effort = ov.effort;
  await atomicWrite(path, `${JSON.stringify(payload)}\n`);
}

/**
 * Request a manual compile by dropping the sentinel the host's daemon watches. Writes
 * inbox/.compile/request with the trigger time and any per-request model/effort override. The host
 * consumes it when it runs.
 */
export async function requestCompile(ov?: JobOverrides): Promise<void> {
  await writeRequestFile(COMPILE_REQUEST, ov);
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
export async function triggerCompile(ov?: JobOverrides): Promise<CompileTrigger> {
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

  await requestCompile(ov);
  return { status: 'triggered' };
}

/** The two judgment-call maintenance jobs the service can trigger on-demand (compile is separate). */
export type MaintenanceJob = 'synthesize' | 'resolve';

const REQUEST_FILE: Record<MaintenanceJob, string> = {
  synthesize: SYNTHESIZE_REQUEST,
  resolve: RESOLVE_REQUEST,
};

/** Outcome of an on-demand maintenance-job trigger. Async: the host runs it and updates the vault. */
export type JobTrigger = { status: 'triggered' };

/**
 * Trigger an on-demand synthesize or resolve by dropping the sentinel the daemon watches
 * (inbox/.compile/request-<job>), mirroring requestCompile. Unlike compile there's no inbox/cooldown
 * guard to report: the daemon serializes every vault job on the shared per-instance lock (a request
 * that lands while another job runs just waits), and resolve short-circuits host-side when nothing is
 * answered — so this always returns `triggered`. Poll vault_status (its `jobs` map) to see the run
 * land. Shared by the MCP synthesize_run/resolve_run tools and the REST POST /synthesize,/resolve
 * routes so the request path lives in one place.
 */
export async function triggerJob(job: MaintenanceJob, ov?: JobOverrides): Promise<JobTrigger> {
  await writeRequestFile(REQUEST_FILE[job], ov);
  return { status: 'triggered' };
}

// --- Agent-driven job instructions ------------------------------------------------------
//
// In agent-driven mode (KNOWLEDGE_AGENT_DRIVEN=true, the stdio/`npx` runtime) there is no host
// daemon to consume a request sentinel, so the trigger tools instead hand the calling agent the
// vault's own procedure to run itself. `skillInstruction` reads the relevant SKILL.md straight out
// of the vault and returns its body — mirroring the Go CLI's skillPrompt/stripFrontmatter
// (cli/internal/jobs/job.go) so the same procedure drives both the host daemon and a local agent.

/** The three procedures a trigger tool can hand off (compile is separate from the two maintenance jobs). */
export type TriggerJobKind = 'compile' | MaintenanceJob;

/**
 * The vault skill name backing each trigger. synthesize/resolve have a git/GitHub variant and a
 * files-only variant; pick by the same REVIEW_CHANNEL that the tools' answer-queue uses, so the
 * agent runs the procedure matching where judgment calls live. compile has a single skill.
 */
function skillName(kind: TriggerJobKind): string {
  if (kind === 'compile') return 'compile-inbox';
  return REVIEW_CHANNEL === 'github' ? kind : `${kind}-files`;
}

/**
 * Strip a leading `---`…`---` YAML frontmatter block, returning the body. A file that doesn't open
 * with a `---` fence, or whose fence is never closed, is returned unchanged. Mirrors the Go
 * stripFrontmatter (cli/internal/jobs/job.go). Kept separate from the review-doc parseFrontmatter
 * because this one is CRLF- and trailing-whitespace-tolerant (a SKILL.md may reach us from a Windows
 * Cowork/Claude Desktop client), whereas parseFrontmatter matches an LF-only fence.
 */
function stripFrontmatter(s: string): string {
  const m = /^---\r?\n/.exec(s);
  if (!m) return s;
  const rest = s.slice(m[0].length);
  const close = /\r?\n---[ \t]*(\r?\n|$)/.exec(rest);
  if (!close) return s;
  return rest.slice(close.index + close[0].length);
}

/**
 * A short lead + trailing housekeeping wrapped around a skill body, telling the agent to run the
 * procedure NOW against this vault (there is no scheduled job/daemon in this deployment). The skills
 * are authored for the daemon/wrapper world, which archives the inbox and commits; with no wrapper
 * here, compile additionally gets an explicit archive step (without it the next compile reprocesses
 * the same inbox files — countInboxCaptures counts top-level files and skips inbox/archive/, so
 * archiving is what drops pending_inbox_count back to 0).
 */
function wrapInstruction(kind: TriggerJobKind, body: string): string {
  const lead =
    `You are running this vault's **${skillName(kind)}** procedure directly — there is no ` +
    `scheduled job or host daemon in this deployment, so carry it out now, yourself, against the ` +
    `vault at \`${VAULT_ROOT}\`. Follow the steps below exactly:\n\n---\n\n`;
  const tail =
    kind === 'compile'
      ? `\n\n---\n\n**After the steps above.** The procedure was written for a scheduled wrapper ` +
        `that archives the processed inbox and commits — this deployment has none, so you do that ` +
        `part: move every top-level file you processed out of \`inbox/\` into \`inbox/archive/\` so ` +
        `a later compile doesn't reprocess it. If the vault is a git repository, commit the ` +
        `library/notebook/index/log changes and the archived inbox files.`
      : `\n\n---\n\n**After the steps above**: if the vault is a git repository, commit the resulting ` +
        `changes. Do not modify \`inbox/\` for this pass.`;
  return lead + body + tail;
}

/**
 * Read the vault's own procedure for `kind` and return it as an instruction string for the calling
 * agent to execute. Resolution mirrors the Go CLI's two candidates so a vault seeded before the
 * skills migration still works: `.agents/skills/<name>/SKILL.md`, then legacy
 * `.claude/commands/<name>.md`. Throws (naming both paths) when neither exists.
 */
export async function skillInstruction(kind: TriggerJobKind): Promise<string> {
  const name = skillName(kind);
  const candidates = [
    path.join(VAULT_ROOT, '.agents', 'skills', name, 'SKILL.md'),
    path.join(VAULT_ROOT, '.claude', 'commands', `${name}.md`),
  ];
  for (const file of candidates) {
    let raw: string;
    try {
      raw = await fs.readFile(file, 'utf8');
    } catch {
      continue;
    }
    const body = stripFrontmatter(raw).replaceAll('$ARGUMENTS', '').trim();
    return cap(wrapInstruction(kind, body));
  }
  throw new Error(
    `No procedure for "${name}": looked for ${candidates[0]} and ${candidates[1]}. ` +
      `Is VAULT_ROOT pointed at a vault? Seed one with \`knowledge-tools init\`.`,
  );
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
  await atomicWrite(full, next);
  return 'answered';
}
