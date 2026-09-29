# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Profile pig round trips against the mock model with PIG_PROFILE, then summarize with go tool pprof."""
import glob
import os
import shutil
import subprocess
import tempfile

from . import proc, registry
from .mockllm import MockServer
from .overhead import prepare


def run(args):
    harness = registry.load(args.registry)["pig"]
    binary = registry.resolve(harness, args.pig)
    if binary is None:
        print("profile: bin/pig not found; run make pig")
        return 1
    out = os.path.abspath(args.out)
    shutil.rmtree(out, ignore_errors=True)
    os.makedirs(out)
    work = tempfile.mkdtemp(prefix="pigeval-profile-")
    with MockServer() as server:
        env, cwd, values = prepare(harness, work, server.base_url)
        env["PIG_PROFILE"] = args.kinds
        env["PIG_PROFILE_DIR"] = out
        argv = [binary, *registry.substitute(dict(values, prompt=args.prompt), harness.print_args)]
        for i in range(args.runs):
            result = proc.run(argv, env, cwd, args.timeout)
            if result["code"] != 0:
                print(f"profile: run {i + 1} exited {result['code']}\n{result['stderr'][-800:]}")
                return 1
    shutil.rmtree(work, ignore_errors=True)
    files = sorted(glob.glob(os.path.join(out, "pig-*")))
    print(f"profile: {args.runs} run(s), {len(files)} file(s) in {out}")
    go = shutil.which("go")
    cpu = [f for f in files if f.endswith("-cpu.pprof")]
    if args.merge:
        if not (go and cpu):
            print("profile: --merge needs go on PATH and cpu in --kinds")
            return 1
        os.makedirs(os.path.dirname(os.path.abspath(args.merge)), exist_ok=True)
        with open(args.merge, "wb") as merged:
            subprocess.run([go, "tool", "pprof", "-proto", *cpu], stdout=merged, stderr=subprocess.DEVNULL, check=True)
        print(f"profile: merged {len(cpu)} CPU profiles into {args.merge}. For a profile-guided build, copy it to cmd/pig/default.pgo and compare make evals before and after.")
        return 0
    for path in files:
        if go and path.endswith(".pprof") and ("-cpu." in path or "-heap." in path or "-allocs." in path):
            index = [] if "-cpu." in path else ["-sample_index=alloc_space"]
            top = subprocess.run([go, "tool", "pprof", "-top", "-nodecount=12", *index, binary, path], capture_output=True, text=True)
            print(f"\n== {os.path.basename(path)}\n" + "\n".join(top.stdout.splitlines()[:18]))
    print("\nExplore: go tool pprof -http=: bin/pig <file>.pprof    Trace: go tool trace <file>.trace")
    return 0
