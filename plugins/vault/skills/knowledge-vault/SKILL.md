---
name: knowledge-vault
description: Capture raw material — knowledge and tasks — into Joe's personal vault and answer questions from it, through the vault's MCP connector. Use whenever Joe wants to SAVE something — "save this", "remember this", "capture this", "file this away" — or stash an action — "remind me to…", "I need to…", "add a task", "todo" — or to RECALL — "what do I know about X", "what's on my plate", "what do I need to do" — or to handle the judgment calls the vault is waiting on — "what's my vault waiting on", "answer that vault question". Use it even when Joe doesn't name the vault but is clearly trying to stash a finding or action, pull up prior knowledge, or settle a question the vault raised. Capture writes raw material to the inbox and does NOT synthesize, categorize, or build tasks — the homelab compiler turns dumps into library notes and TaskNotes tasks, and Joe owns task lifecycle in Obsidian. Heavy compilation runs automatically on homelab and is out of scope here.
---

# Knowledge & Tasks Vault

This skill is the conversational front door to Joe's personal vault: a markdown knowledge
base — plus a TaskNotes task list — living on his home server (homelab), reachable from here
through its MCP connector. Two jobs live in this interface: capturing raw material (knowledge
or tasks) and answering questions from what's already been compiled.

## How the system is split

The work is divided on purpose, so don't duplicate it:

- **This interface (claude.ai): capture + query.** Drop raw material — a fact, a finding, or
  an action item — into the inbox, and answer Joe's questions from the compiled library.
- **homelab (Claude Code + `CLAUDE.md` + a scheduled job): compile + maintain.** The heavy
  synthesis — turning the inbox into durable, cross-linked notes *and* TaskNotes task files,
  and keeping both healthy — runs there, where there's full filesystem access and the vault's
  conventions live.

The reason capture stays dumb here is that synthesis is better done in one place, on a
schedule, with the whole vault in view. Pre-organizing a capture from this interface just
fights that.

**Tasks are create-only from here.** A captured action becomes a task only when the compiler
mints it; this interface never creates, edits, schedules, or completes a task file. Joe owns
task lifecycle — status, due date, priority, completion — in the TaskNotes UI. So never tell
him you've "made" or "scheduled" a task; you've captured raw material the compiler will file.

## Choosing a vault (only when there's more than one)

Usually there's **one** vault and you do nothing here — call its tools directly and never ask.
Joe can run **several** vaults (e.g. personal vs work), each a separate deployment wired as its
own connector, and the tools then arrive namespaced per vault (`knowledge-vault-<label>`, e.g.
`knowledge-vault-work`). Only then does a choice exist:

- **Picking the vault is the one allowed decision — and it's not categorization.** Capture still
  takes *zero* decisions about content (no dedup, no destination, no filing); choosing which
  vault to write to or read from is a separate, coarse routing choice that lives here, not in the
  compiler. So this is the *only* thing you may decide before a capture — nothing about the
  content itself.
