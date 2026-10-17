# SPDX-License-Identifier: MIT
import math
import unittest

from pigbench import stats


class StatsTest(unittest.TestCase):
    def test_wilson_known_values(self):
        lo, hi = stats.wilson(8, 10)  # textbook Wilson 95% interval for 8 of 10
        self.assertAlmostEqual(lo, 0.4902, places=3)
        self.assertAlmostEqual(hi, 0.9433, places=3)

    def test_mcnemar_exact(self):
        self.assertAlmostEqual(stats.mcnemar_exact(0, 5), 0.0625)  # 2 * (1/32)
        self.assertEqual(stats.mcnemar_exact(3, 3), 1.0)
        self.assertEqual(stats.mcnemar_exact(0, 0), 1.0)

    def test_sign_flip_exact_small(self):
        # three equal positive differences: only the all-positive and all-negative sign patterns reach |sum| = 3, so p = 2/8
        self.assertAlmostEqual(stats.sign_flip_p([1, 1, 1]), 0.25)
        self.assertEqual(stats.sign_flip_p([1, -1]), 1.0)

    def test_holm(self):
        # textbook example: p = .01 .04 .03 with m = 3 -> .03 .06 .06
        self.assertEqual([round(x, 10) for x in stats.holm([0.01, 0.04, 0.03])], [0.03, 0.06, 0.06])

    def test_paired_bootstrap_is_seeded_and_covers_the_mean(self):
        diffs = [0.1, -0.2, 0.3, 0.0, 0.2, 0.1, -0.1, 0.4]
        a = stats.paired_bootstrap(diffs, seed=7)
        b = stats.paired_bootstrap(diffs, seed=7)
        self.assertEqual(a["ci"], b["ci"])
        self.assertLess(a["ci"][0], a["mean"])
        self.assertGreater(a["ci"][1], a["mean"])

    def test_tost_decisions(self):
        tight = [0.01, -0.01, 0.0, 0.01, -0.01, 0.0, 0.0, 0.01] * 5
        self.assertTrue(stats.tost(tight, 0.05)["equivalent"])
        shifted = [0.2 + d for d in tight]
        self.assertFalse(stats.tost(shifted, 0.05)["equivalent"])
        self.assertFalse(stats.tost([0.3, -0.3, 0.3, -0.3], 0.05)["equivalent"])  # wide interval: no parity claim

    def test_ratio_of_totals_and_geomean(self):
        a, b = {"t1": 2.0, "t2": 8.0}, {"t1": 1.0, "t2": 4.0}
        r = stats.ratio_of_totals(a, b, resamples=200)
        self.assertAlmostEqual(r["ratio_of_totals"], 2.0)
        self.assertAlmostEqual(r["geomean_ratio"], 2.0)
        r2 = stats.ratio_of_totals({"t1": 1.0, "t2": 9.0}, {"t1": 1.0, "t2": 3.0}, resamples=200)
        self.assertAlmostEqual(r2["ratio_of_totals"], 2.5)
        self.assertAlmostEqual(r2["geomean_ratio"], math.sqrt(3.0))

    def test_mde(self):
        # 100 tasks, sd 0.5: (1.96 + 0.8416) * 0.5 / 10
        self.assertAlmostEqual(stats.mde_paired(0.5, 100), 0.1401, places=3)

    def test_percentile(self):
        self.assertEqual(stats.percentile([1, 2, 3, 4], 50), 2.5)
        self.assertEqual(stats.percentile([5], 90), 5)


if __name__ == "__main__":
    unittest.main()
