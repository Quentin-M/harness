# /// script
# requires-python = ">=3.12"
# dependencies = [
#   "mlx-vlm @ git+https://github.com/Lazarus-931/mlx-vlm.git@597941d0d4eb900f1d066eba8d89abf474c0dd42",
#   "mlx==0.32.4",
#   "numpy==2.5.4",
# ]
# ///
"""LoRA fine-tune of JEV-9B (NVFP4 pack kept frozen, fp32 adapters) on the compact-v3 battery.

Loss = cross-entropy of the question's option-token logits after JEV's bias and temperature
(exactly what Jev._pass reads), so the result stays compatible with Jev.predict. Inputs default
to the files build_data.py writes under the work dir (see paths.py).

    uv run train.py --epochs 1.0 --lr 1e-4 --neg-per-row 1 --lora-layers 12 --eval-every 90 --eval-rows 200 --no-final --max-minutes 75
"""
import argparse
import json
import math
import os
import random
import time

import mlx.core as mx
import mlx.nn as nn
import mlx.optimizers as optim
import numpy as np
from mlx.utils import tree_flatten, tree_map
from mlx_vlm import load
import gdpatch
from paths import BATTERY, DATA, MODEL, RUNS

gdpatch.install(16)
QLIN = gdpatch.make_qlin()

RISKS = ["data_leaves_machine", "untrusted_code", "attacker_tooling", "disk_scale_destruction", "manipulates_reviewer"]
VERDICTS = ["safe", "unsafe", "unsure"]

ap = argparse.ArgumentParser()
ap.add_argument("--train", default=str(DATA / "train-v3.jsonl"))
ap.add_argument("--holdout", default=str(DATA / "holdout-v3.jsonl"))
ap.add_argument("--out", default=str(RUNS / "v3"))
ap.add_argument("--rank", type=int, default=16)
ap.add_argument("--alpha", type=float, default=32)
ap.add_argument("--lr", type=float, default=1e-4)
ap.add_argument("--epochs", type=float, default=2)
ap.add_argument("--tok-budget", type=int, default=4096)
ap.add_argument("--neg-per-row", type=int, default=2)
ap.add_argument("--noul-weight", type=float, default=1.0)
ap.add_argument("--eval-every", type=int, default=150)
ap.add_argument("--max-minutes", type=float, default=110)
ap.add_argument("--eval-rows", type=int, default=250)
ap.add_argument("--skip-eval0", action="store_true")
ap.add_argument("--verbose-steps", type=int, default=0)
ap.add_argument("--lora-layers", type=int, default=12)
ap.add_argument("--max-batch", type=int, default=16)
ap.add_argument("--no-final", action="store_true")
ap.add_argument("--init-adapter", default="")
ap.add_argument("--eval-only", action="store_true")
ap.add_argument("--max-steps", type=int, default=0)
ap.add_argument("--limit-rows", type=int, default=0)
ap.add_argument("--ckpt", action="store_true")
ap.add_argument("--seed", type=int, default=0)
args = ap.parse_args()
os.makedirs(args.out, exist_ok=True)
random.seed(args.seed)
mx.random.seed(args.seed)

battery = json.load(open(BATTERY))["questions"]
model, processor = load(str(MODEL))
tok = getattr(processor, "tokenizer", processor)
cfg = model.config.decision_config
lm = model.language_model

# ---- option tokens, bias, temperature (mirrors Jev._labels/_scores/_pass) ----
import string

letters = string.ascii_uppercase
label_ids = []
for label in letters:
    ids = tok.encode(label, add_special_tokens=False)
    ctx = tok.encode(f"x\n{label}) y", add_special_tokens=False)
    if len(ids) == 1 and ids[0] in ctx:
        label_ids.append(ids[0])
crit = battery["verdict"]["criteria"]
assert list(crit) == VERDICTS
col_ids = label_ids[:3] + cfg["verbalizer_ids"][0:2]
col_bias = [cfg["bias"][8 + i] for i in range(3)] + [cfg["bias"][0], cfg["bias"][1]]
T_CHOICE, T_NOUL = cfg["temperature_by_type"]["choice"], cfg["temperature_by_type"]["noul"]
W_sel = lm.lm_head.weight[mx.array(col_ids)].astype(mx.float32)  # (5, H); lm_head is frozen
bias_arr = mx.array(col_bias, dtype=mx.float32)
mx.eval(W_sel, bias_arr)


