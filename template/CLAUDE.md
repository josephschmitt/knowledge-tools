# Knowledge Vault

This repo is a personal knowledge base. Claude Code is the librarian: I dump raw
material, you turn it into durable, cross-linked knowledge I can reference later.
I rarely edit `wiki/` by hand.

## Structure

- `inbox/` — raw, unprocessed captures: pasted text, links, dictated thoughts,
  clippings. No organization here. Once a capture is compiled it moves to
  `inbox/archive/`, the permanent raw-source trail: immutable, never edited or
  deleted — the source any note can be traced back to.
- `wiki/` — the compiled knowledge base. Synthesized notes, cross-linked, written
  as standing reference. You own this directory.
- `outputs/` — generated briefings and reports produced when answering questions.
  Optional; fold the useful ones back into `wiki/`.
- `index.md` — the navigation map of `wiki/`: one line per note summarizing it,
  grouped by category. Keep it current.
- `log.md` — an append-only, chronological record of what has happened to the
  vault: compiles, queries that filed new knowledge, and health checks. One line
  per event, newest at the bottom, each prefixed with an ISO date so it stays
  grep-able. Append only — never rewrite or delete past entries.

## Core principle: dumb capture, smart compile

Capture never requires a decision about where something goes or how it relates.
That is your job at compile time, not mine at capture time. Treat `inbox/` as a
junk drawer and `wiki/` as the organized result.

## Compiling the inbox

When asked to compile (or process the inbox), for each item in `inbox/`:

1. Decide its shape:
- **Decision** — a choice that was made. Lead with the decision, then the
  reasoning and the alternatives considered, and why they lost.
- **Reference** — durable facts about a thing (a product, a place, a tool, a
  config). Organized for lookup.
- **Concept / hub** — an explanation of an idea, or a page that ties related
  notes together.
1. Search `wiki/` first for related notes. Prefer updating or linking an existing
   note over creating a near-duplicate.
1. Write the entry as knowledge, not transcript. Present tense, stated as
   established fact. Preserve the *why* and the tradeoffs; drop the back-and-forth.
   No “we discussed” or “then I decided.”
1. Add backlinks with `[[wikilinks]]` to related notes, and link back from them
   where it helps.
1. Update `index.md` if you added or substantially changed an entry.
1. Append a one-line, ISO-dated entry to `log.md` noting what you compiled.
1. Move the processed capture to `inbox/archive/`, the permanent raw trail — don't
   delete it. (In the nightly run the wrapper handles archiving and the commit; see
   the `/compile-inbox` command.)

Consolidate vs. split per item: fold a small fact into an existing note; give a
substantial topic its own page.

## Querying

When I ask a question, answer from `wiki/` and `index.md` — my own knowledge — not
the general internet, unless I ask you to research. Point to the notes you drew
from. If the answer reveals a gap, say so plainly. When a query produces something
worth keeping — a synthesis, a researched answer — file it back into `wiki/` as a
new or updated note and append a line to `log.md`.

## Maintenance (health check)

When asked to run a health check, audit `wiki/`:

- Broken or orphaned `[[links]]`
- Internal contradictions between notes
- Stale entries that newer captures supersede
- Topic areas that are thin or missing sources

Fix the mechanical issues (links, obvious dupes) and flag the judgment calls for me.
Append a one-line, ISO-dated summary of the health check to `log.md`.

## Voice

Write plainly and densely. Flowing sentences over staccato. Avoid AI-cliché filler,
marketing tone, and reflexive hedging. Use italics for genuine emphasis. These are
reference notes, not blog posts: clarity and retrievability beat style.
