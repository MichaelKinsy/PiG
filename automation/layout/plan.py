#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Shared, reviewed package boundaries for both layout variants."""

from contextlib import contextmanager

PUBLIC_ROOTS = ("agent", "ai", "coding", "tui")
PRIVATE_HELPERS = ("coding/pigversion", "tui/parity", "tui/termsim")


def package_moves(keep_public_libraries=False):
    roots = PRIVATE_HELPERS if keep_public_libraries else PUBLIC_ROOTS
    return {name: "internal/" + name for name in roots}


def move_path(path, moves):
    for old, new in moves.items():
        if path == old or path.startswith(old + "/"):
            return new + path[len(old):]
    return path


@contextmanager
def selected(namespace, **values):
    previous = {key: namespace[key] for key in values}
    namespace.update(values)
    try:
        yield
    finally:
        namespace.update(previous)
