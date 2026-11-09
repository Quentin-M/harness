#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Download the pinned inputs into the work dir and verify sha256 (see paths.py).

    JUDGE_FT_WORK=... uv run fetch.py [--no-weights] [--no-adapter]

Inputs: the training data, the JEV base weights and the published v3 adapter
(memtalow/jev-9b-ft-v3, the one gates/jev-9b-ft-v3.json was tuned on). Everything lands in the work
dir, never in the repo. An adapter already in runs/v3 is kept whatever its hash: it is a local
`claude judge train` run, and replacing it is that command's `--force`.
"""
import argparse
import hashlib
import sys
import urllib.request

from paths import MODEL, RAW, RUNS

SHELLRISK = "437467862139b4e9cdd5322024ef3434a67c7ec8"
SHELLSAFETY = "fee89770c315d525ef2ee42adee6ef9725a7621e"
JEV = "1680ddb8dded11933558fa57f79a93bbdfcccd46"
ADAPTER = "938078f0b1561d16056118c918938e7ae36a8b45"
HF = "https://huggingface.co"

# (url, destination, sha256)
FILES = [
    (f"{HF}/datasets/kontext-security/ShellRisk-Bench/resolve/{SHELLRISK}/data/train-00000-of-00001.parquet", RAW / "shellrisk-train.parquet",
     "88b2bf56ffba81afe396cf2f1f27b3884dcdf76d588275c44cf9e9a6677fe4af"),
    (f"{HF}/datasets/tomngdev/shell-safety-v1.1/resolve/{SHELLSAFETY}/train.jsonl", RAW / "ss-train.jsonl",
     "698e5ba9cf4984d4fcd845f997e63a8a971ceea71a3b22dcf257379d6e0972ef"),
    (f"{HF}/datasets/tomngdev/shell-safety-v1.1/resolve/{SHELLSAFETY}/validation.jsonl", RAW / "ss-validation.jsonl",
     "ebb74c92b5b879d06af75d2cb456feb0f314c2ae270eb9aa79353c67f364e830"),
]
MODEL_FILES = {
    "README.md": "92f8148306a0ed486a829ee87f1c234b648c77873b577f7bd40f4aeb43144c4e",
    "chat_template.jinja": "a4aee8afcf2e0711942cf848899be66016f8d14a889ff9ede07bca099c28f715",
    "config.json": "2f2d77be04ad8dec1121406b4082226b4284fbcb211bead86a7f796b34bb30a8",
    "model-00001-of-00002.safetensors": "2a8bc5c9ba1279cebfef95b8425b4cdaaa15cfc4e604254448d7d11cc073632e",
    "model-00002-of-00002.safetensors": "4ebd243938167c7f6fb6960e349e8a62a312ff3d682489314661683d475e66f8",
    "model.safetensors.index.json": "b57ef248582c04e4f8d50882a07bb758d06fc153a0b34601d175275e31db1904",
    "tokenizer.json": "06b9509352d2af50381ab2247e083b80d32d5c0aba91c272ca9ff729b6a0e523",
    "tokenizer_config.json": "66e427c470fe580fe8c7b5725d857af23d8417e37fae62667ec698306a19987b",
}
FILES += [(f"{HF}/nativ-community/JEV-9B-MLX-NVFP4/resolve/{JEV}/{name}", MODEL / name, sha) for name, sha in MODEL_FILES.items()]
ADAPTER_FILES = {
    "adapter.safetensors": "26cf41b8e1d7df86c4aadcc477d2c7bed4082922e3ae0de657c6d8b5ad2f36d5",
    "adapter_config.json": "e00141db917bc51e13a1d7ae7260941b75e99a806b175db85e1d701596e62478",
}
ADAPTER_DIR = RUNS / "v3"
FILES += [(f"{HF}/memtalow/jev-9b-ft-v3/resolve/{ADAPTER}/{name}", ADAPTER_DIR / name, sha) for name, sha in ADAPTER_FILES.items()]


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 22), b""):
            h.update(chunk)
    return h.hexdigest()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--no-weights", action="store_true", help="skip the .safetensors files")
    ap.add_argument("--no-adapter", action="store_true", help="skip the published adapter (for a local training run)")
    args = ap.parse_args()
    failed = False
    weights = ADAPTER_DIR / "adapter.safetensors"
    # weights that differ from the pinned ones are a local `claude judge train` run: keep the whole
    # dir. Pinned weights are an earlier fetch, possibly interrupted, so its missing files are fetched.
    local = weights.exists() and sha256(weights) != ADAPTER_FILES[weights.name]
    for url, dest, want in FILES:
        if args.no_weights and dest.suffix == ".safetensors":
            continue
        if dest.parent == ADAPTER_DIR and (args.no_adapter or local):
            if local:
                print(f"local   {dest}")
            continue
        if dest.exists() and sha256(dest) == want:
            print(f"ok      {dest}")
            continue
        dest.parent.mkdir(parents=True, exist_ok=True)
        part = dest.with_name(dest.name + ".part")
        print(f"fetch   {url}")
        urllib.request.urlretrieve(url, part)
        got = sha256(part)
        if got != want:
            part.unlink()
            print(f"MISMATCH {dest}: got {got}, want {want}", file=sys.stderr)
            failed = True
            continue
        part.rename(dest)
        print(f"ok      {dest}")
    sys.exit(1 if failed else 0)


main()
