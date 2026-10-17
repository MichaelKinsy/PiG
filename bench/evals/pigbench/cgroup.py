# SPDX-License-Identifier: MIT
"""Resource meters: one cgroup v2 per measured process tree on Linux, rusage and /proc or ps elsewhere.

Every harness is started inside its own meter. The meter reports CPU time of the
whole tree (harness, its helpers, extension processes), the peak memory the
kernel charged to the tree, and can kill the tree without matching names.

Modes, chosen by `detect()` and recorded in every result:

  cgroup-direct   mkdir a child under a delegated cgroup that has the memory and cpu controllers
                  (a container or pod with its own cgroup namespace, or PIGBENCH_CGROUP_ROOT)
  cgroup-systemd  a transient scope from `systemd-run --user --scope` holds the tree; needs a user manager
  rusage          wait4 rusage for CPU and the largest process RSS after exit; while the tree runs, /proc
                  (Linux: CPU from each process's CPU-time clock, peak memory the sum of VmHWM) or ps (macOS)
                  samples. No cgroup, so memory is the sum of RSS. A pod's cgroup is read-only, so bench/k8s uses this.
"""
import os
import signal
import subprocess
import sys
import threading
import time
import uuid

from . import egress

CGROUP_ROOT = "/sys/fs/cgroup"


def _read(path):
    with open(path) as f:
        return f.read()


def _own_cgroup():
    for line in _read("/proc/self/cgroup").splitlines():
        if line.startswith("0::"):
            return CGROUP_ROOT + line[3:]
    return None


def parse_cpu_stat(text):
    """Return {key: int} from cgroup cpu.stat text."""
    out = {}
    for line in text.splitlines():
        key, _, value = line.partition(" ")
        if value.strip().isdigit():
            out[key] = int(value)
    return out


class Meter:
    """Base: start(argv...) -> process, cpu_ms(), peak_mem_mib(), kill(), close()."""

    mode = "none"

    def popen(self, argv, **kwargs):
        raise NotImplementedError

    def cpu_ms(self):
        raise NotImplementedError

    def peak_mem_mib(self):
        return None

    def max_proc_rss_mib(self):
        return None

    def pids(self):
        """The pids of the measured tree now (for its current resident set); empty when the meter cannot list them."""
        return []

    def current_pss_mib(self):
        """Proportional set size of the process tree now, in MiB (shared pages divided among their users), or None where /proc has no smaps_rollup."""
        return None

    def snapshot(self):
        """Resource state of the process tree now: processes, threads, open file descriptors, RSS, PSS (MiB) and CPU (ms). None where unsupported."""
        return None

    def kill(self):
        pass

    def close(self):
        pass

    egress = frozenset()


