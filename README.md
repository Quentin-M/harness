# harness

Q's AI harness: one launcher, one rule set.
Generated config lives in a state dir; nothing is written into `~/.claude`, `~/.codex`, `~/.omp`
or `~/.agents`. `claude --resume <id>` and `claude --omp --resume <id>` also find sessions bare
claude, bare omp or a herdr restore wrote to `~/.claude` and `~/.omp/agent`.

```
harness/
├── AGENTS.md            persona and layer precedence
├── rules/ styles/       always-on rules, reply style
├── skills/              ours + pstack (PSTACK.md pins upstream), /ketchup
└── go/                  launcher: `claude` CLI + models.yaml
```

The repo root is the base layer for every harness; the superproject's `.agents/`
and `HARNESS_LAYERS` stack on top.

## Start

```
claude ─┬─ (default)          Claude Code
        ├─ --omp              omp: inline images, /goal, task agents
        ├─ --codex            Codex
        └─ --opencode | --oc  opencode
```

```sh
claude                                                  # Claude Code in the current repo
```

## Models

`go/models.yaml` holds model ids, context sizes and roles. The gateway URL and key come from
managed settings (`ANTHROPIC_BASE_URL`, `apiKeyHelper`); `ANTHROPIC_BASE_URL` and `HARNESS_KEY_CMD`
override them.

Memory is Claude Code's per-project auto memory; codex and omp share the same directory through
`harness memory path`. A launch starts `harness dream` once the last one is over 20h old: one
headless claude per active project distils its recent sessions into that directory. Reports and
the log live in `$HARNESS_STATE/dream/`.

## Add a rule

1. Add one line to `rules/<topic>.md` (new topic = new file).
2. Relaunch. Every harness picks it up.

A correction given twice belongs in structure: run `/correct` to turn it into a lint, test or script.
Machine-private rules go in a layer outside any repo, listed in `HARNESS_LAYERS`.

## pstack

Lauren Tan's pstack lives in `skills/`, pinned in `skills/PSTACK.md`.
`skills/vendor-pstack.sh [sha]` updates only the skills listed there. Cursor mechanics map onto
each harness via the `harness-mechanics` skill.

| Command | Use |
|---|---|
| `/poteto-mode` | route any task to the matching playbook |
| `/how` `/why` | code walkthrough, design archaeology |
| `/architect` `/tdd` `/blast-radius` | shape first, failing test first, what else breaks |
| `/arena` `/swarm` `/interrogate` | competing attempts, parallel workers, adversarial review |
| `/review` (omp: `/skill:review`) | K sampled read-only GPT reviewers, vote-merged, fix-or-rebut loop on a PR |
| `/correct` `/unslop` `/reflect` | encode lessons, cut AI prose, learn from a session |

## Change the launcher

Run `cd go && go test ./...` before committing. Run `/leak-check` before any push.
