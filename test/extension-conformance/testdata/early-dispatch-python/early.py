#!/usr/bin/env python3
"""The Python extension of the pre-ready dispatch row (early_dispatch_conformance_test.go): one listener that echoes the payload it was dispatched, or writes its tag to a file when PIG_TEST_PURE_LISTENER names a directory."""
from __future__ import annotations

import os
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / "extensions" / "sdk-py"))

import pig_sdk  # noqa: E402


def new_extension() -> pig_sdk.Extension:
    e = pig_sdk.Extension("early-dispatch")
    pure = os.environ.get("PIG_TEST_PURE_LISTENER")
    if pure:
        e.events.on("early:ch", lambda ctx, data: (pathlib.Path(pure) / str(data["seq"])).write_bytes(data["tag"].encode()))
    else:
        e.events.on("early:ch", lambda ctx, data: ctx.events.emit("early:echo", data))
    if os.environ.get("PIG_TEST_FACTORY_EMIT"):
        e.events.emit("early:factory", 1)
    return e


if __name__ == "__main__":
    new_extension().run()