class CgroupMeter(Meter):
    def __init__(self, path, holder=None, mode="cgroup-direct"):
        self.path, self.holder, self.mode = path, holder, mode
        self._last_cpu = 0
        self._rss_peak_kib = 0
        self.egress = set()  # non-loopback remotes the tree connected to (see egress.py)
        self._sampling = False
        self.has_memory_peak = os.path.exists(os.path.join(path, "memory.peak"))

    def _join(self):
        procs = os.path.join(self.path, "cgroup.procs")

        def join():
            with open(procs, "w") as f:
                f.write(str(os.getpid()))

        return join

    def popen(self, argv, **kwargs):
        user_pre = kwargs.pop("preexec_fn", None)
        join = self._join()

        def pre():
            join()
            if user_pre:
                user_pre()

        self.child = subprocess.Popen(argv, preexec_fn=pre, **kwargs)
        if not self._sampling:
            self.start_sampler()
        return self.child

    def pids(self):
        try:
            return _read(os.path.join(self.path, "cgroup.procs")).split()
        except OSError:
            return []

    def _sample_rss(self):
        """Sum of per-process peak RSS (VmHWM) over the cgroup's members now; processes that already exited are lost."""
        try:
            pids = _read(os.path.join(self.path, "cgroup.procs")).split()
        except OSError:
            return
        try:
            self.egress |= egress.scan(pids)
        except (OSError, ValueError):
            pass
        self._rss_peak_kib = max(self._rss_peak_kib, status_kib(pids, "VmHWM"))

    def start_sampler(self, interval=egress.SAMPLE_INTERVAL_S):
        """Sample the tree's resident set in a thread until close(); the cgroup's memory.peak cannot split anonymous from file pages."""
        self._sampling = True

        def loop():
            while self._sampling:
                self._sample_rss()
                time.sleep(interval)

        self._thread = threading.Thread(target=loop, daemon=True)
        self._thread.start()

    def max_proc_rss_mib(self):
        self._sample_rss()
        return self._rss_peak_kib / 1024

    def snapshot(self):
        try:
            pids = _read(os.path.join(self.path, "cgroup.procs")).split()
        except OSError:
            return None
        threads = fds = rss_kib = alive = 0
        for pid in pids:
            try:
                status = _read(f"/proc/{pid}/status")
                for line in status.splitlines():
                    if line.startswith("Threads:"):
                        threads += int(line.split()[1])
                    elif line.startswith("VmRSS:"):
                        rss_kib += int(line.split()[1])
                fds += len(os.listdir(f"/proc/{pid}/fd"))
                alive += 1
            except (OSError, ValueError):
                pass
        return {"processes": alive, "threads": threads, "fds": fds, "rss_mib": rss_kib / 1024, "pss_mib": self.current_pss_mib(), "cpu_ms": self.cpu_ms()}

    def current_pss_mib(self):
        try:
            pids = _read(os.path.join(self.path, "cgroup.procs")).split()
        except OSError:
            return None
        total, seen = 0, False
        for pid in pids:
            try:
                for line in _read(f"/proc/{pid}/smaps_rollup").splitlines():
                    if line.startswith("Pss:"):
                        total += int(line.split()[1])
                        seen = True
            except (OSError, ValueError):
                pass
        return total / 1024 if seen else None

    def cpu_ms(self):
        try:
            self._last_cpu = parse_cpu_stat(_read(os.path.join(self.path, "cpu.stat"))).get("usage_usec", self._last_cpu)
        except OSError:
            pass
        return self._last_cpu / 1000

    def peak_mem_mib(self):
        try:
            return int(_read(os.path.join(self.path, "memory.peak"))) / 1048576
        except (OSError, ValueError):
            return None

    def pids_peak(self):
        try:
            return int(_read(os.path.join(self.path, "pids.peak")))
        except (OSError, ValueError):
            return None

    def kill(self):
        try:
            with open(os.path.join(self.path, "cgroup.kill"), "w") as f:
                f.write("1")
        except OSError:
            child = getattr(self, "child", None)
            if child and child.poll() is None:
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except OSError:
                    pass

    def close(self):
        self._sampling = False
        self._sample_rss()
        egress.OBSERVED |= self.egress
        self.kill()
        if self.holder is not None:
            self.holder.terminate()
            try:
                self.holder.wait(10)
            except subprocess.TimeoutExpired:
                self.holder.kill()
        elif os.path.isdir(self.path):
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                try:
                    os.rmdir(self.path)
                    break
                except OSError:
                    time.sleep(0.05)


