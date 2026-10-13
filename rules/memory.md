# Memory

One memory directory per project, shared by every harness: Claude Code's auto memory. All
worktrees and subdirectories of a repository share it.

- **Claude** manages it natively. Follow its built-in memory instructions.
- **Codex and omp** get the directory with `harness memory path`. Read its `MEMORY.md` at session
  start (codex gets it injected), then open a topic file when its index line is relevant.

## Format

Same as Claude's, so every harness reads what the others wrote. One fact per file, named
`<slug>.md`:

    ---
    name: <short-kebab-case-slug>
    description: <one-line summary, used to decide relevance during recall>
    metadata:
      type: user | feedback | project | reference
    ---

    <the fact; for feedback and project, follow with **Why:** and **How to apply:** lines.
    Link related memories with [[their-name]].>

Then add one line to `MEMORY.md`: `- [Title](slug.md) - hook`. The index has no frontmatter and
no memory content.

| type | holds |
|---|---|
| `user` | who the user is: role, expertise, preferences |
| `feedback` | how the user wants you to work: corrections and confirmed approaches, with the why |
| `project` | decisions, goals, constraints the code and git history do not show; absolute dates |
| `reference` | where to find things outside the repo: URLs, dashboards, tickets |

## What to save

- Decisions, user preferences and corrections from the user.
- Gotchas and significant discoveries you made about the project that cost real effort.
- Never what the code, git log, `AGENTS.md` or `CLAUDE.md` already says, or what only matters
  to this session.

## Upkeep

- Update the existing file for a fact before writing a new one. No duplicates.
- Delete a memory that turns out wrong or stale, and its index line.
- Keep `MEMORY.md` under 200 lines: only the first 200 lines or 25KB load.
- A recalled memory reflects when it was written. Verify a named file, function or flag still
  exists before relying on it.
