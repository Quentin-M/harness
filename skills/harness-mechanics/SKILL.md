---
name: harness-mechanics
description: "Use whenever a pstack skill or playbook names a Cursor mechanism (Task/subagent_type, poteto-agent, generalPurpose, /loop, cloud agents, AskQuestion, .cursor paths, pstack-models.mdc, setup-pstack) to map it onto the current harness: omp, Claude Code, or Codex."
---

# Harness mechanics

pstack is written for Cursor. This skill translates every Cursor mechanism it names into the
harness you are running in. Keep the pstack intent, swap the mechanism.

## 1. Which harness am I in?

| Signal | Harness |
|---|---|
| `PI_CODING_AGENT_DIR` set, tools named `task` / `ask` | omp |
| `CLAUDE_CONFIG_DIR` set, tools named `Agent` / `AskUserQuestion` | Claude Code |
| `CODEX_HOME` set, tools named `spawn_agent` / `wait_agent` | Codex |

## 2. Spawning subagents (`Task`, `subagent_type`)

| pstack says | omp | Claude Code | Codex |
|---|---|---|---|
| `Task` tool | `task` tool, batch shape `{context, tasks:[{name, agent, task, solutionSpace}]}` | `Agent` tool | `spawn_agent` (then `wait_agent`, `send_input`, `close_agent`) |
| `subagent_type: X` | `agent: X` per task item | `subagent_type: X` | `agent_type: X` |
| `subagent_type: generalPurpose` | `agent: task` (bundled generic worker) | `subagent_type: general-purpose` | `agent_type: worker` (or omit for `default`) |
| `subagent_type: "poteto-agent"` | `agent: task`, role prompt inlined | `subagent_type: general-purpose`, role prompt inlined | `agent_type: worker`, role prompt inlined |
| `subagent_type: "Comment Sicko"` | `agent: task`, role prompt inlined | `subagent_type: general-purpose`, role prompt inlined | `agent_type: worker`, role prompt inlined |
| `model: <slug>` | set by agent frontmatter (`model: "@<role>"`); no per-call model field | `model` param on `Agent` (alias or full id) | `model` + `reasoning_effort` params on `spawn_agent` |
| `run_in_background: true` | default (`async.enabled=true`); results arrive as async messages, `wait` only when blocked | `run_in_background: true` | always async; `wait_agent` to collect |
| `readonly: true` | spawn a read-only agent (`scout`, `reviewer`, or an agent with `tools: read, bash`) | `subagent_type: Explore`, or an agent whose `tools` excludes edits | prompt-level only: say "read-only, no edits" in `message` |
| `readonly: false` (agent mode) | any agent without a `tools` restriction | any agent without a `tools` restriction | default |
| fork current context | not available; pass file pointers in `context` | `subagent_type: fork` | `fork_context: true` |

pstack's `poteto-agent` and `Comment Sicko` are role prompts, not installed agents: paste the body of
`poteto-mode/references/agents/{poteto-agent,comment-sicko}.md` at the top of the spawned subagent's task.
omp bundles `task`, `scout`, `reviewer`, `security-reviewer`, `sonic`. Pod roles live on OpenRig (`agents/`).

## 3. Cloud agents (`environment: "cloud"`)

No harness here has Cursor cloud machines. A cloud agent means "own checkout, own process, does not
touch mine". Map it to a worktree:

| omp | Claude Code | Codex |
|---|---|---|
| `isolated: true` on the task item; needs `task.isolation.enabled: true` (default off) and plan mode off. Changes come back as a patch or `omp/task/<id>` branch (`task.isolation.merge`) | `isolation: "worktree"` on `Agent`, or `isolation: worktree` in agent frontmatter | no per-spawn isolation; create a worktree yourself (`git worktree add`) and tell the worker its path, or use `/worktree` for the session |

"Check the cloud agent's status in the Cursor dashboard" becomes: omp `Alt+A` Agent Hub or
`read proc://<id>`; Claude Code `/tasks`; Codex `/subagents` or `list_agents`.

## 4. Asking the human (`AskQuestion`)

| omp | Claude Code | Codex |
|---|---|---|
| `ask` tool: `questions[{id, question, options[{label}], multi, recommended}]` | `AskUserQuestion` tool | `request_user_input` (1-3 questions) only in Plan mode; elsewhere ask in plain text |

Headless or subagent contexts have no prompt surface in any harness. Decide, record the
assumption, and move on (principle-never-block-on-the-human).

## 5. Loops and goals (`/loop`, "run until done")

