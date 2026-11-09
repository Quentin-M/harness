# /// script
# requires-python = ">=3.12"
# dependencies = ["pyarrow==26.0.1"]
# ///
"""Build {train,holdout,famhold}-v3.jsonl under <work>/data for the compact-v3 JEV judge fine-tune.

Training sources (disjoint from the eval corpus by normalized command):
  * ShellRisk-Bench TRAIN split (local only, never committed)
  * tomngdev/shell-safety-v1.1 train.jsonl (MIT), curated reasons only
  * synthetic rows written for this task (synth.py); the force-push and custom-registry families
    were chosen after seeing earlier models' misses, so `famhold` keeps spellings never trained on
Every row is then labelled under the bench policy (bench/judge/README.md#policy) by relabel().
Commands are inert text; nothing here executes them.

    JUDGE_FT_WORK=... uv run build_data.py [--seed 1]
"""
import argparse
import collections
import glob
import hashlib
import json
import random
import re

import pyarrow.parquet as pq

from paths import BENCH, DATA, RAW
from policy import R, detect
from synth import CWDS, RISKS, synth, synth_auth, synth_hard, synth_v3

ap = argparse.ArgumentParser()
ap.add_argument("--seed", type=int, default=1)
SEED = ap.parse_args().seed
rnd = random.Random(SEED)


def norm(c):
    c = " ".join(c.split())
    return re.sub(r"\s+#[^'\"]*$", "", c)


# ---- eval corpus (never train on it) ----
eval_exact, eval_norm = set(), set()
for f in glob.glob(f"{BENCH}/corpus/*.jsonl") + glob.glob(f"{BENCH}/external/corpus/*.jsonl"):
    for line in open(f):
        c = json.loads(line)["command"]
        eval_exact.add(c.strip())
        eval_norm.add(norm(c))
print("eval commands", len(eval_exact), "normalized", len(eval_norm))

removed = collections.Counter()
rows = []  # dict(command,cwd,userMessages,verdict,risks,tool,language,src,reason)


def keep(r, src):
    c = r["command"]
    if c.strip() in eval_exact:
        removed[f"{src}:exact"] += 1
        return False
    if norm(c) in eval_norm:
        removed[f"{src}:normalized"] += 1
        return False
    if len(c) > 1000 or c.count("\n") > 14 or not c.strip():
        removed[f"{src}:too_long"] += 1
        return False
    return True


def cwd_for(prefer_default):
    return "/Users/dev/project" if rnd.random() < prefer_default else rnd.choice(CWDS)


# ---- ShellRisk train ----
sr = pq.read_table(RAW / "shellrisk-train.parquet").to_pylist()
SCARY = re.compile(r"\brm\b|\bkill|\bsudo\b|\bchmod\b|\bchown\b|git (push|reset|clean|checkout|branch -D|rebase)|\bcurl\b|\bwget\b|base64|\beval\b|\|\s*(ba|z)?sh\b|\bdd\b|\bmkfs|\bpip3? install|\bnpm\b|\bdocker\b|\bssh\b|\bscp\b|\bnc\b|crontab|\bmv\b.*/|>\s*/|\bpkill|\bapt(-get)?\b|\bfind\b.*-(delete|exec)")
HARD = re.compile(r"(curl|wget)[^|;&]*\|\s*(sudo\s+)?(ba|z)?sh|rm\s+-[a-zA-Z]*r[a-zA-Z]*f?\s+(/|~|\$HOME)(\s|$|/\*)|base64\s+(-d|--decode)[^|]*\|\s*(ba)?sh|nc\s+-e|/dev/tcp/|dd\s+.*of=/dev/|mkfs\.|authorized_keys|--no-preserve-root|\bsudo\b")
risky = [r for r in sr if r["label"] == "risky"]
benign = [r for r in sr if r["label"] == "not_risky"]
# conflicting labels for the same command inside this split
lab = collections.defaultdict(set)
for r in sr:
    lab[norm(r["command"])].add(r["label"])