- **One vault → never ask.** If only one `knowledge-vault…` connector is present, just use it.
- **Several vaults → route, or ask once.** If Joe named it ("save this to work", "what's in my
  personal vault about X"), use that vault's connector. If he didn't and the action is
  vault-specific, ask one terse question — "Which vault — personal or work?" — then proceed.
  Don't ask more than once; don't turn it into a categorization prompt.
- To map connectors to readable names, you can call each one's `vault_status` once and read its
  `vault_name` label.

## MCP operations

The connector's tools each arrive with their own name, description, and input schema, so
**you don't need to read anything before calling them** — just call the right one. The
choreography that matters:

- **Capture** → `append_to_inbox`, then confirm.
- **Query** → `search_notes` to find, `get_note` to read; `list_index` / `list_notes` to orient.
- **Judgment calls** → `list_questions` → `get_question` → `answer_question`.
- **Compile** → `compile_run` to trigger, `vault_status` to poll for it to finish.

Only open `references/mcp-operations.md` when you need an exact input/output shape you're
unsure of — not as a reflex before a routine capture or search.

## Capturing

When Joe wants to save something, append it to the inbox raw with `append_to_inbox` and stop
there. **Capture takes zero decisions — just dump.** The vault runs on "dumb capture, smart
compile": friction at capture time is how things go uncaptured, and an uncaptured thought is a
total loss while a redundant capture costs nothing. (The tool enforces the rules — no
dup-searching, no judging worth; your job is the *why* and the wording.)

- **Don't pre-organize.** Dedup, categorizing, and picking a destination are the compiler's
  job, done with the whole vault in view — a "duplicate" you dump just becomes corroboration
  or folds in, and the raw capture is preserved in `inbox/archive/` regardless. Pre-organizing
  here defeats the inbox.
- **Do make the dump *legible* — that's the one thing capture time is for.** Capture the
  *content*, not the conversation: when Joe says "save this," work out what "this" is — the
  conclusion, the snippet, the link — and capture that, not the transcript. Fold the source
  URL and a line of what it is into the `text`. Richness, not organization.
- Confirm briefly what went in; the tool returns the inbox path.
- **Optional area-of-life hint.** A capture may name its area-of-life lane *in the text* — a
  light `Area: home` line, or just saying so naturally — and the compiler honors it; otherwise
  it infers the lane from the substance, or omits it. The lanes the vault uses are `work`,
  `home`, `personal-projects`, and `interests` (the vault owns this vocabulary — mirror it, don't
  invent others). This is purely optional and there's **no schema field for it** — keep it in the
  `text`. **Omission is costless, so never prompt Joe for it and never nag a hint-less capture.**
  It only materializes as a stored tag when the capture compiles into a **library** note; on task
  or notebook captures it's informational and stored nowhere.

**Capturing tasks and action items.** When Joe asks you to remember an *action* — "remind me
to…", "add a task", "I need to…" — that's still a raw capture, not a decision for you. Word it
per the tool's rule (lead with `TODO:`, keep any deadline) and dump it; the compiler decides
it's a task and mints the TaskNotes file. Don't set a due date, status, or priority yourself,
and don't split a capture that's *both* knowledge and an action — dump it once and the
compiler can emit both a note and a task. Keep the confirmation honest: "Captured; it'll
become a task on the next compile" — never "I created a task" or "I scheduled that."

**Examples:**
- Joe: "Save this — we landed on the Waterfield Legion Go 2 case for the GPD Win 5." →
  `append_to_inbox` with the decision (and any link) as `text` → "Captured: the GPD Win 5 case
  decision (Waterfield Legion Go 2). It'll fold into the library on the next compile."
- Joe: "Remind me to order the Rivian charging cable by Friday." → `append_to_inbox` with
  `TODO: order Rivian charging cable — due Friday` → "Captured; it'll become a task on the
  next compile."

## Querying

When Joe asks what he knows, or to look something up, answer from the vault.

- Search the library first with `search_notes` (default `scope: "library"` — his settled,
  authoritative knowledge), then read the relevant notes with `get_note`. Use `list_index` to
  orient if it helps, and `list_notes` to see everything when a search comes up empty or you're
  not sure what exists.
- **The notebook is loose, in-progress thinking — a secondary source.** When a question might turn
  on something half-formed (an open question, a comparison he's still weighing, a research thread),
  widen the search with `scope: "both"` (or `"notebook"`). Always present anything from the
  notebook as *tentative* — say it's in-progress thinking, never state it as settled fact — and
  prefer the library when the two cover the same ground. Search results and `list_index` mark
  notebook material for you; carry that distinction into your answer. `get_note` reads either area
  via its area-qualified path (`notebook/<name>` / `library/<name>`).
- Answer from his own notes, not general web knowledge, when he's asking what *he* knows. Point
  to the notes you drew on by path.
- If the vault has nothing, say so plainly — don't invent a note or a citation. Offer to capture
  the current info, or to research it on the web if that's what he wants.
- If notes conflict or look stale, surface that instead of silently picking one. (Notebook entries
  are *meant* to be tentative and can contradict each other or the library — that's not staleness;
  frame it as unsettled thinking, not a conflict to resolve.)
- **Tasks aren't notes.** `search_notes` / `get_note` see only `library/` and `notebook/`, not `tasks/`, so
  they won't surface to-dos. When Joe asks "what's on my plate" or "what do I need to do," read
  the `## Tasks` block in `index.md` via `list_index` and relay its focus view, then point him
  to the `tasks/index` dashboard or TaskNotes in Obsidian for the live, filterable board. Don't
  fabricate a task list from nothing.

**Example:**
Joe: "What do I know about lake house options for the trip with Adam?"
You: `search_notes` → `get_note` on the matching paths → answer from them, naming the note(s);
if it's thin, say where the gap is.

## Triggering a compile

A scheduled job on homelab compiles the inbox into the library hourly, so a manual compile is
occasional, not routine — capturing alone never requires it. When Joe explicitly wants the
inbox processed sooner, call `compile_run` and relay what it returns: it reports its own
outcome (triggered, throttled, busy, or empty) and whether to wait or not, and his captures
stay safe in the inbox regardless. To confirm a run finished, poll `vault_status`. Never run
the synthesis yourself from this interface — it belongs on homelab where the full vault and
`CLAUDE.md` conventions live.

## Answering judgment calls

When the vault's maintenance pass hits something only Joe can decide — two notes that
contradict each other, or a time-sensitive claim it can't verify — it files a **judgment
call**. These surface here when the vault routes them through its file channel.

- When Joe asks what the vault needs ("what's my vault waiting on", "any open questions"), call
  `list_questions` with `status: "open"` and relay them briefly — each has an id, a kind, and a
  one-line summary.
- `get_question` returns one call's full context; `answer_question` records Joe's decision in
  his own words.
- **Don't apply the decision to the library yourself** — recording the answer is all this interface
  does; the next maintenance pass on homelab applies it and closes the call (or follows up here
  if the answer was ambiguous, which reappears as an open question).

The flow is the same whichever channel the vault uses — list, read, answer. If the vault keeps
its calls on GitHub and the connector isn't wired to them, `list_questions` just comes up empty
and Joe handles them there.

## Conventions

Stay aligned with the vault's `CLAUDE.md` so this interface and homelab agree. At this layer:
**capture raw, never pre-organize, and never touch task lifecycle** — the compiler writes notes
and mints task files under `CLAUDE.md` (knowledge not transcript, present tense, search-first
deduping, `[[wikilinks]]`, create-only tasks), and Joe owns task status/due/completion in
TaskNotes. You don't reproduce any of that at capture time.
