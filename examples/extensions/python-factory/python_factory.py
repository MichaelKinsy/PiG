#!/usr/bin/env python3
"""Minimal Python factory-style Pig extension example.

new_extension() is the conventional factory entry point.
Pig's packed-runner generator calls this function to obtain the extension
when co-locating multiple shared-ok Python extensions in one process.
"""
from __future__ import annotations

import pathlib
import sys

# Resolve the SDK relative to this file so the example works without
# installing the SDK globally.  The replace path mirrors the Go/Rust pattern.
_SDK_ROOT = pathlib.Path(__file__).resolve().parents[3] / "extensions" / "sdk-py"
sys.path.insert(0, str(_SDK_ROOT))

import pig_sdk  # noqa: E402


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("python-factory")
    ext.tool(
        "hello",
        "Return a hello greeting",
        {"type": "object"},
        lambda ctx, args: {"content": "hello from python"},
    )
    return ext
