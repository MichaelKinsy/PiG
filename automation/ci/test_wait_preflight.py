"""Tests of wait-preflight.py against a fake GitHub API; the clock is the fake's own, so no test sleeps."""
import importlib.util
import io
import json
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlparse

spec = importlib.util.spec_from_file_location("wait_preflight", Path(__file__).with_name("wait-preflight.py"))
wp = importlib.util.module_from_spec(spec)
spec.loader.exec_module(wp)

REPO = "o/r"
SHA = "a" * 40
WORKFLOW_PATH = "contents/.github/workflows/preflight.yml"


def run(run_id, status, conclusion=None, created="2026-11-17T11:54:35Z"):
    return {"id": run_id, "status": status, "conclusion": conclusion, "created_at": created, "html_url": "https://example.invalid/runs/%d" % run_id}


def answer(body, status=200, headers=None):
    return wp.Response(status, body if isinstance(body, str) else json.dumps(body), headers or {})


class FakeGitHub:
    """A GitHub whose runs and statuses are functions of the virtual time. sleep() advances that time."""

    def __init__(self, runs=lambda now: [], status=lambda now: None, workflow_on_default=200):
        self.now = 0
        self.runs = runs
        self.status = status  # (state, description) or None
        self.workflow_on_default = workflow_on_default
        self.requests = []  # (time, path)
        self.sleeps = []
        self.faults = {}  # path fragment -> responses to give before the real answer
        self.after_request = None  # called with the request count after each answer

    def sleep(self, seconds):
        self.sleeps.append(seconds)
        self.now += seconds

    def fetch(self, path):
        self.requests.append((self.now, path))
        queue = next((q for fragment, q in self.faults.items() if fragment in path and q), None)
        try:
            if queue:
                return queue.pop(0)
            if WORKFLOW_PATH in path:
                return answer({"type": "file"} if self.workflow_on_default == 200 else "Not Found", self.workflow_on_default)
            if "/actions/workflows/preflight.yml/runs" in path:
                return answer({"workflow_runs": self.runs(self.now)})
            if "/statuses" in path:
                current = self.status(self.now)
                other = {"context": "ci/other", "state": "failure", "description": "not ours"}
                listed = [other] + ([{"context": "preflight", "state": current[0], "description": current[1]}] if current else [])
                return answer(listed)
            return answer("unexpected " + path, 599)
        finally:
            if self.after_request:
                self.after_request(len(self.requests))

    def go(self, sha=SHA):
        out = io.StringIO()
        code = wp.main([REPO, sha, "main"], {"GH_TOKEN": "t"}, fetch=self.fetch, sleep=self.sleep, out=out)
        return code, out.getvalue()

    def paths(self, fragment):
        return [path for _, path in self.requests if fragment in path]


