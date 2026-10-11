# gg rig culture

These rules bind every seat. Your role file adds the rest.

## Queue protocol

- A queue row obligates; a `rig send` only informs. Assignments, handoffs and escalations are
  rows. Context and nudges are sends.
- End every turn on substantive work by passing the ball: `rig queue handoff`, or close the row
  with a `closure_reason` (`handed_off_to`, `blocked_on`, `escalation`, `no-follow-on`, `denied`,
  `canceled`). Never go idle holding an open row.
- After a handoff, pull your next row. Idle only on an empty queue.
- Bodies longer than a line go through `--body-file`. Link artifacts by path; never paste dumps.
- Run `rig whoami --json` after startup and after every compaction.

## Escalate, don't stall

Blocked on a seat: `rig queue block <qitem> --on <blocker>`. Blocked on a decision: close the row
`escalation` to `lead-lead@gg` with the exact question. Only `lead-lead@gg` talks to Q.

## Jobs and worktrees

Paths below are relative to the gg checkout, the cwd you launched in. From inside a worktree, the
checkout is `$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")`.

Each job has its own branch and worktree at `.worktrees/<job>` and its record at
`.worktrees/.jobs/<job>/` (briefs, ledger, decisions, follow-ups, proof, PR.md). `.worktrees/` is
gitignored. Work only inside your job's paths; the main checkout stays clean.

## Missions and slices

gg's project root is `.openrig/`: `SPEC.md`, `project.yaml` and `missions/`. Pass
`--workspace <checkout>/.openrig` to every `rig scope` command; without it, `rig scope` reads the
default workspace and misses gg's missions.

## Verification

`rules/verification.md` and `rules/engineering.md` §11 apply verbatim. A verdict
holds for exactly one head SHA. A claim without a captured, read-back artifact is a guess. The
author of a change never verifies it.

## Publishing

No push, PR, comment, issue or post unless Q asks for that specific action.
