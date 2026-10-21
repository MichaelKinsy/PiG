#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Build and vet the root module and each workspace module with GOWORK=off against their go.mod requirements.

go.work makes every development build resolve nested PiG modules (extensions/sdk) to the working tree, so a root
package that uses an SDK symbol newer than the published version the root go.mod requires builds in CI and fails for
anyone who builds the module on its own: `go install github.com/MichaelKinsy/PiG/cmd/pig@<version>` resolves the SDK
through the required tag. make module-publication proves the tag exists; this script proves the module compiles
against that tag.

Default (release gate): exit 1 when any module fails to build or vet. `make module-build` runs it with --fresh-cache,
so the SDK comes from the module proxy and not from a developer's or runner's cache.
--report: print the missing symbols, write a job summary, and always exit 0. `make module-build-report` runs it on
every pull request, where main legitimately uses SDK API that its release has not tagged yet.
"""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile

# `path:line:col: message`, optionally behind go vet's `vet: ` prefix.
ERROR_LINE = re.compile(r"^(?:vet: )?(?P<file>[^\s:][^:]*\.go):(?P<line>\d+):(?:\d+:)? (?P<message>.+)$")
# How the compiler words an API the required module version lacks. Each pattern names the missing symbol.
MISSING = (
    (re.compile(r"^undefined: (?P<symbol>[\w.]+)$"), "{symbol}"),
    (re.compile(r"^(?P<expr>\S+) undefined \(type (?P<type>\S+) has no field or method (?P<name>\w+)"), "{type}.{name}"),
    (re.compile(r"^unknown field (?P<name>\w+) in struct literal of type (?P<type>\S+)"), "{type}.{name}"),
    (re.compile(r"^undefined: (?P<type>\w+)\.(?P<name>\w+)$"), "{type}.{name}"),
    (re.compile(r"^cannot use .* \(.*\) as (?P<type>\S+) value in .*unknown field (?P<name>\w+)"), "{type}.{name}"),
    (re.compile(r"^(?P<name>\w+) not declared by package (?P<type>\w+)$"), "{type}.{name}"),
    (re.compile(r"^too many arguments in call to (?P<symbol>\S+)$"), "{symbol} (more parameters than the required version has)"),
    (re.compile(r"^not enough arguments in call to (?P<symbol>\S+)$"), "{symbol} (fewer parameters than the required version has)"),
)


def go(args, cwd, env):
    return subprocess.run(["go", *args], cwd=cwd, env=env, text=True, capture_output=True)


def workspace_modules(root: Path, env):
    """The root module first, then every directory go.work uses."""
    modules = [root]
    work = root / "go.work"
    if work.is_file():
        result = go(["work", "edit", "-json"], root, dict(env, GOWORK=str(work)))
        if result.returncode:
            raise ValueError("go work edit -json failed:\n" + result.stdout + result.stderr)
        for use in json.loads(result.stdout).get("Use") or []:
            directory = (root / use["DiskPath"]).resolve()
            if directory != root and directory not in modules:
                modules.append(directory)
    return modules


def manifest(directory: Path, env):
    result = go(["mod", "edit", "-json"], directory, env)
    if result.returncode:
        raise ValueError(f"go mod edit -json failed in {directory}:\n{result.stdout}{result.stderr}")
    return json.loads(result.stdout)


def local_replacements(data):
    """Requirements this module overrides with a directory, which its own build cannot find unpublished."""
    return {r["Old"]["Path"]: r["New"]["Path"] for r in data.get("Replace") or [] if not r["New"].get("Version")}


def imports_of(path: Path, cache):
    if path not in cache:
        found = set()
        try:
            text = path.read_text(encoding="utf-8", errors="replace")
        except OSError:
            text = ""
        # Imports are quoted module-path-shaped strings; this is enough to name the nested module a file depends on.
        for match in re.finditer(r'^\s*(?:[\w.]+\s+)?"([\w.\-]+(?:/[\w.\-~%]+)+)"\s*$', text, re.M):
            found.add(match.group(1))
        cache[path] = found
    return cache[path]


def parse_errors(output: str, directory: Path, nested: list, cache):
    """Compile errors with the symbol the required module lacks and the nested module the file imports."""
    errors, seen = [], set()
    for line in output.splitlines():
        match = ERROR_LINE.match(line.strip())
        if not match:
            continue
        file = (directory / match["file"]).resolve()
        key = (str(file), match["line"], match["message"])
        if key in seen:
            continue
        seen.add(key)
        message, symbol = match["message"], None
        for pattern, template in MISSING:
            found = pattern.match(message)
            if found:
                symbol = template.format(**found.groupdict()).replace("*", "")
                break
        modules = sorted({n for n in nested if any(i == n or i.startswith(n + "/") for i in imports_of(file, cache))})
        try:
            shown = file.relative_to(directory).as_posix()
        except ValueError:
            shown = match["file"]
        errors.append({"file": shown, "line": int(match["line"]), "message": message, "symbol": symbol, "nested": modules})
    return errors


def check_module(directory: Path, root: Path, env, vet: bool, cache):
    data = manifest(directory, env)
    path = data["Module"]["Path"]
    # Requirements on another module of this repository, which go.work would otherwise resolve to the working tree.
    prefix = manifest(root, env)["Module"]["Path"] + "/"
    nested = sorted({r["Path"] for r in data.get("Require") or [] if r["Path"].startswith(prefix)})
    replaced = local_replacements(data)
    result = {"module": path, "dir": directory.relative_to(root).as_posix() or ".", "nested": nested,
              "replaced": sorted(set(nested) & set(replaced)), "failures": [], "errors": []}
    # -e lists every error, not the first ten, so the symbol list is complete.
    for name, args in (("build", ["build", "-gcflags=-e", "./..."]), ("vet", ["vet", "./..."])):
        if name == "vet" and not vet:
            continue
        run = go(args, directory, env)
        if run.returncode:
            output = run.stdout + run.stderr
            result["failures"].append({"step": name, "output": output.strip()})
            for error in parse_errors(output, directory, nested, cache):
                if error not in result["errors"]:
                    result["errors"].append(error)
    return result


def render(results, header):
    lines = [header, ""]
    symbols = {}
    for result in results:
        for error in result["errors"]:
            if error["symbol"]:
                symbols.setdefault((result["dir"], error["symbol"]), []).append(error)
    failed = [r for r in results if r["failures"]]
    for r in results:
        note = ""
        if r["replaced"]:
            note = " (requirement on " + ", ".join(r["replaced"]) + " is replaced by a local directory, so it builds against the working tree by construction)"
        state = "FAIL" if r["failures"] else "ok"
        lines.append(f"- `{r['dir']}` ({r['module']}): {state}{note}")
    if symbols:
        lines += ["", "Symbols the required published module versions lack:", ""]
        for (directory, symbol), where in sorted(symbols.items()):
            sites = ", ".join(sorted({f"{e['file']}:{e['line']}" for e in where}))
            modules = sorted({m for e in where for m in e["nested"]})
            via = f" [{', '.join(modules)}]" if modules else ""
            lines.append(f"- `{symbol}`{via} used at {sites}")
    for r in failed:
        others = [e for e in r["errors"] if not e["symbol"]]
        if others:
            lines += ["", f"Other compile errors in `{r['dir']}`:", ""]
            lines += [f"- {e['file']}:{e['line']}: {e['message']}" for e in others]
        unparsed = [f for f in r["failures"] if not r["errors"]]
        for f in unparsed:
            lines += ["", f"`go {f['step']}` failed in `{r['dir']}` without a compile error:", "", "```", f["output"][-4000:], "```"]
    return "\n".join(lines) + "\n", failed


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--root", type=Path, default=Path("."))
    parser.add_argument("--report", action="store_true", help="never fail; print the findings and append them to $GITHUB_STEP_SUMMARY")
    parser.add_argument("--fresh-cache", action="store_true", help="resolve every module from GOPROXY into an empty module cache")
    parser.add_argument("--no-vet", action="store_true", help="build only")
    args = parser.parse_args()
    root = args.root.resolve()
    env = dict(os.environ, GOWORK="off", GOFLAGS=os.environ.get("PIG_MODULE_BUILD_GOFLAGS", ""), GIT_TERMINAL_PROMPT="0")
    scratch = None
    try:
        if args.fresh_cache:
            scratch = tempfile.mkdtemp(prefix="pig-module-build-")
            env["GOMODCACHE"] = os.path.join(scratch, "cache")
            env["GOFLAGS"] = (env["GOFLAGS"] + " -modcacherw").strip()
        cache, results = {}, []
        for directory in workspace_modules(root, env):
            results.append(check_module(directory, root, env, not args.no_vet, cache))
    except (OSError, ValueError, KeyError) as error:
        if args.report:
            print(f"module-build-report: unavailable: {error}")
            return 0
        sys.exit(f"module-build: {error}")
    finally:
        if scratch:
            shutil.rmtree(scratch, ignore_errors=True)
    name = "module-build-report" if args.report else "module-build"
    header = f"### {name}: GOWORK=off build and vet against the published requirements"
    text, failed = render(results, header)
    print(text)
    if args.report:
        summary = os.environ.get("GITHUB_STEP_SUMMARY")
        if failed and summary:
            with open(summary, "a", encoding="utf-8") as out:
                out.write(text + "\n")
        if failed:
            print("::warning title=module-build-report::the root module uses API its required nested-module versions lack; see the job summary")
        return 0
    if failed:
        print(f"{name}: " + ", ".join(r["dir"] for r in failed) + " does not build with GOWORK=off; publish the nested modules this commit needs "
              "and require them, or remove the dependency on unpublished API", file=sys.stderr)
        return 1
    print(f"{name}: every module builds and vets with GOWORK=off")
    return 0


if __name__ == "__main__":
    sys.exit(main())
