# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT

import json
import unittest

from summarize_live_tests import summarize


def event(action, test=None, output="", package="example/ai"):
    return json.dumps({"Package": package, "Test": test, "Action": action, "Output": output})


class LiveTestSummaryTests(unittest.TestCase):
    def test_terminal_outcomes_not_parent_passes_or_credentials_determine_state(self):
        result = summarize([
            event("output", "TestLive/skip", "helper.go:1: live provider: provider-a\n"),
            event("output", "TestLive/skip", "missing SECRET; secret-value-must-not-appear\n"),
            event("skip", "TestLive/skip"),
            event("pass", "TestLive"),
            event("output", "TestLive/pass", "helper.go:1: live provider: provider-b\n"),
            event("output", "TestLive/pass", "helper.go:1: live provider: provider-b\n"),
            event("pass", "TestLive/pass"),
            event("output", "TestLive/fail", "helper.go:1: live provider: provider-b\n"),
            event("fail", "TestLive/fail"),
            event("fail"),
        ])
        self.assertIn("| provider-a | skipped | 0 | 0 | 1 | 0 |", result)
        self.assertIn("| provider-b | ran | 1 | 1 | 0 | 0 |", result)
        self.assertNotIn("secret-value", result)

    def test_package_identity_shared_providers_and_incomplete_tests(self):
        result = summarize([
            event("output", "TestLive", "live provider: provider-a\n"),
            event("output", "TestLive", "live provider: provider-b\n"),
            event("pass", "TestLive"),
            event("output", "TestLive", "live provider: provider-a\n", package="example/coding"),
        ])
        self.assertIn("| provider-a | ran; incomplete | 1 | 0 | 0 | 1 |", result)
        self.assertIn("| provider-b | ran | 1 | 0 | 0 | 0 |", result)

    def test_empty_or_unmarked_runs_do_not_claim_acceptance(self):
        for lines in ([], [event("pass", "TestHermetic")]):
            self.assertIn("No provider-marked live tests", summarize(lines))

    def test_no_terminal_event_is_incomplete_not_skipped(self):
        result = summarize([event("output", "TestLive", "live provider: provider-a\n")])
        self.assertIn("| provider-a | incomplete | 0 | 0 | 0 | 1 |", result)

    def test_malformed_json_is_not_silently_ignored(self):
        with self.assertRaises(json.JSONDecodeError):
            summarize(["not-json"])


if __name__ == "__main__":
    unittest.main()
