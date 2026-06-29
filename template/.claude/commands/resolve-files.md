---
description: Apply my answered judgment-call questions to the library and close them out — the inbound half of the file-queue loop; pairs with /synthesize-files. Acts only on questions with `status: answered` in inbox/.review/.
model: opus
effort: high
---

The consumer side of the judgment-call loop. `/synthesize-files` files questions in
`inbox/.review/` when it hits something only I can decide; this command reads my answers back
out and lands them in the library. It is targeted, not a whole-corpus pass — only touch the
answered questions and the notes they name.

This is the **file-queue** variant: questions are files in `inbox/.review/`, not GitHub issues.
You need no `gh` and no network — only file edits. It is **library-scoped by design**: judgment
calls arise from library contradictions, so answers apply to `library/` notes only. The notebook
is audit-exempt (it never generates these questions), so it is not in scope here.

## The status field is the go-signal

Act **only** on questions whose frontmatter has `status: answered`. That status is my explicit
"I've decided — apply it," set when I answer through the vault's MCP connector. A question with
`status: open` is one I'm still thinking through (or haven't seen): leave it completely alone,
even if there's text in `## Discussion`. This is what keeps you from acting on a half-formed
reply.

Every note **you** add to a question's `## Discussion` must begin with this marker line:

```
🤖 _via `/resolve-files`_
```

so that I — and any later run of you — can tell your notes apart from my answers. When reading
`## Answer` and `## Discussion` for my decision, **ignore any line that starts with that
marker**: it's your own earlier note, not my answer.

## Find the answered calls

Read every `inbox/.review/*.md` whose frontmatter has `status: answered`. For each, read the
whole file: the `## Question`, my `## Answer`, and any prior `## Discussion`.

## Decide what to do with each — three outcomes

1. **My answer is clear and actionable** (which note is current, what to retire, the verified
   fact). Apply it:
   - Edit, merge, or retire the `library/` notes per my answer, written as established fact in the
     vault's voice and preserving the *why* (and the note's OKF frontmatter — mint `type`/`tags`
     if a touched note still lacks it). Fix any links and update `index.md` if structure changed.
   - Add a marked note to `## Discussion` stating exactly what you changed (name the notes), then
     set `status: applied` and update the `updated:` date in the frontmatter. Leave the file in
     place as the durable record.
2. **The premise is already moot** — a later compile or synthesis resolved it, so there's nothing
   to apply. Add a marked note to `## Discussion` saying it's resolved by the current state of the
   library, set `status: applied`, and update `updated:`. No library edit.
3. **My answer is ambiguous, partial, or asks a follow-up.** Do **not** guess and do **not** edit
   the library. Instead:
   - Add a marked note to `## Discussion` asking the **single** specific question that would
     unblock you — sharper than the last round, never a vague "please clarify."
   - Reset the go-signal so the question waits on me again and your own follow-up can't
     re-trigger you: set `status: open` and update `updated:`.
   - Leave the library untouched. It re-enters the queue only when I answer again (which flips it
     back to `status: answered`).

## Closing

- Append a one-line, ISO-dated entry to `log.md` (newest at the bottom): which questions you
  applied and marked `applied` (with ids), which you bounced back to `open` with a follow-up, and
  what notes changed.
- **Do not** touch top-level `inbox/` captures or `inbox/archive/`, and **do not** run git —
  leave the commit to the tools-repo wrapper so the library changes get reviewed. Editing the
  `inbox/.review/` question files (status, discussion) is your job and is separate from the
  commit.

End by telling me, briefly: which questions you applied and what you changed for each, which you
bounced back with a follow-up, and which (if any) were already moot.
