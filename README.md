# harness

Q's AI harness: one launcher, one rule set, one pod on OpenRig.
Generated config lives in a state dir; nothing is written into `~/.claude`, `~/.codex`, `~/.omp`
or `~/.agents`.

```
harness/
├── AGENTS.md            persona and layer precedence
├── rules/ styles/       always-on rules, reply style
├── skills/              ours + pstack (PSTACK.md pins upstream), /watup
├── agents/<role>/       OpenRig agent.yaml + role.md, one per role, shared by projects
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
lead    lead (opus) ⇄ peer (gpt-6.1-sol)
          │ queue rows, one worktree per job, fresh seats per job
dev     architect · engineer-1…N          pool, opus-5-5[1m]
          │ queue handoff at a head SHA
verify  verifier-1…N · reviewer-1…N      pools, gpt-6.1-sol, observe dev ─▶ PR.md
```

Roles are pools: the lead hands each job to the least-loaded seat of a role and names the
downstream seats in the brief. Grow a pool by adding members to `rigs/gg/rig.yaml` and their edges
(`delegates_to` from `lead.lead`, `escalates_to` back to it, `can_observe` dev for checkers), then
`rig down gg && rig up rigs/gg/rig.yaml`.

The builder never verifies its own work, and a different model family checks it. Seats run
Claude Code or Codex with the real HOME and read the generated `CLAUDE_CONFIG_DIR` and
`CODEX_HOME`. Each seat names its gateway model in `rig.yaml`.

## Models

`go/models.yaml` holds model ids, context sizes and roles. The gateway URL and key come from
managed settings (`ANTHROPIC_BASE_URL`, `apiKeyHelper`); `ANTHROPIC_BASE_URL` and `HARNESS_KEY_CMD`
override them.

Memory is automatic: every harness and seat shares one local Mnemopi bank under the state dir.

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
