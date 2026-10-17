# SPDX-License-Identifier: MIT
import json
import os
import tempfile
import unittest

from pigbench import longhistory, reliability, report


def cell(signature, inv=()):
    return {"signature": signature, "invariant_failures": list(inv)}


class JudgeTest(unittest.TestCase):
    def result(self, cells):
        return {"options": {"oracle": "pi-1.1.0"}, "cells": cells, "egress": {"violations": []}}

    def test_matching_signature_passes(self):
        sig = {"exit": "ok", "reply_printed": 1}
        r = reliability.judge(self.result({"f": {"pi-1.1.0": [cell(sig)] * 2, "pig-0.5": [cell(sig)] * 2}}))
        self.assertTrue(r["verdicts"]["f"]["pig-0.5"]["pass"])
        self.assertTrue(r["ok"])

    def test_negative_control_a_different_outcome_fails(self):
        r = reliability.judge(self.result({"f": {"pi-1.1.0": [cell({"exit": "ok"})], "pig-0.5": [cell({"exit": "error"})]}}))
        self.assertFalse(r["verdicts"]["f"]["pig-0.5"]["pass"])
        self.assertIn("differs from pi-1.1.0", r["verdicts"]["f"]["pig-0.5"]["reasons"][0])
        self.assertFalse(r["ok"])

    def test_invariant_failure_fails_even_the_oracle(self):
        r = reliability.judge(self.result({"f": {"pi-1.1.0": [cell({"a": 1}, ["hung"])]}}))
        self.assertFalse(r["verdicts"]["f"]["pi-1.1.0"]["pass"])

    def test_flaky_outcome_fails(self):
        r = reliability.judge(self.result({"f": {"pi-1.1.0": [cell({"a": 1})] * 2, "pig-0.5": [cell({"a": 1}), cell({"a": 2})]}}))
        self.assertFalse(r["verdicts"]["f"]["pig-0.5"]["pass"])

    def test_other_clis_are_scored_on_invariants_only(self):
        r = reliability.judge(self.result({"f": {"pi-1.1.0": [cell({"a": 1})], "claude": [cell({"b": 2})]}}))
        self.assertTrue(r["verdicts"]["f"]["claude"]["pass"])

    def test_exhausted_retries_reported_as_success_break_the_invariant(self):
        # Every request answers 429, so no reply can be printed; Pi exits 1 (print-mode.ts:145-147). Exit 0 with no reply is a false success.
        res = {"timed_out": False, "code": 0, "stderr": ""}
        self.assertEqual(reliability.print_invariants("http-429-exhausted", res, 0, 120), ["reported success with no reply from the model"])
        self.assertEqual(reliability.print_invariants("http-429-exhausted", dict(res, code=1), 0, 120), [])
        self.assertEqual(reliability.print_invariants("http-429-retry", res, 1, 120), [])
        self.assertEqual(reliability.print_invariants("http-500-retry", dict(res, timed_out=True, stderr="panic: x"), 0, 120), ["hung: no exit within 120s", "crash trace on stderr"])

    def test_role_signature_marks_tool_errors(self):
        class R:
            body = {"messages": [{"role": "system", "content": "s"}, {"role": "user", "content": "q"}, {"role": "assistant", "tool_calls": [{}]},
                                 {"role": "tool", "content": "Error: aborted"}, {"role": "user", "content": "go on"}]}
        self.assertEqual(reliability.role_signature(R), ["user", "assistant:toolcall", "tool:error", "user"])

    def test_session_integrity(self):
        with tempfile.TemporaryDirectory() as tmp:
            good = os.path.join(tmp, "g.jsonl")
            with open(good, "w") as f:
                f.write('{"type":"session"}\n{"type":"message"}\n')
            torn = os.path.join(tmp, "t.jsonl")
            with open(torn, "w") as f:
                f.write('{"type":"session"}\n{"type":"mess')
            self.assertEqual(reliability.session_ok(good), (True, 2))
            self.assertFalse(reliability.session_ok(torn)[0])