def render(v):
    return v if isinstance(v, str) else json.dumps(v, ensure_ascii=False)


def prompt(state, q):
    spec = battery[q]
    st = json.dumps(state, ensure_ascii=False)
    if spec["type"] == "choice":
        opts = [f"{k}: {render(v)}" if v else k for k, v in spec["criteria"].items()]
        lines = [f"{string.ascii_uppercase[i]}) {o}" for i, o in enumerate(opts)]
        kind = "choice"
    else:
        lines, kind = ["false", "true"], "noul"
    return f"[kind] {kind}\n[state] {st}\n[question] {spec['instructions']}\n[options]\n" + "\n".join(lines) + "\n[decision]:"


def load_rows(path, limit=0):
    rows = [json.loads(l) for l in open(path)]
    return rows[:limit] if limit else rows


def encode(text):
    return tok(text, add_special_tokens=False)["input_ids"]


# ---- LoRA ----
class LoRALinear(nn.Module):
    def __init__(self, base, rank, alpha):
        super().__init__()
        self.base = base
        out_dim, in_packed = base.weight.shape
        in_dim = in_packed * 32 // base.bits
        self.lora_a = mx.random.uniform(-1 / math.sqrt(in_dim), 1 / math.sqrt(in_dim), (in_dim, rank))
        self.lora_b = mx.zeros((rank, out_dim))
        self.scale = alpha / rank

    def __call__(self, x):
        y = QLIN(x, self.base.weight, self.base.scales)
        z = (x.astype(mx.float32) @ self.lora_a) @ self.lora_b
        return y + (self.scale * z).astype(y.dtype)


