"""Small statistics helpers."""


def moving_average(values, window):
    """Return the mean of each consecutive run of `window` values."""
    if window <= 0:
        raise ValueError("window must be positive")
    return [sum(values[i:i + window]) / window for i in range(len(values) - window)]
