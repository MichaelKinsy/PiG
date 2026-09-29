import unittest

from geometry import polygon_area
from shapes import square_area, triangle_area


class ShapesTest(unittest.TestCase):
    def test_square(self):
        self.assertEqual(square_area(3), 9)

    def test_triangle(self):
        self.assertEqual(triangle_area(4, 3), 6)

    def test_polygon_area(self):
        self.assertEqual(polygon_area([(0, 0), (2, 0), (2, 2), (0, 2)]), 4)
