#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Compile a listed set of TypeScript extensions into the aot host, before any measurement (the compiled-in, build-time shape).
The set is the extension files, at their absolute paths (a set's identity is its members' names and entry paths, so run it from the same paths it was built from).
Steps, over a runtime slot that holds stock Bun (`<slot>/bun/{bun,runtime.json}`):
  1. the Piglet's worker installs the aot manifest and downloads and verifies the compiler (PIG_AOT_RELEASE_URL mirrors the release);
  2. one launch of the set records its build request (it runs on Bun meanwhile);
  3. the worker builds the request: one executable with the set's extension factories compiled in;
  4. one launch checks that the extension host process is the compiled executable, and exits 1 if not.
What runs afterwards: `PIG_TS_RUNTIME=aot PIG_TS_RUNTIMES_DIR=<slot> pig ... -e <the same files>` starts <slot>/aot/images/<key>-<id>/pig-ts-host with the set compiled in; no slot
Bun or Node starts the extensions and no extension source is loaded or transpiled (the files are only checked for change).
With --binary BIN the set is the TypeScript extensions built into a Piglet Binary that also carries the aot-runtime extension (and any Go-fused members): the Binary
starts with no -e, its own fused aot-runtime worker installs the manifest and compiler and builds the recorded set while a session runs (no go test worker), and
the set's entry paths are the Binary's extracted cell sources under --home (PIG_HOME), so measure with the same --home and the same slot. The compiler downloads
from the network (or PIG_AOT_RELEASE_URL), so PIG_OFFLINE must not be set. The image appears at the third start of a clean slot: start 1 installs the manifest,
start 2 records the request (the set runs on Bun), the worker builds, start 3 runs the compiled host.
usage: build_set.py --pig PIG --slot SLOT [--piglet-dir piglets/aot] EXTENSION.ts...
       build_set.py --binary BIN --slot SLOT --home DIR"""
import argparse, os, subprocess, sys, tempfile, shutil
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import bench

def launch(pig, slot, exts, home, binary=False, hold=None):
    env = {"PATH": os.environ["PATH"], "HOME": home, "PIG_HOME": f"{home}/.pig", "PIG_CODING_AGENT_DIR": f"{home}/agent", "PIG_TELEMETRY": "0",
           "PIG_SKIP_VERSION_CHECK": "1", "TERM": "dumb", "TMPDIR": f"{home}/tmp", "PIG_TS_RUNTIME": "aot", "PIG_TS_RUNTIMES_DIR": slot}
    if not binary: env["PIG_OFFLINE"] = "1"
    for k in ("PIG_AOT_RELEASE_URL", "PIG_AOT_VERBOSE"):
        if k in os.environ: env[k] = os.environ[k]
    os.makedirs(env["TMPDIR"], exist_ok=True); os.makedirs(env["PIG_CODING_AGENT_DIR"], exist_ok=True)
    argv = [pig, "--mode", "rpc", "--no-session"] + ([] if binary else ["--offline", "-ne"])
    for e in exts: argv += ["-e", e]
    rpc = bench.Rpc(argv, env, home)
    try:
        rid = rpc.send({"type": "get_commands"}); rpc.until(lambda e: e.get("type") == "response" and e.get("id") == rid)
        import time; time.sleep(0.5)
        end = time.time() + (hold[1] if hold else 0)
        while hold and time.time() < end and not hold[0](): time.sleep(3)
        return bench.snapshot(rpc.p.pid)["ext_exes"]
    finally:
        rpc.close()

def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--pig"); ap.add_argument("--slot", required=True)
    ap.add_argument("--piglet-dir", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "piglets", "aot")); ap.add_argument("exts", nargs="*")
    ap.add_argument("--binary", help="a Piglet Binary that carries the TypeScript extensions and the aot-runtime extension"); ap.add_argument("--home", help="PIG_HOME root for --binary; keep it for the measurement")
    a = ap.parse_args(); slot = os.path.abspath(a.slot)
    if a.binary:
        if not a.home: sys.exit("--binary needs --home")
        if os.environ.get("PIG_OFFLINE"): sys.exit("PIG_OFFLINE is set: the compiler cannot download")
        binary, home = os.path.abspath(a.binary), os.path.abspath(a.home)
        for d in ("", "agent", "tmp"): os.makedirs(os.path.join(home, d), exist_ok=True)
        has = lambda path: (lambda: os.path.exists(os.path.join(slot, path)))
        def images(): d = os.path.join(slot, "aot", "images"); return os.path.isdir(d) and bool(os.listdir(d))
        launch(binary, slot, [], home, True, (has("aot/runtime.json"), 240)); print("start 1: the aot-runtime extension installed the manifest:", has("aot/runtime.json")(), flush=True)
        print("start 2 (records the request) ran on", launch(binary, slot, [], home, True), flush=True)
        launch(binary, slot, [], home, True, (images, 900)); print("worker built:", images(), flush=True)
        ran = launch(binary, slot, [], home, True); print("start 3 ran on", ran, flush=True)
        if "pig-ts-host" not in ran: sys.exit("the set did not run on the compiled host")
        print("built:", os.listdir(os.path.join(slot, "aot", "images"))); return
    if not a.pig or not a.exts: sys.exit("give --pig and the extension files, or --binary")
    exts = [os.path.abspath(e) for e in a.exts]; pig = os.path.abspath(a.pig)
    worker = f"cd {os.path.abspath(a.piglet_dir)} && GOWORK=off PIG_KEEP_COUNT1=1 PIG_AOT_BUILD_SLOT={slot} go test ./extensions/aotruntime -run '^TestBuildSlotRequests$' -count=1"
    home = tempfile.mkdtemp(prefix="aot-build-set-", dir=os.environ.get("TMPDIR", "/tmp"))
    try:
        subprocess.run(worker, shell=True, check=True)
        print("recording launch ran on", launch(pig, slot, exts, home), flush=True)
        subprocess.run(worker, shell=True, check=True)
        ran = launch(pig, slot, exts, home); print("compiled launch ran on", ran, flush=True)
    finally:
        shutil.rmtree(home, ignore_errors=True)
    if "pig-ts-host" not in ran: sys.exit("the set did not run on the compiled host")
    print("built:", [d for d in os.listdir(os.path.join(slot, "aot", "images"))])

if __name__ == "__main__":
    main()