class WaitOnTheRunTests(unittest.TestCase):
    def test_queued_far_longer_than_the_old_wall_clock_then_success(self):
        # The old script gave up 900 s after it started. The run is queued for 2400 s, runs for 120 s, then records success.
        def runs(now):
            return [run(7, "queued" if now < 2400 else "in_progress")]

        gh = FakeGitHub(runs=runs, status=lambda now: ("success", "6 checks passed") if now >= 2520 else None)
        code, out = gh.go()
        self.assertEqual(0, code, out)
        self.assertGreater(gh.now, 900)
        self.assertGreaterEqual(gh.now, 2520)
        self.assertIn("waiting for pre-flight run 7", out)
        self.assertIn("pre-flight passed for " + SHA, out)
        self.assertNotIn("::error::", out)

    def test_waiting_is_paced_by_state_not_by_a_clock(self):
        gh = FakeGitHub(runs=lambda now: [run(7, "queued" if now < 600 else "in_progress")], status=lambda now: ("success", "ok") if now >= 700 else None)
        gh.go()
        self.assertEqual([wp.QUEUED_POLL_SECONDS] * 10 + [wp.RUNNING_POLL_SECONDS] * 5, gh.sleeps)

    def test_in_progress_then_failure_reports_the_reason_of_the_pre_flight(self):
        gh = FakeGitHub(runs=lambda now: [run(7, "in_progress")], status=lambda now: ("failure", "2 failed: dco, gitleaks") if now >= 60 else ("pending", "scans still to run: gitleaks"))
        code, out = gh.go()
        self.assertEqual(1, code)
        self.assertIn("::error::the pre-flight rejected " + SHA, out)
        self.assertIn("2 failed: dco, gitleaks", out)
        self.assertEqual(3, len(gh.sleeps))

    def test_error_state_fails_like_failure(self):
        gh = FakeGitHub(runs=lambda now: [run(7, "in_progress")], status=lambda now: ("error", "the engine broke"))
        code, out = gh.go()
        self.assertEqual(1, code)
        self.assertIn("the engine broke", out)

    def test_completed_cancelled_without_a_status_fails_at_once_naming_it(self):
        gh = FakeGitHub(runs=lambda now: [run(7, "completed", "cancelled")], status=lambda now: None)
        code, out = gh.go()
        self.assertEqual(1, code)
        self.assertEqual([], gh.sleeps, "the wait must end at once: no run is left to set a verdict")
        self.assertIn("::error::pre-flight run 7", out)
        self.assertIn("completed cancelled without a verdict", out)
        self.assertIn("status none", out)

    def test_every_conclusion_without_a_verdict_fails_at_once(self):
        for conclusion in ("cancelled", "skipped", "startup_failure", "failure", "timed_out", "success"):
            with self.subTest(conclusion):
                gh = FakeGitHub(runs=lambda now, c=conclusion: [run(7, "completed", c)], status=lambda now: None)
                code, out = gh.go()
                self.assertEqual(1, code)
                self.assertEqual([], gh.sleeps)
                self.assertIn("completed %s without a verdict" % conclusion, out)

    def test_completed_with_a_pending_status_says_the_sweep_decides(self):
        gh = FakeGitHub(runs=lambda now: [run(7, "completed", "success")], status=lambda now: ("pending", "waits for the next run: gitleaks"))
        code, out = gh.go()
        self.assertEqual(1, code)
        self.assertEqual([], gh.sleeps)
        self.assertIn("status pending: waits for the next run: gitleaks", out)
        self.assertIn("the hourly pre-flight sweep sets the verdict; rerun this job after it", out)

    def test_a_pending_status_does_not_end_the_wait_while_a_run_is_active(self):
        gh = FakeGitHub(
            runs=lambda now: [run(7, "in_progress" if now < 100 else "completed", None if now < 100 else "success")],
            status=lambda now: ("success", "ok") if now >= 100 else ("pending", "scans still to run: gitleaks"),
        )
        code, _ = gh.go()
        self.assertEqual(0, code)

    def test_no_run_at_all_fails_after_ten_minutes_of_settling(self):
        gh = FakeGitHub()
        code, out = gh.go()
        self.assertEqual(1, code)
        self.assertEqual(wp.SETTLING_READS, len(gh.paths("/actions/workflows/preflight.yml/runs")))
        self.assertEqual(600, wp.SETTLING_READS * wp.SETTLING_SECONDS)
        self.assertEqual(600 - wp.SETTLING_SECONDS, gh.now)
        self.assertIn("::error::no pre-flight run for this head " + SHA, out)
        self.assertIn("none was created within 10 minutes", out)

    def test_a_run_created_late_is_waited_for(self):
        # Actions is slow: the pull_request_target run appears 7 minutes after the head was pushed.
        gh = FakeGitHub(runs=lambda now: [] if now < 420 else [run(7, "queued")], status=lambda now: ("success", "ok") if now >= 900 else None)
        code, out = gh.go()
        self.assertEqual(0, code, out)

    def test_the_settling_count_starts_again_after_a_run_is_seen(self):
        # Five empty reads, one read that lists a queued run, then ten empty reads: 15 minutes, not 10, to the verdict.
        gh = FakeGitHub(runs=lambda now: [run(7, "queued")] if now == 300 else [])
        code, out = gh.go()
        self.assertEqual(1, code)
        self.assertEqual(900, gh.now, out)

    def test_any_active_run_keeps_the_wait_even_when_a_newer_one_was_cancelled(self):
        # An edited event cancelled run 8; rerunning run 7 makes it active again.
        runs = [run(8, "completed", "cancelled", "2026-11-17T12:00:00Z"), run(7, "in_progress", None, "2026-11-17T11:00:00Z")]
        gh = FakeGitHub(runs=lambda now: runs if now < 40 else [runs[0], run(7, "completed", "success", "2026-11-17T11:00:00Z")], status=lambda now: ("success", "ok") if now >= 40 else None)
        code, _ = gh.go()
        self.assertEqual(0, code)

    def test_all_runs_completed_names_the_newest(self):
        runs = [run(7, "completed", "cancelled", "2026-11-17T11:00:00Z"), run(9, "completed", "skipped", "2026-11-17T12:00:00Z")]
        gh = FakeGitHub(runs=lambda now: runs)
        code, out = gh.go()
        self.assertEqual(1, code)
        self.assertIn("run 9", out)
        self.assertIn("completed skipped", out)

    def test_the_runs_are_listed_before_the_status_is_read(self):
        # The run ends and its verdict lands right after the run listing (the second request: the first reads the default branch). Listing the runs first sees the run still active
        # and then the verdict; reading the status first would see no status and then a completed run: a false "no verdict".
        state = {"done": False}
        gh = FakeGitHub(
            runs=lambda now: [run(7, "completed", "success") if state["done"] else run(7, "in_progress")],
            status=lambda now: ("success", "ok") if state["done"] else None,
        )
        gh.after_request = lambda n: state.update(done=True) if n == 2 else None
        code, out = gh.go()
        self.assertEqual(0, code, out)
        listing = [i for i, (_, p) in enumerate(gh.requests) if "/actions/workflows/" in p and "/runs" in p]
        statuses = [i for i, (_, p) in enumerate(gh.requests) if "/statuses" in p]
        self.assertLess(listing[0], statuses[0])

    def test_the_request_selects_the_runs_of_this_head_and_event(self):
        gh = FakeGitHub(runs=lambda now: [run(7, "completed", "success")], status=lambda now: ("success", "ok"))
        gh.go()
        (path,) = gh.paths("/runs")
        query = parse_qs(urlparse(path).query)
        self.assertEqual({"head_sha": [SHA], "event": ["pull_request_target"], "per_page": ["100"]}, query)

    def test_an_unknown_status_state_fails(self):
        gh = FakeGitHub(runs=lambda now: [run(7, "in_progress")], status=lambda now: ("weird", ""))
        code, out = gh.go()
        self.assertEqual(1, code)
        self.assertIn('unknown pre-flight state "weird"', out)

    def test_the_status_of_another_context_is_not_a_verdict(self):
        gh = FakeGitHub(runs=lambda now: [run(7, "completed", "cancelled")], status=lambda now: None)
        code, out = gh.go()  # the fake lists a failing "ci/other" status first
        self.assertEqual(1, code)
        self.assertIn("without a verdict", out)


