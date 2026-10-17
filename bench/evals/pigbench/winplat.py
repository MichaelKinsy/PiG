# SPDX-License-Identifier: MIT
"""Windows process metering with a Job Object: CPU, peak memory, children, kill, per-process working set and handles.

The meter talks to the Win32 API through a small `Api` object, so the unit tests run on any host against a fake. On Windows `Api` is backed by
kernel32 and psapi through ctypes (standard library only). Differences from the Linux cgroup meter, stated in every report:
  * the process is assigned to the job right after it starts, so a child it spawns in that first instant is not counted;
  * peak memory is the job's PeakJobMemoryUsed (committed memory), not a resident-set peak: resident figures come from the per-process
    working-set counters, sampled while the tree runs;
  * there is no PSS on Windows: `current_pss_mib` reports the tree's private bytes (PROCESS_MEMORY_COUNTERS_EX.PrivateUsage, the commit charge,
    which also counts private pages that are paged out) instead. It is not the private working set, which needs QueryWorkingSetEx.
"""
import ctypes
import subprocess
import threading
import time

JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x2000
JOB_BASIC_ACCOUNTING = 1
JOB_BASIC_PROCESS_ID_LIST = 3
JOB_EXTENDED_LIMIT = 9
PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
HUNDRED_NS_PER_MS = 10_000
MAX_PIDS = 4096


class BasicAccounting(ctypes.Structure):
    _fields_ = [("TotalUserTime", ctypes.c_int64), ("TotalKernelTime", ctypes.c_int64), ("ThisPeriodTotalUserTime", ctypes.c_int64), ("ThisPeriodTotalKernelTime", ctypes.c_int64),
                ("TotalPageFaultCount", ctypes.c_uint32), ("TotalProcesses", ctypes.c_uint32), ("ActiveProcesses", ctypes.c_uint32), ("TotalTerminatedProcesses", ctypes.c_uint32)]


class BasicLimit(ctypes.Structure):
    _fields_ = [("PerProcessUserTimeLimit", ctypes.c_int64), ("PerJobUserTimeLimit", ctypes.c_int64), ("LimitFlags", ctypes.c_uint32), ("MinimumWorkingSetSize", ctypes.c_size_t),
                ("MaximumWorkingSetSize", ctypes.c_size_t), ("ActiveProcessLimit", ctypes.c_uint32), ("Affinity", ctypes.c_size_t), ("PriorityClass", ctypes.c_uint32), ("SchedulingClass", ctypes.c_uint32)]


class IoCounters(ctypes.Structure):
    _fields_ = [(n, ctypes.c_uint64) for n in ("ReadOperationCount", "WriteOperationCount", "OtherOperationCount", "ReadTransferCount", "WriteTransferCount", "OtherTransferCount")]


class ExtendedLimit(ctypes.Structure):
    _fields_ = [("BasicLimitInformation", BasicLimit), ("IoInfo", IoCounters), ("ProcessMemoryLimit", ctypes.c_size_t), ("JobMemoryLimit", ctypes.c_size_t),
                ("PeakProcessMemoryUsed", ctypes.c_size_t), ("PeakJobMemoryUsed", ctypes.c_size_t)]


class ProcessMemory(ctypes.Structure):
    _fields_ = [("cb", ctypes.c_uint32), ("PageFaultCount", ctypes.c_uint32), ("PeakWorkingSetSize", ctypes.c_size_t), ("WorkingSetSize", ctypes.c_size_t),
                ("QuotaPeakPagedPoolUsage", ctypes.c_size_t), ("QuotaPagedPoolUsage", ctypes.c_size_t), ("QuotaPeakNonPagedPoolUsage", ctypes.c_size_t),
                ("QuotaNonPagedPoolUsage", ctypes.c_size_t), ("PagefileUsage", ctypes.c_size_t), ("PeakPagefileUsage", ctypes.c_size_t), ("PrivateUsage", ctypes.c_size_t)]


