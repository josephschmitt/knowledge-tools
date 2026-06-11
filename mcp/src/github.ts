// GitHub-issues backend for the judgment-call review tools — the same list/get/answer
// operations as the file queue (vault.ts), but against the vault's GitHub issues via the REST
// API. Selected when the server is configured with MCP_GITHUB_TOKEN + MCP_GITHUB_REPO (see
// config.ts / review.ts). Answering posts a comment and adds the `vault:answered` label, which
// is exactly the manual GitHub action the host's /resolve job acts on — so the loop is identical
// whether I answer on github.com or from chat.
import { GITHUB_API_URL, GITHUB_REPO, GITHUB_TOKEN } from './config.js';
import { cap, type QuestionStatus, type QuestionSummary } from './vault.js';

// The labels the synthesize job files calls under, and the go-signal the human adds.
const KIND_LABELS = ['vault:judgment-call', 'vault:needs-verification'] as const;
const ANSWERED_LABEL = 'vault:answered';

/** A GitHub issue number, with or without a leading '#'. Throws on anything else. */
function issueNumber(id: string): number {
  const n = Number(String(id).replace(/^#/, '').trim());
  if (!Number.isInteger(n) || n <= 0) throw new Error(`not a GitHub issue number: ${id}`);
  return n;
}

interface GhLabel {
  name?: string;
}
interface GhIssue {
  number: number;
  title?: string;
  body?: string;
  state?: string;
  created_at?: string;
  html_url?: string;
  labels?: Array<string | GhLabel>;
  pull_request?: unknown;
}
interface GhComment {
  body?: string;
  created_at?: string;
  user?: { login?: string };
}

async function gh<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${GITHUB_API_URL}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${GITHUB_TOKEN}`,
      Accept: 'application/vnd.github+json',
      'X-GitHub-Api-Version': '2022-11-28',
      'User-Agent': 'knowledge-vault-mcp',
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...(init?.headers ?? {}),
    },
  });
  if (!res.ok) {
    const detail = await res.text().catch(() => '');
    throw new Error(`GitHub ${init?.method ?? 'GET'} ${path} -> ${res.status} ${res.statusText} ${detail.slice(0, 200)}`);
  }
  return (res.status === 204 ? null : await res.json()) as T;
}

function labelNames(issue: GhIssue): string[] {
  return (issue.labels ?? []).map((l) => (typeof l === 'string' ? l : (l.name ?? ''))).filter(Boolean);
}

/** Map an issue's state + labels to our status vocabulary. */
function issueStatus(issue: GhIssue, labels: string[]): QuestionStatus {
  if (issue.state === 'closed') return 'applied';
  return labels.includes(ANSWERED_LABEL) ? 'answered' : 'open';
}

/** Judgment calls in the repo's issues, newest first. `applied` lists closed ones; otherwise open. */
export async function listQuestions(status?: string): Promise<QuestionSummary[]> {
  const state = status === 'applied' ? 'closed' : 'open';
  // GitHub's label filter ANDs, but a call carries only ONE kind label — so fetch the issues in
  // this state and filter client-side for ours (and skip PRs, which this endpoint also returns).
  const issues = await gh<GhIssue[]>(`/repos/${GITHUB_REPO}/issues?state=${state}&per_page=100`);
  const out: QuestionSummary[] = [];
  for (const it of issues) {
    if (it.pull_request) continue;
    const labels = labelNames(it);
    const kind = KIND_LABELS.find((l) => labels.includes(l));
    if (!kind) continue;
    const st = issueStatus(it, labels);
    if (status && st !== status) continue;
    out.push({
      id: String(it.number),
      kind: kind === 'vault:needs-verification' ? 'needs-verification' : 'judgment-call',
      status: st,
      created: it.created_at ?? '',
      title: it.title ?? '(untitled)',
    });
  }
  return out.sort((a, b) => b.created.localeCompare(a.created));
}

/** Full context of one judgment call: the issue body and its comment thread, as markdown. */
export async function getQuestion(id: string): Promise<string> {
  const num = issueNumber(id);
  // The issue and its comments are independent reads — fetch them concurrently.
  const [issue, comments] = await Promise.all([
    gh<GhIssue>(`/repos/${GITHUB_REPO}/issues/${num}`),
    gh<GhComment[]>(`/repos/${GITHUB_REPO}/issues/${num}/comments?per_page=100`),
  ]);
  const labels = labelNames(issue).join(', ') || '(none)';
  let md = `# #${issue.number} ${issue.title ?? ''}\n\n`;
  md += `- state: ${issue.state} (${issueStatus(issue, labelNames(issue))})\n`;
  md += `- labels: ${labels}\n`;
  if (issue.html_url) md += `- url: ${issue.html_url}\n`;
  md += `\n## Question\n\n${issue.body?.trim() || '(no body)'}\n`;
  if (comments.length) {
    md += `\n## Discussion\n`;
    for (const c of comments) {
      md += `\n**${c.user?.login ?? 'unknown'}** (${c.created_at ?? ''}):\n\n${(c.body ?? '').trim()}\n`;
    }
  }
  return cap(md);
}

/**
 * Record a decision: comment the answer on the issue and add the `vault:answered` label. That's
 * the same thing answering on github.com does, so the host's /resolve applies it and closes the
 * issue. Returns the new status.
 */
export async function answerQuestion(id: string, answer: string): Promise<QuestionStatus> {
  const num = issueNumber(id);
  // Posting the comment and adding the label are independent writes — issue them concurrently.
  await Promise.all([
    gh(`/repos/${GITHUB_REPO}/issues/${num}/comments`, {
      method: 'POST',
      body: JSON.stringify({ body: answer.trim() }),
    }),
    gh(`/repos/${GITHUB_REPO}/issues/${num}/labels`, {
      method: 'POST',
      body: JSON.stringify({ labels: [ANSWERED_LABEL] }),
    }),
  ]);
  return 'answered';
}