class RusageMeter(Meter):
    """No cgroup: CPU from wait4 after exit, tree samples while running (/proc on Linux, ps on macOS).

    On Linux a sampler thread reads the tree from /proc every egress.SAMPLE_INTERVAL_S, for its resident set and for the egress audit,
    so a run that falls back to this meter is audited too. A process that leaves the tree (a double fork re-parents it) is not seen;
    a cgroup meter or an isolated network namespace has no such gap.
    """

    mode = "rusage"

    def __init__(self):
        self.child = None
        self.rusage = None
        self._peak_sum = 0.0
        self._hwm_kib = 0  # sum of the members' VmHWM, as CgroupMeter.max_proc_rss_mib, while the tree runs (Linux)
        self.egress = set()
        self._sampling = False

    def popen(self, argv, **kwargs):
        kwargs.setdefault("start_new_session", True)
        self.child = subprocess.Popen(argv, **kwargs)
        if os.path.isdir("/proc/self") and not self._sampling:
            self._sampling = True
            self._thread = threading.Thread(target=self._sample_loop, daemon=True)
            self._thread.start()
        return self.child

    def pids(self):
        if self.child is None or self.child.returncode is not None or not os.path.isdir("/proc/self"):
            return []
        return list(_tree_rows(self.child.pid))

    def _sample(self):
        rows = _tree_rows(self.child.pid)
        members = set(rows)
        self._peak_sum = max(self._peak_sum, sum(rows[p][1] for p in members if p in rows))
        self._hwm_kib = max(self._hwm_kib, status_kib(members, "VmHWM"))
        try:
            self.egress |= egress.scan(members)
        except (OSError, ValueError):
            pass

    def _sample_loop(self):
        # returncode, not poll(): poll() would reap the child and lose the rusage that wait() collects with wait4.
        while self._sampling and self.child.returncode is None:
            self._sample()
            time.sleep(egress.SAMPLE_INTERVAL_S)

    def close(self):
        if self._sampling:
            self._sampling = False
            self._thread.join(1)
            if self.child is not None and self.child.returncode is None:
                self._sample()
        egress.OBSERVED |= self.egress
        self.kill()

    def wait(self, timeout=None):
        """wait4 replacement for Popen.wait so the rusage of the tree is kept."""
        if self.child.returncode is not None:  # already reaped, by an earlier wait() or by Popen.poll()
            return self.child.returncode
        deadline = None if timeout is None else time.monotonic() + timeout
        while True:
            try:
                pid, status, usage = os.wait4(self.child.pid, os.WNOHANG)
            except ChildProcessError:  # Popen reaped it in another thread; its rusage is gone
                return self.child.wait(0)
            if pid:
                self.child.returncode = os.waitstatus_to_exitcode(status)
                self.rusage = usage
                return self.child.returncode
            if deadline is not None and time.monotonic() > deadline:
                raise subprocess.TimeoutExpired(self.child.args, timeout)
            time.sleep(0.005)

    def _tree(self):
        return tree_usage(self.child.pid)

    def cpu_ms(self):
        if self.rusage is not None:
            return (self.rusage.ru_utime + self.rusage.ru_stime) * 1000
        cpu, rss = self._tree()
        self._peak_sum = max(self._peak_sum, rss)
        return cpu

    def peak_mem_mib(self):
        _, rss = self._tree() if self.rusage is None else (0, 0)
        self._peak_sum = max(self._peak_sum, rss)
        return self._peak_sum / 1024

    def max_proc_rss_mib(self):
        # Linux: the sum of the members' VmHWM sampled while the tree runs, which is what a cgroup meter reports; after exit, the last
        # sample (None if the tree exited before one). Not ru_maxrss: exec keeps hiwater_rss, so a child forked from a larger harness
        # reports the harness's peak.
        if os.path.isdir("/proc/self"):
            if self.child is None:
                return None
            if self.child.returncode is None:
                self._sample()
            return self._hwm_kib / 1024 if self._hwm_kib else None
        if self.rusage is None:
            return None
        # macOS reports bytes.
        return self.rusage.ru_maxrss / (1048576 if sys.platform == "darwin" else 1024)

    def kill(self):
        if self.child and self.child.poll() is None:
            try:
                os.killpg(self.child.pid, signal.SIGKILL)
            except OSError:
                pass


def parse_ps(text):
    """Parse `ps -A -o pid=,ppid=,rss=,time=` (macOS cputime M:SS.cc or H:MM:SS) into {pid: (ppid, rss_kib, cpu_ms)}."""
    rows = {}
    for line in text.splitlines():
        parts = line.split()
        if len(parts) != 4:
            continue
        pid, ppid, rss, cpu = parts
        seconds = 0.0
        for piece in cpu.replace("-", ":").split(":"):
            seconds = seconds * 60 + float(piece)
        rows[int(pid)] = (int(ppid), int(rss), seconds * 1000)
    return rows


def _proc_rows():
    """{pid: (ppid, rss_kib, cpu_ms)} for every process, from /proc (Linux) or ps (elsewhere)."""
    rows = {}
    if os.path.isdir("/proc/self"):
        tick = os.sysconf("SC_CLK_TCK")
        page = os.sysconf("SC_PAGE_SIZE") // 1024
        for name in os.listdir("/proc"):
            if not name.isdigit():
                continue
            try:
                rows[int(name)] = _stat_row(name, tick, page)
            except (OSError, IndexError, ValueError):
                continue
    else:
        out = subprocess.run(["ps", "-A", "-o", "pid=,ppid=,rss=,time="], capture_output=True, text=True, timeout=10).stdout
        rows = parse_ps(out)
    return rows


def _members(rows, root):
    """`root` and its descendants in `rows`."""
    members, grew = {root}, True
    while grew:
        grew = False
        for pid, (ppid, _, _) in rows.items():
            if ppid in members and pid not in members:
                members.add(pid)
                grew = True
    return members


def _stat_row(pid, tick, page):
    fields = _read(f"/proc/{pid}/stat").rsplit(")", 1)[1].split()
    cpu = process_cpu_ms(int(pid))
    if cpu is None:
        cpu = (int(fields[11]) + int(fields[12])) * 1000 / tick
    return int(fields[1]), int(fields[21]) * page, cpu


def process_cpu_ms(pid):
    """The CPU time of process pid (all its threads, live and exited) in ms, from its CPU-time clock, which counts
    nanoseconds; /proc/<pid>/stat counts clock ticks (10 ms), too coarse for a turn's CPU. None when the clock cannot be
    read (the process exited, or not Linux). The clock id is Linux's MAKE_PROCESS_CPUCLOCK(pid, CPUCLOCK_SCHED), what
    glibc's clock_getcpuclockid returns."""
    if not sys.platform.startswith("linux"):
        return None
    try:
        return time.clock_gettime_ns(((~pid) << 3) | 2) / 1e6
    except OSError:
        return None


