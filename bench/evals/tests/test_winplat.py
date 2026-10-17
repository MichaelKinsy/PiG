# SPDX-License-Identifier: MIT
"""The Windows Job Object meter against a fake Win32 API. Expected values are written by hand from the documented units (100 ns CPU ticks, bytes).
HostTest reads the real Win32 API and runs on Windows only."""
import ctypes
import os
import unittest

from pigbench import winplat


class FakeApi:
    def __init__(self):
        self.calls = []
        self.cpu_100ns, self.active, self.peak_job = 0, 0, 0
        self.procs = {}  # pid -> (peak_ws, ws, private, handles)
        self.handles = {}  # open process handle -> the same counters, kept after the process exits

    def create_job(self):
        self.calls.append("create")
        return 7

    def assign(self, job, handle):
        self.calls.append(("assign", job, handle))
        return True

    def accounting(self, job):
        return self.cpu_100ns, self.active

    def peak_job_bytes(self, job):
        return self.peak_job

    def pids(self, job):
        return sorted(self.procs)

    def process_memory(self, pid):
        return self.procs.get(pid)

    def handle_memory(self, handle):
        return self.handles.get(handle)

    def terminate(self, job):
        self.calls.append(("terminate", job))

    def close(self, handle):
        self.calls.append(("close", handle))


class JobMeterTest(unittest.TestCase):
    def test_struct_sizes_match_the_documented_x64_layouts(self):
        if ctypes.sizeof(ctypes.c_size_t) != 8:
            self.skipTest("the documented sizes are the 64-bit ones")
        self.assertEqual(ctypes.sizeof(winplat.BasicAccounting), 48)
        self.assertEqual(ctypes.sizeof(winplat.ExtendedLimit), 144)
        self.assertEqual(ctypes.sizeof(winplat.ProcessMemory), 80)

    def test_cpu_is_converted_from_100ns_ticks(self):
        api = FakeApi()
        api.cpu_100ns = 25_000_000  # 2.5 s
        self.assertEqual(winplat.JobMeter(api).cpu_ms(), 2500.0)

    def test_peak_job_memory_is_in_mib(self):
        api = FakeApi()
        api.peak_job = 300 * 2**20
        self.assertEqual(winplat.JobMeter(api).peak_mem_mib(), 300.0)

    def test_peak_working_sets_survive_a_process_that_exits(self):
        api = FakeApi()
        meter = winplat.JobMeter(api)
        api.procs = {1: (50 * 2**20, 40 * 2**20, 30 * 2**20, 100), 2: (20 * 2**20, 20 * 2**20, 10 * 2**20, 50)}
        self.assertEqual(meter.max_proc_rss_mib(), 70.0)
        del api.procs[2]  # the child exits
        api.procs[1] = (50 * 2**20, 10 * 2**20, 5 * 2**20, 90)
        self.assertEqual(meter.max_proc_rss_mib(), 70.0)  # its 20 MiB peak is kept

    def test_a_child_that_exits_before_any_sample_keeps_its_peak(self):
        # `pig --version` runs about 20 ms, less than the 25 ms sampling interval: the job lists no process by the time it is sampled.
        from types import SimpleNamespace
        api = FakeApi()
        meter = winplat.JobMeter(api)
        meter.child = SimpleNamespace(pid=9, _handle=123)
        api.handles = {123: (12 * 2**20, 0, 0, 0)}
        self.assertEqual(meter.max_proc_rss_mib(), 12.0)

    def test_pss_is_the_private_bytes_and_snapshot_counts_handles(self):
        api = FakeApi()
        api.cpu_100ns, api.active = 10_000, 2
        api.procs = {1: (50 * 2**20, 40 * 2**20, 30 * 2**20, 100), 2: (20 * 2**20, 20 * 2**20, 10 * 2**20, 50)}
        meter = winplat.JobMeter(api)
        self.assertEqual(meter.current_pss_mib(), 40.0)
        snap = meter.snapshot()
        self.assertEqual((snap["processes"], snap["fds"], snap["rss_mib"], snap["pss_mib"], snap["cpu_ms"]), (2, 150, 60.0, 40.0, 1.0))

    def test_close_terminates_the_job_before_closing_its_handle(self):
        api = FakeApi()
        winplat.JobMeter(api).close()
        self.assertEqual(api.calls[-2:], [("terminate", 7), ("close", 7)])

    def test_a_process_that_cannot_join_the_job_is_killed_and_reported(self):
        api = FakeApi()
        api.assign = lambda job, handle: False
        meter = winplat.JobMeter(api)
        import sys
        with self.assertRaises(OSError):
            meter.popen([sys.executable, "-c", "import time; time.sleep(5)"])
        self.assertIsNotNone(meter.child.poll())  # killed and reaped, not left running


