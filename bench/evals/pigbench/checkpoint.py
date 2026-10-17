# SPDX-License-Identifier: MIT
"""Partial results: a bench that runs for an hour keeps what it has measured if it is interrupted."""
import json
import os


def save(result, out, name="raw.partial.json"):
    """Write `name` (raw.partial.json) atomically. The final raw.json replaces it when the bench finishes. Concurrent writers use distinct names."""
    os.makedirs(out, exist_ok=True)
    path = os.path.join(out, name)
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(result, f)
    os.replace(tmp, path)


def clear(out, name="raw.partial.json"):
    try:
        os.unlink(os.path.join(out, name))
    except FileNotFoundError:
        pass
