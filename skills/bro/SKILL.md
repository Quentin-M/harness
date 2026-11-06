---
name: bro
description: Re-explain your last reply, a PR description, a file or pasted text in plain words and the ADHD style.
argument-hint: "[<pr number|url> | <file> | <text>]"
disable-model-invocation: true
---

Rewrite the target the way one human explains it to another: no jargon, short sentences. Follow
the Communication rule in your instructions to the letter: the action or verdict on line 1, a
status bar for multi-step work, an ASCII picture, a caption of at most 4 sentences, any tangent as
one closing question.

## Target

|Argument|Rewrite|
|---|---|
|none|your last substantive reply in this conversation|
|PR number or URL|its description (`gh pr view <n> --json title,body`)|
|file path|that file|
|anything else|the text given|

## Content

The rewrite carries what the original left implicit. Find it in the conversation, the diff or the
code before writing; drop a part only when it is empty.

    context   the situation in one line: what exists, what broke or was asked
    why       the problem or motivation, with the number that proves it
    how       what changed or how it works, drawn
    next      what Q does now, or what is waiting on Q

Keep every fact, number, path and command from the original. Cut everything else.

## Where it goes

- Chat reply: answer in chat. Nerd Font icons are fine.
- File: edit it in place.
- PR description: show the rewrite, then `gh pr edit <n> --body-file` once Q says go.
- Files, PRs and commits use only the Unicode glyph kit (GitHub shows Nerd Font icons as tofu)
  and pass the leak check: no private IPs, internal hostnames, home paths or secrets.

## Check before sending

1. Write the draft to a temp `.md` file and run `harness diagrams` on it. Fix every issue and rerun
   until it prints nothing. Inside boxes, use plain names: no Nerd Font icons, since their width
   breaks the column count.
2. The first nonblank line is an action or verdict in plain text. A fence, bar or heading there
   fails.
3. One picture per idea. Two pictures that show the same fact fail: cut one.
4. Every number, path and command from the original appears in the draft.
