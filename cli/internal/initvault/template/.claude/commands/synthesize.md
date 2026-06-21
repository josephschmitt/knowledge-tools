---
description: Periodic whole-vault pass — reconcile drift and contradictions, then find new cross-note connections. Run infrequently; not part of the nightly compile.
argument-hint: "[optional topic/area to focus on]"
model: opus
effort: xhigh
allowed-tools: "Bash(gh issue list:*), Bash(gh issue view:*), Bash(gh issue create:*), Bash(gh search issues:*)"
---

A deliberate, infrequent maintenance-and-synthesis pass over the **whole** `wiki/`.
This is the opposite of the nightly compile: the compile processes fresh captures one
at a time with a local view; this pass reads the entire corpus at once to keep it true
and to discover structure that only emerges across many notes.

If an argument is given (`$ARGUMENTS`), focus the pass on that topic/cluster and notes
adjacent to it. With no argument, sweep the entire vault.

Read `index.md` and every note in `wiki/` before changing anything — both phases need a
whole-corpus view. Also list the already-open judgment calls so you don't raise the same
thing twice (two calls — `--label` flags AND together, so query each separately):

```
gh issue list --state open --label "vault:judgment-call"
gh issue list --state open --label "vault:needs-verification"
```

Then work in two phases, **in order**.

## Phase 1 — Reconcile (truth-maintenance, do this first)

Knowledge changes. Get the vault to a correct, consistent baseline *before* synthesizing,
so new connections build on current truth rather than propagating stale claims.

- **Broken / orphaned `[[links]]`** — fix links that point to renamed or missing notes;
  add the obvious missing backlink where two notes clearly reference each other.
- **Internal contradictions** — where note A asserts something note B contradicts: if one
  clearly supersedes the other (a later decision, a newer fact), update the stale note to
  match and preserve the *why* of the change. If it's genuinely ambiguous which is current,
  **do not guess** — leave both notes as-is and open a GitHub issue for the decision (see
  Closing) so it notifies me and has a durable home instead of vanishing into terminal output.
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

**GitHub issues are where judgment calls live** — the durable surface for anything that
needs *me*, since there's no UI and terminal output disappears. Filing an issue also
notifies me, which a repo file can't. File one issue per distinct call; before filing,
check it against the open issues you listed at the start so you don't duplicate one.

- **Ambiguous contradictions** — the unresolved conflicts from Phase 1 you refused to guess
  on. One issue each, labeled `vault:judgment-call`. Title names the conflict; body states
  both sides, names the notes (`wiki/<file>.md`), and asks the specific question I need to
  answer.
- **Needs external verification** — internal contradictions are fixable from the corpus
  alone, but the vault can't know the *outside world* changed. Claims that look
  time-sensitive, dated, or likely-aged (rates, prices, "current" anything, fast-moving
  facts) get an issue labeled `vault:needs-verification`. Do **not** edit these on a guess.
  Where it fits, note in the body that pointing `deep-research` at it would confirm it.

File with, e.g.:

```
gh issue create --title "<short question>" --label "vault:judgment-call" \
  --assignee "@me" --body "<both sides, notes involved, the decision needed>"
```

Always pass `--assignee "@me"` so the call lands in my assigned queue. `@me` is gh's
built-in alias for the authenticated account (the host's `~/.config/gh`), so it needs no
configured username and stays correct for whoever owns the vault.

End every issue body with a one-line reminder of how I act on it, so the loop explains itself in
GitHub:

> Reply with your decision, then add the `vault:answered` label to apply it. If anything's
> unclear, `/resolve` asks a follow-up here and clears the label.

This command only **opens** issues — it never closes them; applying my answers and closing is
`/resolve`'s job, and `/resolve` acts only on issues I've labeled `vault:answered`. If your
Phase 1 reconciliation makes an already-open issue moot, leave it open (you can't close it here) —
I'll close it, or mark it `vault:answered` for `/resolve` to close. Just don't duplicate it.

Then:

- Append a one-line, ISO-dated entry to `log.md` (newest at the bottom) summarizing the run:
  what you reconciled, what you synthesized, how many notes touched, and how many issues you
  opened.
- **Do not** touch `inbox/` or `inbox/archive/`, and **do not** run git — leave the commit
  to me so I can review the wiki changes first. (Issues are separate from the commit; file
  them directly.)

End by telling me, briefly: what you fixed, what new connections/hubs you added, and the
issues you opened (with their numbers). The terminal summary is a convenience echo — the
**issue list is the system of record**.
