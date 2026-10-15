#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Benchmark extension (Python): a tool_call handler that does about 2 ms of work per call and counts its calls in $BENCHEXT_COUNTER."""
from __future__ import annotations

import hashlib
import os
import pathlib
import sys

_SDK_ROOT = pathlib.Path(__file__).resolve().parents[3] / "extensions" / "sdk-py"
sys.path.insert(0, str(_SDK_ROOT))

import pig_sdk  # noqa: E402

_BLOCK = bytes(65536)


def _on_tool_call(ctx, data):
    h = hashlib.sha256()
    for _ in range(16):
        h.update(_BLOCK)
    path = os.environ.get("BENCHEXT_COUNTER")
    if path:
        with open(path, "a") as f:
            f.write("py\n")
    return None


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("bench-py")
    ext.tool("hello_py", "Return a hello greeting", {"type": "object"}, lambda ctx, args: {"content": "hello from python"})
    ext.on_event("tool_call", _on_tool_call)
    return ext
