#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Generate and pack PiG's npm packages from verified release archives.

PiG ships on npm as `@pi-in-go/pig`, a tiny Node launcher, plus six platform
packages `@pi-in-go/pig-<os>-<cpu>` that each carry one native binary (the
esbuild/biome optionalDependencies pattern; nothing is downloaded at install
time). Binaries come ONLY from the release archives that release-candidate.yml
builds: every archive is verified against SHA256SUMS before anything is
extracted from it.

Usage:
  pack_npm.py --archives DIR --version 0.2.0 --out DIR [--no-pack]

DIR must hold SHA256SUMS and pig-<version>-<goos>-<goarch>.{tar.gz,zip} for all
six targets. The output directory receives one package directory per package
and, unless --no-pack, the `npm pack` tarballs plus `publish-order.txt`
("<package name> <tarball>" per line; platform packages first, launcher last).
"""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import zipfile

SCOPE = "@pi-in-go"
LAUNCHER = f"{SCOPE}/pig"
REPOSITORY = {"type": "git", "url": "git+https://github.com/MichaelKinsy/PiG.git"}
HOMEPAGE = "https://pi-in-go.dev"
BUGS = "https://github.com/MichaelKinsy/PiG/issues"
LICENSE = "MIT"
NOTICE_FILES = ("LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md")
HERE = pathlib.Path(__file__).resolve().parent
PIGVERSION_GO = HERE.parents[2] / "coding" / "pigversion" / "pigversion.go"

# (goos, goarch) in release archive names -> (npm os, npm cpu).
TARGETS = {
    ("darwin", "amd64"): ("darwin", "x64"),
    ("darwin", "arm64"): ("darwin", "arm64"),
    ("linux", "amd64"): ("linux", "x64"),
    ("linux", "arm64"): ("linux", "arm64"),
    ("windows", "amd64"): ("win32", "x64"),
    ("windows", "arm64"): ("win32", "arm64"),
}

SEMVER = re.compile(r"^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$")


class PackError(Exception):
    pass


def package_name(npm_os: str, npm_cpu: str) -> str:
    return f"{SCOPE}/pig-{npm_os}-{npm_cpu}"


def archive_name(version: str, goos: str, goarch: str) -> str:
    suffix = "zip" if goos == "windows" else "tar.gz"
    return f"pig-{version}-{goos}-{goarch}.{suffix}"


def binary_name(goos: str) -> str:
    return "pig.exe" if goos == "windows" else "pig"


def upstream_version(path: pathlib.Path = PIGVERSION_GO) -> str | None:
    try:
        match = re.search(r'^const UpstreamVersion = "([^"]+)"$', path.read_text(), re.M)
    except OSError:
        return None
    return match.group(1) if match else None


def npm_version(release_version: str) -> str:
    """The npm version: the PiG release version without build metadata."""
    version = release_version.removeprefix("v").split("+", 1)[0]
    if not SEMVER.match(version):
        raise PackError(f"not a semantic version: {release_version!r}")
    return version


def description(pi_base: str | None) -> str:
    base = f" (based on Pi {pi_base})" if pi_base else ""
    return f"PiG, the Go port of the Pi coding agent{base}"


def platform_manifest(version: str, npm_os: str, npm_cpu: str, goos: str, pi_base: str | None) -> dict:
    return {
        "name": package_name(npm_os, npm_cpu),
        "version": version,
        "description": f"{description(pi_base)}: native {npm_os}-{npm_cpu} binary for {LAUNCHER}",
        "license": LICENSE,
        "homepage": HOMEPAGE,
        "bugs": BUGS,
        "repository": REPOSITORY,
        "os": [npm_os],
        "cpu": [npm_cpu],
        "files": [binary_name(goos), "README.md", *NOTICE_FILES],
        "preferUnplugged": True,
    }


def launcher_manifest(version: str, pi_base: str | None) -> dict:
    optional = {package_name(o, c): version for o, c in sorted(TARGETS.values())}
    return {
        "name": LAUNCHER,
        "version": version,
        "description": description(pi_base),
        "license": LICENSE,
        "homepage": HOMEPAGE,
        "bugs": BUGS,
        "repository": REPOSITORY,
        "keywords": ["pig", "pi", "coding-agent", "cli", "ai"],
        "bin": {"pig": "bin/pig.js"},
        "files": ["bin/pig.js", "README.md", "LICENSE", "NOTICE"],
        "engines": {"node": ">=18"},
        "optionalDependencies": optional,
    }


def read_sha256sums(path: pathlib.Path) -> dict[str, str]:
    sums: dict[str, str] = {}
    for line in path.read_text().splitlines():
        if not line.strip():
            continue
        match = re.match(r"^([0-9a-f]{64}) [ *](?:\./)?(.+)$", line)
        if not match:
            raise PackError(f"malformed SHA256SUMS line: {line!r}")
        sums[match.group(2)] = match.group(1)
    return sums


def sha256_of(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_archive(archive: pathlib.Path, sums: dict[str, str]) -> None:
    expected = sums.get(archive.name)
    if expected is None:
        raise PackError(f"{archive.name} is not listed in SHA256SUMS")
    actual = sha256_of(archive)
    if actual != expected:
        raise PackError(f"checksum mismatch for {archive.name}: expected {expected}, got {actual}")


def read_members(archive: pathlib.Path, prefix: str, names: list[str]) -> dict[str, bytes]:
    """Read exactly the named members under prefix/ without extracting anything else."""
    wanted = {f"{prefix}/{name}": name for name in names}
    found: dict[str, bytes] = {}
    if archive.name.endswith(".zip"):
        with zipfile.ZipFile(archive) as zf:
            for member, name in wanted.items():
                try:
                    found[name] = zf.read(member)
                except KeyError:
                    pass
    else:
        with tarfile.open(archive, "r:gz") as tf:
            for member, name in wanted.items():
                try:
                    info = tf.getmember(member)
                except KeyError:
                    continue
                if not info.isfile():
                    raise PackError(f"{archive.name}: {member} is not a regular file")
                stream = tf.extractfile(info)
                assert stream is not None
                found[name] = stream.read()
    missing = sorted(set(names) - set(found))
    if missing:
        raise PackError(f"{archive.name} lacks {', '.join(f'{prefix}/{m}' for m in missing)}")
    return found


def write_json(path: pathlib.Path, data: dict) -> None:
    path.write_text(json.dumps(data, indent=2) + "\n")


def platform_readme(name: str, npm_os: str, npm_cpu: str) -> str:
    return (
        f"# {name}\n\n"
        f"The native `pig` binary for {npm_os}-{npm_cpu}. Do not install this package directly;\n"
        f"install [`{LAUNCHER}`](https://www.npmjs.com/package/{LAUNCHER}), which selects it.\n"
    )


def launcher_readme(version: str, pi_base: str | None) -> str:
    base = f" PiG {version} is based on Pi {pi_base}." if pi_base else ""
    return f"""# {LAUNCHER}