@unittest.skipUnless(os.name == "nt", "reads the Win32 API")
class HostTest(unittest.TestCase):
    def test_the_environment_block_names_memory_cpu_and_power_plan(self):
        from pigbench import envinfo
        self.assertGreater(envinfo.mem_gib(), 0)
        self.assertNotIn("Family", envinfo.cpu_model())  # the brand string, not platform.processor()'s family/model/stepping
        self.assertTrue(winplat.plan_name())  # read through the Power API, so a localized name needs no code page
        self.assertEqual(envinfo.governor(), f"{winplat.plan_name()} plan, " + {1: "AC power", 0: "battery power"}.get(self.ac_line(), "unknown power source"))

    def ac_line(self):
        status = winplat.PowerStatus()
        return status.ACLineStatus if ctypes.WinDLL("kernel32").GetSystemPowerStatus(ctypes.byref(status)) else None

    def test_cpu_time_is_charged_in_whole_clock_ticks(self):
        # The reports state the tick and average CPU because of this: a busy child's job CPU time is a whole number of ticks.
        import sys
        tick_100ns = round(winplat.clock_tick_ms() * winplat.HUNDRED_NS_PER_MS)
        meter = winplat.JobMeter()
        try:
            meter.popen([sys.executable, "-c", "import time\nend = time.perf_counter() + 0.3\nwhile time.perf_counter() < end: pass"]).wait(30)
            cpu_100ns = winplat.Api().accounting(meter.job)[0]
        finally:
            meter.close()
        self.assertGreater(cpu_100ns, 0)
        self.assertEqual(cpu_100ns % tick_100ns, 0, (cpu_100ns, tick_100ns))


class ArchiveTest(unittest.TestCase):
    def test_zip_and_tar_members_are_extracted_by_base_name(self):
        import os, tarfile, tempfile, zipfile
        from pigbench import arms
        with tempfile.TemporaryDirectory() as tmp:
            z = os.path.join(tmp, "a.zip")
            with zipfile.ZipFile(z, "w") as f:
                f.writestr("pig-0.4.1-windows-amd64/pig.exe", b"MZ-bytes")
                f.writestr("pig-0.4.1-windows-amd64/README.md", b"x")
            out = os.path.join(tmp, "o")
            os.makedirs(out)
            arms.extract_member(z, "pig.exe", out, "t", "a.zip")
            self.assertEqual(open(os.path.join(out, "pig.exe"), "rb").read(), b"MZ-bytes")
            t = os.path.join(tmp, "a.tar.xz")
            src = os.path.join(tmp, "bin")
            open(src, "wb").write(b"ELF")
            with tarfile.open(t, "w:xz") as f:
                f.add(src, arcname="dir/pi")
            arms.extract_member(t, "pi", out, "t", "a.tar.xz")
            self.assertEqual(open(os.path.join(out, "pi"), "rb").read(), b"ELF")
            with self.assertRaises(SystemExit):
                arms.extract_member(z, "missing.exe", out, "t", "a.zip")


if __name__ == "__main__":
    unittest.main()