class LongHistoryTest(unittest.TestCase):
    def test_generation_is_deterministic_and_has_the_requested_shape(self):
        with tempfile.TemporaryDirectory() as tmp:
            spec = {"segments": 3, "tokens_per_segment": 40_000, "compactions": 3, "tail_tokens": 10_000}
            self.assertNotIn("t", longhistory.CASES)
            longhistory.CASES["t"] = spec
            try:
                a = longhistory.generate(os.path.join(tmp, "a.jsonl"), "t", 5, "/p")
                b = longhistory.generate(os.path.join(tmp, "b.jsonl"), "t", 5, "/p")
                c = longhistory.generate(os.path.join(tmp, "c.jsonl"), "t", 6, "/p")
            finally:
                del longhistory.CASES["t"]
            self.assertEqual(a["sha256"], b["sha256"])
            self.assertNotEqual(a["sha256"], c["sha256"])
            with open(os.path.join(tmp, "a.jsonl")) as f:
                records = [json.loads(line) for line in f]
            self.assertEqual(records[0]["type"], "session")
            self.assertEqual(records[0]["version"], 3)
            compactions = [r for r in records if r["type"] == "compaction"]
            self.assertEqual(len(compactions), 3)
            ids = {r["id"] for r in records[1:]}
            for r in records[1:]:
                self.assertTrue(r["parentId"] is None or r["parentId"] in ids)  # a tree, as Pi's session format requires
            for comp in compactions:
                self.assertIn(comp["firstKeptEntryId"], ids)
            # three segments of 40k tokens plus a 10k tail, within one turn of slack per segment
            self.assertGreater(a["history_tokens"], 130_000)
            self.assertLess(a["history_tokens"], 150_000)
            # the TUI renders only what follows the last compaction: the kept entries, the summary and the tail
            self.assertLess(a["context_tokens"], 50_000)

    def test_frames_counts_synchronized_output_blocks(self):
        chunks = [(1, b"\x1b[?2026hAAA\x1b[?2026l"), (2, b"\x1b[?2026hBB\x1b[?2026l\x1b[?2026hCC\x1b[?2026l")]
        self.assertEqual(len(longhistory.frames(chunks)), 3)
        self.assertEqual(len(longhistory.frames([(1, b"x"), (2, b"y")])), 2)


class ReportTest(unittest.TestCase):
    def test_overlapping_intervals_are_inconclusive_not_parity(self):
        self.assertEqual(report.label(0.4, False), "inconclusive")  # a large ratio with overlapping intervals is still no claim
        self.assertEqual(report.label(1.0, False), "inconclusive")
        self.assertEqual(report.label(1.0, True), "about the same")
        self.assertEqual(report.label(0.4, True), "much lower")
        self.assertEqual(report.label(None, True), "inconclusive")

    def test_overhead_report_renders_every_section_from_raw(self):
        raw = {"kind": "overhead", "env": {"arms": {"pi-1.1.0": {"pinned_version": "1.1.0", "version_line": "1.1.0", "pinned": True, "executable_sha256": "ab" * 32, "license": "MIT", "footprint": {"bytes": 1000, "runtime": None, "runtime_bytes": 0}}},
                                           "seed": 3, "arms_lock_sha256": "x" * 64, "commit": "c"},
               "options": {"runs": 3, "warmup": 1, "rpc_turns": 2, "long_turns": 5, "scenarios": ["print"]},
               "samples": {"print": {"pi-1.1.0": [{"first_request_ms": 100.0, "wall_ms": 150.0}, {"first_request_ms": 110.0, "wall_ms": 160.0}, {"first_request_ms": 120.0, "wall_ms": 170.0}]}},
               "failures": {}, "skipped": {}, "limitations": {"claude": {"pty": "boom"}}, "egress": {"mode": "socket-audit", "violations": []}}
        with tempfile.TemporaryDirectory() as tmp:
            with open(os.path.join(tmp, "raw.json"), "w") as f:
                json.dump(raw, f)
            report.render(tmp)
            text = open(os.path.join(tmp, "report.md")).read()
            rows = open(os.path.join(tmp, "summary.csv")).read()
        self.assertIn("110 [100, 120] (n=3)", text)  # median of 100, 110, 120 and its bootstrap interval
        self.assertIn("arms_lock_sha256", text)
        self.assertIn("boom", text)  # a limitation is reported with its exact error
        self.assertIn("overhead,print,pi-1.1.0,first_request_ms,3,110.0", rows)


if __name__ == "__main__":
    unittest.main()