class Api:
    """The Win32 calls the meter needs, each returning plain Python values. Backed by ctypes on Windows."""

    def __init__(self):
        from ctypes import wintypes
        self.k32 = ctypes.WinDLL("kernel32", use_last_error=True)
        self.psapi = ctypes.WinDLL("psapi", use_last_error=True)
        k = self.k32
        k.CreateJobObjectW.restype = wintypes.HANDLE
        k.CreateJobObjectW.argtypes = [ctypes.c_void_p, wintypes.LPCWSTR]
        k.AssignProcessToJobObject.argtypes = [wintypes.HANDLE, wintypes.HANDLE]
        k.SetInformationJobObject.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD]
        k.QueryInformationJobObject.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD, ctypes.c_void_p]
        k.TerminateJobObject.argtypes = [wintypes.HANDLE, wintypes.UINT]
        k.OpenProcess.restype = wintypes.HANDLE
        k.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
        k.GetProcessHandleCount.argtypes = [wintypes.HANDLE, ctypes.POINTER(wintypes.DWORD)]
        k.CloseHandle.argtypes = [wintypes.HANDLE]
        self.psapi.GetProcessMemoryInfo.argtypes = [wintypes.HANDLE, ctypes.c_void_p, wintypes.DWORD]

    def create_job(self):
        job = self.k32.CreateJobObjectW(None, None)
        if not job:
            raise OSError(ctypes.get_last_error(), "CreateJobObject failed")
        info = ExtendedLimit()
        info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
        if not self.k32.SetInformationJobObject(job, JOB_EXTENDED_LIMIT, ctypes.byref(info), ctypes.sizeof(info)):
            raise OSError(ctypes.get_last_error(), "SetInformationJobObject failed")
        return job

    def assign(self, job, process_handle):
        return bool(self.k32.AssignProcessToJobObject(job, process_handle))

    def accounting(self, job):
        """(cpu_100ns, active_processes) of the whole job."""
        info = BasicAccounting()
        if not self.k32.QueryInformationJobObject(job, JOB_BASIC_ACCOUNTING, ctypes.byref(info), ctypes.sizeof(info), None):
            raise OSError(ctypes.get_last_error(), "QueryInformationJobObject failed")
        return info.TotalUserTime + info.TotalKernelTime, info.ActiveProcesses

    def peak_job_bytes(self, job):
        info = ExtendedLimit()
        if not self.k32.QueryInformationJobObject(job, JOB_EXTENDED_LIMIT, ctypes.byref(info), ctypes.sizeof(info), None):
            raise OSError(ctypes.get_last_error(), "QueryInformationJobObject failed")
        return info.PeakJobMemoryUsed

    def pids(self, job):
        size = ctypes.sizeof(ctypes.c_uint32) * 2 + ctypes.sizeof(ctypes.c_size_t) * MAX_PIDS
        buf = ctypes.create_string_buffer(size)
        if not self.k32.QueryInformationJobObject(job, JOB_BASIC_PROCESS_ID_LIST, buf, size, None):
            raise OSError(ctypes.get_last_error(), "QueryInformationJobObject failed")
        listed = ctypes.c_uint32.from_buffer(buf, 4).value
        return list((ctypes.c_size_t * listed).from_buffer(buf, 8))  # two DWORDs, then the ULONG_PTR array

    def process_memory(self, pid):
        """(peak_working_set, working_set, private_usage, handles) in bytes and counts, or None when the process is gone."""
        handle = self.k32.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, False, pid)
        if not handle:
            return None
        try:
            return self.handle_memory(handle)
        finally:
            self.k32.CloseHandle(handle)

    def handle_memory(self, handle):
        """process_memory for an open process handle. The counters outlive the process while a handle is open, so an exited child still has its peak."""
        counters = ProcessMemory()
        counters.cb = ctypes.sizeof(counters)
        if not self.psapi.GetProcessMemoryInfo(handle, ctypes.byref(counters), counters.cb):
            return None
        from ctypes import wintypes
        handles = wintypes.DWORD()
        self.k32.GetProcessHandleCount(handle, ctypes.byref(handles))
        return counters.PeakWorkingSetSize, counters.WorkingSetSize, counters.PrivateUsage, handles.value

    def terminate(self, job):
        self.k32.TerminateJobObject(job, 1)

    def close(self, handle):
        self.k32.CloseHandle(handle)


def clock_tick_ms():
    """The clock interrupt interval in ms (GetSystemTimeAdjustment's time increment). Windows charges process CPU time in whole ticks of it."""
    from ctypes import wintypes
    adjustment, increment, disabled = wintypes.DWORD(), wintypes.DWORD(), wintypes.BOOL()
    if not ctypes.WinDLL("kernel32", use_last_error=True).GetSystemTimeAdjustment(ctypes.byref(adjustment), ctypes.byref(increment), ctypes.byref(disabled)):
        raise OSError(ctypes.get_last_error(), "GetSystemTimeAdjustment failed")
    return increment.value / HUNDRED_NS_PER_MS


class MemoryStatus(ctypes.Structure):
    _fields_ = [("dwLength", ctypes.c_uint32), ("dwMemoryLoad", ctypes.c_uint32)] + [(n, ctypes.c_uint64) for n in (
        "ullTotalPhys", "ullAvailPhys", "ullTotalPageFile", "ullAvailPageFile", "ullTotalVirtual", "ullAvailVirtual", "ullAvailExtendedVirtual")]


class PowerStatus(ctypes.Structure):
    _fields_ = [("ACLineStatus", ctypes.c_uint8), ("BatteryFlag", ctypes.c_uint8), ("BatteryLifePercent", ctypes.c_uint8), ("SystemStatusFlag", ctypes.c_uint8),
                ("BatteryLifeTime", ctypes.c_uint32), ("BatteryFullLifeTime", ctypes.c_uint32)]


def mem_gib():
    """Physical memory usable by Windows in GiB (GlobalMemoryStatusEx ullTotalPhys), or None."""
    status = MemoryStatus()
    status.dwLength = ctypes.sizeof(status)
    if not ctypes.WinDLL("kernel32").GlobalMemoryStatusEx(ctypes.byref(status)):
        return None
    return round(status.ullTotalPhys / 2**30, 1)


