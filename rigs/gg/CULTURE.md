# gg rig culture

These rules bind every seat. Your role file adds the rest. The mission `SPEC.md` and playbook win
over any row, brief or message.

## Flow

```
            Q ⇄ lead-lead          asks, decisions, 30-min digest
                    ▲ rows blocked on the lead (decisions only)
 ┌──────────────────┴─────────────────────────────────────────────┐
 │ architect ─▶ reviewer ─approve─▶ engineer                      │
 │ engineer ─head─▶ reviewer ─approve─▶ verifier ─verified─▶ judge│
 │ reviewer / verifier ─changes|failed─▶ engineer                 │
 │ judge ─reject─▶ owning reviewer|verifier   judge ─accept─▶ close│
 └────────────────────────────────────────────────────────────────┘
```

Every arrow is a `rig queue handoff` straight to the next seat. Nobody reports to the lead.

## Queue protocol

- A queue row obligates; a `rig send` only informs. Assignments and handoffs are rows. Questions
  between seats are sends, straight to the seat that knows.
- End every turn on substantive work by passing the ball: `rig queue handoff`, or close the row
  with a `closure_reason` (`handed_off_to`, `blocked_on`, `no-follow-on`, `denied`, `canceled`).
  Never go idle holding an open row.
- After a handoff, pull your next row. Idle only on an empty queue.
- Load-balance every handoff across the whole pool of the target role: count pending +
  in-progress + blocked rows per seat in `rig queue list --json`, tie → lowest number, re-count
  after. After `rig queue fallback` or any reroute, read the row back: it can fail silently.
- Bodies longer than a line go through `--body-file`. Link artifacts by path; never paste dumps.
- Write in plain sentences with spaces. SHAs as full or 8-char prefixes. Never glue numbers to
  words.
- A send can time out ("Delivery UNCONFIRMED"). Confirm by effect before resending.

## Fresh context per row

Engineers, reviewers, verifiers and the judge start every row from a blank context: role, this
file and the mission SPEC, nothing from the previous row. Carry state in the row body and the
slice files, never in memory. After you hand off or close a row, run `harness fresh` and end your
turn; the seat relaunches blank and pulls its next row. Run `rig whoami --json` on every start.

## Silence is the default

Never send: acknowledgements, "rule read", "ready", "onboarded", status, "handed to X", "merged".
The queue, the ledger, `PROGRESS.md` and `rig proof show` already say it. The lead's digest
reads them.

## Decisions

Blocked on a seat: `rig queue block <qitem> --on <seat>`. Blocked on a decision the SPEC does not
settle: `rig queue block <qitem> --on lead-lead@gg` with the exact question, the options and your
recommendation. Only the lead talks to Q.

Nobody reads your pane. A question typed there, or a turn that ends on "Should I …?", reaches no one and
stalls silently. Ask the seat that knows with `rig send`, or park the row on it; then end the turn.

## Shared resources

The mission SPEC lists each shared resource with a concurrency cap and which runs count as heavy.
Before a heavy run, take a lease; release it when the run ends, pass or fail:

    harness lease acquire <resource> <cap> <your session>   # exit 0 leased, 75 full
    harness lease release <resource> <your session>
    harness lease list <resource>

A lease expires after 4h; a run longer than that re-acquires before the TTL.

Full: run the light scope now and queue the heavy run. Never ask a seat for capacity. Saturation
symptoms (socket timeouts, container conflicts) are not product failures: re-run serially.

## Jobs and worktrees

Paths below are relative to the gg checkout, the cwd you launched in. From inside a worktree, the
checkout is `$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")`.

Each job has its own branch and worktree at `.worktrees/<job>` and its record at
`.worktrees/.jobs/<job>/` (briefs, ledger, decisions, follow-ups). `.worktrees/` is gitignored.
Work only inside your job's paths; the main checkout stays clean. Remove your own worktrees on
exit.

## Missions and slices

gg's project root is `.openrig/`: `SPEC.md`, `project.yaml` and `missions/`. Pass
`--workspace <checkout>/.openrig` to every `rig scope` command.

- A slice is done only when the judge closes it. Its `PROOF.md` and `PROGRESS.md` are filled by
  the judge at close.
- Every drop goes through `rig proof add` with the head SHA and the proof item it answers.
  Markdown artifacts only (`.md`); attach raw logs and media as `--media`.

## Verification

`rules/verification.md` and `rules/engineering.md` §11 apply verbatim, limited to the gates the
mission SPEC lists. A verdict holds for exactly one head SHA. A claim without a captured,
read-back artifact is a guess. The author of a change never verifies or judges it.

- When the base moves, re-run only what the delta touches, once per merge attempt.
- Seats on gpt-6.1-sol never open images (the gateway rejects them and one image poisons the
  session). Media belongs to opus seats.

## Publishing

No push, PR, comment, merge, issue or post unless the mission SPEC grants that action to your role.
