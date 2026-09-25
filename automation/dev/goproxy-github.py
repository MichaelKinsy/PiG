#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Serve the GOPROXY protocol from GitHub source archives, verified against go.sum.

Use this only where proxy.golang.org is unreachable and github.com is reachable,
such as locked-down sandboxes. `automation/dev/setup.sh` starts it in that case.

Each module path maps to its GitHub repository. The server downloads the tagged
or pseudo-versioned tree, rebuilds the module zip with the golang.org/x/mod/zip
file rules, hashes it with dirhash.Hash1, and compares the hash with the go.sum
files it loaded. A mismatch is refused, so `go` receives no unverified content
for a module that go.sum lists. GOSUMDB=off is safe with this server only
because of that check.

    python3 automation/dev/goproxy-github.py --root . --daemon
    GOPROXY=http://127.0.0.1:8765 GOSUMDB=off go build ./cmd/pig

Only the Python standard library is required.
"""
import argparse
import base64
import hashlib
import io
import json
import os
import re
import signal
import subprocess
import sys
import tarfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# Module roots served from one fixed repository.
VANITY_REPO = {
    "google.golang.org/protobuf": "protocolbuffers/protobuf-go",
    "google.golang.org/grpc": "grpc/grpc-go",
    "google.golang.org/genproto": "googleapis/go-genproto",
    "google.golang.org/appengine": "golang/appengine",
    "cloud.google.com/go": "googleapis/google-cloud-go",
    "go.yaml.in/yaml": "yaml/go-yaml",
    "honnef.co/go/tools": "dominikh/go-tools",
    "modernc.org/sqlite": "modernc-org/sqlite",
}
# Prefixes whose next path element names a repository under a fixed owner.
VANITY_OWNER = {
    "golang.org/x/": "golang",
    "go.uber.org/": "uber-go",
    "mvdan.cc/": "mvdan",
    "charm.land/": "charmbracelet",
    "4d63.com/": "leighmcculloch",
    "go-simpler.org/": "go-simpler",
    "go.augendre.info/": "Crocmagnon",
    "github.com/": None,
}
PSEUDO = re.compile(r"^v\d+\.\d+\.\d+-(?:.*\.)?\d{14}-([0-9a-f]{12})$")
DEV_HOME = os.environ.get("PIG_DEV_HOME") or os.path.expanduser("~/.cache/pig-dev")
CACHE = os.path.join(DEV_HOME, "goproxy-github")
CACHE_LIMIT = 1 << 30  # Oldest archives beyond this total are deleted; the Go module cache keeps the built zips.
SUMS = {}
GUARD = threading.Lock()
ARCHIVE_LOCKS = {}
LOG_PATH = os.path.join(CACHE, "goproxy.log")


def log(line):
    with GUARD:
        os.makedirs(os.path.dirname(LOG_PATH), exist_ok=True)
        with open(LOG_PATH, "a") as f:
            f.write(line + "\n")


def unescape(path):
    path = urllib.parse.unquote(path)
    return re.sub(r"!([a-z])", lambda m: m.group(1).upper(), path)


def repo_for(module):
    """Return (GitHub owner/repo, module directory inside the repository) or None."""
    if module.startswith("gopkg.in/"):
        name = re.sub(r"\.v\d+$", "", module[len("gopkg.in/"):])
        return (name if "/" in name else f"go-{name}/{name}"), ""
    base = re.sub(r"/v\d+$", "", module)
    for root, repo in VANITY_REPO.items():
        if base == root or base.startswith(root + "/"):
            return repo, base[len(root):].lstrip("/")
    for prefix, owner in VANITY_OWNER.items():
        if base.startswith(prefix):
            parts = base[len(prefix):].split("/")
            if owner is None:
                owner, parts = parts[0], parts[1:]
            return f"{owner}/{parts[0]}", "/".join(parts[1:])
    return None


def fetch(url):
    request = urllib.request.Request(url, headers={"User-Agent": "pig-goproxy-github"})
    with urllib.request.urlopen(request, timeout=600) as response:
        return response.read()


def prune(keep):
    entries = [os.path.join(CACHE, n) for n in os.listdir(CACHE) if n.endswith(".tar.gz")]
    entries.sort(key=os.path.getmtime)
    total = sum(os.path.getsize(p) for p in entries)
    for path in entries:
        if total <= CACHE_LIMIT:
            break
        if path != keep:
            total -= os.path.getsize(path)
            os.remove(path)


def archive(repo, ref, prefix=""):
    """Return the files of repo at ref under prefix, plus every go.mod and the root LICENSE."""
    safe = re.sub(r"[^A-Za-z0-9._-]", "_", f"{repo}@{ref}")
    path = os.path.join(CACHE, safe + ".tar.gz")
    with GUARD:
        lock = ARCHIVE_LOCKS.setdefault(path, threading.Lock())
    with lock:  # One download per archive; different archives download in parallel.
        if not os.path.exists(path):
            os.makedirs(CACHE, exist_ok=True)
            kind = "" if re.fullmatch(r"[0-9a-f]{12,40}", ref) else "refs/tags/"
            data = fetch(f"https://github.com/{repo}/archive/{kind}{ref}.tar.gz")
            with open(path + ".tmp", "wb") as f:
                f.write(data)
            os.replace(path + ".tmp", path)
            with GUARD:
                prune(keep=path)
    files = {}
    with tarfile.open(path) as tar:
        for member in tar.getmembers():
            name = member.name.split("/", 1)[1] if "/" in member.name else ""
            if member.isfile() and name and (name.startswith(prefix) or name.endswith("go.mod") or name == "LICENSE"):
                files[name] = tar.extractfile(member).read()  # Filtering keeps monorepo submodules cheap.
    return files


def module_files(module, version):
    located = repo_for(module)
    if located is None:
        raise LookupError(f"no GitHub source for {module}")
    repo, subdir = located
    plain = version.removesuffix("+incompatible")
    pseudo = PSEUDO.match(plain)
    major = re.search(r"/(v\d+)$", module)
    ref = pseudo.group(1) if pseudo else (f"{subdir}/{plain}" if subdir else plain)
    files = archive(repo, ref, f"{subdir}/" if subdir else "")
    if major and not module.startswith("gopkg.in/"):
        candidate = f"{subdir}/{major.group(1)}".lstrip("/")
        if f"{candidate}/go.mod" in files and module_line(files[f"{candidate}/go.mod"]) == module:
            subdir = candidate
    prefix = f"{subdir}/" if subdir else ""
    nested = {n[: -len("/go.mod")] + "/" for n in files if n.endswith("/go.mod") and n.startswith(prefix) and n != f"{prefix}go.mod"}
    selected = {}
    for name, data in files.items():
        if not name.startswith(prefix):
            continue
        rel = name[len(prefix):]
        if any(name.startswith(n) for n in nested) or vendored(rel):
            continue
        selected[rel] = data
    if subdir and "LICENSE" not in selected and "LICENSE" in files:
        selected["LICENSE"] = files["LICENSE"]  # cmd/go copies the repository LICENSE into subdirectory modules.
    return selected


def raw_go_mod(module, version):
    """Return go.mod from raw.githubusercontent.com, or None when only the archive can decide."""
    located = repo_for(module)
    if located is None:
        raise LookupError(f"no GitHub source for {module}")
    repo, subdir = located
    plain = version.removesuffix("+incompatible")
    if PSEUDO.match(plain):
        return None
    ref = f"{subdir}/{plain}" if subdir else plain
    major = re.search(r"/(v\d+)$", module)
    candidates = []
    if major and not module.startswith("gopkg.in/"):
        candidates.append(f"{subdir}/{major.group(1)}/go.mod".lstrip("/"))
    candidates.append(f"{subdir}/go.mod".lstrip("/"))
    for path in candidates:
        try:
            data = fetch(f"https://raw.githubusercontent.com/{repo}/{ref}/{path}")
        except urllib.error.HTTPError as error:
            if error.code == 404:
                continue
            raise
        if module_line(data) == module:
            return data
    return None


def module_line(data):
    match = re.search(rb"^module\s+\"?([^\s\"]+)", data, re.M)
    return match.group(1).decode() if match else ""


def vendored(name):
    if name.startswith("vendor/"):
        index = len("vendor/")
    elif "/vendor/" in name:
        index = len("/vendor/")  # x/mod/zip keeps this historical offset for checksum stability.
    else:
        return False
    return "/" in name[index:]


def hash1(entries):
    summary = "".join(f"{hashlib.sha256(data).hexdigest()}  {name}\n" for name, data in sorted(entries.items()))
    return "h1:" + base64.b64encode(hashlib.sha256(summary.encode()).digest()).decode()


def load_sums(paths):
    for path in paths:
        with open(path) as f:
            for line in f:
                fields = line.split()
                if len(fields) == 3:
                    SUMS[(fields[0], fields[1])] = fields[2]


def verified(module, key, entries):
    expected = SUMS.get((module, key))
    actual = hash1(entries)
    if expected is None:
        log(f"unlisted {module} {key} {actual}")  # go -mod=readonly still refuses modules missing from go.sum.
        return True
    if expected != actual:
        log(f"MISMATCH {module} {key} expected {expected} got {actual}")
        return False
    return True


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        match = re.fullmatch(r"/(.+)/@v/(.+)\.(info|mod|zip)", self.path)
        if not match:
            return self.send_error(404)
        module, version, kind = unescape(match.group(1)), unescape(match.group(2)), match.group(3)
        try:
            body = self.render(module, version, kind)
        except Exception as error:  # The go command reports a 404 with the module path.
            log(f"miss {module}@{version}.{kind}: {error}")
            return self.send_error(404, str(error)[:200])
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def render(self, module, version, kind):
        if kind == "info":
            return json.dumps({"Version": version, "Time": "2026-01-01T00:00:00Z"}).encode()
        if kind == "mod":
            data = raw_go_mod(module, version)
            if data is None:
                data = module_files(module, version).get("go.mod", f"module {module}\n".encode())
            if not verified(module, f"{version}/go.mod", {"go.mod": data}):  # cmd/go hashes the bare name.
                raise ValueError("go.mod checksum mismatch")
            return data
        entries = {f"{module}@{version}/{name}": data for name, data in module_files(module, version).items()}
        if not verified(module, version, entries):
            raise ValueError("zip checksum mismatch")
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, "w", zipfile.ZIP_DEFLATED) as out:
            for name, data in sorted(entries.items()):
                out.writestr(name, data)
        return buffer.getvalue()

    def log_message(self, *_):
        pass


def answering(port):
    try:
        urllib.request.urlopen(f"http://127.0.0.1:{port}/probe/@v/v0.0.0.info", timeout=2).close()
    except urllib.error.HTTPError:
        return True
    except OSError:
        return False
    return True


def alive(port):
    """Report whether the server started by --daemon is still running, however busy it is."""
    try:
        os.kill(int(open(pid_path(port)).read()), 0)
    except (OSError, ValueError):
        return False
    return True


def pid_path(port):
    return os.path.join(DEV_HOME, f"goproxy-github-{port}.pid")


def stop(port):
    try:
        os.kill(int(open(pid_path(port)).read()), signal.SIGTERM)
        time.sleep(0.3)
    except (OSError, ValueError):
        pass
    try:
        os.remove(pid_path(port))
    except OSError:
        pass


def start_background(argv, port):
    os.makedirs(DEV_HOME, exist_ok=True)
    out_path = os.path.join(DEV_HOME, f"goproxy-github-{port}.out")
    with open(out_path, "ab") as out:
        child = subprocess.Popen([sys.executable, os.path.abspath(__file__), *argv], stdin=subprocess.DEVNULL, stdout=out, stderr=out, start_new_session=True)
    with open(pid_path(port), "w") as f:
        f.write(str(child.pid))
    for _ in range(100):
        if answering(port):
            return 0
        if child.poll() is not None:
            break
        time.sleep(0.1)
    print(f"goproxy-github: failed to start; see {out_path}", file=sys.stderr)
    return 1


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--port", type=int, default=int(os.environ.get("PIG_GOPROXY_PORT", "8765")))
    parser.add_argument("--sum", action="append", default=[], help="go.sum file whose hashes every response must match")
    parser.add_argument("--root", action="append", default=[], help="load every go.sum under this directory (node_modules excluded)")
    parser.add_argument("--daemon", action="store_true", help="(re)start in a new session and return once it answers")
    parser.add_argument("--ensure", action="store_true", help="like --daemon, but keep a server that already answers")
    parser.add_argument("--stop", action="store_true", help="stop the server started with --daemon")
    args = parser.parse_args()
    if args.stop:
        stop(args.port)
        return 0
    if args.ensure and (alive(args.port) or answering(args.port)):
        return 0  # Never restart a live server: a slow answer means busy, and a restart drops in-flight downloads.
    if args.daemon or args.ensure:
        stop(args.port)
        return start_background([a for a in sys.argv[1:] if a not in ("--daemon", "--ensure")], args.port)
    sums = list(args.sum)
    for root in args.root:
        for directory, subdirs, names in os.walk(root):
            subdirs[:] = [d for d in subdirs if d not in ("node_modules", ".git")]
            sums += [os.path.join(directory, n) for n in names if n == "go.sum"]
    load_sums(sums)
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    print(f"goproxy-github: 127.0.0.1:{args.port}, {len(SUMS)} go.sum entries from {len(sums)} files", file=sys.stderr, flush=True)
    server.serve_forever()
    return 0


if __name__ == "__main__":
    sys.exit(main())
