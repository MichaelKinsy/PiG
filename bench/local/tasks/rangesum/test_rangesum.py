import unittest

from rangesum import range_sum


class RangeSumTest(unittest.TestCase):
    def test_inclusive_end(self):
        self.assertEqual(range_sum([1, 2, 3, 4], 1, 2), 5)

    def test_single_element(self):
        self.assertEqual(range_sum([7, 8, 9], 2, 2), 9)

    def test_whole_list(self):
        self.assertEqual(range_sum([1, 2, 3], 0, 2), 6)


if __name__ == "__main__":
    unittest.main()
