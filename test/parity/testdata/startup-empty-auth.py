"""Observe auth.json creation and preservation through real offline print startup."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile

binary = os.environ["PIG_PARITY_PIG_BIN" if sys.argv[1] == "pig" else "PIG_PARITY_PI_BIN"]
with tempfile.TemporaryDirectory(prefix="startup-auth-") as root:
    home = Path(root)
    agent = home / "agent"
    auth = agent / "auth.json"
    env = dict(os.environ, HOME=root, PIG_HOME=str(home / ".pig"),
               PIG_CODING_AGENT_DIR=str(agent), PI_CODING_AGENT_DIR=str(agent))
    args = [binary, "--offline", "--print", "--model", "openai/gpt-4o-mini",
            "--api-key", "startup-fixture-key", "--no-session", "--no-extensions",
            "--no-skills", "--no-prompt-templates", "--no-themes"]

    def startup():
        result = subprocess.run(args, cwd=root, env=env, input=b"", capture_output=True, timeout=30)
        assert result.returncode == 0 and result.stdout == b"" and result.stderr == b"", result
        assert not Path(str(auth) + ".lock").exists(), "startup left the auth lock behind"

    startup()
    assert auth.read_bytes() == b"{}", "startup did not create the empty auth store"
    assert auth.stat().st_mode & 0o777 == 0o600, "new auth file is not mode 0600"
    print("startup: auth.json={} mode=0600 lock=released")
    # Existing formatting and administrator-managed permissions must remain intact.
    auth.write_bytes(b"{ }\n")
    auth.chmod(0o660)
    before = auth.stat()
    startup()
    after = auth.stat()
    assert auth.read_bytes() == b"{ }\n", "restart rewrote existing auth.json"
    assert (after.st_ino, after.st_mtime_ns, after.st_mode) == (before.st_ino, before.st_mtime_ns, before.st_mode)
    print("restart: existing bytes, identity, mtime and mode=0660 preserved")