PiG is the Go port of the Pi coding agent.{base}

```bash
npm install -g {LAUNCHER}     # install `pig`
npx {LAUNCHER} --version      # or run it without installing
npm update -g {LAUNCHER}      # update
npm uninstall -g {LAUNCHER}   # uninstall
```

`pig` is a native binary. npm installs the one matching your platform
(`@pi-in-go/pig-<os>-<cpu>`, as an optional dependency) and this package's small
Node.js (>= 18) launcher runs it. Supported: macOS, Linux and Windows on x64
and arm64. Installing with `--no-optional`/`--omit=optional` leaves the binary
out; reinstall without that flag.

Other install methods, documentation and source: {HOMEPAGE} and
https://github.com/MichaelKinsy/PiG.
"""


def generate(archives: pathlib.Path, release_version: str, out: pathlib.Path, pi_base: str | None) -> list[pathlib.Path]:
    """Emit the seven package directories into out; return them in publish order."""
    version = npm_version(release_version)
    archive_version = release_version.removeprefix("v")
    sums_path = archives / "SHA256SUMS"
    if not sums_path.is_file():
        raise PackError(f"missing {sums_path}")
    sums = read_sha256sums(sums_path)
    out.mkdir(parents=True, exist_ok=True)
    order: list[pathlib.Path] = []
    notices: dict[str, bytes] = {}
    for (goos, goarch), (npm_os, npm_cpu) in sorted(TARGETS.items()):
        archive = archives / archive_name(archive_version, goos, goarch)
        if not archive.is_file():
            raise PackError(f"missing release archive {archive.name}")
        verify_archive(archive, sums)
        binary = binary_name(goos)
        members = read_members(archive, f"pig-{archive_version}-{goos}-{goarch}", [binary, *NOTICE_FILES])
        name = package_name(npm_os, npm_cpu)
        pkg_dir = out / name.split("/", 1)[1]
        if pkg_dir.exists():
            shutil.rmtree(pkg_dir)
        pkg_dir.mkdir()
        target = pkg_dir / binary
        target.write_bytes(members[binary])
        target.chmod(0o755)
        for notice in NOTICE_FILES:
            (pkg_dir / notice).write_bytes(members[notice])
            notices.setdefault(notice, members[notice])
        (pkg_dir / "README.md").write_text(platform_readme(name, npm_os, npm_cpu))
        write_json(pkg_dir / "package.json", platform_manifest(version, npm_os, npm_cpu, goos, pi_base))
        order.append(pkg_dir)

    launcher_dir = out / "pig"
    if launcher_dir.exists():
        shutil.rmtree(launcher_dir)
    (launcher_dir / "bin").mkdir(parents=True)
    script = launcher_dir / "bin" / "pig.js"
    shutil.copyfile(HERE / "launcher" / "bin" / "pig.js", script)
    script.chmod(script.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    for notice in ("LICENSE", "NOTICE"):
        (launcher_dir / notice).write_bytes(notices[notice])
    (launcher_dir / "README.md").write_text(launcher_readme(version, pi_base))
    write_json(launcher_dir / "package.json", launcher_manifest(version, pi_base))
    order.append(launcher_dir)
    return order


def npm_pack(pkg_dir: pathlib.Path, out: pathlib.Path) -> pathlib.Path:
    result = subprocess.run(
        ["npm", "pack", "--json", "--pack-destination", str(out.resolve())],
        cwd=pkg_dir,
        check=True,
        capture_output=True,
        text=True,
        shell=sys.platform == "win32",
    )
    filename = json.loads(result.stdout)[0]["filename"]
    return out / filename


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--archives", required=True, type=pathlib.Path, help="directory with SHA256SUMS and the six archives")
    parser.add_argument("--version", required=True, help="PiG release version, e.g. 0.2.0")
    parser.add_argument("--out", required=True, type=pathlib.Path, help="output directory")
    parser.add_argument("--pi-base", default=None, help="Pi base version for descriptions (default: coding/pigversion)")
    parser.add_argument("--no-pack", action="store_true", help="generate directories only")
    args = parser.parse_args(argv)
    pi_base = args.pi_base or upstream_version()
    try:
        order = generate(args.archives, args.version, args.out, pi_base)
    except PackError as err:
        print(f"pack_npm: error: {err}", file=sys.stderr)
        return 1
    if args.no_pack:
        for pkg_dir in order:
            print(pkg_dir)
        return 0
    tarballs = [npm_pack(pkg_dir, args.out) for pkg_dir in order]
    names = [json.loads((d / "package.json").read_text())["name"] for d in order]
    (args.out / "publish-order.txt").write_text("".join(f"{n} {t.name}\n" for n, t in zip(names, tarballs)))
    for tarball in tarballs:
        print(tarball)
    return 0


if __name__ == "__main__":
    sys.exit(main())
