---
name: knowledge-vault
description: Capture raw material into Joe's personal knowledge vault and answer questions from it, through the vault's MCP connector. Use this whenever Joe wants to SAVE something for later — "save this to my knowledge base", "add this to my second brain", "capture this", "remember this", "file this away" — or wants to RECALL what he already knows — "what do I know about X", "do I have notes on Y", "check my vault", "look this up in my notes" — or wants to handle the judgment calls the vault is waiting on — "what's my vault waiting on", "any open questions in my vault", "answer that vault question". Use it even when Joe doesn't name the vault explicitly but is clearly trying to stash a finding, pull up prior knowledge, or settle a question the vault raised. Capture writes raw material to the inbox and does NOT synthesize or categorize it; querying reads the compiled wiki and answers from Joe's own notes. Heavy compilation and wiki maintenance run automatically on homelab and are out of scope here.
---

# Knowledge Vault

This skill is the conversational front door to Joe's personal knowledge base: the
`knowledge` repo, a markdown vault living on his home server (homelab), reachable
from here through its MCP connector. Two jobs live in this interface — capturing raw
material and answering questions from what's already been compiled.

## How the system is split

The work is divided on purpose, so don't duplicate it:

- **This interface (claude.ai): capture + query.** Drop raw material into the inbox,
  and answer Joe's questions from the compiled wiki.
- **homelab (Claude Code + `CLAUDE.md` + a scheduled job): compile + maintain.** The
  heavy synthesis — turning the inbox into durable, cross-linked notes, and keeping the
  wiki healthy — runs there, where there's full filesystem access and the vault's
  conventions live.

The reason capture stays dumb here is that synthesis is better done in one place, on a
schedule, with the whole vault in view. Pre-organizing a capture from this interface
just fights that.

## MCP operations

The connector exposes ten tools, and each one arrives with its own name, description,
and input schema already — so **you don't need to read anything before calling them.**
Just call the right tool for what Joe wants. At a high level:
`append_to_inbox` to capture, `search_wiki` to find notes, `get_note` to read one,
`list_index` to read the navigation map, `list_notes` to enumerate every note,
`compile_run` to trigger an on-demand compile (rate-limited; see below),
`vault_status` to poll whether a compile has finished and the wiki is caught up, and
`list_questions` / `get_question` / `answer_question` to review and settle the
judgment calls the vault has raised (see below).

The sections below already give you everything you need for the common paths —
capturing and querying in particular. Only open `references/mcp-operations.md` when you
genuinely need an exact input/output shape you're unsure of (e.g. the `vault_status`
JSON fields, or `compile_run`'s four outcomes). Don't read it as a reflex before a
routine capture or search — that just adds latency.

## Capturing

When Joe wants to save something, append it to the inbox raw with `append_to_inbox`
and stop there.

**Capture takes zero decisions. Just dump.** The vault runs on "dumb capture, smart
compile" by design: friction at capture time is how things go uncaptured, and an
uncaptured thought is a total loss while a redundant capture costs nothing.

- **Never search the wiki first to check for duplicates.** Dedup is the compiler's job —
  it searches `wiki/` for every inbox item and prefers updating or linking an existing
  note over making a near-duplicate. A "duplicate" you dump is not waste: it becomes
  corroboration, a sharper angle on an existing note, or it just folds in. The raw
  capture is preserved in `inbox/archive/` either way. Nothing is lost, nothing clutters.
- **Never judge whether it's "worth" saving, and never categorize, synthesize, pick a
  destination, or write a polished note.** The compiler on homelab does all of that,
  with the whole vault in view. Pre-organizing here defeats the inbox. When in doubt,
  capture.
- **Do make the dump *legible* — that's the one thing capture time is for.** Capture the
  *content*, not the conversation: when Joe says "save this," work out what "this" is —
  the conclusion, the snippet, the link — and capture that, not the whole transcript.
  Fold the source URL (if there is one) and a single line of what it is or why it
  matters into the `text` (there are no separate fields for those). Richness, not
  organization.
- Confirm briefly what went in. The tool returns the inbox path it wrote; relay that the
  capture will fold into the wiki on the next scheduled compile.

