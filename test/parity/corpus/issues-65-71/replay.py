#!/usr/bin/env python3
"""Replay the locked issue packages through Pi and PiG print mode.

Requires a POSIX host, Node on PATH, npm, a built PiG and the locked Pi oracle.
The output directory owns all homes, package copies, logs and installed dependencies.
A failed install, timeout, load, print or inventory comparison fails the run.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time

CORPUS = Path(__file__).resolve().parent
REPO = CORPUS.parents[2]


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def environment(home):
    # Isolate agent configuration and remove API_KEY, TOKEN and proxy variables.
    env = {k: v for k, v in os.environ.items()
           if not k.startswith(("PIG_", "PI_"))
           and not any(word in k.upper() for word in ("TOKEN", "API_KEY", "PROXY"))}
    env.update(HOME=str(home), PIG_HOME=str(home / ".pig"),
               PIG_CODING_AGENT_DIR=str(home / "pig"),
               PI_CODING_AGENT_DIR=str(home / "pi"),
               PI_OFFLINE="1", PI_SKIP_VERSION_CHECK="1", PIG_TEST_FAUX="1")
    return env


def execute(command, cwd, env, output):
    start = time.monotonic()
    with output.with_suffix(".stdout").open("wb") as stdout, output.with_suffix(".stderr").open("wb") as stderr:
        process = subprocess.Popen(command, cwd=cwd, env=env, stdout=stdout,
                                   stderr=stderr, start_new_session=True)
        try:
            code = process.wait(timeout=90)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
            code = "timeout"
    return {"command": command, "exit": code, "seconds": round(time.monotonic() - start, 3),
            "stdout": output.with_suffix(".stdout").read_text(),
            "stderr": output.with_suffix(".stderr").read_text()}


def materialize(entry, output, installed):
    lock = CORPUS / "locks" / entry["lock"]
    target = installed / entry["lock"] if installed else output / "npm" / entry["lock"]
    if installed:
        if digest(target / "package-lock.json") != digest(lock / "package-lock.json"):
            raise RuntimeError(f"cached lock differs: {target}")
    else:
        target.mkdir(parents=True)
        for name in ("package.json", "package-lock.json"):
            shutil.copyfile(lock / name, target / name)
        result = execute(["npm", "ci", "--no-audit", "--no-fund"], target,
                         environment(target), target / "install")
        if result["exit"] != 0:
            raise RuntimeError(f"npm ci failed: {target}: {result}")
    manifest = json.loads((target / "node_modules" / entry["name"] / "package.json").read_text())
    if manifest["version"] != entry["version"]:
        raise RuntimeError(f"installed package version differs: {entry['name']}")
    locked = json.loads((lock / "package-lock.json").read_text())["packages"]["node_modules/" + entry["name"]]
    if locked["integrity"] != entry["integrity"]:
        raise RuntimeError(f"corpus integrity differs: {entry['name']}")
    return target


def probe(entry, source, binary, label, output, observer):
    target = output / "runs" / entry["lock"] / label
    target.mkdir(parents=True)
    home, cwd = target / "home", target / "cwd"
    home.mkdir()
    cwd.mkdir()
    env = environment(home)
    trace = target / "trace.json"
    env["CORPUS_TRACE"] = str(trace)
    # Copies are writable and private even if an extension writes beside itself.
    package = target / "package"
    shutil.copytree(source / "node_modules", package / "node_modules", symlinks=True)
    selected = package / "node_modules" / entry["name"]
    for agent in (home / "pig", home / "pi"):
        agent.mkdir()
        (agent / "settings.json").write_text(json.dumps({"packages": [str(selected)]}) + "\n")
    command = [str(binary), "--no-session", "--no-skills", "--no-prompt-templates",
               "--no-themes", "--model", "test-faux/faux-1"]
    if label == "pi":
        command += ["-e", str(REPO / "test/parity/testdata/test-faux-provider.ts")]
    # A package can select a command when its injected model context is outside the faux script. That row declares its expected stdout and explains the narrower probe in packages.json.
    command += ["-e", str(observer), "--print", entry.get("prompt", "What is 20+22?")]
    result = execute(command, cwd, env, target / "print")
    result["trace"] = json.loads(trace.read_text()) if trace.exists() else None
    (target / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    shutil.rmtree(package)
    return result


def comparable(result):
    return {key: result[key] for key in ("exit", "stdout", "stderr", "trace")}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pig", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--installed", type=Path, help="reuse these installs only if their locks match")
    parser.add_argument("--public", type=Path, help="also record public/main, without treating it as the oracle")
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    pi = REPO / "extensions/sdk-ts/node_modules/.bin/pi"
    version = subprocess.check_output([str(pi), "--version"], env=environment(output), text=True).strip()
    if version != "0.87.1":
        raise RuntimeError(f"expected Pi 0.87.1, found {version}")
    node = subprocess.check_output(["node", "--version"], env=environment(output), text=True).strip()
    observer = output / "observer.ts"
    observer.write_text('''import { writeFileSync } from "node:fs";
export default function(pi: any) {
  pi.on("session_start", () => {
    writeFileSync(process.env.CORPUS_TRACE!, JSON.stringify({
      commands: pi.getCommands().filter(x => x.source === "extension").map(x => x.name).sort(),
      tools: pi.getAllTools().map(x => x.name).sort(),
    }));
  });
}
''')
    report = {"pi": version, "node": node, "pigSha256": digest(args.pig),
              "corpusSha256": digest(CORPUS / "packages.json"), "results": []}
    for entry in json.loads((CORPUS / "packages.json").read_text()):
        source = materialize(entry, output, args.installed.resolve() if args.installed else None)
        oracle = probe(entry, source, pi, "pi", output, observer)
        candidate = probe(entry, source, args.pig.resolve(), "pig", output, observer)
        matches = (oracle["exit"] == 0 and oracle["stdout"] == entry.get("stdout", "42\n")
                   and oracle["stderr"] == "" and oracle["trace"] is not None
                   and comparable(candidate) == comparable(oracle))
        item = {"package": entry["name"], "version": entry["version"], "matches": matches,
                "pi": oracle, "pig": candidate}
        if args.public:
            item["public"] = probe(entry, source, args.public.resolve(), "public", output, observer)
        report["results"].append(item)
        (output / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        print(entry["name"], entry["version"], "PASS" if matches else "FAIL", flush=True)
    return 0 if all(item["matches"] for item in report["results"]) else 1


if __name__ == "__main__":
    raise SystemExit(main())
