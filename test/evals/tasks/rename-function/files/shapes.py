"""Named shapes built on geometry."""
from geometry import area_of


def square(side):
    return [(0, 0), (side, 0), (side, side), (0, side)]


def square_area(side):
    return area_of(square(side))


def triangle_area(base, height):
    return area_of([(0, 0), (base, 0), (0, height)])
