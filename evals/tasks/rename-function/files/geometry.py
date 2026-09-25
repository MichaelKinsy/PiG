"""Polygon geometry."""


def area_of(points):
    """Return the area of a simple polygon from its (x, y) vertices (shoelace formula)."""
    total = 0.0
    for (x1, y1), (x2, y2) in zip(points, points[1:] + points[:1]):
        total += x1 * y2 - x2 * y1
    return abs(total) / 2