def cpu_name():
    """The processor's brand string from the registry, or None."""
    import winreg
    try:
        with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, r"HARDWARE\DESCRIPTION\System\CentralProcessor\0") as key:
            return winreg.QueryValueEx(key, "ProcessorNameString")[0].strip()
    except OSError:
        return None


def plan_name():
    """The active power plan's display name (PowerGetActiveScheme, PowerReadFriendlyName: UTF-16, in the display language), or None."""
    from ctypes import wintypes
    powrprof, k32 = ctypes.WinDLL("powrprof"), ctypes.WinDLL("kernel32")
    powrprof.PowerGetActiveScheme.argtypes = [ctypes.c_void_p, ctypes.POINTER(ctypes.c_void_p)]
    powrprof.PowerReadFriendlyName.argtypes = [ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p, ctypes.POINTER(wintypes.DWORD)]
    k32.LocalFree.argtypes = [ctypes.c_void_p]
    guid = ctypes.c_void_p()
    if powrprof.PowerGetActiveScheme(None, ctypes.byref(guid)) != 0:
        return None
    try:
        size = wintypes.DWORD(0)
        if powrprof.PowerReadFriendlyName(None, guid, None, None, None, ctypes.byref(size)) != 0 or not size.value:
            return None
        buf = ctypes.create_string_buffer(size.value)
        if powrprof.PowerReadFriendlyName(None, guid, None, None, buf, ctypes.byref(size)) != 0:
            return None
        return buf.raw[:size.value].decode("utf-16-le", "replace").rstrip("\0") or None
    finally:
        k32.LocalFree(guid)


def power_plan():
    """The active power plan and the power source, the Windows counterpart of a CPU governor: `Balanced plan, AC power`."""
    status = PowerStatus()
    line = status.ACLineStatus if ctypes.WinDLL("kernel32").GetSystemPowerStatus(ctypes.byref(status)) else None
    source = {1: "AC power", 0: "battery power"}.get(line, "unknown power source")
    return f"{plan_name() or 'unknown'} plan, {source}"


class JobMeter:
    """Meter with the interface of cgroup.Meter, backed by one Job Object."""

    mode = "windows-job"

    def __init__(self, api=None):
        self.api = api or Api()
        self.job = self.api.create_job()
        self.child = None
        self._peak_ws = {}
        self._lock = threading.Lock()
        self._sampling = False
        self.egress = set()

    def popen(self, argv, **kwargs):
        kwargs.setdefault("creationflags", 0)
        kwargs["creationflags"] |= getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0)
        self.child = subprocess.Popen(argv, **kwargs)
        handle = getattr(self.child, "_handle", None)
        if handle is None or not self.api.assign(self.job, int(handle)):
            self.child.kill()
            self.child.wait()
            raise OSError("could not assign the process to the job object")
        self.start_sampler()
        return self.child

    def start_sampler(self, interval=0.025):
        self._sampling = True

        def loop():
            while self._sampling:
                self._sample()
                time.sleep(interval)

        threading.Thread(target=loop, daemon=True).start()

    def _sample(self):
        try:
            pids = self.api.pids(self.job)
        except OSError:
            return
        with self._lock:
            for pid in pids:
                mem = self.api.process_memory(pid)
                if mem:
                    self._peak_ws[pid] = max(self._peak_ws.get(pid, 0), mem[0])
            handle = getattr(self.child, "_handle", None)
            if handle is not None:  # read through Popen's handle: a child that exits between two samples still reports its peak
                mem = self.api.handle_memory(int(handle))
                if mem:
                    self._peak_ws[self.child.pid] = max(self._peak_ws.get(self.child.pid, 0), mem[0])

    def cpu_ms(self):
        return self.api.accounting(self.job)[0] / HUNDRED_NS_PER_MS

    def peak_mem_mib(self):
        return self.api.peak_job_bytes(self.job) / 2**20

    def max_proc_rss_mib(self):
        """Sum of the peak working sets of every process seen in the job (a process that exits keeps its peak)."""
        self._sample()
        with self._lock:
            return sum(self._peak_ws.values()) / 2**20

    def current_pss_mib(self):
        """The private bytes (PrivateUsage, commit charge) of the tree now: Windows has no PSS."""
        total = 0
        for pid in self.api.pids(self.job):
            mem = self.api.process_memory(pid)
            if mem:
                total += mem[2]
        return total / 2**20

    def snapshot(self):
        cpu_100ns, active = self.api.accounting(self.job)
        threads_unknown = None
        handles = rss = 0
        for pid in self.api.pids(self.job):
            mem = self.api.process_memory(pid)
            if mem:
                rss += mem[1]
                handles += mem[3]
        return {"processes": active, "threads": threads_unknown, "fds": handles, "rss_mib": rss / 2**20, "pss_mib": self.current_pss_mib(), "cpu_ms": cpu_100ns / HUNDRED_NS_PER_MS}

    def kill(self):
        self.api.terminate(self.job)

    def close(self):
        self._sampling = False
        self.kill()
        self.api.close(self.job)
