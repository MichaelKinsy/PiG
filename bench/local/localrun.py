# SPDX-License-Identifier: MIT
"""python3 bench/local/localrun.py --arms pig-0.5 pi-1.1.0 --model qwen2.5-0.5b-instruct-q4_k_m --turns 10 --out DIR

Installs the pinned llama.cpp server and model, serves them on 127.0.0.1, installs the pinned arms in a throwaway root and runs each arm through
the recording proxy for N turns, in `--rounds` interleaved rounds. Writes raw.json. A hosted model instead of the local one: --hosted-base-url URL
with the key in LOCAL_PACK_API_KEY (never written anywhere; the proxy's own upstream connection is not the harness's, so the harness is still audited).
"""
import argparse
import json
import os
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import localpack  # noqa: E402
import pack  # noqa: E402
from pigbench import arms as armlib, envinfo  # noqa: E402


def run_one(arm, ws, upstream, opts, **kwargs):
    if opts.task:
        return pack.run_task(arm, ws, upstream, opts.task, opts.attempts, context_window=opts.context, **kwargs)
    if opts.mode == "print" or (opts.mode == "auto" and arm.family not in ("pig", "pi")):
        return pack.run_print_arm(arm, ws, upstream, runs=max(3, opts.turns // 3), context_window=opts.context, **kwargs)
    return pack.run_arm(arm, ws, upstream, opts.turns, context_window=opts.context, **kwargs)


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--arms", nargs="+", default=["pig-0.5", "pi-1.1.0"])
    p.add_argument("--model", default="qwen2.5-0.5b-instruct-q4_k_m")
    p.add_argument("--turns", type=int, default=10)
    p.add_argument("--rounds", type=int, default=1)
    p.add_argument("--context", type=int, default=16384)
    p.add_argument("--threads", type=int, default=None)
    p.add_argument("--mode", choices=("auto", "rpc", "print"), default="auto", help="auto: RPC for Pi-family arms, one-shot prompts for the others")
    p.add_argument("--task", help="run the pinned task-completion cell (tasks/<id>) instead of the turn metrics")
    p.add_argument("--attempts", type=int, default=3)
    p.add_argument("--hosted-base-url")
    p.add_argument("--out", required=True)
    opts = p.parse_args(argv)
    os.makedirs(opts.out, exist_ok=True)
    selected = armlib.select(armlib.load(), opts.arms)
    result = {"kind": "local", "provisional": True, "options": {k: v for k, v in vars(opts).items() if k != "out"}, "rounds": []}
    with tempfile.TemporaryDirectory(prefix="pigpack-") as root, armlib.Workspace() as ws:
        ws.install_all(selected)
        result["env"] = envinfo.collect(selected, seed=0)
        runnable = [a for a in selected if a.binary]
        if opts.hosted_base_url:
            result["model"] = {"hosted": opts.hosted_base_url}
            for i in range(opts.rounds):  # rotated as in the local mode below
                order = runnable[i % len(runnable):] + runnable[:i % len(runnable)]
                result["rounds"].append([run_one(a, ws, opts.hosted_base_url, opts, api_key=os.environ["LOCAL_PACK_API_KEY"]) for a in order])
        else:
            binary = localpack.install_runtime(root)
            model = localpack.fetch_model(os.environ.get("PIGPACK_CACHE") or os.path.join(tempfile.gettempdir(), "pigpack-cache"), opts.model)
            result["model"] = dict(localpack.read(localpack.MODELS)["models"][opts.model], name=opts.model)
            result["runtime"] = dict(localpack.read(localpack.RUNTIMES)["runtimes"]["llama.cpp"], binary_version=localpack.server_version(binary))
            with localpack.Server(binary, model, context=opts.context, threads=opts.threads) as server:
                server.complete("warm up", max_tokens=2)  # load weights and allocate buffers before the first measured arm
                for i in range(opts.rounds):
                    order = runnable[i % len(runnable):] + runnable[:i % len(runnable)]
                    result["rounds"].append([run_one(a, ws, server.base_url, opts) for a in order])
                    with open(os.path.join(opts.out, "raw.partial.json"), "w") as f:
                        json.dump(result, f)
    result["summary"] = [[pack.summarize(o) if "turns" in o else o for o in rnd] for rnd in result["rounds"]]
    with open(os.path.join(opts.out, "raw.json"), "w") as f:
        json.dump(result, f, indent=1)
    print(json.dumps(result["summary"], indent=1))


if __name__ == "__main__":
    main()
