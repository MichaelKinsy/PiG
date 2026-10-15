# SPDX-License-Identifier: MIT
"""The measurement code against stub harnesses whose timing is known by construction, so the expected numbers do not come from the code under test."""
import os
import subprocess
import sys
import tempfile
import textwrap
import time
import unittest

from pigbench import cgroup, egress, longhistory, mock, procio

STUB = textwrap.dedent('''
    import http.client, json, sys, time, os
    port, delay, rounds = int(sys.argv[1]), float(sys.argv[2]), int(sys.argv[3])
    time.sleep(delay)
    msgs = [{"role": "user", "content": "go"}]
    tools = [{"type": "function", "function": {"name": "read", "parameters": {"properties": {"path": {}}}}}]
    for i in range(rounds + 1):
        c = http.client.HTTPConnection("127.0.0.1", port)
        c.request("POST", "/v1/chat/completions", json.dumps({"model": "m", "stream": False, "messages": msgs, "tools": tools}))
        reply = json.loads(c.getresponse().read())
        c.close()
        m = reply["choices"][0]["message"]
        msgs.append({"role": "assistant", "content": m.get("content"), "tool_calls": m.get("tool_calls")})
        if m.get("tool_calls"):
            time.sleep(0.05)  # the "tool"
            msgs.append({"role": "tool", "tool_call_id": m["tool_calls"][0]["id"], "content": "ok"})
    print(m["content"])
''')