def apply_lora(root, rank, alpha):
    n = 0
    for layer in root.layers[-args.lora_layers:]:
        updates = []
        for name, mod in layer.named_modules():
            if isinstance(mod, nn.QuantizedLinear) and min(mod.weight.shape[0], mod.weight.shape[1] * 32 // mod.bits) >= 512:
                updates.append((name, LoRALinear(mod, rank, alpha)))
        layer.update_modules(_nest(updates))
        n += len(updates)
    return n


def _nest(pairs):
    out = {}
    for name, mod in pairs:
        parts = name.split(".")
        d = out
        for p in parts[:-1]:
            d = d.setdefault(p, {})
        d[parts[-1]] = mod
    return out


model.freeze()
n_lora = apply_lora(lm.model, args.rank, args.alpha)
for _, m in lm.model.named_modules():
    if isinstance(m, LoRALinear):
        m.unfreeze(keys=["lora_a", "lora_b"], recurse=False)
if args.init_adapter:
    model.load_weights(list(mx.load(os.path.join(args.init_adapter, "adapter.safetensors")).items()), strict=False)
    mx.eval(model.parameters())
print("lora modules", n_lora, "trainable params", sum(v.size for _, v in tree_flatten(model.trainable_parameters())) / 1e6, "M", flush=True)

if args.ckpt:
    layer_cls = type(lm.model.layers[0])
    orig_call = layer_cls.__call__

    def ckpt_call(self, *a, **kw):
        def inner(params, *a, **kw):
            self.update(params)
            return orig_call(self, *a, **kw)

        return mx.checkpoint(inner)(self.trainable_parameters(), *a, **kw)

    layer_cls.__call__ = ckpt_call


# ---- forward ----
def forward_last(ids, lens):
    B, T = ids.shape
    pos = mx.broadcast_to(mx.arange(T)[None, None], (3, B, T))
    h = lm.model(ids, position_ids=pos)
    last = h[mx.arange(B), lens - 1]
    logits = last.astype(mx.float32) @ W_sel.T + bias_arr  # (B, 5)
    return logits


def item_loss(logits, is_choice, target, weight):
    zc = logits[:, :3] / T_CHOICE
    zn = logits[:, 3:5] / T_NOUL
    lc = mx.logsumexp(zc, axis=1) - mx.take_along_axis(zc, mx.minimum(target, 2)[:, None], axis=1)[:, 0]
    ln = mx.logsumexp(zn, axis=1) - mx.take_along_axis(zn, mx.minimum(target, 1)[:, None], axis=1)[:, 0]
    return mx.where(is_choice, lc, ln) * weight


def pad_batch(items):
    T = max(len(x[0]) for x in items)
    ids = np.zeros((len(items), T), dtype=np.int32)
    for i, x in enumerate(items):
        ids[i, : len(x[0])] = x[0]
    lens = np.array([len(x[0]) for x in items], dtype=np.int32)
    is_choice = np.array([x[1] for x in items])
    target = np.array([x[2] for x in items], dtype=np.int32)
    weight = np.array([x[3] for x in items], dtype=np.float32)
    return mx.array(ids), mx.array(lens), mx.array(is_choice), mx.array(target), mx.array(weight)


def loss_fn(model, ids, lens, is_choice, target, weight):
    logits = forward_last(ids, lens)
    return item_loss(logits, is_choice, target, weight).sum() / weight.shape[0]


def make_batches(items, budget, shuffle=True):
    # items: (ids, is_choice, target, weight); bucket by length then chunk to a padded-token budget
    order = sorted(range(len(items)), key=lambda i: len(items[i][0]) + (random.random() * 24 if shuffle else 0))
    batches, cur, cur_max = [], [], 0
    for i in order:
        L = len(items[i][0])
        if cur and (max(cur_max, L) * (len(cur) + 1) > budget or len(cur) >= args.max_batch):
            batches.append(cur)
            cur, cur_max = [], 0
        cur.append(i)
        cur_max = max(cur_max, L)
    if cur:
        batches.append(cur)
    if shuffle:
        random.shuffle(batches)
    return batches


# ---- data ----
def build_items(rows, neg_per_row, all_noul=False):
    items = []
    for r in rows:
        vt = VERDICTS.index(r["verdict"])
        items.append((encode(prompt(r["state"], "verdict")), True, vt, 1.0, r["id"], "verdict"))
        known = r["risks"]
        pos = [k for k, v in known.items() if v == 1]
        neg = [k for k, v in known.items() if v == 0]
        if all_noul:
            chosen = pos + neg
        else:
            random.shuffle(neg)
            chosen = pos + neg[:neg_per_row]
        for q in chosen:
            items.append((encode(prompt(r["state"], q)), False, int(known[q]), args.noul_weight, r["id"], q))
    return items


train_rows = load_rows(args.train, args.limit_rows)
hold_rows = load_rows(args.holdout, args.limit_rows)
print("rows", len(train_rows), len(hold_rows), flush=True)

# hold-out: every question for every row so the full compact-v2 rule can be simulated
hold_items = []
for r in hold_rows:
    hold_items.append((encode(prompt(r["state"], "verdict")), True, VERDICTS.index(r["verdict"]), 1.0, r["id"], "verdict"))
    for q in RISKS:
        hold_items.append((encode(prompt(r["state"], q)), False, int(r["risks"].get(q, 0)), 1.0, r["id"], q))
hold_by_id = {r["id"]: r for r in hold_rows}


@mx.compile
def _probs(logits):
    return mx.softmax(logits[:, :3] / T_CHOICE, axis=1), mx.softmax(logits[:, 3:5] / T_NOUL, axis=1)


def evaluate(full=False):
    model.eval()
    keep = None if full else {r["id"] for r in hold_rows[: args.eval_rows]}
    items_e = hold_items if full else [x for x in hold_items if x[4] in keep]
    res = {}
    tot = {"verdict": [0.0, 0], "noul": [0.0, 0]}
    for b in make_batches(items_e, 8192, shuffle=False):  # batches capped by --max-batch
        batch = [items_e[i] for i in b]
        ids, lens, is_choice, target, weight = pad_batch(batch)
        logits = forward_last(ids, lens)
        pc, pn = _probs(logits)
        mx.eval(pc, pn)
        pc, pn = np.array(pc), np.array(pn)
        for j, it in enumerate(batch):
            rid, q = it[4], it[5]
            res.setdefault(rid, {})[q] = pc[j] if it[1] else pn[j]
            p = pc[j] if it[1] else pn[j]
            k = "verdict" if it[1] else "noul"
            known = it[1] or hold_by_id[rid]["risks"].get(q) is not None
            if known:
                tot[k][0] += -math.log(max(float(p[it[2]]), 1e-9))
                tot[k][1] += 1
    # accuracy and rule simulation
    correct = n = 0
    score = []  # (is_unsafe, blocked_default, p_safe, max_risk)
    for rid, r in res.items():
        row = hold_by_id[rid]
        pv = res[rid]["verdict"]
        n += 1
        correct += int(np.argmax(pv[:2]) == VERDICTS.index(row["verdict"]))
        max_risk = max(float(res[rid][q][1]) for q in RISKS)
        score.append((row["verdict"] == "unsafe", float(pv[0]), max_risk, bool(row.get("critical"))))
    unsafe = [s for s in score if s[0]]
    safe = [s for s in score if not s[0]]
    blocked = lambda s, a=0.5, b=0.5: (s[1] < a) or (s[2] >= b)
    dang_blocked = sum(blocked(s) for s in unsafe) / max(len(unsafe), 1)
    ben_passed = sum(not blocked(s) for s in safe) / max(len(safe), 1)
    # benign passed when ≥98% of unsafe are blocked, sweeping the verdict threshold
    best = 0.0
    for a in np.linspace(0.05, 0.99, 95):
        if sum(blocked(s, a) for s in unsafe) / max(len(unsafe), 1) >= 0.98:
            best = max(best, sum(not blocked(s, a) for s in safe) / max(len(safe), 1))
    crit = [s for s in unsafe if s[3]]
    best_c = 0.0
    for a in np.linspace(0.05, 0.99, 95):
        if sum(blocked(s, a) for s in unsafe) / max(len(unsafe), 1) >= 0.98 and all(blocked(s, a) for s in crit):
            best_c = max(best_c, sum(not blocked(s, a) for s in safe) / max(len(safe), 1))
    model.train()
    return {"benign_passed@98dang_nocrit": best_c, "critical_blocked@rule": sum(blocked(s) for s in crit) / max(len(crit), 1), "verdict_ce": tot["verdict"][0] / max(tot["verdict"][1], 1), "noul_ce": tot["noul"][0] / max(tot["noul"][1], 1), "verdict_acc": correct / n,
            "dangerous_blocked@rule": dang_blocked, "benign_passed@rule": ben_passed, "benign_passed@98dang": best, "n": n}


def save(path):
    flat = {k: v for k, v in tree_flatten(model.trainable_parameters())}
    mx.save_safetensors(os.path.join(path, "adapter.safetensors"), flat)
    json.dump({"rank": args.rank, "alpha": args.alpha, "args": vars(args)}, open(os.path.join(path, "adapter_config.json"), "w"), indent=1)


if args.eval_only:
    r = evaluate(full=True)
    print(json.dumps(r))
    json.dump(r, open(os.path.join(args.out, "eval_full.json"), "w"), indent=1)
    raise SystemExit

# ---- kernel (eval) vs patched chunked scan (train) agreement ----
_b = [hold_items[i] for i in range(min(16, len(hold_items)))]
_ids, _lens, *_ = pad_batch(_b)
model.eval()
_a = forward_last(_ids, _lens)
model.train()
_c = forward_last(_ids, _lens)
mx.eval(_a, _c)
print("max |logit diff| eval-kernel vs train-chunked:", float(mx.abs(_a - _c).max()), "logit scale", float(mx.abs(_a).max()), flush=True)

# ---- train ----
def make_steps(items):
    """Each optimizer step = one verdict micro-batch + k noul micro-batches (mixed tasks per step)."""
    v_idx = [i for i, x in enumerate(items) if x[1]]
    n_idx = [i for i, x in enumerate(items) if not x[1]]
    vb = [[v_idx[j] for j in b] for b in make_batches([items[i] for i in v_idx], args.tok_budget)]
    nb = [[n_idx[j] for j in b] for b in make_batches([items[i] for i in n_idx], args.tok_budget)]
    k = max(1, round(len(nb) / max(len(vb), 1)))
    steps = []
    for si, v in enumerate(vb):
        steps.append([v] + nb[si * k : (si + 1) * k])
    return steps


est_items = build_items(train_rows, args.neg_per_row)
est_steps = make_steps(est_items)
total_steps = int(len(est_steps) * args.epochs)
if args.max_steps:
    total_steps = min(total_steps, args.max_steps)
print(f"items/epoch {len(est_items)} tokens/epoch {sum(len(x[0]) for x in est_items)} steps/epoch {len(est_steps)} total_steps {total_steps}", flush=True)
warm = max(10, int(0.05 * total_steps))
sched = lambda s: args.lr * min(1.0, (s + 1) / warm) * (0.1 + 0.9 * 0.5 * (1 + math.cos(math.pi * min(1.0, s / max(total_steps, 1)))))
opt = optim.AdamW(learning_rate=args.lr, weight_decay=0.0)
model.train()
loss_and_grad = nn.value_and_grad(model, loss_fn)

log = open(os.path.join(args.out, "log.jsonl"), "a")


def logline(d):
    log.write(json.dumps(d) + "\n")
    log.flush()
    print(json.dumps(d), flush=True)


t0 = time.time()
e0 = {"verdict_ce": 1.0, "noul_ce": 1.0} if args.skip_eval0 else evaluate()
logline({"step": 0, "eval": e0, "t": 0})
best_loss, best_step = (e0.get("benign_passed@98dang_nocrit", 0), -(e0["verdict_ce"] + e0["noul_ce"])), 0
save(os.path.join(args.out))  # step-0 adapter == base model
step, ep, seen_tokens = 0, 0, 0
ema_v = ema_n = None
stop = False
while not stop:
    items = est_items if ep == 0 else build_items(train_rows, args.neg_per_row)
    steps = est_steps if ep == 0 else make_steps(items)
    random.shuffle(steps)
    for micro in steps:
        opt.learning_rate = sched(step)
        acc, lv, ln = None, None, None
        for mb in micro:
            batch = [items[i] for i in mb]
            ids, lens, is_choice, target, weight = pad_batch(batch)
            loss, grads = loss_and_grad(model, ids, lens, is_choice, target, weight)
            mx.eval(loss, grads)
            seen_tokens += int(ids.shape[0] * ids.shape[1])
            if batch[0][1]:
                lv = float(loss)
            else:
                ln = float(loss) if ln is None else 0.5 * (ln + float(loss))
            scale = 1.0 / len(micro)
            grads = tree_map(lambda g: g * scale, grads)
            acc = grads if acc is None else tree_map(lambda a, g: a + g, acc, grads)
            mx.eval(acc)
        acc, _ = optim.clip_grad_norm(acc, 1.0)
        opt.update(model, acc)
        mx.eval(model.parameters(), opt.state)
        step += 1
        ema_v = lv if ema_v is None else 0.95 * ema_v + 0.05 * lv
        if ln is not None:
            ema_n = ln if ema_n is None else 0.95 * ema_n + 0.05 * ln
        if step % 10 == 0:
            logline({"step": step, "ep": ep, "verdict_loss_ema": round(ema_v, 4), "noul_loss_ema": round(ema_n or 0, 4), "lr": opt.learning_rate.item(), "tok_s": round(seen_tokens / (time.time() - t0)), "mem_gb": round(mx.get_peak_memory() / 1e9, 1), "t": round(time.time() - t0)})
        if step % args.eval_every == 0 or step == total_steps:
            ev = evaluate()
            logline({"step": step, "eval": ev, "t": round(time.time() - t0)})
            cur = (ev["benign_passed@98dang_nocrit"], -(ev["verdict_ce"] + ev["noul_ce"]))
            if cur > best_loss:
                best_loss, best_step = cur, step
                save(os.path.join(args.out))
                logline({"step": step, "saved_best": cur})
        if step >= total_steps or (time.time() - t0) / 60 > args.max_minutes:
            stop = True
            break
    ep += 1
if not args.no_final:
    logline({"final_full_eval": evaluate(full=True)})
logline({"done": True, "best_step": best_step, "best_holdout_loss": best_loss, "steps": step, "minutes": round((time.time() - t0) / 60, 1)})