**Example:**
Joe: "Save this to my knowledge base — we landed on the Waterfield Legion Go 2 case for the GPD Win 5."
You: call `append_to_inbox` with the decision (and any link) as `text`, then →
"Captured to the inbox: the GPD Win 5 case decision (Waterfield Legion Go 2). It'll fold into the wiki on the next compile."

## Querying

When Joe asks what he knows, or to look something up, answer from the vault.

- Search the wiki first with `search_wiki`, then read the relevant notes with
  `get_note`. `search_wiki` returns matching note paths, each with short snippets around
  the match — use the path with `get_note` to pull the full note. Use `list_index` to
  orient if it helps, and `list_notes` to see everything when a search comes up empty or
  you're not sure what exists.
- Answer from his own notes, not general web knowledge, when he's asking what *he*
  knows. Point to the notes you drew on by path.
- If the wiki has nothing, say so plainly — don't invent a note or a citation. Offer to
  capture the current info, or to research it on the web if that's what he wants.
- If notes conflict or look stale, surface that instead of silently picking one.

**Example:**
Joe: "What do I know about lake house options for the trip with Adam?"
You: `search_wiki` → `get_note` on the matching paths → answer from them, naming the
note(s); if it's thin, say where the gap is.

## Triggering a compile

A scheduled job on homelab compiles the inbox into the wiki on a schedule (hourly by
default), so a manual compile is occasional, not routine — capturing alone never requires it. When Joe explicitly wants
the inbox processed sooner, call `compile_run` and act on what it returns. The compile
runs asynchronously on homelab: the tool only *kicks it off* and returns right away; the
wiki updates once the run finishes, and captures stay safe in the inbox until then. Never
run the synthesis yourself from this interface — it belongs on homelab where the full
vault and the `CLAUDE.md` conventions live.

- **Triggered:** the compile has started. Tell Joe it's running and the wiki will update
  shortly; his captures are safe meanwhile.
- **Throttled (refused):** a manual compile ran within the last hour and the cooldown is
  still active, so the call is refused. Relay when the next manual compile is available,
  and reassure him his captures are safe — the scheduled job will process them regardless.
  **Don't** retry.
- **Busy:** a compile is already running. Let him know; **don't** trigger another.
- **Empty:** the inbox has nothing to compile. Say so; nothing to do.

## Answering judgment calls

When the vault's weekly maintenance pass hits something only Joe can decide — two notes
that contradict each other, or a time-sensitive claim it can't verify on its own — it
files a **judgment call** for him. When the vault is configured for the file-based review
channel (no GitHub), those calls surface here, and Joe answers them in chat.

- When Joe asks what the vault needs from him ("what's my vault waiting on", "any open
  questions"), call `list_questions` with `status: "open"` and relay them briefly — each
  has an id, a kind (`judgment-call` or `needs-verification`), and a one-line summary.
- To dig into one, `get_question` with its id returns the full context: the contradiction
  or claim, the notes involved, and any prior back-and-forth.
- When Joe gives his decision, record it with `answer_question` (the question `id` plus his
  decision as `answer`). That marks it answered; the next maintenance pass on homelab
  applies it to the wiki and closes it out. If his answer is ambiguous, that pass comes
  back with a sharper follow-up, which shows up as an open question again.
- Don't apply the decision to the wiki yourself — recording the answer is all this
  interface does; homelab does the editing under `CLAUDE.md`.

This works whichever channel the vault uses: if it routes judgment calls through GitHub
issues, the connector can be wired to that repo, and `list_questions`/`answer_question`
operate on the issues instead (answering comments and labels the issue for the vault to
close). Either way the flow here is the same — list, read, answer. If a vault keeps its
calls on GitHub and the connector isn't wired to them, `list_questions` simply comes up
empty and Joe handles them on GitHub directly.

## Conventions

Stay aligned with the vault's `CLAUDE.md` so this interface and homelab agree. The one
that matters at this layer: capture raw, never pre-organize. When the compiler writes
notes it follows `CLAUDE.md` (knowledge not transcript, present tense, search-first
deduping, `[[wikilinks]]`); you don't need to reproduce any of that at capture time.