def status_kib(pids, field):
    """The sum of one /proc/<pid>/status field (VmHWM, VmRSS) in KiB over pids; a pid that is gone counts 0."""
    total = 0
    for pid in pids:
        try:
            for line in _read(f"/proc/{pid}/status").splitlines():
                if line.startswith(field + ":"):
                    total += int(line.split()[1])
        except (OSError, ValueError):
            pass
    return total


def _tree_rows(root):
    """{pid: (ppid, rss_kib, cpu_ms)} for `root` and its descendants. On Linux it follows /proc/<pid>/task/<tid>/children, so the cost is the
    size of the tree, not of the host's process table (a full /proc scan takes hundreds of milliseconds on a busy host)."""
    if not os.path.exists(f"/proc/{os.getpid()}/task/{os.getpid()}/children"):
        rows = _proc_rows()
        return {p: rows[p] for p in _members(rows, root) if p in rows}
    tick, page = os.sysconf("SC_CLK_TCK"), os.sysconf("SC_PAGE_SIZE") // 1024
    rows, queue = {}, [root]
    while queue:
        pid = queue.pop()
        if pid in rows:
            continue
        try:
            rows[pid] = _stat_row(pid, tick, page)
            for tid in os.listdir(f"/proc/{pid}/task"):
                queue.extend(int(c) for c in _read(f"/proc/{pid}/task/{tid}/children").split())
        except (OSError, IndexError, ValueError):
            continue
    return rows


def tree_usage(root):
    """(cpu_ms, rss_kib) summed over `root` and its descendants, now."""
    rows = _tree_rows(root)
    members = set(rows)
    cpu = sum(rows[p][2] for p in members if p in rows)
    rss = sum(rows[p][1] for p in members if p in rows)
    return cpu, rss


def _controllers(path):
    try:
        return set(_read(os.path.join(path, "cgroup.controllers")).split())
    except OSError:
        return set()


def _try_direct(root):
    if not root or not os.path.isdir(root):
        return None
    path = os.path.join(root, f"pigbench-{uuid.uuid4().hex[:12]}")
    try:
        os.mkdir(path)
    except OSError:
        return None
    meter = CgroupMeter(path)
    if not meter.has_memory_peak:
        os.rmdir(path)
        return None
    return meter


def _try_systemd():
    if not os.path.exists("/usr/bin/systemd-run") and not any(os.access(os.path.join(p, "systemd-run"), os.X_OK) for p in os.environ.get("PATH", "").split(os.pathsep)):
        return None
    unit = f"pigbench-{uuid.uuid4().hex[:12]}"
    holder = subprocess.Popen(["systemd-run", "--user", "--scope", "--quiet", "--collect", f"--unit={unit}", "-p", "MemoryAccounting=yes", "-p", "CPUAccounting=yes",
                               "-p", "TasksAccounting=yes", "sleep", "infinity"], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    deadline = time.monotonic() + 10
    path = None
    while time.monotonic() < deadline and holder.poll() is None:
        try:
            for line in _read(f"/proc/{holder.pid}/cgroup").splitlines():
                if line.startswith("0::") and line.endswith(f"{unit}.scope"):
                    path = CGROUP_ROOT + line[3:]
        except OSError:
            pass
        if path:
            break
        time.sleep(0.01)
    if not path:
        holder.kill()
        return None
    meter = CgroupMeter(path, holder, "cgroup-systemd")
    if not meter.has_memory_peak or not os.access(os.path.join(path, "cgroup.procs"), os.W_OK):
        meter.close()
        return None
    return meter


def available_mode():
    """The mode `new_meter()` will use on this host, probed once."""
    meter = new_meter()
    mode = meter.mode
    meter.close()
    return mode


def new_meter(force=None):
    """A fresh meter for one process tree. `force` or PIGBENCH_METER pins the mode."""
    force = force or os.environ.get("PIGBENCH_METER")
    if sys.platform == "win32":
        from . import winplat
        return winplat.JobMeter()
    if force != "rusage" and sys.platform.startswith("linux") and os.path.isdir(CGROUP_ROOT) and os.path.exists(os.path.join(CGROUP_ROOT, "cgroup.controllers")):
        if force in (None, "cgroup-direct"):
            meter = _try_direct(os.environ.get("PIGBENCH_CGROUP_ROOT") or _own_cgroup())
            if meter:
                return meter
        if force in (None, "cgroup-systemd"):
            meter = _try_systemd()
            if meter:
                return meter
        if force:
            raise RuntimeError(f"meter {force} is not available on this host")
    return RusageMeter()
