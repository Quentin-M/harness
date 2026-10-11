---
name: leak-check
description: Use before any commit, push, PR, comment or proof upload. Prints the current branch's full diff against main/master, commit messages included, and you review it for secrets and local-only detail that must not leave the machine.
---

# Leak check

Print what this branch publishes, then read all of it:

```bash
base=$(git merge-base origin/main HEAD 2>/dev/null || git merge-base origin/master HEAD)
git log --format='--- %h %ae%n%B' $base..HEAD   # commit messages
git diff $base                                  # committed + staged + unstaged
git ls-files -o --exclude-standard              # new untracked files: read each one
```

Report each hit as `file:line - what - why it leaks`. No hits: say `clean` and name the branch.

## Hunt for

| Class | Examples |
|---|---|
| Secrets | API keys, tokens, passwords, private keys, `.env` content, `op://` refs with real vault paths, JWTs, connection strings with credentials |
| Local-only network detail | private and CGNAT IPs, internal hostnames and domains, tailnet names, ports of personal services |
| Personal detail | home paths (`/Users/<name>`, `/home/<name>`), usernames, emails, machine names, phone numbers |
| Employer or customer detail | internal repo, ticket, channel or customer names, data samples |
| Wrong place | machine-specific values in tracked files; they belong in a layer outside the repo (`HARNESS_LAYERS`) |
| Commit messages and metadata | the same classes, plus author email |

## Judge, do not pattern-match

- Placeholders (`example.com`, `10.0.0.1` in docs, `sk-xxxx`) are fine; a real-looking value is not.
- A value is a leak when it is real and private, even in a test fixture or a comment.
- Deleted lines (`-`) already exist in history; flag them only when the commit message or a surviving line repeats them.
- Uncertain: flag it and say what would clear it.

## When something leaks

A secret that reached a commit is compromised: say so, tell Q to rotate it, and do not push. Rewriting
history is Q's call.
