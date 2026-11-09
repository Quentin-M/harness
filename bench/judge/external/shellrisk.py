# /// script
# requires-python = ">=3.11"
# dependencies = ["pyarrow>=17"]
# ///
"""Convert ShellRisk-Bench's test split into bench/judge cases (local only, not committed).

ShellRisk-Bench (https://huggingface.co/datasets/kontext-security/ShellRisk-Bench) has no blanket
license, so its rows never enter the repo. Commands are inert text: nothing here runs them.

    uv run bench/judge/external/shellrisk.py   # writes bench/judge/external/corpus/shellrisk.jsonl
"""
import json
import pathlib
import random
import urllib.request

import pyarrow.parquet as pq

URL = "https://huggingface.co/datasets/kontext-security/ShellRisk-Bench/resolve/main/data/test-00000-of-00001.parquet"
BENIGN_SAMPLE = 400
SEED = 13

here = pathlib.Path(__file__).resolve().parent
cache = here / "cache" / "shellrisk-test.parquet"
out = here / "corpus" / "shellrisk.jsonl"
cache.parent.mkdir(parents=True, exist_ok=True)
out.parent.mkdir(parents=True, exist_ok=True)
if not cache.exists():
    urllib.request.urlretrieve(URL, cache)

rows = pq.read_table(cache).to_pylist()
risky = [r for r in rows if r["label"] == "risky"]
benign = [r for r in rows if r["label"] == "not_risky"]
random.Random(SEED).shuffle(benign)

with out.open("w") as f:
    for i, r in enumerate(risky + benign[:BENIGN_SAMPLE]):
        block = r["label"] == "risky"
        case = {
            "id": f"shellrisk-{i:04d}",
            "tool": "bash",
            "command": r["command"],
            "cwd": "/Users/dev/project",
            "label": "block" if block else "allow",
            "category": "shellrisk",
            "note": f"ShellRisk-Bench test, source {r['source']}, upstream {r['upstream_id']}",
            "source": "kontext-security/ShellRisk-Bench",
        }
        if block:
            case["severity"] = "high"
        f.write(json.dumps(case) + "\n")
print(f"wrote {out}: {len(risky)} block, {min(BENIGN_SAMPLE, len(benign))} allow")
