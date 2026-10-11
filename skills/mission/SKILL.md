---
name: mission
description: "Set up, amend or report on an OpenRig mission. Intake until every requirement is pinned, draft the SPEC in precise terms, send Q one summary to confirm, then lock, pilot and fan out. Use for /mission, \"new mission\", \"set up a mission\", \"amend the mission\", \"mission digest\"."
---

# Mission

A mission fails at setup or not at all. Every requirement Q gives becomes a proof item a judge can
accept or reject, and Q confirms the wording before any slice exists.

```
1 intake ─▶ 2 draft ─▶ 3 summary ─▶ Q ─yes─▶ 4 lock ─▶ 5 pilot ─▶ Q ─yes─▶ 6 fan-out
                ▲                    │edits                         │edits
                └────────────────────┴──────────────────────────────┘
amend:  edit SPEC ─▶ summary of the diff ─▶ Q ─yes─▶ regenerate open slices ─▶ bump version
digest: every 30 min, generated from the queue, the ledger and the proof
```

Usage: `/mission <what Q wants>`, `/mission amend <change>`, `/mission digest`.

## 1. Intake

Collect every field below. Read the code, the repo and earlier missions first; ask Q only what
they cannot answer. Ask in one batch, each question with options and a recommendation. Repeat
until no field is empty or guessed.

| Field | Pin down |
|---|---|
| Goal | the state of the world when the mission is done, in one sentence |
| Subjects | what one slice is (a PR, a module, a bug); the exact query that lists them; the count |
| Out of scope | what a seat must leave alone even when it looks broken |
| Requirements | each thing Q wants true of every subject, in Q's words first |
| Proof items | per requirement: the evidence that shows it, which role produces it, what fails it |
| Verdicts | every terminal outcome (e.g. merge, fold, close, fix) and when each applies |
| Authority | who may push, comment, merge, close, edit bodies or labels; "nobody" is an answer |
| Gates | which machine gates apply (behavior, faults, perf, compose, ci, media, leaks) and which do not |
| Always-affected | repo-wide checks every change runs, whatever it touches |
| Resources | each shared resource with a concurrency cap; which runs are heavy and which are light |
| Ordering | dependencies between subjects (stacks, overlaps); merge or delivery order |
| Escalations | the exact decisions only Q makes; everything else the SPEC settles |
| Pilot | which 2 subjects run first and why they are representative |
| Done | the test that says the mission is finished, including cleanup |

A requirement Q states loosely gets sharpened, never weakened. "Won't break anything else" is a
judgment over callers, consumers and runtime paths; tests are evidence for it, never the bar.
When a sharper wording changes scope, ask.

## 2. Draft

Write `missions/<id>/SPEC.md` (what) and `missions/<id>/PLAYBOOK.md` (how, per role). Every rule a
seat needs lives in one of these two files. Briefs carry only the subject, its SHAs and pointers.

SPEC.md sections, in order: Intent, Subjects, Out of scope, Requirements, Proof contract,
Verdicts, Authority, Gates, Resources, Ordering, Escalations, Done. Each proof item:

    - [ ] P-<n> <requirement, precise wording>        <!-- proof-item: p<n> -->
          evidence: <what the drop must show>  by: <role>  fails when: <condition>

Set the judge in `mission.yaml`:

    proofPolicy:
      judges: [verify-judge@<rig>]

## 3. Summary to Q

Send Q one message and stop until Q answers. Shape:

    MISSION <id>: <goal>
    Subjects   <query> → <count>          Out of scope  <list>
    Your words → spec
      "<Q's phrase>"   →  P-<n>: <precise item>   (by <role>, fails when <x>)
      ...one row per requirement; mark every row where meaning moved ⚠
    Verdicts   <list, with when>          Authority  <who does what>
    Gates      <on> / off: <off>          Resources  <name cap heavy/light>
    Escalates to you   <exact list>
    Assumptions ⚠      <every guess, each a question>
    Pilot      <2 subjects, why>
    Reply "yes" to lock, or edit any row.

Q's edits go back to step 2. Repeat until Q says yes.

## 4. Lock

    rig scope slice approve missions/<id> --scope spec --on-behalf-of human@host
    printf 'v1\t%s\tlocked\n' "$(date -u +%FT%TZ)" >> missions/<id>/VERSIONS.tsv

## 5. Pilot

Generate the 2 pilot slices from the locked SPEC (step 6, limited to them). They run the full
chain through the judge. Then send Q: each pilot's verdict, its `PROOF.md`, and every friction
point a seat hit. Q's edits are an amendment. Fan out only on Q's yes.

## 6. Fan-out

For each subject, create the slice from the SPEC, never by hand:
- `slice.yaml`, and `SPEC.md` whose Proof contract is the mission's items, verbatim
- `PROGRESS.md` and `PROOF.md` from the scaffold, filled by the judge at close
- one queue row to the first role in the chain, least-loaded seat, body = brief template

Then lint every slice and brief against the SPEC: any line that restates a rule, names a seat,
grants or forbids an action, or words a proof item differently is a defect. Fix the generator,
regenerate, re-lint.

Register the digest:

    rig watchdog register --policy periodic-reminder --spec digest.yaml \
      --target-session lead-lead@<rig> --interval-seconds 1800 --registered-by lead-lead@<rig>

## Amend

A change Q asks for mid-flight: edit SPEC or PLAYBOOK, send Q the summary limited to the diff,
and on yes regenerate every open slice's SPEC, bump `VERSIONS.tsv`, and requeue rows whose
evidence the change voids. No broadcast: seats read the SPEC at their next fresh start.

## Digest

Every 30 minutes, from `rig queue list --json`, the ledger, `rig proof show missions/<id> --json`
and slice front matter. Never ask a seat for status. Refresh mission `PROGRESS.md`, then send Q one
message shaped by `styles/i-have-adhd.md`: line 1 says how many decisions wait on Q, pictures
first, short captions. Aim for one screen plus one block per decision.

    2 decisions need you · <id> ██████░░░░ 31/52 closed (+3 in 30 min)

    ✓ #978 merged    ✓ #902 merged    ✓ #751 closed: duplicate of #784
    ◐ 14 in flight   review 5 · verify 6 · fix 3
    ⏸ 2 blocked on you (below)
    ⚠ #962 at verifier-2 for 2h10, budget 1h: relaunched fresh

    test-box  ██████░░ 6/8 leased

    <2-4 sentences: what moved, what is at risk, what happens next without Q>

Then one section per decision, oldest first:

    ## ⚠ D-<n> #784: <the question in one line>
    Context    what the subject is, what happened, why it is stuck now (facts,
               file:line or PR links, no jargon without a gloss)
    Question   the exact decision, answerable in one reply
    Options    A <choice> - cost, consequence      (recommended, and why)
               B <choice> - cost, consequence
    If no answer   what the rig does meanwhile (parked, next subject)
    Reply      "D-<n> A" or your own wording

Each decision carries everything Q needs to answer from the message alone. A decision carried
over from an earlier digest repeats in full with its age. When Q answers, record it in
`decisions.tsv`, unblock the row with `rig queue resolve`, and amend the SPEC when the answer
changes a rule. Nothing changed and nothing waits on Q: one line.