class MeasureTest(unittest.TestCase):
    def test_a_cgroup_meter_without_cgroup_procs_has_no_snapshot(self):
        # The soak sampler fails the run on None ("no per-process /proc accounting"); a zero sample would be recorded as data.
        meter = cgroup.CgroupMeter(os.path.join(self.tmp, "gone"))
        self.assertIsNone(meter.snapshot())
        self.assertEqual(meter.pids(), [])

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.script = os.path.join(self.tmp, "stub.py")
        with open(self.script, "w") as f:
            f.write(STUB)

    def run_stub(self, srv, delay, rounds):
        meter = cgroup.new_meter()
        srv.on_request = lambda r: meter.cpu_ms()
        try:
            res = procio.Batch([sys.executable, self.script, str(srv.port), str(delay), str(rounds)], dict(os.environ), self.tmp, meter, 60).run()
        finally:
            meter.close()
        return res

    def test_first_request_time_follows_the_known_startup_delay(self):
        with mock.MockServer(policy=mock.agentic_policy(self.tmp, tool_turns=0)) as srv:
            res = self.run_stub(srv, 0.4, 0)
        self.assertEqual(res["code"], 0)
        first = (srv.requests[0].t_recv - res["t_spawn"]) / 1e6
        self.assertGreaterEqual(first, 400)
        self.assertLess(first, 1500)

    def test_turn_gap_includes_the_known_tool_time(self):
        with mock.MockServer(policy=mock.agentic_policy(self.tmp, tool_turns=3)) as srv:
            res = self.run_stub(srv, 0.0, 3)
        self.assertEqual(res["code"], 0)
        self.assertEqual(len(srv.requests), 4)
        gaps = [(b.t_recv - a.t_end) / 1e6 for a, b in zip(srv.requests, srv.requests[1:])]
        for g in gaps:
            # The stub sleeps 50 ms per tool call after it has read the reply, and the mock stamps the reply's end before its last write,
            # so a gap is at least the stub's sleep by construction, however the threads are scheduled.
            self.assertGreaterEqual(g, 50)
            self.assertLess(g, 400)

    def test_a_handler_that_runs_late_after_writing_does_not_shrink_the_gap(self):
        # The handler thread may wait to run again after its write reached the client (a busy host, the GIL). Simulate a 30 ms wait:
        # a reply end stamped after the write would make each gap about 30 ms shorter than the stub's 50 ms sleep.
        with mock.MockServer(policy=mock.agentic_policy(self.tmp, tool_turns=3)) as srv:
            handler = srv.httpd.RequestHandlerClass
            original = handler._json

            def late(self, *args, **kwargs):
                original(self, *args, **kwargs)
                time.sleep(0.03)

            handler._json = late
            res = self.run_stub(srv, 0.0, 3)
        self.assertEqual(res["code"], 0)
        gaps = [(b.t_recv - a.t_end) / 1e6 for a, b in zip(srv.requests, srv.requests[1:])]
        self.assertEqual(len(gaps), 3)
        for g in gaps:
            self.assertGreaterEqual(g, 50)

    def test_cpu_hook_sees_cpu_grow_between_requests(self):
        busy = os.path.join(self.tmp, "busy.py")
        with open(busy, "w") as f:
            f.write(STUB.replace("time.sleep(0.05)  # the \"tool\"", "x = sum(i * i for i in range(2_000_000))"))
        with mock.MockServer(policy=mock.agentic_policy(self.tmp, tool_turns=2)) as srv:
            meter = cgroup.new_meter()
            srv.on_request = lambda r: meter.cpu_ms()
            try:
                procio.Batch([sys.executable, busy, str(srv.port), "0", "2"], dict(os.environ), self.tmp, meter, 60).run()
            finally:
                meter.close()
        cpus = [b.hook - a.hook for a, b in zip(srv.requests, srv.requests[1:])]
        self.assertTrue(all(c > 20 for c in cpus), cpus)  # two million multiplications cost well over 20 ms of CPU

    def test_memory_peak_sees_an_allocation(self):
        grab = os.path.join(self.tmp, "grab.py")
        with open(grab, "w") as f:
            f.write("b = bytearray(150 * 1024 * 1024)\nfor i in range(0, len(b), 4096): b[i] = 1\nprint('ok')\n")
        meter = cgroup.new_meter()
        try:
            res = procio.Batch([sys.executable, grab], dict(os.environ), self.tmp, meter, 60).run()
        finally:
            meter.close()
        self.assertEqual(res["code"], 0)
        peak = res["max_proc_rss_mib"] if res["max_proc_rss_mib"] else res["peak_mem_mib"]
        self.assertGreater(peak, 140)

    @unittest.skipUnless(sys.platform.startswith("linux"), "socket audit reads /proc")
    def test_egress_audit_flags_a_connection_that_leaves_the_loopback(self):
        meter = cgroup.new_meter()
        if meter.mode == "rusage":
            meter.close()
            self.skipTest("no cgroup meter on this host, so no socket audit")
        egress.take()
        script = os.path.join(self.tmp, "leak.py")
        with open(script, "w") as f:  # TEST-NET-1 (RFC 5737): never routable; the SYN stays pending long enough to be sampled
            f.write("import socket\ns = socket.socket(); s.settimeout(1.5)\ntry:\n    s.connect(('192.0.2.1', 9))\nexcept OSError:\n    pass\n")
        try:
            procio.Batch([sys.executable, script], dict(os.environ), self.tmp, meter, 30).run()
        finally:
            meter.close()
        self.assertIn("192.0.2.1:9", egress.take())

    @unittest.skipUnless(sys.platform.startswith("linux"), "socket audit reads /proc")
    def test_egress_audit_also_runs_under_the_rusage_meter(self):
        # A Linux host without a delegated cgroup or a user systemd falls back to rusage; the report still says socket-audit, so the audit must run.
        meter = cgroup.new_meter("rusage")
        egress.take()
        script = os.path.join(self.tmp, "leak.py")
        with open(script, "w") as f:  # TEST-NET-1 (RFC 5737), as above, from a grandchild so the tree walk is exercised
            f.write("import subprocess, sys\nsubprocess.run([sys.executable, '-c', \"import socket\\ns = socket.socket(); s.settimeout(1.5)\\ntry:\\n    s.connect(('192.0.2.1', 9))\\nexcept OSError:\\n    pass\\n\"])\n")
        try:
            res = procio.Batch([sys.executable, script], dict(os.environ), self.tmp, meter, 30).run()
        finally:
            meter.close()
        self.assertEqual(res["meter"], "rusage")
        self.assertEqual(res["code"], 0, res["stderr"])
        self.assertIn("192.0.2.1:9", egress.take())

    def test_rusage_wait_after_the_child_was_reaped(self):
        # The PTY scenario waits for the exit, then Pty.close() polls and waits again; under rusage that second wait4 used to raise ChildProcessError.
        meter = cgroup.new_meter("rusage")
        try:
            proc = meter.popen([sys.executable, "-c", "pass"])
            self.assertTrue(procio.wait(meter, proc, 30))
            self.assertIsNotNone(meter.cpu_ms())
            proc.poll()
            self.assertTrue(procio.wait(meter, proc, 1))
            other = cgroup.new_meter("rusage")
            child = other.popen([sys.executable, "-c", "pass"])
            child.wait(30)  # reaped by Popen, so wait4 finds no child
            self.assertTrue(procio.wait(other, child, 1))
            other.close()
        finally:
            meter.close()

    @unittest.skipUnless(sys.platform.startswith("linux"), "reads /proc and the process CPU clock")
    def test_rusage_meter_measures_a_running_tree(self):
        # A pod's cgroup is read-only, so bench/k8s runs under this meter, and longhistory reads the CPU time, the peak RSS
        # and the current RSS while the agent runs. Clock ticks would make every CPU time a multiple of 10 ms.
        meter = cgroup.new_meter("rusage")
        script = os.path.join(self.tmp, "hold.py")
        with open(script, "w") as f:  # the measured process is a grandchild: 100 MiB touched, then about 0.3 s of CPU, then idle
            f.write("import subprocess, sys\nsubprocess.run([sys.executable, '-c', \"import time\\nb = bytearray(100 << 20)\\nfor i in range(0, len(b), 4096): b[i] = 1\\n"
                    "t = time.process_time()\\nwhile time.process_time() - t < 0.3: pass\\nprint('ready', flush=True)\\ntime.sleep(30)\"])\n")
        try:
            proc = meter.popen([sys.executable, script], stdout=subprocess.PIPE)
            self.assertEqual(proc.stdout.readline().strip(), b"ready")
            cpu = meter.cpu_ms()
            self.assertGreater(cpu, 300)
            self.assertNotEqual(cpu % 10, 0, cpu)
            self.assertGreater(meter.max_proc_rss_mib() or 0, 100)
            self.assertGreater(longhistory.rss_now(meter) or 0, 100)
        finally:
            meter.close()
            proc.wait(10)

    @unittest.skipIf(sys.platform == "win32", "the rusage meter reads the tree from ps; Windows meters with a Job Object")
    def test_tree_rows_follow_descendants(self):
        meter = cgroup.new_meter("rusage")
        try:
            proc = meter.popen(["sh", "-c", "sleep 3 & sleep 3 & wait"])
            deadline = time.monotonic() + 10
            rows = {}
            while time.monotonic() < deadline and len(rows) < 3:
                rows = cgroup._tree_rows(proc.pid)
                time.sleep(0.05)
            self.assertEqual(len(rows), 3, rows)  # the shell and its two sleeps
            self.assertEqual(sum(1 for ppid, _, _ in rows.values() if ppid == proc.pid), 2)
        finally:
            meter.close()

    def test_egress_audit_is_quiet_for_loopback_only_traffic(self):
        egress.take()
        with mock.MockServer(policy=mock.agentic_policy(self.tmp, tool_turns=1)) as srv:
            self.run_stub(srv, 0.0, 1)
        self.assertEqual(egress.take(), set())

    @unittest.skipUnless(sys.platform.startswith("linux"), "smaps_rollup")
    def test_pss_sees_private_memory_of_the_tree(self):
        grab = os.path.join(self.tmp, "grab2.py")
        with open(grab, "w") as f:
            f.write("import time\nb = bytearray(120 * 1024 * 1024)\nfor i in range(0, len(b), 4096): b[i] = 1\nprint('up', flush=True)\ntime.sleep(3)\n")
        meter = cgroup.new_meter()
        if meter.mode == "rusage":
            meter.close()
            self.skipTest("no cgroup meter")
        try:
            proc = meter.popen([sys.executable, grab], stdout=__import__("subprocess").PIPE)
            proc.stdout.readline()
            pss = meter.current_pss_mib()
            proc.wait()
        finally:
            meter.close()
        self.assertGreater(pss, 110)  # private pages count in full
        self.assertLess(pss, 200)

    def test_the_ten_extension_fixtures_resolve_from_the_package(self):
        from pigbench import overhead
        paths = overhead.extensions()
        self.assertEqual(len(paths), 10)
        self.assertTrue(all(os.path.isfile(p) for p in paths))

    def test_terminal_queries_are_answered_once_across_chunks(self):
        da = b"\x1b[c"
        first = b"hello" + da
        self.assertEqual(procio.new_queries(b"", first, da), 1)
        self.assertEqual(procio.new_queries(first[-16:], b"more output", da), 0)  # still in the carried-over tail: answered already
        self.assertEqual(procio.new_queries(b"abc\x1b", b"[c and " + da, da), 2)  # split across the boundary, then a new one
        self.assertEqual(procio.new_queries(b"", da + da, da), 2)

    @unittest.skipIf(sys.platform == "win32", "procio.Pty is a POSIX pseudo-terminal; the PTY scenarios do not run on Windows")
    def test_a_key_counts_as_accepted_only_when_the_application_echoes_it(self):
        from pigbench.overhead import ANSI
        seen = {}
        for echo in (True, False):
            meter = cgroup.RusageMeter()
            term = procio.Pty(["sh", "-c", "printf 'frame\\n'; sleep 3"], dict(os.environ), self.tmp, meter, echo=echo)  # never reads its input
            try:
                term.wait_text("frame", 10)
                mark = len(term.chunks)
                term.type("x")
                time.sleep(0.3)
                seen[echo] = any(b"x" in ANSI.sub(b"", c) for _, c in term.chunks[mark:])
            finally:
                term.close(grace=1)
                meter.close()
        self.assertEqual(seen, {True: True, False: False})  # the kernel echoes in cooked mode; with echo off only the application can

    def test_concurrent_checkpoint_writers_use_their_own_files(self):
        from pigbench import checkpoint
        checkpoint.save({"a": 1}, self.tmp, "raw.partial.x.json")
        checkpoint.save({"b": 2}, self.tmp, "raw.partial.y.json")
        self.assertTrue(os.path.exists(os.path.join(self.tmp, "raw.partial.x.json")))
        checkpoint.clear(self.tmp, "raw.partial.x.json")
        self.assertFalse(os.path.exists(os.path.join(self.tmp, "raw.partial.x.json")))
        self.assertTrue(os.path.exists(os.path.join(self.tmp, "raw.partial.y.json")))

    def test_parse_ps_formats(self):
        rows = cgroup.parse_ps("  10     1  2048  0:01.50\n  11    10  1024  1:02:03\n")
        self.assertEqual(rows[10], (1, 2048, 1500.0))
        self.assertEqual(rows[11][2], (3600 + 120 + 3) * 1000)


if __name__ == "__main__":
    unittest.main()