| pstack says | omp | Claude Code | Codex |
|---|---|---|---|
| `/loop <interval> <prompt>` | `/loop [count\|duration] [--while\|--until '<cmd>'] [prompt]`; re-submits after every yield, the shell exit code gates the next iteration | `/loop [interval] [prompt]` bundled skill; omit interval to self-pace | no `/loop`; use `/goal` |
| `/loop` dynamic mode, "until X" | `/loop --until '<check cmd>' <prompt>` | `/loop <prompt>` (self-paced) | `/goal <objective>` |
| drive one task to a predicate | `/goal set <objective>` (`show`, `pause`, `resume`, `drop`, `budget <N\|off>`); `omp --goal "<objective>"` at launch | `/goal <condition>`: keeps working across turns until the condition holds; `/goal clear` stops it | `/goal <objective>`; `/goal resume` after pause or usage limit |

Every pod role runs its brief as a `/goal` whose finish condition is the brief's ACCEPTANCE.
A loop needs a check that can pass or fail; a vague condition spins forever.

## 6. Paths (`.cursor/...`)

| pstack path | omp | Claude Code | Codex |
|---|---|---|---|
| `~/.cursor/rules/pstack-models.mdc` | `## pstack models` in `$PI_CODING_AGENT_DIR/AGENTS.md` | `## pstack models` in `$CLAUDE_CONFIG_DIR/CLAUDE.md` | `## pstack models` in `$CODEX_HOME/AGENTS.md` |
| `~/.cursor/skills/`, `~/.cursor/plugins/` | `tools/harness/skills/` (linked into `$PI_CODING_AGENT_DIR/skills/`; never write to the generated dir) | `tools/harness/skills/` (linked into `$CLAUDE_CONFIG_DIR/skills/`) | `tools/harness/skills/` (linked into `$CODEX_HOME/skills/`) |
| `.cursor/skills/<x>/` (project) | `.agents/skills/<x>/` | `.agents/skills/<x>/`, with `.claude/skills` a symlink to `.agents/skills` | `.agents/skills/<x>/` |
| `~/.cursor/projects/<slug>/agent-transcripts/` | `$PI_CODING_AGENT_DIR/sessions/<encoded-cwd>/*.jsonl`; subagents `history://<id>`, `agent://<id>` | `$CLAUDE_CONFIG_DIR/projects/<encoded-cwd>/<session>.jsonl`; subagents `<session>/subagents/agent-<id>.jsonl` | `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl` (filter by `session_meta.payload.cwd`) |
| `.cursor/worktrees/<repo>/<x>` | isolation backend dirs; `git worktree list` is the source of truth | `.claude/worktrees/<x>` | `git worktree list` |
| `.cursor/automations/benny/` | not vendored | not vendored | not vendored |

The transcript rule still holds: read only the current workspace's bucket, never glob across all
projects.

## 7. Models (`/setup-pstack`, `pstack-models.mdc`)

Never run `setup-pstack`. Models are not configured per user here.

```
╭─────────────────────────────────╮   ╭──────────────────────────────────╮
│ models.yaml (+ layer)           │   │ skills/pstack-sheet.yaml         │
│ roles: default, review, ...     │   │ "feature, refactoring": default  │
│ profile: sub | <layer profiles> │   │ arena runners: [default, review] │
╰────────────────┬────────────────╯   ╰────────────────┬─────────────────╯
                 ╰──────────────────┬──────────────────╯
                                    ▼
                          ╭──────────────────╮
                          │ harness launcher │
                          ╰─────────┬────────╯
                                    ▼
            "## pstack models" in the generated instructions
```

- Read the model for a pstack role from the `## pstack models` section of your instructions.
  Its labels match the role lines pstack skills name (`how explorer`, `arena runners`, ...).
- A list value is a panel: one subagent per entry. Panels always span two model families.
- To change a model: edit `models.yaml` roles (or a layer `models.yaml`), then relaunch or `harness sync`.
  To change which role a pstack line uses: edit `skills/pstack-sheet.yaml`.
- omp: pass no model per call; pick the agent whose `@<role>` matches, or leave it to the agent
  file. Claude Code and Codex: pass the resolved id from the sheet as `model`.
- "If the Task tool rejects a slug" means the id is not served by the active profile. Fall back to
  the `default` role and say so.

## 8. Not available

| pstack feature | Status |
|---|---|
| Benny / `automations/` | not vendored; ignore references |
| `/create-skill` (Cursor built-in) | use the `skill-creator` skill |
| `make-bot-ui` skill | not vendored |
| Graphite `gt` | optional everywhere; pstack already falls back to plain git + `gh` |

## Unverified

- Claude Code: `subagent_type: fork` is inferred from the docs ("requesting the `fork` subagent
  type"); needs fork mode on (default in interactive sessions).
- omp: whether `task` accepts agent names containing spaces (`Comment Sicko`).
- Codex: launcher rendering of `$CODEX_HOME/agents/<name>.toml` role files (`name`, `description`,
  `model`, `developer_instructions`); the role loader reads that dir, but the end-to-end spawn is
  untested here.
- Codex: exact `/goal` subcommand words beyond `resume` (pause and clear exist in the goal menu).
