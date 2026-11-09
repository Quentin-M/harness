# /// script
# requires-python = ">=3.12"
# dependencies = ["mlx-vlm @ git+https://github.com/Lazarus-931/mlx-vlm.git@597941d0d4eb900f1d066eba8d89abf474c0dd42"]
# ///
"""System One wire adapter over mlx-vlm's JEV decision models: the local judge and the bench adapter.

    uv run bench/judge/adapters/jev_systemone.py <model-dir> <port> [<adapter-dir>] [--model-id ID] [--idle-exit-secs N]

With <adapter-dir> (adapter.safetensors + adapter_config.json from bench/judge/finetune.md) the LoRA
weights are applied unmerged on top of the quantized model. Listens on 127.0.0.1 only once the model
is loaded, so GET /v1/models lists it as loaded and ready. POST /v1/systemone answers decisions.
--idle-exit-secs exits after that long without a decision request, freeing the GPU memory.
JEV takes noul questions without criteria and only 6-level scores, so noul criteria fold into the
instructions and other scores ask as a choice.
"""
import argparse
import json
import os
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import mlx.core as mx
import mlx.nn as nn
from mlx_vlm import load, predict

ap = argparse.ArgumentParser()
ap.add_argument("model_dir")
ap.add_argument("port", type=int)
ap.add_argument("adapter_dir", nargs="?")
ap.add_argument("--model-id", help="id served in /v1/models (default: the model dir's name)")
ap.add_argument("--idle-exit-secs", type=int, default=0, help="exit after this long idle; 0 never")
ARGS = ap.parse_args()
MODEL_DIR, PORT, ADAPTER_DIR = ARGS.model_dir, ARGS.port, ARGS.adapter_dir
MODEL_ID = ARGS.model_id or os.path.basename(os.path.normpath(MODEL_DIR))


class LoRALinear(nn.Module):
    def __init__(self, base, lora_a, lora_b, scale):
        super().__init__()
        self.base, self.lora_a, self.lora_b, self.scale = base, lora_a, lora_b, scale

    def __call__(self, x):
        y = self.base(x)
        z = (x.astype(mx.float32) @ self.lora_a) @ self.lora_b
        return y + (self.scale * z).astype(y.dtype)


def apply_adapter(model, directory):
    cfg = json.load(open(os.path.join(directory, "adapter_config.json")))
    scale = cfg["alpha"] / cfg["rank"]
    weights = mx.load(os.path.join(directory, "adapter.safetensors"))
    for key in sorted(k for k in weights if k.endswith(".lora_a")):
        path = key.removesuffix(".lora_a")
        parent = model
        *parents, leaf = path.split(".")
        for part in parents:
            parent = parent[int(part)] if isinstance(parent, list) else getattr(parent, part)
        base = parent[int(leaf)] if isinstance(parent, list) else getattr(parent, leaf)
        wrapped = LoRALinear(base, weights[key], weights[path + ".lora_b"], scale)
        if isinstance(parent, list):
            parent[int(leaf)] = wrapped
        else:
            setattr(parent, leaf, wrapped)
    mx.eval(model.parameters())


model, processor = load(MODEL_DIR)
if ADAPTER_DIR:
    apply_adapter(model, ADAPTER_DIR)
lock = threading.Lock()
last_use = time.monotonic()


def to_jev(questions):
    out, plan = {}, {}
    for name, q in questions.items():
        kind, crit, instr = q.get("type"), q.get("criteria"), q.get("instructions") or name
        if kind == "noul":
            if isinstance(crit, dict) and crit:
                instr += "".join(f"\n{k}: {v}" for k, v in crit.items())
            out[name] = {"type": "noul", "instructions": instr}
            plan[name] = ("noul", None)
        elif kind == "choice":
            out[name] = {"type": "choice", "instructions": instr, "criteria": crit}
            plan[name] = ("choice", None)
        elif kind == "score":
            levels = list(crit)
            if len(levels) == 6:
                out[name] = {"type": "score", "instructions": instr, "criteria": levels}
            else:
                out[name] = {"type": "choice", "instructions": instr + " Answer with the level number.",
                             "criteria": {str(i): l if isinstance(l, str) else json.dumps(l) for i, l in enumerate(levels)}}
            plan[name] = ("score", levels)
        else:
            raise ValueError(f"unsupported question type {kind!r}")
    return out, plan


def to_systemone(answers, plan):
    out = {}
    for name, (kind, levels) in plan.items():
        a = answers[name]
        if kind == "noul":
            out[name] = {"type": "noul", "noul": a["probability"]}
        elif kind == "choice":
            out[name] = {"type": "choice", "choice": a["value"], "probabilities": a["probabilities"],
                         "confidence": a["metadata"]["confidence"]}
        else:
            probs = a["probabilities"]
            out[name] = {"type": "score", "score": round(sum(int(k) * v for k, v in probs.items()), 4),
                         "legend": {str(i): l for i, l in enumerate(levels)}, "probabilities": probs,
                         "confidence": max(probs.values())}
    return out


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.rstrip("/") == "/v1/models":
            return self._send(200, {"object": "list", "data": [
                {"id": MODEL_ID, "object": "model", "capabilities": ["decisions"], "loaded": True, "state": "ready"}]})
        self._send(200, {"status": "ok"})

    def do_POST(self):
        if self.path.rstrip("/") not in ("/v1/systemone", "/v1/decisions"):
            return self._send(404, {"error": "not found"})
        try:
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            questions, plan = to_jev(body["questions"])
            global last_use
            with lock:
                res = predict(model, processor, body["state"], questions)
                last_use = time.monotonic()
            code, out = 200, {"model": body.get("model"), "answers": to_systemone(res["answers"], plan), "usage": res.get("usage", {})}
        except Exception as e:
            code, out = 400, {"error": str(e)}
        self._send(code, out)

    def _send(self, code, obj):
        data = json.dumps(obj).encode()
        try:
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def log_message(self, *a):
        pass


server = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
if ARGS.idle_exit_secs > 0:
    def watch():
        while True:
            time.sleep(min(60, ARGS.idle_exit_secs))
            if time.monotonic() - last_use > ARGS.idle_exit_secs:
                os._exit(0)
    threading.Thread(target=watch, daemon=True).start()
server.serve_forever()
