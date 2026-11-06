# harness

One launcher and one shared config for every AI coding agent: Claude Code, omp, Codex and
opencode. Write a rule, skill or model once; each agent gets it in its own native format.

```
rules/ · skills/ · styles/ · AGENTS.md · go/models.yaml
+ the project's .agents/ and HARNESS_LAYERS
│
▼
╭──────────────────────╮
│ claude (go/ launcher)│   renders native config into $HARNESS_STATE
╰──────────┬───────────╯
           ▼
Claude Code · omp · Codex · opencode
```

On top of that it gives every agent:

- one memory directory per project, which `harness dream` refills from recent sessions every ~20h
- herdr agent-state hooks, plus the herdr and Herdr.app config
- omp built from our fork, pinned as a submodule

## Layout

```
harness/
├── AGENTS.md         persona and layer precedence
├── rules/            always-on rules, one topic per file
├── styles/           reply style
├── skills/           ours + vendored pstack (PSTACK.md pins upstream)
├── go/               launcher: `claude` CLI, models.yaml
├── herdr/
│   ├── config.toml   herdr TUI       → ~/.config/herdr/config.toml
│   └── app/          Herdr.app       → ~/.config/herdr-app/
└── vendors/
    └── omp/          submodule: our oh-my-pi fork, branch qmachu
```

Generated config goes to a state dir. Nothing is written into `~/.claude`, `~/.codex`, `~/.omp`
or `~/.agents`. `claude --resume <id>` still finds sessions that bare claude, bare omp or a
herdr restore wrote there.

## Start

```
claude ─┬─ (default)          Claude Code
        ├─ --omp              omp: inline images, /goal, task agents
        ├─ --codex            Codex
        └─ --opencode | --oc  opencode
```

The repo root is the base layer for every harness. The superproject's `.agents/` and
`HARNESS_LAYERS` stack on top.

## Add a rule

1. Add one line to `rules/<topic>.md`. A new topic gets a new file.
2. Relaunch. Every harness picks it up.

A correction given twice belongs in structure: `/correct` turns it into a lint, test or script.
Machine-private rules go in a layer outside any repo, listed in `HARNESS_LAYERS`.

## Models and memory

`go/models.yaml` holds model ids, context sizes and roles. The gateway URL and key come from
managed settings (`ANTHROPIC_BASE_URL`, `apiKeyHelper`). `ANTHROPIC_BASE_URL` and
`HARNESS_KEY_CMD` override them.

Memory is Claude Code's per-project auto memory. codex and omp reach the same directory through
`harness memory path`. A launch starts `harness dream` when the last run is over 20h old: one
headless claude per active project distils recent sessions into that directory. Reports and the
log live in `$HARNESS_STATE/dream/`.

## omp fork

```
can1357/oh-my-pi main
│ rebase, one squashed commit per PR
▼
qmachu
│ build
▼
~/.local/bin/omp
```

The superproject's `tools/omp-fork.sh` drives the submodule:

| Command | Does |
|---|---|
| `make omp` | build the checkout, install `~/.local/bin/omp`; `NATIVES=1` recompiles the Rust addon |
| `make omp-sync` | rebase qmachu onto upstream main, push to the fork |
| `make omp-pick PR=<N>` | squash an upstream PR onto qmachu |

Clone with `--recursive`, or let `make omp` initialise the submodule. Each command moves a
detached checkout onto qmachu first.

## herdr

`herdr/` is linked out of the store, so the herdr settings UI and Herdr.app write straight back
here. Images are copied, since the app never loads them through a link. After editing a mod, run
`touch -h ~/.config/herdr-app/mods/<mod>/main.js` to make the app reload it.

## pstack

Lauren Tan's pstack lives in `skills/`, pinned in `skills/PSTACK.md`.
`skills/vendor-pstack.sh [sha]` updates only the skills listed there. The `harness-mechanics`
skill maps Cursor mechanics onto each harness.

| Command | Use |
|---|---|
| `/poteto-mode` | route any task to the matching playbook |
| `/how` `/why` | code walkthrough, design archaeology |
| `/architect` `/tdd` `/blast-radius` | shape first, failing test first, what else breaks |
| `/arena` `/swarm` `/interrogate` | competing attempts, parallel workers, adversarial review |
| `/review` (omp: `/skill:review`) | K sampled read-only GPT reviewers, vote-merged, fix-or-rebut loop on a PR |
| `/correct` `/unslop` `/reflect` | encode lessons, cut AI prose, learn from a session |

## Before a commit

1. `cd go && go test ./...`
2. `/leak-check` before any push.
