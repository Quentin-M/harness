---
name: verify-template
description: Shape of a per-project verify-<app> skill (Launch, Doctor, Drive, Evidence, Faults, Perf, Cleanup + features/ map). Use when writing or reviewing a project's verify-<app> skill, or when /create-verification-skill output needs checking against the expected structure.
---

# verify-<app> template

pstack's `/create-verification-skill` generates the real skill into the target repo at
`.agents/skills/verify-<app>/` (or `.cursor/skills/`, `.claude/skills/`). This file is the shape that
output must match. Copy it, replace every `<...>`, delete sections the app cannot have, and prove it
once end to end before handing it over. Shape reference: poteto/verification-skill-example.

```
.agents/skills/verify-<app>/
├── SKILL.md            launch, doctor, drive, evidence, faults, perf, cleanup
├── control-<app>       optional driver script; `--help` lists every command
└── features/
    ├── README.md       index, baseline preconditions, sweep order
    └── <area>.md       one per user-facing area, from features/_area.md
```

Frontmatter of the generated skill:

```yaml
name: verify-<app>
description: Drive <app> (<surface>) like a user: launch, doctor, drive, capture proof. Use for screenshots, flows, fault injection and perf checks on <app>.
```

## Launch

- Command: `<exact dev/verify command>`; ready when `<log line | port answers | prompt>`.
- Isolation: `<ports, data dir, profile>` derived per checkout so two runs never share state.
  If the app cannot run twice, say so and refuse to drive a shared instance.
- CLI/TUI: build once, then each drive runs in its own PTY/tmux session.

## Doctor

One read-only command that answers "is this instance worth driving?":
process up, build matches `git rev-parse HEAD`, port owned by this checkout, auth valid, fixtures loaded.
Run it first and whenever anything looks off. A capture against a stale build is not evidence.

## Drive

- Harness: `<playwright | control-<app> | curl | tmux send-keys>` with real selectors from this repo.
- Handles: ARIA role + name, `data-*` attributes, route paths, prompt strings. Coordinates last.
- Wait on observable end states, never fixed sleeps.
- Pick the feature file(s) the change touches in `features/`; drive every entry point they list.
- Broad regression: walk `features/README.md` top to bottom.

## Evidence

Capture with the `proof` skill scripts (`shot`, `record`, `frames`, `term`, `diagram`) into
`<artifact dir>`, then follow the `proof` skill: read every artifact back, attach with `pr-proof`.

- Drive the production user path. Inspect internals only after the user path ran.
- One capture shows the trigger and the stable end state.
- Verify side effects: DB row, file, network/RPC log, reload round trip.
- Mocks only behind a boundary production already isolates.
- Unreachable path: name it, name the blocker (account, OS, entitlement), cover the closest real path.

## Faults

Every row of the design brief's fault matrix gets a recipe here. Show the failure and the recovery.

| Fault | Inject | Expect |
|---|---|---|
| dependency slow / down | `toxiproxy-cli toxic add -t latency -a latency=2000 <proxy>`; `toxiproxy-cli toggle <proxy>` | timeout + retry inside budget, clear error, no hang |
| process crash | `kill -9 <pid we started>` mid-request | restart, no partial write, idempotent replay |
| disk full | small tmpfs / `hdiutil` image for the data dir, fill with `dd` | write fails cleanly, no corrupt file, alert/log |
| network partition | toxiproxy `timeout` toxic or `docker network disconnect <net> <ctr>` | stale reads flagged, writes refused or queued, heal converges |
| `<app-specific>` | `<command>` | `<observable result>` |

Route dependencies through toxiproxy in Launch (`<dep>` listens on `<proxy port>`), so faults need no code change.

## Perf

Compare against `main` on the same machine, same count. Budgets come from the design brief §2.

- Benchmarks: `git stash; <bench cmd> -count=10 > /tmp/old.txt; git stash pop;`
  `<bench cmd> -count=10 > /tmp/new.txt; benchstat /tmp/old.txt /tmp/new.txt`.
  No regression outside noise (p < 0.05), allocs/op within budget.
- SQL: every changed or added query runs under
  `EXPLAIN (ANALYZE, BUFFERS) <query>;` against `<seeded db>`. No seq scan on hot tables; note
  rows, buffers hit/read, time.
- HOT updates on touched tables:
  `SELECT relname, n_tup_upd, n_tup_hot_upd, round(n_tup_hot_upd::numeric / nullif(n_tup_upd, 0), 3) AS hot_ratio FROM pg_stat_user_tables WHERE relname IN (<tables>);`
  Reset with `pg_stat_reset()` before the run; ratio must not drop vs main.
- Latency: `<load tool> <endpoint>` p50/p99 vs budget.

## Cleanup

- Stop what this run started (saved pids, container names, tmux sessions). Never kill by process name.
- Remove scratch data dirs, proxies and fixtures.
- Keep the evidence: `<artifact dir>` survives cleanup. Confirm it after teardown.

## Maintenance

A UI or behavior change updates its `features/<area>.md` in the same PR. pstack
`/maintain-verification-skill` re-syncs drift.
