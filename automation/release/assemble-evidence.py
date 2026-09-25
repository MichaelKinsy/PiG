#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Retain each target's archive, signing bundle and scan evidence in a release."""
import argparse
import hashlib
from pathlib import Path
import shutil

TARGETS = ('linux-amd64', 'linux-arm64', 'darwin-amd64', 'darwin-arm64',
           'windows-amd64', 'windows-arm64', 'source')


def assemble(candidates, release, version):
    plan = []
    for target in TARGETS:
        name = f'pig-{version}-{target}'
        directory = candidates / name
        archive = name + ('.zip' if target.startswith('windows-') else '.tar.gz')
        prefix = 'source' if target == 'source' else 'sbom'
        names = [(archive, archive), (archive + '.sigstore.json', archive + '.sigstore.json')]
        for evidence in (prefix + '.spdx.json', prefix + '.cyclonedx.json',
                         'licenses.csv', 'inventory-validation.json', 'grype-report.sarif'):
            names.append(('evidence/' + evidence, name + '-' + evidence))
        recorded = {}
        for line in (directory / 'SHA256SUMS').read_text().splitlines():
            digest, relative = line.split(maxsplit=1)
            relative = relative.removeprefix('./')
            if relative in recorded:
                raise ValueError(f'Duplicate build checksum: {relative}')
            recorded[relative] = digest
        for source, destination in names:
            path = directory / source
            if not path.is_file() or path.is_symlink() or not path.stat().st_size:
                raise ValueError(f'Missing or invalid release evidence: {path}')
            # Bundles are generated after the build checksum step. Every
            # archive and SBOM must still equal the bytes that step verified.
            if not source.endswith('.sigstore.json'):
                if recorded.get(source) != hashlib.sha256(path.read_bytes()).hexdigest():
                    raise ValueError(f'Build checksum mismatch: {path}')
            plan.append((path, destination))
    # Validate the entire plan before producing any assets.
    release.mkdir(parents=True, exist_ok=True)
    for source, destination in plan:
        shutil.copyfile(source, release / destination)
    # Preserve hashes of the evidence as well as archives. Installer SHA256SUMS
    # remains archive-only and is generated separately by combine-checksums.py.
    lines = [f'{hashlib.sha256((release / name).read_bytes()).hexdigest()}  {name}\n'
             for _, name in sorted(plan, key=lambda pair: pair[1])]
    (release / 'EVIDENCE-SHA256SUMS').write_text(''.join(lines), encoding='utf-8')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('candidates', type=Path)
    parser.add_argument('release', type=Path)
    parser.add_argument('version')
    args = parser.parse_args()
    assemble(args.candidates, args.release, args.version)