for r in risky:
    if keep(r, "shellrisk-risky") and len(lab[norm(r["command"])]) == 1:
        rows.append(dict(command=r["command"], cwd=cwd_for(0.65), userMessages=None, verdict="unsafe", risks={}, tool="bash", language=None,
                         src="shellrisk-risky:" + r["source"], reason=r["source"]))
rnd.shuffle(benign)
seen, scary_rows, plain_rows = set(), [], []
for r in benign:
    n = norm(r["command"])
    if n in seen or len(lab[n]) > 1:
        continue
    seen.add(n)
    if not keep(r, "shellrisk-benign"):
        continue
    if HARD.search(r["command"]):
        removed["shellrisk-benign:hard_pattern_suspect"] += 1
        continue
    (scary_rows if SCARY.search(r["command"]) else plain_rows).append(r)
N_SCARY, N_PLAIN = 1100, 1300
by_src = collections.defaultdict(list)
for r in plain_rows:
    by_src[r["source"]].append(r)
plain_pick = []
for s, lst in by_src.items():  # stratify over sources
    plain_pick += lst[: int(N_PLAIN * len(lst) / len(plain_rows))]
for r in scary_rows[:N_SCARY] + plain_pick:
    rows.append(dict(command=r["command"], cwd=cwd_for(0.65), userMessages=None, verdict="safe", risks={k: 0 for k in RISKS}, tool="bash", language=None,
                     src="shellrisk-benign:" + r["source"], reason="benign"))
print("shellrisk benign pools: scary", len(scary_rows), "plain", len(plain_rows))

# ---- shell-safety train (curated) ----
ALLOW_OK = {"agent_touched_delete", "chained_all_safe", "declared_dependency", "git_push_remote", "git_routine", "identity_query", "local_build", "local_read", "localhost_server"}
UNSAFE_RISK = {  # reason -> risks known true (others masked)
    "data_exfil": ["leaks_data"], "pipe_to_shell": ["runs_untrusted_code"], "download_then_run": ["runs_untrusted_code"],
    "persistence": ["escalates_or_persists"], "reverse_tunnel": ["escalates_or_persists"], "kernel_write": ["escalates_or_persists"],
    "chmod_worldwrite": ["escalates_or_persists"], "security_disarm": ["escalates_or_persists"], "mount_escape": ["escalates_or_persists"],
    "irreversible_delete": ["irreversible_loss"], "system_wipe": ["irreversible_loss"], "sudo_destructive": ["irreversible_loss", "escalates_or_persists"],
    "sudo_install": ["escalates_or_persists"], "sudo_config": ["escalates_or_persists"], "sudo_build": ["escalates_or_persists"],
    "sudo_log": ["escalates_or_persists"], "sysctl_tune": ["escalates_or_persists"], "iam_grant": ["escalates_or_persists"],
    "chained_unsafe_component": [], "chained_risky_component": [], "git_force": [], "git_delete_ref": [], "tree_wide_discard": [],
    "secret_to_stdout": [], "secret_store_write": [], "prod_ssh": [], "public_publish": [], "pr_force_merge": [], "self_approval": [],
    "ci_bypass": [], "terraform_apply": [], "pulumi_up": [], "helm_upgrade": [], "cluster_action": [], "aws_ec2_manage": [], "destructive_sql": ["irreversible_loss"],
    "block_device": ["irreversible_loss"], "audit_tamper": [], "nonloopback_bind": [], "repoint_registry": [], "self_modify": [],
}
ss = [json.loads(l) for l in open(RAW / "ss-train.jsonl")]
ss = [r for r in ss if r["shell"] == "posix"]
conf = collections.defaultdict(set)
for r in ss:
    conf[norm(r["command"])].add(r["label"] == "allow")
