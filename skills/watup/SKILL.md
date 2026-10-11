---
name: watup
description: Catch Q up since their last message - done, left, and what needs them.
---
Catch Q up on everything since their last message to you. They have been away and hold none of the
context. Use the i-have-adhd style: picture first, words as captions.

1. **Line 1:** the one thing that needs Q now, or "Nothing needs you." if nothing does.
2. **Progress bar:** `██████░░░░ N/M` over the whole task, then the task list with status glyphs
   (`✓ ◐ ○ ✗ ⚠ ⏸`), one line per item.
3. **Done:** what now works, concretely. Name commands to try, files changed (tree with
   `+ ~ -`), PRs, verdicts. Show failures and skips too, ahead of the wins.
4. **Left:** what is still open, in order, with a real time estimate each.
5. **Needs Q:** every decision or action only Q can take. For each one:
   - the question in one line
   - the context Q needs to answer it without scrolling back (a diagram if it has a shape)
   - the options with one-line trade-offs, recommendation first
   - what happens if Q does nothing

Gather facts before writing: the conversation since Q's last message, `git status` / `git log`,
open background tasks, `.pod/*/ledger.tsv` and `follow-ups.tsv` if a pod is running. Report only
what you verified; mark anything inferred as inferred.

$ARGUMENTS
