#!/usr/bin/env python3
"""Record real terminal commands. Keep raw takes and unredacted diagnostics in scratch."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import time
import urllib.request

DEMO = Path(__file__).resolve().parent


def messages(path):
    return [row["message"] for line in path.read_text().splitlines()
            if (row := json.loads(line)).get("type") == "message"]


def text(message):
    content = message.get("content", [])
    if isinstance(content, str):
        return content
    return "".join(part.get("text", "") for part in content)


def chain_evidence(session, work, env):
    rows = messages(session)
    steps = [text(m).split("]", 1)[0] + "]" for m in rows
             if m.get("role") == "user" and text(m).startswith("[chain ")]
    expected = [f"[chain {i}/4 · {name}]" for i, name in enumerate(("scout", "plan", "build", "test"), 1)]
    if steps != expected:
        raise RuntimeError(f"Incomplete or reordered chain: {steps}")
    results = [m for m in rows if m.get("role") == "toolResult"]
    if [m["toolName"] for m in results] != ["bash", "read", "edit", "bash"]:
        raise RuntimeError("Expected actual scout/read/edit/test tool results")
    if any(m.get("isError") for m in results) or "\nOK\n" not in text(results[-1]):
        raise RuntimeError("Chain contains a failing tool or no passing unittest result")
    original = (work.parent / "original/stats.py").read_text()
    expected_source = original.replace("range(len(values) - window)", "range(len(values) - window + 1)")
    if (work / "stats.py").read_text() != expected_source:
        raise RuntimeError("On-disk repair is not the exact intended one-line change")
    subprocess.run(["python3", "-B", "-m", "unittest", "-v", "test_stats"], cwd=work, env=env, check=True)
    return {"chain": steps, "tools": [{"name": m["toolName"], "isError": m.get("isError"), "output": text(m)} for m in results]}


def record(args):
    root, scratch = args.root.resolve(), args.scratch.resolve()
    if root.parent != Path("/tmp") or not root.name.startswith("pig-launch-"):
        raise RuntimeError("Use only a stage.sh-created /tmp HOME")
    env = json.loads((root / "env.json").read_text())
    env["PATH"] = f"{args.tools.resolve()}:{env['PATH']}"
    env["VHS_NO_SANDBOX"] = "1"
    for name in ("vhs", "ttyd", "ffmpeg", "ffprobe", "google-chrome"):
        if not shutil.which(name, path=env["PATH"]):
            raise RuntimeError(f"BLOCKED: {name} is not installed. Do not download it.")
    scratch.mkdir(parents=True, exist_ok=True)
    step = args.step
    tape = scratch / f"run-{step}.tape"
    raw = scratch / f"raw-{step}.mp4"
    tape.write_text(f'Output "{raw}"\nSource tapes/{step}.tape\n')
    agent = ".pi" if step == "a" else ".pig"
    home = Path(env["HOME"])
    before = set(home.rglob("*.jsonl"))
    if step == "f":
        subprocess.run(["pig", "install", str(home / "chain-rs"), "--validate-only", "--json"],
                       env=env, cwd=home / "work", check=True, stdout=subprocess.DEVNULL)
        if (home / "work/.pig/extensions/doom-overlay").exists():
            raise RuntimeError("Use a fresh staged HOME for the reload take; Doom is already active")
    with (scratch / f"provider-{step}.log").open("w") as log:
        provider = subprocess.Popen(["python3", "-B", str(DEMO / "model/demo-model.py"),
                                     "--port", env["DEMO_PORT"], "--pace", "0.03"], env=env, stdout=log, stderr=log)
        try:
            for _ in range(100):
                if provider.poll() is not None:
                    raise RuntimeError("Local provider exited; inspect its log (port conflict or startup error)")
                try:
                    with urllib.request.urlopen(f"http://127.0.0.1:{env['DEMO_PORT']}/v1/models", timeout=0.1) as response:
                        if json.load(response)["data"][0]["id"] == "demo-1":
                            break
                except (OSError, ValueError):
                    time.sleep(0.05)
            else:
                raise RuntimeError("Local provider did not become ready")
            start = time.monotonic()
            with (scratch / f"vhs-{step}.log").open("w") as output:
                subprocess.run(["vhs", str(tape)], cwd=DEMO, env=env, stdout=output, stderr=output, check=True, timeout=240)
            elapsed = time.monotonic() - start
        finally:
            provider.terminate()
            try:
                provider.wait(timeout=5)
            except subprocess.TimeoutExpired:
                provider.kill()
                provider.wait()
    sessions = sorted(set(home.rglob("*.jsonl")) - before)
    evidence = {"step": step, "recording_wall_seconds": round(elapsed, 2), "faux_provider": True,
                "status": "BLOCKED" if step == "f" else "PASS"}
    if step == "f":
        evidence["blocker"] = "Rust chain and reload execute; Node overlay sizing crops Doom. See DEMO-BLOCKERS.md."
    if step in "abf":
        candidates = [p for p in sessions if f"/{agent}/agent/sessions/" in str(p)]
        if len(candidates) != 1:
            raise RuntimeError(f"Expected exactly one new {agent} session, got {len(candidates)}")
        evidence.update(chain_evidence(candidates[0], home / "work", env))
    if step == "c":
        evidence["binaries"] = {}
        for name in ("pig-go", "pig-demo"):
            path = home / "dist" / name
            evidence["binaries"][name] = {"sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "bytes": path.stat().st_size}
    if step == "d":
        copied = home / "clean/pig-demo"
        if copied.read_bytes() != (home / "dist/pig-demo").read_bytes():
            raise RuntimeError("Handoff binary changed")
        rows = [m for p in sessions if "/clean/" in str(p) for m in messages(p)]
        if not any(m.get("role") == "assistant" and "scripted faux provider" in text(m) for m in rows):
            raise RuntimeError("Clean HOME did not complete a faux-provider request")
        evidence["empty_path_execution"] = True
    if step == "e":
        state = json.loads((home / ".pig/state/pig-standard/pigrunner.json").read_text())
        if state.get("highScore", 0) <= 0:
            raise RuntimeError("Runner did not advance or persist a nonzero score")
        evidence["runner_high_score"] = state["highScore"]
    evidence["raw_sha256"] = hashlib.sha256(raw.read_bytes()).hexdigest()
    (scratch / f"evidence-{step}.json").write_text(json.dumps(evidence, indent=2, ensure_ascii=False) + "\n")
    print(f"Recorded {step}; raw take and checked evidence are in scratch. Review gameplay frames before accepting e/f.")


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--root", type=Path, required=True)
    ap.add_argument("--scratch", type=Path, required=True)
    ap.add_argument("--tools", type=Path, required=True)
    ap.add_argument("--step", choices=list("abcdef"), required=True)
    record(ap.parse_args())


if __name__ == "__main__":
    main()
