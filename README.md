# harness

Q's AI harness: one launcher, one rule set, one pod on OpenRig.
Generated config lives in a state dir; nothing is written into `~/.claude`, `~/.codex`, `~/.omp`
or `~/.agents`. `claude --resume <id>` and `claude --omp --resume <id>` also find sessions bare
claude, bare omp or a herdr restore wrote to `~/.claude` and `~/.omp/agent`.

```
harness/
├── AGENTS.md            persona and layer precedence
├── rules/ styles/       always-on rules, reply style
├── skills/              ours + pstack (PSTACK.md pins upstream), /ketchup
├── agents/<role>/       agent.yaml + role.md per role, shared by projects
├── rigs/<project>/      rig.yaml (every pod) · CULTURE.md
├── openrig.yaml         OpenRig project catalog
└── go/                  launcher: `claude` CLI + models.yaml
```

The repo root is the base layer for every harness and every seat; the superproject's `.agents/`
and `HARNESS_LAYERS` stack on top.

## Start

```
claude ─┬─ (default)          Claude Code
        ├─ --omp              omp: inline images, /goal, task agents
        ├─ --codex            Codex
        ├─ --opencode | --oc  opencode
        └─ --rig <rig args>   the OpenRig `rig` CLI, wired to the gateway
```

```sh
claude                                                  # Claude Code in the current repo
rig up rigs/gg/rig.yaml --cwd ~/workspace/gg
rig tui                                                 # watch and steer seats
rig ps
```

## Pod

```
lead    lead (opus) ⇄ Q            missions via /mission, 30-min digest
        peer (gpt-6.1-sol)         seat health
dev     architect · engineer-1…N   opus-5-5[1m]
verify  reviewer-1…N               gpt-6.1-sol, code review only
        verifier-1…N               opus-5-5[1m], machine evidence + media
        judge                      opus-5-5[1m], accepts proof, closes slices

engineer ─head─▶ reviewer ─approve─▶ verifier ─▶ judge ─accept─▶ closed
    ▲                │ changes           │ failed   │ reject ─▶ owning seat
    └────────────────┴───────────────────┘
```

Seats hand off to each other through the queue and relaunch blank after each row
(`harness fresh`). The lead only hears decisions. Heavy runs on a shared resource take a
`harness lease`. The mission SPEC holds every rule; `rigs/gg/CULTURE.md` binds every seat.
Grow a pool by adding members and their edges to `rigs/gg/rig.yaml`, then
`rig down gg && rig up rigs/gg/rig.yaml`.

The builder never verifies its own work, and a different model family checks it. Seats run
Claude Code or Codex with the real HOME and read the generated `CLAUDE_CONFIG_DIR` and
`CODEX_HOME`. Each seat names its gateway model in `rig.yaml`.

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
2. Relaunch. Every harness and seat picks it up.

A correction given twice belongs in structure: run `/correct` to turn it into a lint, test or script.
Machine-private rules go in a layer outside any repo, listed in `HARNESS_LAYERS`.

## Add a project

1. `cp -r rigs/gg rigs/<project>`, and add the project to `openrig.yaml`.
2. Edit the rig name, `CULTURE.md` and the cwd in `rig.yaml`.
3. `rig up rigs/<project>/rig.yaml --cwd <repo>`.

## Add a role

1. Create `agents/<role>/agent.yaml` and `role.md` (copy a neighbour).
2. Seat it in a pod file under `rigs/<project>/`.

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
| `/correct` `/unslop` `/reflect` | encode lessons, cut AI prose, learn from a session |

## Change the launcher

Run `cd go && go test ./...` before committing. Run `/leak-check` before any push.