class ApiFaultTests(unittest.TestCase):
    def passing(self):
        return FakeGitHub(runs=lambda now: [run(7, "completed", "success")], status=lambda now: ("success", "ok"))

    def test_a_fault_is_retried_and_the_wait_goes_on(self):
        for fault in (answer("bad gateway", 502), answer("unavailable", 503), answer("slow down", 429), answer("limit", 403, {"X-RateLimit-Remaining": "0"}), answer("secondary rate limit", 403), wp.Response(0, "connection reset")):
            with self.subTest(fault.status):
                gh = self.passing()
                gh.faults["/statuses"] = [fault, fault]
                code, out = gh.go()
                self.assertEqual(0, code, out)
                self.assertEqual(list(wp.FAULT_BACKOFF[:2]), gh.sleeps)
                self.assertEqual(3, len(gh.paths("/statuses")))

    def test_a_fault_that_lasts_is_surfaced_after_a_bounded_number_of_tries(self):
        self.assertEqual(4, wp.FAULT_ATTEMPTS)
        gh = self.passing()
        gh.faults["/statuses"] = [answer("bad gateway", 502)] * 50
        code, out = gh.go()
        self.assertEqual(1, code)
        self.assertEqual(wp.FAULT_ATTEMPTS, len(gh.paths("/statuses")))
        self.assertEqual(list(wp.FAULT_BACKOFF), gh.sleeps)
        self.assertIn("::error::GitHub API fault on GET", out)
        self.assertIn("after 4 tries", out)
        self.assertIn("HTTP 502", out)

    def test_the_run_listing_is_retried_too(self):
        gh = self.passing()
        gh.faults["/actions/workflows/"] = [answer("boom", 500)] * 3
        code, _ = gh.go()
        self.assertEqual(0, code)
        self.assertEqual(4, len(gh.paths("/actions/workflows/")))

    def test_an_answer_that_is_not_a_fault_is_not_retried(self):
        for status in (401, 403, 404, 422):
            with self.subTest(status):
                gh = self.passing()
                gh.faults["/statuses"] = [answer("no", status)] * 5
                code, out = gh.go()
                self.assertEqual(1, code)
                self.assertEqual(1, len(gh.paths("/statuses")))
                self.assertEqual([], gh.sleeps)
                self.assertIn("HTTP %d" % status, out)

    def test_a_fault_while_waiting_costs_only_its_backoff(self):
        def world():
            return FakeGitHub(runs=lambda now: [run(7, "queued" if now < 300 else "in_progress")], status=lambda now: ("success", "ok") if now >= 400 else None)

        clean, faulty = world(), world()
        faulty.faults["/statuses"] = [answer("x", 502)]
        self.assertEqual(0, clean.go()[0])
        self.assertEqual(0, faulty.go()[0])
        self.assertEqual(clean.now + wp.FAULT_BACKOFF[0], faulty.now)


