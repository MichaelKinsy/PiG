#!/usr/bin/env python3
"""Prepare disposable Linux demo files. Never install a tool or mutate a real HOME."""
import argparse
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile

DEMO = Path(__file__).resolve().parent
SOURCE = DEMO.parents[1]


def tool(name):
    if name in ("cargo", "rustc"):
        return subprocess.check_output(["rustup", "which", name], text=True).strip()
    if shutil.which("mise"):
        result = subprocess.run(["mise", "which", name], capture_output=True, text=True,
                                env={**os.environ, "MISE_OFFLINE": "1"})
        if result.returncode == 0:
            return result.stdout.strip()
    path = shutil.which(name)
    if not path:
        raise RuntimeError(f"Required tool is unavailable: {name}")
    return path


def put_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2) + "\n")


def configure_home(home, port):
    for kind in (".pi", ".pig"):
        agent = home / kind / "agent"
        put_json(agent / "models.json", {"providers": {"demo": {
            "baseUrl": f"http://127.0.0.1:{port}/v1", "api": "openai-completions", "apiKey": "demo-no-key",
            "compat": {"supportsDeveloperRole": False, "supportsStore": False, "supportsReasoningEffort": False},
            "models": [{"id": "demo-1", "name": "Scripted local demo", "reasoning": False, "input": ["text"],
                        "contextWindow": 128000, "maxTokens": 4096,
                        "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}],
        }}})
        put_json(agent / "settings.json", {"defaultProvider": "demo", "defaultModel": "demo-1",
                                          "theme": "dark", "quietStartup": True,
                                          "enableInstallTelemetry": False})
    # Choose original pink artwork explicitly, not the HPE-named default variant.
    put_json(home / ".pig/state/pig-standard/login.json", {"variant": "pink"})


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--pig", type=Path, required=True, help="PiG built from this checkout")
    ap.add_argument("--assets", type=Path, required=True, help="staged doom-overlay directory (local WAD + WASM)")
    ap.add_argument("--port", type=int, default=17878)
    ap.add_argument("--cache", type=Path, required=True, help="writable lane scratch cache")
    args = ap.parse_args()
    if not args.pig.resolve().is_file():
        ap.error("--pig must name the already-built executable")
    if not 1024 <= args.port <= 65535:
        ap.error("--port must be an unprivileged TCP port")
    for name in ("doom1.wad", "doom/build/doom.js", "doom/build/doom.wasm"):
        if not (args.assets / name).is_file():
            ap.error(f"missing local Doom asset: {name}; no download fallback")
    tools = {name: tool(name) for name in ("node", "go", "cargo", "rustc", "python3", "rg", "fd")}
    try:
        tools["pi"] = tool("pi")
    except RuntimeError:
        pass  # The runbook records step a as blocked when Pi is absent.
    root = Path(tempfile.mkdtemp(prefix="pig-launch-", dir="/tmp"))
    home = root / "home"
    home.mkdir()
    configure_home(home, args.port)
    work = home / "work"
    shutil.copytree(SOURCE / "evals/tasks/fix-off-by-one/files", work)
    shutil.copytree(SOURCE / "evals/tasks/fix-off-by-one/files", home / "original")
    (work / ".pig/extensions").mkdir(parents=True)
    for kind in (".pig", ".pi"):
        put_json(home / kind / "agent/trust.json", {str(work): True})
    for name in ("chain", "chain-rs"):
        shutil.copytree(DEMO / "sources" / name, home / name)
    # The TypeScript extension is exact current-upstream source; assets are local inputs.
    doom = home / "doom-overlay"
    shutil.copytree(SOURCE / ".upstream/current/packages/coding-agent/examples/extensions/doom-overlay", doom)
    for directory in [doom, *(p for p in doom.rglob("*") if p.is_dir())]:
        directory.chmod(0o700)
    for name in ("doom1.wad", "doom/build/doom.js", "doom/build/doom.wasm"):
        target = doom / name
        target.parent.mkdir(parents=True, exist_ok=True)
        if target.exists():
            target.chmod(0o600)
        shutil.copy2(args.assets / name, target)
    piglet = home / "piglet"
    shutil.copytree(SOURCE / "piglets/standard", piglet)
    mod = piglet / "go.mod"
    old = "replace github.com/MichaelKinsy/PiG/extensions/sdk => ../../extensions/sdk\n"
    text = mod.read_text()
    assert text.count(old) == 1
    mod.write_text(text.replace(old, ""))
    shutil.copytree(DEMO / "sources/chain-rs", piglet / "extensions/chain-rs")
    for name, rust in (("pig-go", False), ("pig-demo", True)):
        yaml = f'name: {name}\ndescription: "Local launch rehearsal"\nextensions:\n'
        yaml += '  - name: pigrunner\n    origins: [local:extensions/pigrunner]\n'
        if rust:
            yaml += '  - name: chain-rs\n    origins: [local:extensions/chain-rs]\n'
        yaml += 'discovery:\n  extensions: []\n  skills: []\n'
        (piglet / f"{name}.yaml").write_text(yaml)
    (home / "dist").mkdir()
    (root / "logs").mkdir()
    bindir = root / "bin"
    bindir.mkdir()
    tools["pig"] = str(args.pig.resolve())
    for name, path in tools.items():
        (bindir / name).symlink_to(path)
    for name in ("build-piglets.sh", "remote-stage.sh"):
        (bindir / name).symlink_to(DEMO / name)
    cache = args.cache.resolve()
    cache.mkdir(parents=True, exist_ok=True)
    cargo_home = cache / "cargo"
    if not cargo_home.exists():
        cargo_home.mkdir()
        registry = Path(os.environ.get("CARGO_HOME", str(Path.home() / ".cargo"))) / "registry"
        shutil.copytree(registry, cargo_home / "registry")
    env = {
        "DEMO_ROOT": str(root), "DEMO": str(DEMO), "DEMO_PORT": str(args.port), "HOME": str(home),
        "PATH": f"{bindir}:/usr/bin:/bin", "TERM": "xterm-256color", "COLORTERM": "truecolor", "LANG": "C.UTF-8",
        "PIG_OFFLINE": "1", "PI_OFFLINE": "1", "PI_TELEMETRY": "0", "PIG_SOURCE_ROOT": str(SOURCE),
        "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOCACHE": str(cache / "go-build"),
        "GOPATH": str(cache / "go"), "GOMODCACHE": subprocess.check_output([tools["go"], "env", "GOMODCACHE"], text=True).strip(),
        "CARGO_HOME": str(cargo_home), "CARGO_NET_OFFLINE": "true", "PYTHONDONTWRITEBYTECODE": "1",
    }
    (root / "env.sh").write_text("".join(f"export {k}={shlex.quote(v)}\n" for k, v in env.items()))
    (root / "env.json").write_text(json.dumps(env, indent=2) + "\n")
    failed = subprocess.run([tools["python3"], "-B", "-m", "unittest", "-v", "test_stats"], cwd=work,
                            env=env, capture_output=True, text=True)
    (root / "logs/before-tests.txt").write_text(failed.stdout + failed.stderr)
    if failed.returncode != 1 or "FAILED (failures=2)" not in failed.stderr:
        raise RuntimeError("The fixture must fail exactly its last-window and single-window tests")
    print(root)


if __name__ == "__main__":
    main()
