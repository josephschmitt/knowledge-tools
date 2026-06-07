---
description: Apply my answered judgment-call issues to the wiki and close them — the inbound half of the issue loop; pairs with /synthesize. Acts only on issues I've labeled `vault:answered`.
model: opus
effort: high
allowed-tools: "Bash(gh issue list:*), Bash(gh issue view:*), Bash(gh issue comment:*), Bash(gh issue edit:*), Bash(gh issue close:*)"
---

The consumer side of the judgment-call loop. `/synthesize` files GitHub issues when it hits
something only I can decide; this command reads my answers back out and lands them in the
wiki. It is targeted, not a whole-corpus pass — only touch the issues and the notes they name.

## The label is the go-signal

Act **only** on issues I've labeled `vault:answered`. That label is my explicit "I've decided —
apply it," *not* the mere presence of a comment. An issue without the label is one I'm still
thinking through (or haven't seen): leave it completely alone, even if there's discussion on it.
This is what keeps you from acting on a half-formed reply.

Because `gh` may be authed as me, every comment **you** post must begin with this marker line:

```
🤖 _via `/resolve`_
```

so that I — and any later run of you — can tell your notes apart from my answers. When reading a
thread for my decision, **ignore any comment that starts with that marker**: it's your own earlier
note, not my answer.

## Find the answered calls

List the open issues I've marked answered:

```
gh issue list --state open --label "vault:answered"
```

For each, read the full thread including my replies:

```
gh issue view <number> --comments
```

## Decide what to do with each — three outcomes

1. **My answer is clear and actionable** (which note is current, what to retire, the verified
   fact). Apply it:
   - Edit, merge, or retire the `wiki/` notes per my answer, written as established fact in the
     vault's voice and preserving the *why*. Fix any links and update `index.md` if structure
     changed.
   - Post a marked `gh issue comment` stating exactly what you changed (name the notes), then
     `gh issue close <number>`.
2. **The premise is already moot** — a later compile or synthesis resolved it, so there's nothing
   to apply. Post a marked comment noting it's resolved by the current state of the wiki, then
   close. No edit.
3. **My answer is ambiguous, partial, or asks a follow-up.** Do **not** guess and do **not** edit.
   Instead:
   - Post a marked `gh issue comment` asking the **single** specific question that would unblock
     you — sharper than the last round, never a vague "please clarify."
   - Clear the go-signal so the issue waits on me and your own question can't re-trigger you:

     ```
     gh issue edit <number> --remove-label "vault:answered"
     ```
   - Leave the issue open and the wiki untouched. It re-enters the queue only when I reply and
     re-add `vault:answered`.

## Closing

- Append a one-line, ISO-dated entry to `log.md` (newest at the bottom): which issues you applied
  and closed (with numbers), which you bounced back with a follow-up, and what notes changed.
- **Do not** touch `inbox/` or `inbox/archive/`, and **do not** run git — leave the commit to me
  (or the tools-repo wrapper) so the wiki changes get reviewed. Commenting, removing labels, and
  closing issues are side effects you do directly; they're separate from the commit.

End by telling me, briefly: which issues you closed and what you applied for each, which you
bounced back with a follow-up question, and which (if any) were already moot.