per_reason = collections.defaultdict(int)
CAP_ALLOW, CAP_UNSAFE = 150, 70
rnd.shuffle(ss)
seen = set()
for r in ss:
    n = norm(r["command"])
    if n in seen or len(conf[n]) > 1:
        continue
    reason, allow = r["reason"], r["label"] == "allow"
    if allow and reason not in ALLOW_OK:
        continue
    if not allow and reason not in UNSAFE_RISK:
        continue
    cap = CAP_ALLOW if allow else CAP_UNSAFE
    if per_reason[(allow, reason)] >= cap:
        continue
    if not keep(r, "shellsafety"):
        continue
    seen.add(n)
    per_reason[(allow, reason)] += 1
    um = [r["session_context"]["lastUserPrompt"]] if r["session_context"].get("lastUserPrompt") else None
    risks = {k: 0 for k in RISKS} if allow else {k: 1 for k in UNSAFE_RISK[reason]}
    rows.append(dict(command=r["command"], cwd=cwd_for(0.5), userMessages=um, verdict="safe" if allow else "unsafe", risks=risks, tool="bash", language=None,
                     src=f"shellsafety-{'allow' if allow else 'ask/deny'}", reason=reason, sev="critical" if r["label"] == "deny" else "high"))

# ---- synthetic ----
for r in synth(SEED + 6):
    if keep(r, "synthetic"):
        rows.append(dict(r, src="synthetic-" + ("safe" if r["verdict"] == "safe" else "unsafe"), reason="synthetic"))

for r in synth_auth(SEED + 10):
    if keep(r, "synthetic-auth"):
        rows.append(dict(r, src="synthetic-auth-" + ("safe" if r["verdict"] == "safe" else "unsafe"), reason="intent-contrast"))

for r in synth_hard(SEED + 20):
    if keep(r, "synthetic-hard"):
        rows.append(dict(r, src="synthetic-hard-" + ("safe" if r["verdict"] == "safe" else "unsafe"), reason="hard"))

fam_rows = []
tr3, fam3 = synth_v3(SEED + 30)
for r in tr3:
    if keep(r, "synthetic-v3"):
        rows.append(dict(r, src="synthetic-v3-" + ("safe" if r["verdict"] == "safe" else "unsafe"), reason="v3-family"))
for r in fam3:
    if keep(r, "synthetic-v3-fam"):
        fam_rows.append(dict(r, src="family-holdout-" + ("safe" if r["verdict"] == "safe" else "unsafe"), reason="family-holdout"))
# oversample the rarest risky sources (atomicredteam, payloads) x3 with new working directories
extra = []
for r in list(rows):
    if r["src"] in ("shellrisk-risky:atomicredteam", "shellrisk-risky:payloads"):
        for _ in range(2):
            extra.append(dict(r, cwd=cwd_for(0.5), src="shellrisk-oversample:" + r["reason"]))
rows += extra

# global dedupe on (norm command) with conflicting verdicts dropped
byc = collections.defaultdict(list)
final = []
for r in rows:
    if r["src"].startswith(("synthetic-auth", "synthetic-hard", "synthetic-v3", "shellrisk-oversample")):
        final.append(r)  # intent-contrast pairs share a command on purpose
    else:
        byc[norm(r["command"])].append(r)
for n, lst in byc.items():
    if len({x["verdict"] for x in lst}) > 1:
        removed["conflicting_verdict_across_sources"] += len(lst)
        continue
    final += lst[:2]
rows = final
rnd.shuffle(rows)
hold, train = [], []
for r in rows:
    h = int(hashlib.md5((norm(r["command"]) + str(SEED)).encode()).hexdigest(), 16) % 100
    (hold if h < 10 else train).append(r)


def state(r):
    s = {"tool": r["tool"], "workingDirectory": r["cwd"], "command": r["command"]}
    if r.get("language"):
        s["language"] = r["language"]
    if r.get("userMessages"):
        s["userMessages"] = r["userMessages"]
    return s



# ---- relabel under the bench policy (README Policy): labels and drops only, no new command text ----
OLD2NEW = {"leaks_data": "data_leaves_machine", "runs_untrusted_code": "untrusted_code", "manipulates_reviewer": "manipulates_reviewer"}
NEWR = ["data_leaves_machine", "untrusted_code", "attacker_tooling", "disk_scale_destruction", "manipulates_reviewer"]
CAT2R = {"exfil": "data_leaves_machine", "untrusted": "untrusted_code", "shell": "attacker_tooling", "malware": "attacker_tooling", "persist": "attacker_tooling",
         "evasion": "attacker_tooling", "cred": "attacker_tooling", "disk": "disk_scale_destruction", "reviewer": "manipulates_reviewer"}
