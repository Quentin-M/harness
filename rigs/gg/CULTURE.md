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
- A send can time out ("Delivery UNCONFIRMED"). Confirm by effect in the target pane before resending; a durable
  rule or decision always goes as a row, never only as a send.
- Load-balance every handoff across the whole pool of that role. Count pending + in-progress + blocked rows per
  destination, tie → lowest number, re-count after. A brief may not narrow the pool; only the lead names exceptions.
- After `rig queue fallback` or any reroute, read the row back and check its destination: it can fail silently.
- Never hand a row to the lead as a question while you still need it: the handoff closes it. Ask with a send, or
  `rig queue block --on lead-lead@gg`.

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

- A mission has one playbook (`REVIEW.md` or equivalent) for the how. Where a brief or row disagrees with it, the
  playbook wins. The lead fixes the playbook, not each brief.
- Close a slice when its proof is good, whatever the verdict (merged, closed, folded): `status: closed` plus
  `verdict:` in the slice `SPEC.md` front matter. `.openrig/` is untracked, so `rig scope slice close` refuses.
- Record proof with `rig proof --workspace <checkout>/.openrig add`. Markdown artifacts only (`.md`); attach raw logs
  as `--media`.

## Verification

- Every gg test runs on the EC2 test box, never the laptop. Respect the full-suite cap in the mission playbook;
  a Docker socket timeout or ryuk conflict is box saturation, not a product failure.
- When master moves, re-run only what the delta touches, batched across PRs by one verifier. Core changes (state
  machine, db, ledger, migrations) need a full suite; one re-verification per merge attempt.
- Keep the state machine frozen: prefer live-only checks; a new rules bundle only when unavoidable, with the PR
  saying why; never edit a released bundle in place.
- A latent defect counts as real when committed shared-prod config reaches it once prod is enabled; the PR says
  "latent", never claims an occurrence.
- Seats on gpt-6.1-sol never open images (the gateway rejects them and one image poisons the session); use an
  opus seat or sub-agent for media.

`rules/verification.md` and `rules/engineering.md` §11 apply verbatim. A verdict
holds for exactly one head SHA. A claim without a captured, read-back artifact is a guess. The
author of a change never verifies it.

## Publishing

No push, PR, comment, issue or post unless Q asks for that specific action.
