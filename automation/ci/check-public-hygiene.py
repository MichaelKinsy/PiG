#!/usr/bin/env python3
"""Reject private coordination paths and infrastructure references in tracked source.

Default: tracked working files (for source-hygiene). --ref: immutable Git tree
(for publication). This supplements, and does not replace, secret scanning and
review of third-party redistribution rights.
"""
import argparse
from pathlib import Path
import re
import subprocess
import sys

# Assemble operator-specific literals so this policy does not exempt itself.
PRIVATE = re.compile(
    r"(?:/Users/|/home/)" + "kin" + r"sy(?:/|\b)|"
    + "imla" + r"dris|(?:[\w.-]+\.)?hpe" + r"corp\.net|labs\.hpe" + r"corp|"
    + "pig" + r"-staging|(?:~/|\$HOME/)pig" + r"-lanes|"
    + r"\.dev" + r"cache/scratch|(?:/Users|/home)/[^/\s]+/PiG-launch"
)
LANE = re.compile(r"(^|/)(REVIEW[^/]*|QUESTIONS|REPORT|NATIVE-PROOF-REQUEST[^/]*|[^/]*-REPORT|TASK|[^/]*\.TASK)\.md$")
ENV = re.compile(r"(^|/)(\.env(\.[^/]*)?|[^/]+\.env)$")
PRIVATE_DIR = re.compile(r"(^|/)(\.dev" + r"cache|pig-handoff)/")
UNCLEARED = re.compile(r"^media/demo/(demo-(f|full)\.(mp4|gif|png)|evidence/doom-[^/]*\.png)$")


def findings(path, data):
    # Reviewed image identifiers, not credentials; content is still scanned.
    image_metadata = path in ('automation/images/ci-go/metadata.env', 'automation/images/ci-parity/metadata.env')
    if any(rule.search(path) for rule in (LANE, PRIVATE_DIR, UNCLEARED)) or (ENV.search(path) and not image_metadata):
        yield f"{path}: forbidden publication path"
    # Match bytes too: an embedded private path is still private in a binary.
    for number, line in enumerate(data.decode('utf-8', errors='replace').splitlines(), 1):
        if PRIVATE.search(line):
            # Do not echo possibly sensitive line contents into CI logs.
            yield f"{path}:{number}: private infrastructure or operator path"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--ref', help='scan committed blobs at this Git ref, not working files')
    args = parser.parse_args()
    root = Path(subprocess.check_output(['git', 'rev-parse', '--show-toplevel'], text=True).strip())
    command = ['git', '-C', str(root)]
    failures = []
    if args.ref:
        tree = subprocess.check_output(command + ['rev-parse', args.ref + '^{tree}'], text=True).strip()
        entries = subprocess.check_output(command + ['ls-tree', '-r', '-z', tree])
        # One batch process, not a Git subprocess per file in a release tree.
        with subprocess.Popen(command + ['cat-file', '--batch'], stdin=subprocess.PIPE, stdout=subprocess.PIPE) as batch:
            for entry in entries.split(b'\0'):
                if not entry:
                    continue
                metadata, raw_path = entry.split(b'\t', 1)
                _, kind, oid = metadata.split()
                if kind != b'blob':
                    raise RuntimeError('non-blob tree entry requires explicit publication review')
                batch.stdin.write(oid + b'\n')
                batch.stdin.flush()
                header = batch.stdout.readline().split()
                if len(header) != 3 or header[0] != oid or header[1] != b'blob':
                    raise RuntimeError('could not read committed blob')
                size = int(header[2])
                data = batch.stdout.read(size)
                if len(data) != size or batch.stdout.read(1) != b'\n':
                    raise RuntimeError('truncated committed blob')
                failures.extend(findings(raw_path.decode('utf-8'), data))
            batch.stdin.close()
            if batch.wait() != 0:
                raise RuntimeError('Git blob reader failed')
    else:
        names = subprocess.check_output(command + ['ls-files', '-z'])
        for raw in names.split(b'\0'):
            if not raw:
                continue
            path = raw.decode('utf-8')
            target = root / path
            if not target.exists():  # tracked deletion in the candidate
                continue
            failures.extend(findings(path, target.read_bytes()))
    if failures:
        print('\n'.join(failures), file=sys.stderr)
        return 1
    print('public hygiene: tracked paths and private-reference checks passed')
    return 0


if __name__ == '__main__':
    sys.exit(main())
