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
    ├── omp/              submodule: our oh-my-pi fork, branch qmachu
    └── omp-classifier/   submodule: STRML/omp-classifier, pinned
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

Pro laptops get the gateway, key helper and managed settings together. A personal laptop has
none of them, or only a keyless gateway from `ANTHROPIC_BASE_URL`. No gateway means
subscription mode: each harness signs in with its own login (claude.ai, ChatGPT, omp `/login`).
A model's `subscription` field names the provider that serves it there; a role whose model no
login serves takes the model's `fallback` (DeepSeek → Sonnet), else `default`.

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

## Local command classifier

On an Apple Silicon Mac with 32 GiB or more, `--omp` launches load [omp-classifier](https://github.com/STRML/omp-classifier)
(`-e vendors/omp-classifier`), which judges every bash call that no rule already decides. Its judge
is JEV-9B (NVFP4) with a LoRA adapter fine-tuned in this repo (`bench/judge/finetune.md`) and
published as [memtalow/jev-9b-ft-v3](https://huggingface.co/memtalow/jev-9b-ft-v3) (60 MB), served as
`jev-9b-ft-v3` by `bench/judge/adapters/jev_systemone.py` on `127.0.0.1:11436`, so no command leaves
the machine and no API key is needed. The plugin decides by the committed gate battery
`bench/judge/gates/jev-9b-ft-v3.json` (questions and thresholds tuned at a 2% false-allow budget).
Anything else (Linux, Intel, less RAM, no `uv`, no checked-out plugin, `HARNESS_JUDGE=off`) launches
plain omp, which is omp's default.

A launch walks these steps and stops at the first that fails, launching plain omp:

1. The host is macOS, arm64, 32 GiB or more; `uv` is installed; the plugin and the gate battery are
   checked out.
2. The gate battery's own questions, asked about `true`, come back from the judge as typed answers
   in under 5 s.
3. If nothing listens: a missing model or adapter starts `claude judge fetch` in the background (at
   most hourly); otherwise
   `uv run --script bench/judge/adapters/jev_systemone.py <model> <port> <adapter> --model-id
   jev-9b-ft-v3 --idle-exit-secs 21600` is started (detached, one per Mac) and the launch waits up to
   30 s for step 2. The first `uv run` installs mlx-vlm and can take minutes; the server keeps going
   and a later launch finds it.

The plugin blocks every bash call while its judge is down, so it is only loaded after a real
decision request has just succeeded; the server binds its port only after the model is loaded.
Launches share one server (detached, survives the session, exits after 6 idle hours) and serialise on
`$HARNESS_STATE/judge/start.lock`. Nothing trains by itself. `claude judge fetch` downloads the base
model (6.5 GB) and the published adapter, both pinned by revision and sha256-checked, in the
foreground. `claude judge train [--force]` rebuilds the adapter locally instead: it fetches the
inputs, then runs `build_data.py` and `train.py` with the v3 recipe, streaming their output (about 75
minutes and 40 GB of unified memory on the GPU), and refuses to replace an existing adapter without
`--force`. A local adapter is never overwritten by a fetch, but the gate's thresholds were tuned on
the published one (`bench/judge/finetune.md#gate`).
Everything lives in `$HARNESS_STATE/judge/ft` (`JUDGE_FT_WORK` for the scripts): model in `models/`,
adapter in `runs/v3`. Logs: `$HARNESS_STATE/judge/{server,fetch}.log`. `claude doctor` shows why the
classifier is on or off.

Config is `$HARNESS_STATE/omp/omp-classifier/omp-classifier.json`, found through `OMP_JEV_CONFIG`. The
harness owns exactly `judgeBackend`, `battery` and `hardBlocks` (always `false`: the battery's rule is
the policy) and rewrites them every launch; everything else (`enabled`, `timeoutMs`, `jevPolicy`,
`reviewerModel`) is yours through `/classifier` and survives. Dependencies install with
`bun install --frozen-lockfile --production` on the first launch. Knobs: `HARNESS_JUDGE=off`,
`HARNESS_JUDGE_PORT`, `HARNESS_JUDGE_MODEL_DIR`. Bump the plugin like the omp fork: update the
submodule, run the tests.

### Turning it off: `--yolo`

When the classifier misbehaves (it blocks routine commands, or hangs a session), launch with
`--yolo`: `claude --omp --yolo` loads no classifier, never probes or starts the judge, and passes
omp `--approval-mode=yolo`, so nothing asks for approval. `claude --yolo doctor` shows
`classifier: off: --yolo`. It is a launcher flag, given before the subcommand or harness args like
`--model` (`claude doctor --yolo` ignores it). `--claude`
already launches with `--dangerously-skip-permissions`, so `--yolo` changes nothing there; `--codex` and
`--opencode` reject it. Narrower escapes: `HARNESS_JUDGE=off` launches omp without the classifier but
keeps omp's approval prompts, and `/classifier off` inside a session switches the classifier off for
that session.

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
