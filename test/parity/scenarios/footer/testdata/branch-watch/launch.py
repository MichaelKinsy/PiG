import os
from pathlib import Path
import shutil
import subprocess
import sys
import threading
import time

# The mocked git fallback mirrors footer-data-provider.test.ts. Native directory
# events and the real CLI footer are deliberately not mocked.
root = Path.cwd()
metadata = root / '.git'
metadata.mkdir(exist_ok=True)
(metadata / 'reftable').mkdir(exist_ok=True)
(metadata / 'HEAD').write_text('ref: refs/heads/.invalid\n')
table = metadata / 'reftable' / 'tables.list'
table.write_text('0\n')
branch = root / 'resolved-branch'
branch.write_text('main\n')
ready = root / 'git-ready'
bin_dir = root / 'fixture-bin'
bin_dir.mkdir()
real_git = shutil.which('git')
fake = bin_dir / 'git'
fake.write_text('#!/usr/bin/env python3\nimport os,sys\nfrom pathlib import Path\n' +
    f'if sys.argv[1:] == ["--no-optional-locks","symbolic-ref","--quiet","--short","HEAD"]:\n Path({str(ready)!r}).touch()\n print(Path({str(branch)!r}).read_text().strip())\nelse: os.execv({real_git!r}, [{real_git!r}]+sys.argv[1:])\n')
fake.chmod(0o755)
kind = sys.argv[1]
if kind == 'pig':
    binary = os.environ.get('PIG_PARITY_PIG_BIN') or os.environ.get('PIG_BIN') or shutil.which('pig')
else:
    binary = os.environ.get('PIG_PARITY_PI_BIN') or os.environ.get('PI_BIN') or shutil.which('pi')
if not binary:
    raise RuntimeError('Pinned CLI binary is not configured')
env = dict(os.environ, PATH=str(bin_dir)+os.pathsep+os.environ['PATH'])
child = subprocess.Popen([binary, '--provider', 'openai', '--model', 'gpt-4.1', '--no-extensions'], env=env)
stop = threading.Event()
def change_branch():
    deadline = time.monotonic()+20
    while not ready.exists() and not stop.wait(.01):
        if time.monotonic()>deadline:
            return
    if stop.wait(1):
        return
    branch.write_text('intermediate\n')
    table.write_text('1\n')
    # Let the old five-second HEAD-only poll record the unchanged HEAD mtime.
    # The second reftable-only update then distinguishes it from real watching.
    if stop.wait(6):
        return
    branch.write_text('final-branch\n')
    table.write_text('2\n')
worker = threading.Thread(target=change_branch)
worker.start()
try:
    code = child.wait()
finally:
    stop.set()
    worker.join()
sys.exit(code)
