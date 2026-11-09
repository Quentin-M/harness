"""Shared locations for the judge fine-tune scripts.

BENCH  bench/judge, from this file's own location
WORK   $JUDGE_FT_WORK, else $HARNESS_STATE/judge/ft, else ~/.local/state/harness/judge/ft
MODEL  $JUDGE_FT_MODEL, else <WORK>/models/JEV-9B-MLX-NVFP4
"""
import os
from pathlib import Path

BENCH = Path(__file__).resolve().parents[1]
BATTERY = BENCH / "batteries" / "compact-v3.json"


def _work():
    if os.environ.get("JUDGE_FT_WORK"):
        return Path(os.environ["JUDGE_FT_WORK"]).expanduser()
    state = os.environ.get("HARNESS_STATE")
    base = Path(state).expanduser() if state else Path.home() / ".local" / "state" / "harness"
    return base / "judge" / "ft"


WORK = _work()
RAW = WORK / "raw"
DATA = WORK / "data"
RUNS = WORK / "runs"
MODEL = Path(os.environ["JUDGE_FT_MODEL"]).expanduser() if os.environ.get("JUDGE_FT_MODEL") else WORK / "models" / "JEV-9B-MLX-NVFP4"
