# SPDX-License-Identifier: MIT
"""Local-model eval pack: a pinned llama.cpp server and a pinned model in a throwaway root, served on 127.0.0.1.

runtimes.lock and models.lock pin every download by sha256 and size (models also by Hugging Face repo and revision). Nothing is installed globally:
the server and the model live under a temporary root (a verified model cache can be kept with PIGPACK_CACHE). The server speaks the OpenAI
chat-completions API on the loopback only. The pack's later stages (harness arms, metrics, report) build on `Server`.
"""
import json
import os
import platform
import shutil
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(HERE), "evals"))
from pigbench import lock as lockmod  # noqa: E402  (shared hashing helpers; bench/evals is the owner of the arms installer)

RUNTIMES = os.path.join(HERE, "runtimes.lock")
MODELS = os.path.join(HERE, "models.lock")


def read(path):
    with open(path) as f:
        return json.load(f)


def host_key():
    system, machine = {"Linux": "linux", "Darwin": "darwin"}.get(platform.system(), platform.system().lower()), {"x86_64": "amd64", "AMD64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine(), platform.machine())
    return f"{system}-{machine}"


def fetch_verified(url, dest, sha256, size):
    """Download url to dest unless dest already has the pinned sha256 and size; fail on a mismatch."""
    if os.path.exists(dest) and os.path.getsize(dest) == size and lockmod.sha256_file(dest) == sha256:
        return dest
    tmp = dest + ".part"
    with urllib.request.urlopen(url, timeout=600) as response, open(tmp, "wb") as out:  # noqa: S310 - https URL from the lock
        shutil.copyfileobj(response, out, 1 << 20)
    got = lockmod.sha256_file(tmp)
    if got != sha256 or os.path.getsize(tmp) != size:
        os.unlink(tmp)
        raise SystemExit(f"{url}: sha256 {got} (size mismatch or not), the lock pins {sha256}")
    os.replace(tmp, dest)
    return dest


def install_runtime(root, name="llama.cpp", key=None):
    """Install the pinned server for this host under root; returns the path of its executable."""
    spec = read(RUNTIMES)["runtimes"][name]
    key = key or host_key()
    asset = spec["assets"].get(key)
    if not asset:
        raise SystemExit(f"{name} {spec['version']} has no pinned asset for {key}")
    archive = fetch_verified(spec["release"]["base"] + asset["name"], os.path.join(root, asset["name"]), asset["sha256"], asset["size"])
    dest = os.path.join(root, f"{name}-{spec['version']}")
    os.makedirs(dest, exist_ok=True)
    with tarfile.open(archive) as tar:
        tar.extractall(dest, filter="data")
    binary = next((os.path.join(b, f) for b, _, fs in os.walk(dest) for f in fs if f == spec["release"]["member"]), None)
    if binary is None:
        raise SystemExit(f"{asset['name']} has no {spec['release']['member']}")
    os.chmod(binary, 0o755)
    return binary


def fetch_model(cache, name):
    """Download the pinned model into cache (kept between runs); returns its path."""
    spec = read(MODELS)["models"][name]
    os.makedirs(cache, exist_ok=True)
    url = f"https://huggingface.co/{spec['repo']}/resolve/{spec['revision']}/{spec['file']}"
    return fetch_verified(url, os.path.join(cache, f"{spec['revision'][:12]}-{spec['file']}"), spec["sha256"], spec["size"])


def server_version(binary):
    out = subprocess.run([binary, "--version"], capture_output=True, text=True, timeout=60)
    return (out.stdout + out.stderr).strip().splitlines()[:1]


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class Server:
    """llama-server on 127.0.0.1 for one model; a with-block starts it and stops it."""

    def __init__(self, binary, model_path, context=4096, threads=None, extra=()):
        self.binary, self.model_path, self.context, self.threads, self.extra = binary, model_path, context, threads, list(extra)
        self.port = free_port()
        self.base_url = f"http://127.0.0.1:{self.port}/v1"
        self.proc = None
        self.log = tempfile.TemporaryFile()

    def __enter__(self):
        argv = [self.binary, "--host", "127.0.0.1", "--port", str(self.port), "-m", self.model_path, "-c", str(self.context), "--no-webui", "--jinja", *self.extra]
        if self.threads:
            argv += ["-t", str(self.threads)]
        self.proc = subprocess.Popen(argv, stdout=self.log, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL)
        deadline = time.monotonic() + 180
        while time.monotonic() < deadline:
            if self.proc.poll() is not None:
                raise SystemExit(f"llama-server exited {self.proc.returncode}:\n{self.tail()}")
            try:
                with urllib.request.urlopen(f"http://127.0.0.1:{self.port}/health", timeout=2) as r:  # noqa: S310 - loopback
                    if r.status == 200:
                        return self
            except OSError:
                pass
            time.sleep(0.2)
        raise SystemExit(f"llama-server did not become healthy in 180 s:\n{self.tail()}")

    def __exit__(self, *_):
        if self.proc and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(15)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait()

    def tail(self):
        self.log.seek(0)
        return self.log.read()[-1500:].decode("utf8", "replace")

    def complete(self, prompt, max_tokens=16):
        body = json.dumps({"model": "local", "messages": [{"role": "user", "content": prompt}], "max_tokens": max_tokens, "temperature": 0, "stream": False}).encode()
        request = urllib.request.Request(self.base_url + "/chat/completions", body, {"Content-Type": "application/json", "Authorization": "Bearer none"})
        with urllib.request.urlopen(request, timeout=300) as response:  # noqa: S310 - loopback
            return json.load(response)

    def models(self):
        with urllib.request.urlopen(self.base_url + "/models", timeout=30) as response:  # noqa: S310 - loopback
            return json.load(response)


def dry_run(model="qwen2.5-0.5b-instruct-q4_k_m", cache=None):
    """Install the runtime, fetch the model, serve it, answer one completion; returns the facts a report needs."""
    cache = cache or os.environ.get("PIGPACK_CACHE") or os.path.join(tempfile.gettempdir(), "pigpack-cache")
    with tempfile.TemporaryDirectory(prefix="pigpack-") as root:
        binary = install_runtime(root)
        version = subprocess.run([binary, "--version"], capture_output=True, text=True, timeout=60)
        path = fetch_model(cache, model)
        with Server(binary, path, threads=os.cpu_count() and min(8, os.cpu_count())) as server:
            models = server.models()
            reply = server.complete("Say hi in one word.")
        return {"runtime": read(RUNTIMES)["runtimes"]["llama.cpp"]["version"], "server_version": (version.stdout + version.stderr).strip().splitlines()[:2], "model": model,
                "served_models": [m["id"] for m in models.get("data", [])], "reply": reply["choices"][0]["message"]["content"], "usage": reply.get("usage")}


if __name__ == "__main__":
    print(json.dumps(dry_run(), indent=1))
