#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Build the real embedding example and four library imports outside PiG's module tree."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def check(root):
    root = root.resolve()
    caches = json.loads(subprocess.check_output(["go", "env", "-json", "GOPATH", "GOMODCACHE", "GOCACHE"]))
    with tempfile.TemporaryDirectory(prefix="layout-import-") as directory:
        consumer = Path(directory)
        (consumer / "go.mod").write_text(
            "module example.org/layout-consumer\n\ngo 1.26.0\n"
            "require github.com/MichaelKinsy/PiG v0.0.0\n"
            f"replace github.com/MichaelKinsy/PiG => {json.dumps(str(root))}\n"
            f"replace github.com/MichaelKinsy/PiG/extensions/sdk => {json.dumps(str(root / 'extensions/sdk'))}\n"
        )
        shutil.copyfile(root / "examples/sdk/main.go", consumer / "main.go")
        (consumer / "imports.go").write_text('''package main
import (
    "github.com/MichaelKinsy/PiG/agent"
    "github.com/MichaelKinsy/PiG/ai"
    "github.com/MichaelKinsy/PiG/coding"
    "github.com/MichaelKinsy/PiG/coding/extension"
    "github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
    "github.com/MichaelKinsy/PiG/coding/rpcclient"
    "github.com/MichaelKinsy/PiG/tui"
    "github.com/MichaelKinsy/PiG/tui/widthx"
)
var (
    _ agent.AgentTool
    _ ai.Model
    _ = coding.SessionOptions{Runner: (*inproc.Runner)(nil)}
    _ extension.Extension
    _ = rpcclient.NewRpcClient
    _ = tui.NewText
    _ = widthx.VisibleWidth
)
''')
        env = os.environ | caches | {"GOWORK": "off", "GOPROXY": "off", "HOME": str(consumer / "home"),
                            "PIG_CODING_AGENT_DIR": str(consumer / "pig"),
                            "PI_CODING_AGENT_DIR": str(consumer / "pi")}
        for name in ("home", "pig", "pi"):
            (consumer / name).mkdir()
        subprocess.run(["go", "build", "-mod=mod", "."], cwd=consumer, env=env, check=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", type=Path)
    check(parser.parse_args().root)
