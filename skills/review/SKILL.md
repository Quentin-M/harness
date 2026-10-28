---
name: review
description: "Use for \"/review\", \"sampled review\", \"review loop\", \"get this PR clean\", \"loop until the reviewers are happy\", or a PR URL with review threads to handle. Samples K independent read-only GPT reviewers over one diff, merges their findings by vote, then fixes or rebuts each confirmed finding and repeats until clean. Replies to and resolves PR threads."
argument-hint: "[<pr number|url> | --branch <base> | --worktree] [--samples N] [--post] [--with-bot] [--open] [--focus text]"
disable-model-invocation: true
---

# review

A sampled review loop. K independent reviewers on the harness `review` model read the same diff in
read-only headless processes. A deterministic merge turns their JSON into votes. You verify every
confirmed finding against the code, fix or rebut it, and re-run on the new head until the sample is happy.

```
 scope ─▶ sample (K reviewers, staggered) ─▶ merge (votes) ─▶ verify each confirmed ─▶ fix batch
   ▲                                                                                      │
   └────────────────── re-run on the new head, cap 6 rounds ◀──── tests, commit, push ◀───┘
```

Scripts live in `scripts/` next to this file (call it `$RV`):

| script | job |
|---|---|
| `$RV/sample` | resolve scope, run K reviewers, print the merged table; run dir on first and last line |
| `$RV/merge <run-dir> --samples K [--json\|--markdown]` | cluster, vote, rank. No LLM |
| `$RV/threads` | `state`, `thread`, `reply`, `resolve` for PR threads; `bot-request`, `bot-wait` only with `--with-bot` |
| `tests/test_merge.sh` | behavior tests for the merge |

## Arguments

| arg | meaning |
|---|---|
| `<pr>` | PR number or URL. Diff comes from `gh`, never from the local checkout. A detached worktree at the head SHA is used when the cwd is a checkout of that repo |
| `--branch <base>` | three-dot diff `base...HEAD` of the current checkout |
| `--worktree` | staged and unstaged changes; untracked files are named in the prompt |
| `--samples N` | reviewers, 1-5, default 3 |
| `--focus text` | extra emphasis passed to every reviewer |
| `--open` | open the PR first (step 1) |
| `--post` | post one consolidated review comment at the end |
| `--with-bot` | also request the BitMEX-Claude bot and wait for it (step 6) |

No scope given: use the PR of the current branch if one exists (`gh pr view`), else `--branch` against the default branch.

## Reviewer contract

Each reviewer runs `omp -p --no-session --mode json` on the model from `omp config get modelRoles`
(`review` role, `:high`; override with `REVIEW_MODEL`), with `--tools read,grep,glob`, no extensions,
skills or LSP. It cannot edit or run commands. Its prompt is `references/reviewer-prompt.md`. Its last
message is one JSON object whose findings carry severity (`blocker|major|minor`), file, line range,
rule, title, claim, evidence quote and minimal fix. Findings must be provable and introduced by the
diff; pre-existing defects and style nits are out.

Starts are staggered 8 s apart: `litellm-credential-helper` mints a key per call and a burst of
concurrent starts returns 401s. Never override `HOME`; it breaks the keychain.

## Merge rules

```
cluster   same file, line ranges within 3 lines, and (title token overlap >= 0.25 or range IoU >= 0.5);
          one sample votes once per cluster
severity  max across the cluster's members
confirmed votes >= ceil(K/2) and severity blocker|major
maybe     everything else: singletons, minors (reported, never blocking)
happy     no confirmed cluster and no blocker singleton
usable    a sample with invalid JSON or no findings array failed; the run continues with >= min(2, K)
          usable samples, otherwise exit 2
```

## Loop

Round cap 6. One round is steps 2 to 5.

1. **Open the PR** (only with `--open` or when asked). Feature branch only, never the base. Commit, `git push -u`, `gh pr create` without `--draft` (existing draft: `gh pr ready <n>`). Body: what and why, terse, current state only, no round history. Add human reviewers only when Q names them.
2. **Sample.** `$RV/sample <scope> --samples N`, `run_in_background: true`; a run takes about 30 s to a few minutes. If `sample` exits 2, report the failed samples and stop: never fake a pass.
3. **Triage every `confirmed` cluster.** Read the code at `file:line` and decide, in the style of `bitmex:github-pr-reviews-receive`:
   - valid: fix it.
   - wrong: rebut with evidence (`file:line`, a test, an invariant ID).
   - objects to the change's own purpose: the intent stands, say so.
   - valid but out of scope: say where it belongs. File a follow-up only if Q asked.
   Read `maybe` and `blocker?` rows too; fix one when it is provably real and cheap, otherwise list it in the report.
4. **Fix in one batch.** Delegate to a sub-agent when fixes span files or need tests. Run the covering tests. Never delete or weaken a test to satisfy a finding. Commit and push (PR or branch mode).
5. **Existing PR threads.** With a PR, run `$RV/threads state <pr>` and handle every unresolved thread the same way: `$RV/threads thread <id>` to read, verify, then `$RV/threads reply <id> --resolve <<'EOF' ... EOF`. One or two sentences: what changed plus the sha, or why not. Reply and resolve together; a thread never closes unexplained. Human threads: reply and resolve when fixed; on pushback reply only and leave it open for the human. A finding that exists only in a review body gets one `gh pr comment`. `mergeable: CONFLICTING`: rebase or merge the base per repo convention, re-run tests, push.
6. **Bot (`--with-bot` only).** `$RV/threads bot-request <pr>` dismisses the bot's stale CHANGES_REQUESTED reviews and re-requests it; `bot-wait <pr>` (background) blocks until it reviews. `threads state <pr> --with-bot` adds `bot_happy`. Done requires both the sample and `bot_happy`. Never dismiss a human's review.
7. **Re-run** step 2 on the new head.

Stop when `happy` is true (and CI is green for a PR), or at round 6.

## Guardrails

- No flip-flopping. Keep a list of rebutted findings (file, lines, claim). A cluster that matches one, raised again by 2 or more samples, goes to Q instead of a second rebuttal.
- Round cap 6. Still finding new real defects after 6 means the design needs rethinking: stop and report.
- Funds safety and repo invariants (INVARIANTS docs, P1/P2): fix or escalate to Q, never rebut on taste.
- Never delete or weaken a test to satisfy a finding.
- Never dismiss a human's review without Q's say-so. Re-request it instead.
- Ask Q before applying a suggestion that touches auth or security config, token claims, cert paths, signing keys, credential scopes, CODEOWNERS or branch protection.
- A CI failure that also fails on the base branch: report it, do not chase it.
- Several PRs: one sub-agent per PR running this skill; the main agent coordinates and reports.

## Finish

1. With `--post`: `$RV/merge <run-dir> --samples K --markdown > /tmp/review-comment.md`, read it back, then `gh pr comment <pr> --body-file /tmp/review-comment.md`. One comment, summary table with agreement counts. Default is local only.
2. Report: scope and head, rounds run, the last merged table, what changed per round, rebuttals worth Q's eyes, `maybe` rows left, threads left open for humans, approximate cost (about $0.04 per reviewer on a small diff).
