#!/usr/bin/env python3
"""Reject private coordination paths and infrastructure references in tracked source.

Default: tracked working files (for source-hygiene). --ref: immutable Git tree
(for publication). This supplements, and does not replace, secret scanning and
review of third-party redistribution rights.
"""
import argparse
import hashlib
from pathlib import Path
import re
import subprocess
import sys

# Assemble operator-specific literals so this policy does not exempt itself.
# Domain probes search anywhere in a line; hostname prefixes add no matches and need no greedy scan.
PRIVATE = re.compile(
    r"(?:/Users/|/home/|/(?:var/)?tmp/)" + "kin" + r"sy(?:/|\b)|"
    + "imla" + r"dris|hpe" + r"corp\.net|labs\.hpe" + r"corp|"
    + "pig" + r"-staging|(?:~/|\$HOME/)pig" + r"-lanes|"
    + r"\.dev" + r"cache/scratch|(?:/Users|/home)/[^/\s]+/PiG-launch"
)
# Provenance of the plugin UI work (D614): inside GUI_PATHS only, since the legacy SDK upgrade path legitimately names
# an old module path elsewhere. Another organization's name, an event label, employer names outside copyright headers
# and person names (SHA-256 of each word against gui-name-hashes.txt, so the list publishes nothing) never enter them.
# Terms are assembled so this file does not match itself.
PROVENANCE = re.compile(
    "main" + r"st(?:ai|\.ai)|hack" + r"athon|cdi" + r"-eig|\bf" + r"tc\b|\bc" + r"xo\b|github\.h" + r"pe", re.IGNORECASE
)
EMPLOYER = re.compile(r"hew" + r"lett|\bh" + r"pe\b", re.IGNORECASE)
GUI_PATHS = re.compile(
    r"^(?:coding/uiview/|extensions/pig-ui/|extensions/sdk/(?:ui|view)|extensions/sdk-py/pig_sdk/ui|"
    r"extensions/sdk-rs/src/ui|coding/presentation/|cmd/pig/rpc_gui|"
    r"coding/extension/host/subprocess/(?:uiview|view_|presentation_bridge)|"
    r"coding/extension/host/subprocess/runtime-node/shims/pig-ui|docs/ui-)"
)
WORD = re.compile(r"[A-Za-z]{3,}")
NAME_HASHES = frozenset(
    line.strip() for line in (Path(__file__).with_name('gui-name-hashes.txt').read_text().splitlines())
    if line.strip() and not line.startswith('#')
)
LANE = re.compile(r"(^|/)(REVIEW[^/]*|QUESTIONS|REPORT|NATIVE-PROOF-REQUEST[^/]*|[^/]*-REPORT|TASK|[^/]*\.TASK)\.md$")
ENV = re.compile(r"(^|/)(\.env(\.[^/]*)?|[^/]+\.env)$")
PRIVATE_DIR = re.compile(r"(^|/)(\.dev" + r"cache|pig-handoff)/")
# Launch-video production files are not part of the public tree.
UNCLEARED = re.compile(r"^docs/media/demo/")


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
        if GUI_PATHS.search(path):
            if PROVENANCE.search(line):
                yield f"{path}:{number}: forbidden provenance term"
            if EMPLOYER.search(line) and 'SPDX-FileCopyrightText' not in line:
                yield f"{path}:{number}: employer name outside a copyright header"
            for word in WORD.findall(line):
                if hashlib.sha256(word.lower().encode()).hexdigest() in NAME_HASHES:
                    yield f"{path}:{number}: person name"
                    break


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
