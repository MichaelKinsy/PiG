# SPDX-License-Identifier: MIT
"""The environment block every report starts with: host, toolchains, commit, arms, meter mode, seed."""
import os
import platform
import subprocess
import time

from . import arms as armlib
from . import cgroup, lock


def _cmd(argv, cwd=None):
    try:
        out = subprocess.run(argv, capture_output=True, text=True, timeout=30, cwd=cwd)
        return (out.stdout.strip() or out.stderr.strip()).splitlines()[0] if out.returncode == 0 else None
    except (OSError, subprocess.TimeoutExpired, IndexError):
        return None


def cpu_model():
    if os.name == "nt":
        from . import winplat
        return winplat.cpu_name() or platform.processor() or None
    try:
        with open("/proc/cpuinfo") as f:
            for line in f:
                if line.startswith("model name"):
                    return line.split(":", 1)[1].strip()
    except OSError:
        pass
    return _cmd(["sysctl", "-n", "machdep.cpu.brand_string"]) or platform.processor() or None


def mem_gib():
    if os.name == "nt":
        from . import winplat
        return winplat.mem_gib()
    try:
        with open("/proc/meminfo") as f:
            return round(int(f.readline().split()[1]) / 1048576, 1)
    except (OSError, ValueError):
        out = _cmd(["sysctl", "-n", "hw.memsize"])
        return round(int(out) / 2**30, 1) if out and out.isdigit() else None


def governor():
    """The CPU frequency governor; on Windows the active power plan and power source."""
    if os.name == "nt":
        from . import winplat
        return winplat.power_plan()
    try:
        with open("/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor") as f:
            return f.read().strip()
    except OSError:
        return None


def collect(arm_list=(), seed=None, extra=None):
    """Return the environment dict. Nothing here names a person or a host: no user name, hostname or path outside the repo."""
    repo = armlib.REPO
    dirty = bool(_cmd(["git", "status", "--porcelain", "--untracked-files=no"], cwd=repo))
    block = {
        "generated_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "os": f"{platform.system()} {platform.release()}",
        "machine": platform.machine(),
        "cpu": cpu_model(),
        "logical_cpus": os.cpu_count(),
        "memory_gib": mem_gib(),
        "cpu_governor": governor(),
        "load_average_1m": round(os.getloadavg()[0], 2) if hasattr(os, "getloadavg") else None,
        "python": platform.python_version(),
        "go": _cmd(["go", "version"]),
        "node": _cmd(["node", "--version"]),
        "commit": _cmd(["git", "rev-parse", "HEAD"], cwd=repo),
        "commit_dirty": dirty,
        "meter": cgroup.available_mode(),
        "seed": seed,
        "arms_lock_sha256": lock.lock_hash(),
        "arms": {},
    }
    if os.name == "nt":
        from . import winplat
        block["cpu_tick_ms"] = winplat.clock_tick_ms()  # Windows charges process CPU time in whole ticks; the reports say so
    for a in arm_list:
        info = {"name": a.name, "family": a.family, "source": a.source, "pinned_version": a.version, "license": a.pin.get("license"), "license_note": a.pin.get("license_note")}
        if a.pin.get("package_lock_sha256"):
            info["package_lock_sha256"] = a.pin["package_lock_sha256"]
            info["integrity"] = a.pin.get("integrity")
        if a.binary:
            info["pinned"] = not a.unpinned
            info["version_line"] = getattr(a, "version_line", None)
            info["executable_sha256"] = lock.sha256_file(os.path.realpath(a.binary))
            info["footprint"] = armlib.footprint(a)
            info.update(a.record)
        else:
            info["skipped"] = a.skip
        block["arms"][a.id] = info
    if extra:
        block.update(extra)
    return block
