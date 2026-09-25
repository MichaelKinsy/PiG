import unittest

from stats import moving_average


class MovingAverageTest(unittest.TestCase):
    def test_includes_last_window(self):
        self.assertEqual(moving_average([1, 2, 3, 4], 2), [1.5, 2.5, 3.5])

    def test_single_window(self):
        self.assertEqual(moving_average([2, 4], 2), [3.0])

    def test_rejects_zero_window(self):
        with self.assertRaises(ValueError):
            moving_average([1], 0)
