# SPDX-License-Identifier: MIT
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import report  # noqa: E402


def row(arm, tokens, cpu, failure=None):
    return {"arm": arm, "turns": 3, "failure": failure, "prompt_tokens_first": tokens, "tools": 4, "system_bytes": 100, "harness_before_request_ms": 5.0, "harness_after_first_byte_ms": None,
            "cpu_ms": cpu, "generation_cpu_share": 0.01, "rss_mib": {"end": 50.0}, "storage_bytes": 10}


class ReportTest(unittest.TestCase):
    def test_ratios_to_the_reference_losses_and_the_provisional_banner(self):
        raw = {"model": {"name": "m"}, "rounds": [[row("ref", 1000, 10), row("a", 2000, 30)], [row("ref", 1000, 12), row("a", 2000, 33, failure="boom")]]}
        raw["summary"] = raw["rounds"]
        text = report.render(raw, "ref")
        self.assertIn("PROVISIONAL", text)
        self.assertIn("2,000 (2.00x [2.00, 2.00])", text)   # prompt tokens: 2000 vs 1000
        self.assertIn("- a: boom", text)      # a failure is listed, not dropped
        self.assertIn("| ref | 1,000 |", text)

    def test_rounds_with_equal_raw_results_keep_their_own_summaries(self):
        same = [{"arm": "a", "turns": []}]
        raw = {"model": {"name": "m"}, "rounds": [same, list(same)], "summary": [[row("a", 100, 1)], [row("a", 300, 1)]]}
        by_arm = report.collect(raw)
        self.assertEqual([r["prompt_tokens_first"] for r in by_arm["a"]], [100, 300])  # rounds.index() gave round 0's summary twice

    def test_a_harness_that_connected_out_is_listed_as_a_loss(self):
        r = dict(row("a", 100, 1), egress=["203.0.113.9:443"])
        raw = {"model": {"name": "m"}, "rounds": [[row("ref", 100, 1), r]], "summary": [[row("ref", 100, 1), r]]}
        self.assertIn("- a: connected outside the loopback: 203.0.113.9:443", report.render(raw, "ref"))

    def test_print_and_rpc_rows_get_separate_tables_and_ratios_stay_within_a_mode(self):
        rpc_ref, print_arm = row("ref", 1000, 10), dict(row("p", 3000, 500), mode="print")
        raw = {"model": {"name": "m"}, "rounds": [[rpc_ref, print_arm], [rpc_ref, print_arm]]}
        raw["summary"] = raw["rounds"]
        text = report.render(raw, "ref")
        self.assertIn("## Harness cost, rpc mode", text)
        self.assertIn("## Harness cost, print mode", text)
        self.assertNotIn("3.00x", text)  # the print arm is not compared with an RPC reference


if __name__ == "__main__":
    unittest.main()
