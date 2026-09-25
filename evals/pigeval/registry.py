# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Load evals/harnesses.toml and install or locate each harness."""
import json
import os
import shutil
import subprocess
import tomllib
from dataclasses import dataclass, field

EVALS_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
REPO_ROOT = os.path.dirname(EVALS_DIR)
DEFAULT_REGISTRY = os.path.join(EVALS_DIR, "harnesses.toml")
APIS = {"openai-completions", "openai-responses", "anthropic-messages", "none"}
CONFIGS = {"pi-models", "opencode", "none"}
KEYS = {"name", "npm", "bin", "api", "config", "config_dir", "print", "session", "live", "env", "mock", "version", "own_version"}


@dataclass
class Harness:
    id: str
    name: str
    bin: str
    api: str
    print_args: list
    config: str = "none"
    config_dir: str = ""
    npm: list = field(default_factory=list)
    session_args: list = field(default_factory=list)
    live_args: list = field(default_factory=list)
    version_args: list = field(default_factory=lambda: ["--version"])
    own_version_args: list = field(default_factory=list)
    env: dict = field(default_factory=dict)
    mock: bool = True


def load(path=DEFAULT_REGISTRY):
    with open(path, "rb") as f:
        data = tomllib.load(f)
    harnesses = {}
    for hid, raw in data.items():
        unknown = set(raw) - KEYS
        if unknown:
            raise ValueError(f"{path}: [{hid}] has unknown keys: {', '.join(sorted(unknown))}")
        for key in ("name", "bin", "api", "print"):
            if key not in raw:
                raise ValueError(f"{path}: [{hid}] is missing {key}")
        if raw["api"] not in APIS:
            raise ValueError(f"{path}: [{hid}] api must be one of {sorted(APIS)}")
        config = raw.get("config", "none")
        if config not in CONFIGS:
            raise ValueError(f"{path}: [{hid}] config must be one of {sorted(CONFIGS)}")
        if config == "pi-models" and not raw.get("config_dir"):
            raise ValueError(f"{path}: [{hid}] config pi-models needs config_dir")
        for key in ("print", "live"):
            if key in raw and "{prompt}" not in raw[key]:
                raise ValueError(f"{path}: [{hid}] {key} must contain {{prompt}}")
        if "session" in raw and "{session}" not in raw["session"]:
            raise ValueError(f"{path}: [{hid}] session must contain {{session}}")
        harnesses[hid] = Harness(
            id=hid, name=raw["name"], bin=raw["bin"], api=raw["api"], print_args=list(raw["print"]),
            config=config, config_dir=raw.get("config_dir", ""), npm=list(raw.get("npm", [])),
            session_args=list(raw.get("session", [])), live_args=list(raw.get("live", [])),
            version_args=list(raw.get("version", ["--version"])),
            own_version_args=list(raw.get("own_version", [])), env=dict(raw.get("env", {})),
            mock=bool(raw.get("mock", True)))
    return harnesses


def select(harnesses, spec):
    if not spec or spec == "all":
        return list(harnesses.values())
    chosen = []
    for hid in (part.strip() for part in spec.split(",")):
        if hid not in harnesses:
            raise SystemExit(f"pigeval: unknown harness {hid!r}; known: {', '.join(harnesses)}")
        chosen.append(harnesses[hid])
    return chosen


def dev_home():
    return os.environ.get("PIG_DEV_HOME") or os.path.expanduser("~/.cache/pig-dev")


def substitute(values, args):
    out = []
    for arg in args:
        for key, value in values.items():
            arg = arg.replace("{" + key + "}", str(value))
        out.append(arg)
    return out


def resolve(harness, pig=None, prefix=None):
    """Return the executable for harness, or None when it is not installed."""
    if harness.bin == "{pig}":
        candidate = os.path.abspath(pig or os.path.join(REPO_ROOT, "bin", "pig"))
        return candidate if os.access(candidate, os.X_OK) else None
    candidate = os.path.join(prefix or os.path.join(dev_home(), "harnesses"), harness.id, "node_modules", ".bin", harness.bin)
    return candidate if os.access(candidate, os.X_OK) else shutil.which(harness.bin)


def npm_spec(spec):
    name, _, version = spec.rpartition("@") if spec.count("@") > (1 if spec.startswith("@") else 0) else (spec, "", "")
    return name, version


def installed(target, specs):
    for spec in specs:
        name, version = npm_spec(spec)
        try:
            with open(os.path.join(target, "node_modules", name, "package.json")) as f:
                if version and json.load(f).get("version") != version:
                    return False
        except OSError:
            return False
    return True


def install(harnesses, prefix, check=False):
    """Install each harness's pinned npm packages into prefix/<id>. Return True when all are present."""
    ok = True
    for h in harnesses:
        if h.bin == "{pig}":
            print(f"install: {h.id:9} built from this checkout by make build")
            continue
        target = os.path.join(prefix, h.id)
        if installed(target, h.npm):
            print(f"install: {h.id:9} ok       {' '.join(h.npm)}")
            continue
        if check:
            print(f"install: {h.id:9} missing  {' '.join(h.npm)}")
            ok = False
            continue
        os.makedirs(target, exist_ok=True)
        result = subprocess.run(["npm", "install", "--no-audit", "--no-fund", "--loglevel=error", "--prefix", target, *h.npm])
        good = result.returncode == 0 and installed(target, h.npm)
        print(f"install: {h.id:9} {'ok' if good else 'FAILED':8} {' '.join(h.npm)}")
        ok = ok and good
    return ok
