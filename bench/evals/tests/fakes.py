# SPDX-License-Identifier: MIT
"""Executable fakes that run on every host the bench supports.

A fake is a Python script run by this interpreter. On POSIX it is the script with a shebang; on Windows, which runs no shebang, it is a `.cmd`
wrapper next to the script. Code that names the executable itself, as an installed release member does, needs a real `.exe` on Windows:
`echo_payload` builds one with Go and skips the test where Go is absent.
"""
import json
import os
import shutil
import subprocess
import sys
import unittest

WINDOWS = os.name == "nt"


def python_command(path, source):
    """An executable that runs `source` with this interpreter. Returns its path: `path` on POSIX, `path + '.cmd'` on Windows."""
    if not WINDOWS:
        with open(path, "w") as f:
            f.write(f"#!{sys.executable}\n" + source)
        os.chmod(path, 0o755)
        return path
    with open(path + ".py", "w") as f:
        f.write(source)
    with open(path + ".cmd", "w", encoding="oem") as f:  # cmd.exe reads a batch file in the OEM code page; % starts a variable there
        f.write(f'@"{sys.executable}" "{path}.py" %*\n'.replace("%", "%%").replace("%%*", "%*"))
    return path + ".cmd"


def exe_member(name):
    """A release archive's member name for `name` on this host."""
    return name + ".exe" if WINDOWS else name


def echo_payload(line, workdir):
    """The bytes of an executable that prints `line` and exits 0: a shell script on POSIX, a Go-built .exe on Windows."""
    if not WINDOWS:
        return f"#!/bin/sh\necho '{line}'\n".encode()
    go = shutil.which("go")
    if not go:
        raise unittest.SkipTest("a Windows .exe fake is built with Go, which is not on PATH")
    os.makedirs(workdir, exist_ok=True)
    src, out = os.path.join(workdir, "echo.go"), os.path.join(workdir, "echo.exe")
    with open(src, "w") as f:
        f.write(f"package main\n\nimport \"fmt\"\n\nfunc main() {{ fmt.Println({json.dumps(line)}) }}\n")  # a JSON string is a Go string literal
    env = {k: v for k, v in os.environ.items() if k not in ("GOOS", "GOARCH")}  # build for this host, whatever the caller cross-compiles
    env.update(CGO_ENABLED="0", GOTOOLCHAIN="local", GOFLAGS="")
    subprocess.run([go, "build", "-o", out, src], check=True, capture_output=True, env=env, cwd=workdir, timeout=300)
    with open(out, "rb") as f:
        return f.read()
