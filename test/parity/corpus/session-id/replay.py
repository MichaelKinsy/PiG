#!/usr/bin/env python3
"""Replay #83 with the corpus's locked pi-warden and no live model.

Use a fresh output directory. Every run owns its HOME, cwd and package copy.
The wrapper invokes the real warden_loops definition from a print command;
the hermetic parity scenario separately exercises host-dispatched tools.
"""

import argparse
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("corpus", HERE.parent / "issues-65-71/replay.py")
corpus = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(corpus)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pig", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    pi = corpus.REPO / "extensions/sdk-ts/node_modules/.bin/pi"
    version = subprocess.check_output([str(pi), "--version"], env=corpus.environment(output), text=True).strip()
    if version != "0.87.1":
        raise RuntimeError(f"expected Pi 0.87.1, got {version}")
    entry = next(row for row in json.loads((corpus.CORPUS / "packages.json").read_text()) if row["name"] == "pi-warden")
    source = corpus.materialize(entry, output, None)
    results = []
    for no_session in (False, True):
        pair = []
        for name, binary in (("pi", pi), ("pig", args.pig.resolve())):
            target = output / f"{name}-{'memory' if no_session else 'persisted'}"
            target.mkdir()
            home, cwd = target / "home", target / "cwd"
            home.mkdir()
            cwd.mkdir()
            shutil.copytree(source / "node_modules", target / "node_modules", symlinks=True)
            env = corpus.environment(home)
            report = target / "loops.json"
            env.update(SESSION_ID_REPORT=str(report))
            wrapper = target / "warden-session-id.ts"
            shutil.copyfile(HERE / wrapper.name, wrapper)
            command = [str(binary), "--no-extensions", "--no-skills", "--no-prompt-templates", "--model", "test-faux/faux-1", "-e", str(wrapper)]
            if name == "pi":
                command += ["-e", str(corpus.REPO / "test/parity/testdata/test-faux-provider.ts")]
            if no_session:
                command += ["--no-session"]
            command += ["--print", "/warden-session-id"]
            result = corpus.execute(command, cwd, env, target / "run")
            result["loops"] = json.loads(report.read_text()) if report.exists() else None
            pair.append(result)
        matches = (pair[0]["exit"] == pair[1]["exit"] == 0
                   and pair[0]["loops"] is not None
                   and pair[0]["loops"] == pair[1]["loops"]
                   and "trial check" in json.dumps(pair[0]["loops"])
                   and "not available" not in json.dumps(pair[0]["loops"])
                   and pair[0]["stderr"] == pair[1]["stderr"] == ""
                   and pair[0]["stdout"] == pair[1]["stdout"] == "")
        results.append({"noSession": no_session, "matches": matches, "pi": pair[0], "pig": pair[1]})
        print("--no-session" if no_session else "persisted", "PASS" if matches else "FAIL", flush=True)
    (output / "report.json").write_text(json.dumps({"package": entry, "pigSha256": corpus.digest(args.pig), "results": results}, indent=2) + "\n")
    return 0 if all(row["matches"] for row in results) else 1


if __name__ == "__main__":
    raise SystemExit(main())
