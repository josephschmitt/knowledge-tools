---
description: Periodic whole-vault pass — reconcile drift and contradictions, then find new cross-note connections. File-queue variant — opens judgment calls as files in inbox/.review/ instead of GitHub issues. Run infrequently; not part of the nightly compile.
argument-hint: "[optional topic/area to focus on]"
model: opus
effort: xhigh
---

A deliberate, infrequent maintenance-and-synthesis pass over the **whole** `wiki/`.
This is the opposite of the nightly compile: the compile processes fresh captures one
at a time with a local view; this pass reads the entire corpus at once to keep it true
and to discover structure that only emerges across many notes.

This is the **file-queue** variant: judgment calls are written as files in `inbox/.review/`
rather than GitHub issues, so the loop works without git or GitHub. I answer them from chat
(through the vault's MCP connector), and `/resolve-files` applies my answers. You need no `gh`
and no network — only file edits.

If an argument is given (`$ARGUMENTS`), focus the pass on that topic/cluster and notes
adjacent to it. With no argument, sweep the entire vault.

Read `index.md` and every note in `wiki/` before changing anything — both phases need a
whole-corpus view. Also read the already-open judgment calls in `inbox/.review/` (any
`*.md` with `status: open`) so you don't raise the same thing twice.

Then work in two phases, **in order**.

## Phase 1 — Reconcile (truth-maintenance, do this first)

Knowledge changes. Get the vault to a correct, consistent baseline *before* synthesizing,
so new connections build on current truth rather than propagating stale claims.

- **Broken / orphaned `[[links]]`** — fix links that point to renamed or missing notes;
  add the obvious missing backlink where two notes clearly reference each other.
- **Internal contradictions** — where note A asserts something note B contradicts: if one
  clearly supersedes the other (a later decision, a newer fact), update the stale note to
  match and preserve the *why* of the change. If it's genuinely ambiguous which is current,
  **do not guess** — leave both notes as-is and open a review-queue file for the decision
  (see Closing) so it has a durable home instead of vanishing into terminal output.
- **Stale / superseded entries** — when a newer note has overtaken an older one, update or
  retire the old claim rather than leaving two versions of the truth in the vault.
- **Near-duplicates** — fold a small redundant note into the fuller one and redirect links.
- Keep `index.md` in sync with anything you rename, merge, or retire.

## Phase 2 — Synthesize (find new structure)

Now that the corpus is consistent, look across it for knowledge that wasn't visible at
capture time.

- **Latent connections** — notes that should reference each other but don't yet. Add the
  `[[wikilinks]]` (both directions where it helps).
- **Emergent hubs** — when several notes cluster around an idea that has no home page,
  create a concept/hub note that ties them together and links out to each. Add it to
  `index.md`.
- **Patterns worth stating** — a synthesis that's true across multiple notes but written
  down nowhere. Capture it as durable knowledge in the most fitting note, or a new one.

Write everything as established fact in the vault's voice (present tense, dense, no
transcript). Same standard as the compile.

## Closing

**The review queue is where judgment calls live** — the durable surface for anything that
needs *me*, since there's no UI and terminal output disappears. File one question per distinct
call; before filing, check it against the open questions you read at the start so you don't
duplicate one.

Each question is a new markdown file at `inbox/.review/<date>-<short-slug>.md` (e.g.
`inbox/.review/2026-06-09-redis-vs-valkey.md`; if a slug collides on the same day, append
`-2`). Use this exact shape — `status` and `kind` drive the rest of the loop:

```markdown
---
id: <date>-<short-slug>
status: open
kind: judgment-call
created: <today's ISO date>
updated: <today's ISO date>
notes: [wiki/a.md, wiki/b.md]
---

## Question

<State both sides plainly, name the notes involved, and ask the single specific
decision I need to make.>

## Answer

## Discussion
```

- **Ambiguous contradictions** — the unresolved conflicts from Phase 1 you refused to guess
  on. `kind: judgment-call`. The `## Question` states both sides, names the notes, and asks the
  specific decision I need to make.
- **Needs external verification** — internal contradictions are fixable from the corpus alone,
  but the vault can't know the *outside world* changed. Claims that look time-sensitive, dated,
  or likely-aged (rates, prices, "current" anything, fast-moving facts) get `kind:
  needs-verification`. Do **not** edit these on a guess. Where it fits, note in the body that
  pointing `deep-research` at it would confirm it.

Leave `## Answer` empty and `## Discussion` empty — I fill the answer (the MCP `answer_question`
tool flips `status` to `answered`), and `/resolve-files` applies it.

This command only **opens** questions — it never applies or closes them; that's
`/resolve-files`'s job, and it acts only on questions with `status: answered`. If your Phase 1
reconciliation makes an already-open question moot, leave the file as-is — `/resolve-files` will
catch and close it. Just don't duplicate it.

Then:

- Append a one-line, ISO-dated entry to `log.md` (newest at the bottom) summarizing the run:
  what you reconciled, what you synthesized, how many notes touched, and how many questions you
  opened.
- **Do not** touch top-level `inbox/` captures or `inbox/archive/`, and **do not** run git —
  leave the commit to the tools-repo wrapper so the wiki changes get reviewed. (Writing
  `inbox/.review/` question files is your job and is separate from the commit.)

End by telling me, briefly: what you fixed, what new connections/hubs you added, and the
questions you opened (with their ids). The terminal summary is a convenience echo — the
**`inbox/.review/` queue is the system of record**.
