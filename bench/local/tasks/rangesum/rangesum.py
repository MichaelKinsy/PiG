def range_sum(values, start, end):
    """Sum of values[start:end+1], where both ends are inclusive indexes."""
    total = 0
    for i in range(start, end):
        total += values[i]
    return total