CRIT = {"exfil", "shell", "malware", "untrusted", "disk"}
ALWAYS_UNSAFE = {"data_exfil", "pipe_to_shell", "reverse_tunnel", "repoint_registry", "audit_tamper", "block_device", "download_then_run"}
DTR_SAFE = R(r"^(npx|pipx run|pnpm (create|dlx)|deno run -A npm:|node node_modules/)(?!.*--proxy)|\|\s*tar\s+xz\b")
DEFAULT_REG = R(r"(registry\.npmjs\.org|pypi\.org|files\.pythonhosted\.org|proxy\.golang\.org|crates\.io|index\.crates\.io|rubygems\.org|registry\.yarnpkg\.com)")


def relabel(r):
    src, reason, cmd = r["src"], r["reason"], r["state"]["command"]
    old = r["risks"]
    if src.startswith(("shellrisk-risky", "shellrisk-oversample")):
        return "unsafe", {}, False, "keep-shellrisk-risky"
    if src.startswith(("shellrisk-benign", "shellsafety-allow")):
        return "safe", {k: 0 for k in NEWR}, False, "keep-safe"
    cats = detect(cmd)
    old_unsafe = r["verdict"] == "unsafe"
    if not old_unsafe:  # synthetic safe rows: only custom registries flip
        if "untrusted" in cats and re.search(r"registry|index|mirror|source add|npmRegistryServer|--index", cmd, re.I) and not DEFAULT_REG.search(cmd):
            return "unsafe", {"untrusted_code": 1}, False, "safe->unsafe:custom-registry"
        return "safe", {k: 0 for k in NEWR}, False, "keep-safe"
    keep_unsafe = bool(cats) or any(old.get(k) == 1 for k in OLD2NEW)
    if reason in ALWAYS_UNSAFE and src == "shellsafety-ask/deny":
        keep_unsafe = True
        if reason == "download_then_run" and DTR_SAFE.search(cmd) and not cats:
            keep_unsafe = False
    if not keep_unsafe:
        return "safe", {k: 0 for k in NEWR}, False, "unsafe->safe"
    risks = {}
    for c in sorted(cats):  # sorted: set order would vary with the hash seed
        if c != "host":
            risks[CAT2R[c]] = 1
    for o, n in OLD2NEW.items():
        if old.get(o) == 1:
            risks[n] = 1
    if old.get("irreversible_loss") == 1 and "disk_scale_destruction" not in risks:
        risks["disk_scale_destruction"] = 0
    crit = bool(cats & CRIT) or reason in ("data_exfil", "pipe_to_shell", "reverse_tunnel", "block_device") or (old.get("leaks_data") == 1)
    return "unsafe", risks, crit, "keep-unsafe"


DATA.mkdir(parents=True, exist_ok=True)
table = collections.Counter()
for name, lst in (("train", train), ("holdout", hold), ("famhold", fam_rows)):
    n_out = collections.Counter()
    with open(DATA / f"{name}-v3.jsonl", "w") as f:
        for i, r in enumerate(lst):
            row = {"id": f"{name}-{i}", "src": r["src"], "reason": r["reason"], "state": state(r), "verdict": r["verdict"], "risks": r["risks"],
                   "critical": bool(r["verdict"] == "unsafe" and (r.get("sev") == "critical" or r["src"].startswith("synthetic")))}
            v, risks, crit, why = relabel(row)
            table[(row["src"].split(":")[0], row["reason"], row["verdict"], v)] += 1
            n_out[v] += 1
            f.write(json.dumps(dict(row, verdict=v, risks=risks, critical=crit), ensure_ascii=False) + "\n")
    print(name, len(lst), dict(n_out))
json.dump({"|".join(k): c for k, c in sorted(table.items())}, open(DATA / "relabel-table.json", "w"), indent=1)
print("sources", collections.Counter(r["src"] for r in rows))
print("removed", dict(removed))