class DefaultBranchTests(unittest.TestCase):
    def test_the_wait_is_skipped_when_the_default_branch_lacks_the_workflow(self):
        with tempfile.TemporaryDirectory() as directory:
            summary = Path(directory) / "summary"
            gh = FakeGitHub(workflow_on_default=404)
            out = io.StringIO()
            code = wp.main([REPO, SHA, "main"], {"GH_TOKEN": "t", "GITHUB_STEP_SUMMARY": str(summary)}, fetch=gh.fetch, sleep=gh.sleep, out=out)
            self.assertEqual(0, code)
            self.assertIn("::notice::pre-flight is not on the default branch yet: verdict skipped", out.getvalue())
            self.assertIn("is absent from main", summary.read_text())
        self.assertEqual([], gh.paths("/statuses"))
        self.assertEqual("repos/o/r/contents/.github/workflows/preflight.yml?ref=main", gh.requests[0][1])

    def test_an_error_on_the_default_branch_keeps_the_wait(self):
        gh = FakeGitHub(runs=lambda now: [run(7, "completed", "cancelled")])
        gh.faults[WORKFLOW_PATH] = [answer("boom", 502)] * 10
        code, out = gh.go()
        self.assertEqual(1, code)
        self.assertNotIn("verdict skipped", out)
        self.assertIn("without a verdict", out)

    def test_other_answers_keep_the_wait(self):
        for status in (200, 403):
            with self.subTest(status):
                gh = FakeGitHub(runs=lambda now: [run(7, "completed", "success")], status=lambda now: ("success", "ok"), workflow_on_default=status)
                code, out = gh.go()
                self.assertEqual(0, code)
                self.assertNotIn("verdict skipped", out)


class Handler(BaseHTTPRequestHandler):
    log = []
    script = []  # statuses of the status endpoint, one per request

    def do_GET(self):
        Handler.log.append((self.path, self.headers.get("Authorization")))
        path = urlparse(self.path).path
        if path.endswith("/runs"):
            body = {"workflow_runs": [run(7, "in_progress")]}
        elif path.endswith("/statuses"):
            if Handler.script:
                code = Handler.script.pop(0)
                if code != 200:
                    self.send_response(code)
                    self.end_headers()
                    return
            body = [{"context": "preflight", "state": "success", "description": "6 checks passed"}]
        else:
            body = {"type": "file"}
        data = json.dumps(body).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *args):
        pass


class HttpTests(unittest.TestCase):
    def setUp(self):
        Handler.log, Handler.script = [], []
        self.server = HTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        self.env = {"GH_TOKEN": "secret-token", "GITHUB_API_URL": "http://127.0.0.1:%d" % self.server.server_port}

    def test_the_real_fetch_sends_the_token_and_reads_the_verdict(self):
        out, sleeps = io.StringIO(), []
        code = wp.main([REPO, SHA, "main"], self.env, sleep=sleeps.append, out=out)
        self.assertEqual(0, code, out.getvalue())
        self.assertEqual([], sleeps)
        self.assertTrue(Handler.log)
        self.assertTrue(all(auth == "Bearer secret-token" for _, auth in Handler.log), Handler.log)
        self.assertTrue(any("/actions/workflows/preflight.yml/runs?head_sha=%s&event=pull_request_target" % SHA in path for path, _ in Handler.log))

    def test_the_real_fetch_retries_a_5xx_and_succeeds(self):
        Handler.script = [503, 502]
        out, sleeps = io.StringIO(), []
        code = wp.main([REPO, SHA, "main"], self.env, sleep=sleeps.append, out=out)
        self.assertEqual(0, code, out.getvalue())
        self.assertEqual(list(wp.FAULT_BACKOFF[:2]), sleeps)

    def test_a_refused_connection_is_a_fault_that_is_surfaced(self):
        self.server.shutdown()
        self.server.server_close()
        out, sleeps = io.StringIO(), []
        code = wp.main([REPO, SHA, "main"], self.env, sleep=sleeps.append, out=out)
        self.assertEqual(1, code)
        self.assertIn("GitHub API fault", out.getvalue())
        self.assertEqual(list(wp.FAULT_BACKOFF), sleeps[: len(wp.FAULT_BACKOFF)])


if __name__ == "__main__":
    unittest.main()
