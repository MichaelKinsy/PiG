#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Binary size report for the PiG-vs-phi comparison (stdlib only; run on xd670).

  binsize.py BINARY                      ELF section table + file size
  binsize.py BINARY --json               same, JSON
  binsize.py --per-tag [--src DIR]       build baseline, each single strip tag, and all tags
                                         with release flags; print delta table (sequential,
                                         incremental in $GOCACHE, niced onto the build CPUs)
  binsize.py --attrib BINARY_UNSTRIPPED  aggregate `go tool nm -size` bytes by Go package
                                         (--top N, default 30)
  binsize.py --build-unstripped OUT      build the release-flag binary without -s -w

Environment expected: PATH has go, GOCACHE/GOMODCACHE set, GOTOOLCHAIN=go1.27.3.
"""
import argparse, collections, json, os, re, subprocess, sys

HOME = os.path.expanduser("~")
SECTIONS = [".text", ".rodata", ".gopclntab", ".go.type", ".go.func", ".typelink", ".itablink",
            ".noptrdata", ".data", ".bss", ".noptrbss"]
BUILD_CPUS = "64-95,160-191"
BUILD = "github.com/MichaelKinsy/PiG/coding/cli.Build=3ee123c6"


def sections(path):
    out = subprocess.run(["readelf", "-S", "-W", path], capture_output=True, text=True, check=True).stdout
    res = {}
    for line in out.splitlines():
        m = re.match(r"\s*\[\s*\d+\]\s+(\S+)\s+\S+\s+[0-9a-f]+\s+[0-9a-f]+\s+([0-9a-f]+)", line)
        if m:
            res[m.group(1)] = int(m.group(2), 16)
    res["file"] = os.path.getsize(path)
    return res


def print_sections(path, as_json):
    s = sections(path)
    if as_json:
        print(json.dumps(s, indent=1)); return
    print(f"| section | bytes | MiB |\n|---|---:|---:|")
    for k in SECTIONS:
        if k in s:
            print(f"| {k} | {s[k]} | {s[k]/2**20:.2f} |")
    other = {k: v for k, v in s.items() if k not in SECTIONS and k != "file"}
    print("(.typelink/.itablink are absent in go1.27 ELF output; type data is in .go.type)")
    print(f"| other sections (sum) | {sum(other.values())} | {sum(other.values())/2**20:.2f} |")
    print(f"| **file** | {s['file']} | {s['file']/2**20:.2f} |")
    print("\nsize -A:")
    print(subprocess.run(["size", "-A", path], capture_output=True, text=True).stdout)


def build(src, out, tags, strip=True):
    ld = ("-s -w " if strip else "") + "-X " + BUILD
    cmd = ["taskset", "-c", BUILD_CPUS, "nice", "-n", "10", "go", "build", "-buildvcs=false",
           "-trimpath", "-ldflags", ld]
    if tags:
        cmd += ["-tags", tags]
    cmd += ["-o", out, "./cmd/pig"]
    env = dict(os.environ, CGO_ENABLED="0")
    r = subprocess.run(cmd, cwd=src, env=env, capture_output=True, text=True)
    return r.returncode == 0, (r.stdout + r.stderr).strip()[-600:]


def all_tags(src):
    r = subprocess.run(["go", "run", "./coding/piglet/internal/genstripids", "-print-tags"],
                       cwd=src, capture_output=True, text=True, check=True)
    return [t for t in re.split(r"[,\s]+", r.stdout.strip()) if t]


def per_tag(src, outdir):
    os.makedirs(outdir, exist_ok=True)
    tags = all_tags(src)
    print(f"# {len(tags)} tags", flush=True)
    jobs = [("baseline", "")] + [(t, t) for t in tags] + [("ALL", ",".join(tags))]
    rows = {}
    for name, tg in jobs:
        out = os.path.join(outdir, "pig-" + name)
        ok, msg = build(src, out, tg)
        if ok:
            rows[name] = sections(out)
            print(f"built {name} {rows[name]['file']}", flush=True)
        else:
            rows[name] = None
            print(f"FAILED {name}: {msg}", flush=True)
    base = rows["baseline"]
    print("\n| tag | file bytes | Δ file | Δ .text | Δ .rodata | Δ .gopclntab |\n|---|---:|---:|---:|---:|---:|")
    for name, _ in jobs:
        r = rows[name]
        if r is None:
            print(f"| {name} | BUILD FAILED | | | | |"); continue
        d = lambda k: r.get(k, 0) - base.get(k, 0)
        print(f"| {name} | {r['file']} | {d('file'):+d} | {d('.text'):+d} | {d('.rodata'):+d} | {d('.gopclntab'):+d} |")
    json.dump(rows, open(os.path.join(outdir, "per-tag.json"), "w"), indent=1)


def pkg_of(sym):
    # Go symbol: import/path.Type.Method, import/path.(*T).M, import/path.init.func1, type:..., go:...
    if sym.startswith(("type:", "go:", "*", "func(", "struct {", "[", "map[", "chan ")) or "/" not in sym and "." not in sym:
        return "(compiler-synthesized: type descriptors, eq/hash funcs, string data)"
    # import path ends at the first '.' after the last '/'
    i = sym.rfind("/")
    j = sym.find(".", i + 1)
    if j < 0:
        return sym
    return sym[:j]


def group(pkg):
    if "awssdk" in pkg or pkg.startswith("github.com/aws"): return "github.com/aws (incl. third_party/awssdk)"
    if "third_party/" in pkg:
        if "wazero" in pkg: return "wazero"
        if "highlight" in pkg or "hljs" in pkg or "grammar" in pkg: return "highlight grammars"
        return "third_party/ (PiG vendored)"
    if "tetratelabs/wazero" in pkg: return "wazero"
    if pkg.startswith("go/types") or pkg.startswith("golang.org/x/tools") or "internal/typeparams" in pkg: return "go/types + x/tools"
    if pkg.startswith("golang.org/x/"): return "golang.org/x/* (excl. tools)"
    if pkg.startswith("github.com/MichaelKinsy/PiG"): return "PiG own code"
    if "." not in pkg.split("/")[0]: return "std"
    return "other third-party"


def attrib(binary, top, build_total=None):
    out = subprocess.run(["go", "tool", "nm", "-size", "-sort", "size", binary],
                         capture_output=True, text=True, check=True).stdout
    pk = collections.Counter(); gr = collections.Counter(); typ = collections.Counter()
    total = 0
    for line in out.splitlines():
        p = line.split(None, 3)
        if len(p) < 4 or not p[1].isdigit():
            continue
        size, kind, name = int(p[1]), p[2], p[3]
        typ[kind] += size
        if kind in "Bb_":  # bss: occupies no file bytes
            continue
        total += size
        pkg = pkg_of(name)
        pk[pkg] += size
        gr[group(pkg)] += size
    s = sections(binary)
    print(f"nm file-resident symbol bytes (T t R r D d; bss B/b excluded): {total}")
    print("by symbol class: " + ", ".join(f"{k}={v}" for k, v in typ.most_common()))
    res = sum(s.get(k, 0) for k in [".text", ".rodata", ".noptrdata", ".data"])
    print(f"file-resident sections .text+.rodata+.noptrdata+.data: {res}; nm covers {total} of them; "
          f"unattributed remainder (mostly embedded blobs in .rodata, string data): {res-total}")
    print(f".gopclntab {s.get('.gopclntab',0)} and .go.type {s.get('.go.type',0)} are not attributed by nm; "
          f".bss/.noptrbss ({s.get('.bss',0)+s.get('.noptrbss',0)} B) occupy no file bytes; bss by nm class B = {typ['B']+typ['b']}")
    print(f"\n| rank | package | bytes |\n|---:|---|---:|")
    for i, (k, v) in enumerate(pk.most_common(top), 1):
        print(f"| {i} | {k} | {v} |")
    print(f"\n| group | bytes | share of nm total |\n|---|---:|---:|")
    for k, v in gr.most_common():
        print(f"| {k} | {v} | {100*v/total:.1f}% |")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("binary", nargs="?")
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--per-tag", action="store_true")
    ap.add_argument("--src", default=os.path.join(HOME, "phi-cmp/binsize-src"))
    ap.add_argument("--out", default=os.path.join(HOME, "phi-cmp/binsize-out"))
    ap.add_argument("--attrib", action="store_true")
    ap.add_argument("--top", type=int, default=30)
    ap.add_argument("--build-unstripped", metavar="OUT")
    a = ap.parse_args()
    if a.per_tag:
        per_tag(a.src, a.out)
    elif a.build_unstripped:
        ok, msg = build(a.src, a.build_unstripped, "", strip=False)
        print("ok" if ok else "FAILED " + msg)
    elif a.attrib:
        attrib(a.binary, a.top)
    elif a.binary:
        print_sections(a.binary, a.json)
    else:
        ap.error("need BINARY or a mode")


if __name__ == "__main__":
    main()
