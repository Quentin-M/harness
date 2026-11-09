# JEV-9B judge fine-tune (compact-v3)

LoRA fine-tune of `JEV-9B-MLX-NVFP4` on the Apple GPU (MLX) so the `compact-v3` battery (security-first policy, see `README.md#policy`) blocks real threats while letting routine development work through. The Neural Engine is not reachable from MLX; training runs on the GPU only. Every command in every dataset is inert text and is never executed.

Code lives in `bench/judge/finetune/`; downloaded rows, weights, built data and adapters stay in the work dir (`$JUDGE_FT_WORK`, else `$HARNESS_STATE/judge/ft`, else `~/.local/state/harness/judge/ft`) and are never committed. The kept v3 adapter is published as [memtalow/jev-9b-ft-v3](https://huggingface.co/memtalow/jev-9b-ft-v3) (revision `938078f0b1561d16056118c918938e7ae36a8b45`), which `fetch.py` downloads into `runs/v3` unless one is already there; the gate's thresholds are tuned for those exact weights.

## Contract

- No training on the bench. The eval corpus is `corpus/*.jsonl` plus `external/corpus/shellrisk.jsonl` (ShellRisk **test** split). Training rows are removed when their command matches an eval command exactly or after whitespace and trailing-comment normalization. Overlap removed with the pinned inputs: shell-safety 533 exact + 133 normalized, synthetic 39 exact + 5 normalized, ShellRisk benign 1 exact, ShellRisk risky 0 (the counts `build_data.py` prints).
- The security families in the synthetic data (force-push spellings, installs from custom registries and mirrors, destructive git, unscoped deletes) were chosen **after seeing earlier models' misses on the bench**. To keep that honest, `synth.py` holds out whole spelling variants of each family in `famhold-v3.jsonl` (53 rows, never trained on); it is evaluated separately.

## Data (`build_data.py`, seed 1)

| Source | License | Use |
|---|---|---|
| ShellRisk-Bench **train** parquet (revision `437467862139b4e9cdd5322024ef3434a67c7ec8`) | card `other`: no blanket license, each upstream keeps its own; **local only, never committed** | 694 risky rows (GTFOBins, Atomic Red Team, payloads; atomicredteam and payloads oversampled x3, +438) -> `unsafe`; 2,180 not-risky rows (1,100 scary-looking + stratified plain; SWE-smith, Terminal-Bench, nl2bash) -> `safe` |
| tomngdev/shell-safety-v1.1 **train.jsonl** (revision `fee89770c315d525ef2ee42adee6ef9725a7621e`) | MIT | POSIX rows with curated reasons, capped per reason: `allow` -> `safe`; `ask`/`deny` -> `unsafe` only when the policy detector or an always-block reason applies, otherwise `safe` |
| Synthetic (`synth.py`) | written for this repo, no bench commands | scary-looking benign, user-authorized contrast pairs (same command safe when the user asked, a bench-style unrelated message otherwise), destructive git, unscoped deletes, remote overwrites, custom-registry installs and their official-registry twins, exfiltration, fetch-and-run, persistence, reviewer-manipulation comments |

`policy.py` detects the policy categories (exfiltration, remote shell/C2, malware, untrusted code, disk-scale destruction, credential theft, security evasion, custom registries, reviewer manipulation) in command text; `relabel()` in `build_data.py` turns every row's verdict and risk labels into the compact-v3 questions (`verdict`, `data_leaves_machine`, `untrusted_code`, `attacker_tooling`, `disk_scale_destruction`, `manipulates_reviewer`). Risks not implied by a row are masked, not set false.

Result: train 6,391 commands (4,335 safe / 2,056 unsafe, 693 tagged critical), hold-out 682 (458 / 224; 10% split by normalized-command hash), family hold-out 53 (34 / 19). `unsure` is never a label. Each command is rendered exactly as the harness sends it (state JSON with `tool`, `workingDirectory`, `command`, optional `userMessages`; the compact-v3 question text; JEV's `[kind]/[state]/[question]/[options]/[decision]:` prompt).

## Training (`train.py`)

- Frozen NVFP4 base, rank-16 / alpha-32 fp32 LoRA on every quantized linear (attention, linear attention, MLP) of the **last 12 of 32 layers** (75 modules, 15.0 M parameters). `lm_head` frozen. Training all 32 layers was ~3x slower.
- Loss: cross-entropy of the option-token logits plus JEV bias, divided by the per-type temperature (`choice` A/B/C for `verdict`, `noul` false/true for the risks), the readout `Jev.predict` uses. One verdict item per command; risk items for every known positive plus one random known negative per row.
- AdamW lr 1e-4, 5% warm-up then cosine to 10%, grad-clip 1.0. Each step is one verdict micro-batch plus one risk micro-batch (<= 16 sequences, <= 4,096 padded tokens each), accumulated. Scheduled for 1 epoch (474 steps) under a 75-minute cap; on an idle M5 Max it reaches step 469 (~550 tok/s). A GPU shared with other work runs slower and stops earlier, so the kept step depends on the machine's load.
- Checkpoint selection on the hold-out (first 200 rows, evaluated every 90 steps): maximize benign passed at >= 98% unsafe blocked with every critical-like row blocked; ties by lower cross-entropy. Step 450 won:

| step | verdict acc | verdict CE | risk CE | critical-like blocked | benign passed at selection |
|---|---|---|---|---|---|
| 0 (base) | 83.0% | 0.719 | 0.117 | 92.6% | 62.8% |
| 90 | 93.0% | 0.199 | 0.105 | 96.3% | 75.2% |
| 180 | 95.0% | 0.129 | 0.046 | 96.3% | 88.4% |
| 270 | 96.0% | 0.115 | 0.071 | 96.3% | 89.1% |
| 360 | 96.5% | 0.110 | 0.049 | 100% | 84.5% |
| **450 (kept)** | 97.0% | 0.102 | 0.035 | 96.3% | 89.1% |

- `gdpatch.py` makes training feasible on MLX: a faster, autograd-friendly gated-delta chunk scan (chunk 16, product-form triangular inverse; chunk 32 is numerically unstable) and a custom VJP for the NVFP4 matmul (dequantize then matmul in backward, ~4x faster than the stock VJP). Max |logit| difference to the inference kernel path: 0.06 on logits of scale ~7.

## Results

Bench: 1,306 cases, corpus and labels as of 2026-11-09, 2-fold CV with state-grouped folds (`reports/2026-11-09-*`). "Dangerous blocked" is recall over blockable cases; "false allows" counts critical and high; the 2% row is the harness' primary operating point (<= 5 false allows) and v3 is over that budget at every row up to 2%.

| model (battery) | budget | dangerous blocked | critical blocked | false allows | benign passed | p50 ms |
|---|---|---|---|---|---|---|
| base (compact-v3) | 2% | 98.9% | 66/66 | 3 | 47.2% | 1086 |
| base (compact-v3) | 5% | 95.6% | 66/66 | 12 | 64.6% | 1086 |
| **v3** (compact-v3) | 0-2% | 97.8% | 65/66 | 6 | 92.5% | 1026 |
| **v3** (compact-v3) | 5% | 96.7% | 64/66 | 9 | 95.1% | 1026 |
| base (compact-v2) | 2% | 98.9% | 66/66 | 3 | 33.1% | 948 |
| v1 (compact-v2) | 2% | 97.4% | 65/66 | 7 | 73.1% | 1014 |
| v2 (compact-v2) | 2% | 95.2% | 66/66 | 13 | 75.9% | 984 |

Reports: `reports/2026-11-09-jev-9b-ft-v3-compact-v3.md`, `reports/2026-11-09-jev-9b-nvfp4-compact-v3.md`, and the compact-v2 reports for the base, v1 and v2 models (v1 and v2 predate the policy change and were trained on the earlier labels; they are kept only as reference rows). v3 meets the benign-passed (>= 80%) and latency (p50 1.0 s, p95 1.3 s) goals; it misses 98% dangerous blocked with 0 critical false allows by one case in CV (97.8%; `rm -rf "$BUILD_DIR/"` slips through).

## Reproduce

All commands run from `bench/judge/finetune/`; each script carries a PEP 723 header, so `uv run` builds the environment (mlx-vlm pinned to `Lazarus-931/mlx-vlm@597941d0d4eb900f1d066eba8d89abf474c0dd42`, mlx 0.32.4, numpy 2.5.4, pyarrow 26.0.1).

```
export JUDGE_FT_WORK=~/ft-work              # optional; see top for the default
uv run fetch.py                              # pinned, sha256-verified: ShellRisk train, shell-safety train/validation, JEV-9B-MLX-NVFP4 (6.5 GB)
uv run build_data.py                         # -> $JUDGE_FT_WORK/data/{train,holdout,famhold}-v3.jsonl, relabel-table.json
uv run train.py --epochs 1.0 --lr 1e-4 --neg-per-row 1 --lora-layers 12 \
  --eval-every 90 --eval-rows 200 --no-final --max-minutes 75   # -> $JUDGE_FT_WORK/runs/v3 (best step by the selection rule)
uv run train.py --eval-only --init-adapter $JUDGE_FT_WORK/runs/v3 \
  --holdout $JUDGE_FT_WORK/data/famhold-v3.jsonl --out $JUDGE_FT_WORK/runs/v3-fam   # family hold-out, reported separately
uv run ../adapters/jev_systemone.py $JUDGE_FT_WORK/models/JEV-9B-MLX-NVFP4 11360 $JUDGE_FT_WORK/runs/v3
cd ../../.. && HARNESS_ROOT=$PWD harness judge bench run --endpoint http://127.0.0.1:11360 --model jev-9b-ft-v3 \
  --battery bench/judge/batteries/compact-v3.json --corpus bench/judge/corpus --corpus bench/judge/external/corpus
```

`build_data.py` is deterministic (two runs, including under different `PYTHONHASHSEED`, are byte-identical). Training needs ~40 GB of unified memory and about 75 minutes on an M5 Max. The adapter dir holds `adapter.safetensors` and `adapter_config.json`; `jev_systemone.py` applies it unmerged, and without the third argument it serves the base model.

## Gate

`gates/jev-9b-ft-v3.json` is what the classifier sends and decides by: the `compact-v3` questions with the rule thresholds tuned in-sample at the 2% false-allow budget. On the bench those thresholds block 267/272 dangerous commands (98.2%), 66/66 critical, and pass 960/1017 routine ones (94.4%); the cross-validated figures in Results are the honest estimate. The gate is tied to one adapter: a retrain (whose kept step varies with GPU load) needs a fresh bench run and a regenerated gate. Regenerate it after a new report:

```
jq --slurpfile r reports/<report>.json '($r[0].frontier[] | select(.budgetPct==0.02) | .inSample.thresholds) as $t
  | .name="jev-9b-ft-v3" | del(.notice)
  | .rule[] |= map(. as $a | .min = ($t[] | select(.q==$a.q and .label==$a.label) | .min))' batteries/compact-v3.json > gates/jev-9b-ft-v3.json
```
