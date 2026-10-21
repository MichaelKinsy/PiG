#!/usr/bin/env python3
"""Count which Pi modules the npm `pi-package` extensions import.

Writes test/parity/extension-import-usage.json, the usage counts the SDK
export ledger (test/parity/sdk-exports.toml) uses to decide which gaps block a
release. Refresh it per upstream leap:

    automation/gen/sdk-import-usage.py --fetch --work /tmp/sdkusage

--fetch lists the top npm results for the `pi-package` keyword plus the named
Pigpen originals, packs each tarball into <work>/pk and unpacks it. Without
--fetch the script rescans an existing <work>/pk. A count is the number of
distinct packages with a value import (`import {x} from`, `import x, {y}`,
`const {x} = require/import()`) of the symbol; `import type` does not count.
The sample is npm search relevance, not downloads.
"""
import argparse
import collections
import concurrent.futures as cf
import datetime
import json
import os
import re
import subprocess
import sys

# Only the modules the loader serves from Pi's own bundle are counted.
MODULES = {
    "pi-coding-agent": "pi-coding-agent",
    "pi-tui": "pi-tui",
    "pi-ai": "pi-ai",
    "pi-ai/compat": "pi-ai/compat",
    "pi-agent-core": "pi-agent-core",
}
ORIGINALS = [
    "@tintinweb/pi-subagents", "@tintinweb/pi-tasks", "@juicesharp/rpiv-todo", "pi-warden",
    "@liyu1981/pi-tweaks", "pi-typesafe", "@dietrichgebert/ponytail", "@y0usaf/pi-jev", "pi-acp",
    "pi-ahp", "@gotgenes/pi-permission-system", "pi-goal-x", "@juicesharp/rpiv-ask-user-question",
    "pi-powerline-footer", "@juicesharp/rpiv-web-tools",
]

IMPORT = re.compile(r"import\s+(?:type\s+)?(?:([\w$]+)\s*,?\s*)?(?:\*\s+as\s+([\w$]+)|\{([^}]*)\})?\s*from\s*['\"]([^'\"]+)['\"]", re.S)
REQUIRE = re.compile(r"(?:const|let|var)\s*\{([^}]*)\}\s*=\s*(?:await\s+)?(?:require\(|import\()\s*['\"]([^'\"]+)['\"]", re.S)


def module(spec):
    m = re.sub(r"^@(mariozechner|earendil-works)/", "", spec)
    return MODULES.get(m)


def fetch(work):
    out = subprocess.run(["npm", "search", "keywords:pi-package", "--json", "--searchlimit=250"], capture_output=True, text=True, check=True)
    names = list(dict.fromkeys(ORIGINALS + [p["name"] for p in json.loads(out.stdout)]))

    def pack(name):
        dest = os.path.join(work, "pk", name.replace("/", "__").replace("@", ""))
        if os.path.exists(dest):
            return
        os.makedirs(dest)
        subprocess.run(["npm", "pack", name, "--pack-destination", dest, "--silent"], capture_output=True, text=True, timeout=120)
        for f in os.listdir(dest):
            if f.endswith(".tgz"):
                subprocess.run(["tar", "xzf", os.path.join(dest, f), "-C", dest], capture_output=True)

    with cf.ThreadPoolExecutor(12) as ex:
        list(ex.map(pack, names))


def scan(work):
    use = collections.defaultdict(set)
    importing = set()
    pk = os.path.join(work, "pk")
    for pkg in sorted(os.listdir(pk)):
        for root, dirs, files in os.walk(os.path.join(pk, pkg)):
            dirs[:] = [d for d in dirs if d != "node_modules"]
            for f in files:
                if not f.endswith((".ts", ".js", ".mjs", ".cjs", ".tsx")) or f.endswith(".d.ts"):
                    continue
                try:
                    src = open(os.path.join(root, f), errors="ignore").read()
                except OSError:
                    continue
                for m in IMPORT.finditer(src):
                    default, star, names, spec = m.groups()
                    mod = module(spec)
                    if not mod:
                        continue
                    importing.add(pkg)
                    if m.group(0).lstrip().startswith("import type"):
                        continue
                    for n in (names or "").split(","):
                        n = n.strip()
                        if not n or n.startswith("type "):
                            continue
                        use[(mod, n.split(" as ")[0].strip())].add(pkg)
                for m in REQUIRE.finditer(src):
                    names, spec = m.groups()
                    mod = module(spec)
                    if mod:
                        importing.add(pkg)
                        for n in names.split(","):
                            n = n.split(":")[0].strip()
                            if n:
                                use[(mod, n)].add(pkg)
    usage = collections.defaultdict(dict)
    for (mod, name), pkgs in sorted(use.items()):
        usage[mod][name] = len(pkgs)
    return {
        "scan": {
            "date": datetime.date.today().isoformat(),
            "source": "npm search keywords:pi-package (top 250) plus the named Pigpen originals; relevance order, not downloads",
            "packages": len(os.listdir(pk)),
            "importing": len(importing),
            "script": "automation/gen/sdk-import-usage.py",
        },
        "usage": usage,
    }


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--work", required=True, help="directory holding (or receiving) pk/<package> trees")
    ap.add_argument("--fetch", action="store_true")
    ap.add_argument("--out", default="test/parity/extension-import-usage.json")
    args = ap.parse_args()
    if args.fetch:
        fetch(args.work)
    if not os.path.isdir(os.path.join(args.work, "pk")):
        sys.exit("no %s/pk: run with --fetch" % args.work)
    result = scan(args.work)
    with open(args.out, "w") as f:
        json.dump(result, f, indent=1, sort_keys=True)
        f.write("\n")
    print("%d of %d packages import a Pi module; wrote %s" % (result["scan"]["importing"], result["scan"]["packages"], args.out))


main()
