# SPDX-License-Identifier: MIT
"""The upstream Pi pin: one committed setting, one run-time override.

`.upstream/pin` holds the committed pin. PI_UPSTREAM_REF=<tag|commit|branch>
overrides it for one run without editing a file. Every consumer reads the pin
through this module or `automation/pin/pin.py`; nothing else carries the Pi
version, so setting the pin back to a tag (v1.1.0) reproduces that release.

Pin fields
  ref              what the pin was bumped to: a release tag (v1.1.0) or a commit
  commit           the exact 40-hex commit
  package_version  the version Pi's package.json files and `pi --version` report
  version          the display and ledger version: the package version for a
                   release tag, "<package_version>+<commit[:8]>" for a commit
"""
from __future__ import annotations

import json
import os
import re
import subprocess
import tarfile
import tempfile
from dataclasses import dataclass
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PIN_FILE = ROOT / ".upstream" / "pin"
PIGVERSION = ROOT / "internal" / "coding" / "pigversion" / "pigversion.go"
UPSTREAM_REPO = "earendil-works/pi"
OVERRIDE_ENV = "PI_UPSTREAM_REF"

TAG_RE = re.compile(r"^v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)$")
HEX_RE = re.compile(r"^[0-9a-f]{7,40}$")
FULL_RE = re.compile(r"^[0-9a-f]{40}$")


class PinError(Exception):
    pass


@dataclass(frozen=True)
class Pin:
    ref: str
    commit: str
    package_version: str
    version: str

    @property
    def kind(self) -> str:
        return "tag" if TAG_RE.match(self.ref) else "commit"

    @property
    def tag(self) -> str:
        """The git tag of a release pin, or the commit of a commit pin."""
        return "v" + self.package_version if self.kind == "tag" else self.commit

    @property
    def short(self) -> str:
        return self.commit[:8]

    @property
    def build_metadata(self) -> str:
        """The version as one semver build-metadata string (no second plus)."""
        return self.version.replace("+", ".")

    @property
    def mirror_dir(self) -> str:
        return "v" + self.version

    def fields(self) -> dict[str, str]:
        return {"ref": self.ref, "commit": self.commit, "package_version": self.package_version, "version": self.version}


def cache_dir() -> Path:
    base = os.environ.get("XDG_CACHE_HOME") or str(Path.home() / ".cache")
    return Path(base) / "checkouts" / "github.com" / UPSTREAM_REPO


def read_pin_file(path: Path = PIN_FILE) -> Pin:
    values: dict[str, str] = {}
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        text = line.strip()
        if not text or text.startswith("#"):
            continue
        key, sep, value = text.partition("=")
        if not sep or key.strip() in values:
            raise PinError(f"{path}:{number}: expected one unique key=value per line")
        values[key.strip()] = value.strip()
    want = {"ref", "commit", "package_version", "version"}
    if set(values) != want:
        raise PinError(f"{path}: keys {sorted(values)} must be exactly {sorted(want)}")
    return validate(Pin(values["ref"], values["commit"], values["package_version"], values["version"]), str(path))


def validate(pin: Pin, where: str) -> Pin:
    if not FULL_RE.match(pin.commit):
        raise PinError(f"{where}: commit must be 40 lowercase hex digits")
    if not re.match(r"^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$", pin.package_version):
        raise PinError(f"{where}: package_version {pin.package_version!r} is not a semver release")
    want = pin.package_version if pin.kind == "tag" else f"{pin.package_version}+{pin.commit[:8]}"
    if pin.version != want:
        raise PinError(f"{where}: version must be {want!r} for a {pin.kind} pin, got {pin.version!r}")
    if pin.kind == "tag" and pin.ref != "v" + pin.package_version:
        raise PinError(f"{where}: tag ref {pin.ref!r} does not name package_version {pin.package_version!r}")
    if pin.kind == "commit" and pin.ref != pin.commit:
        raise PinError(f"{where}: a commit pin's ref must be its full commit")
    return pin


def render_pin_file(pin: Pin) -> str:
    return (
        "# The upstream Pi pin: the one committed setting that names the Pi build PiG targets.\n"
        "# Change it only with `make pin-bump PI_UPSTREAM_REF=<tag|commit>`, which also regenerates\n"
        "# every file derived from it. PI_UPSTREAM_REF=<tag|commit> overrides it for one run.\n"
        "# ref: a release tag, or the full commit of a pinned pi-mono commit.\n"
        "# package_version: what Pi's package.json files and `pi --version` report.\n"
        "# version: the display and ledger version (a commit pin appends +<commit[:8]>).\n"
        f"ref={pin.ref}\ncommit={pin.commit}\npackage_version={pin.package_version}\nversion={pin.version}\n"
    )


def committed() -> Pin:
    return read_pin_file()


def effective() -> tuple[Pin, bool]:
    """The pin this run uses and whether PI_UPSTREAM_REF overrides the committed pin."""
    base = committed()
    ref = os.environ.get(OVERRIDE_ENV, "").strip()
    if not ref or ref == "pin":
        return base, False
    pin = resolve(ref, base)
    return pin, pin != base


def _run(cmd: list[str], **kwargs) -> str:
    return subprocess.run(cmd, check=True, capture_output=True, text=True, timeout=kwargs.pop("timeout", 120), **kwargs).stdout


def _auth_args() -> list[str]:
    token = os.environ.get("GH_PUBLIC_TOKEN")
    return ["-H", f"Authorization: Bearer {token}"] if token else []


def ls_remote(ref: str) -> str:
    out = _run(["git", "ls-remote", f"https://github.com/{UPSTREAM_REPO}.git", f"refs/tags/{ref}", f"refs/tags/{ref}^{{}}", f"refs/heads/{ref}"])
    found = {}
    for line in out.splitlines():
        sha, name = line.split("\t")
        found[name] = sha
    for name in (f"refs/tags/{ref}^{{}}", f"refs/tags/{ref}", f"refs/heads/{ref}"):
        if name in found:
            return found[name]
    raise PinError(f"{UPSTREAM_REPO} has no tag or branch {ref!r}")


def full_commit(ref: str) -> str:
    """Expand a short commit through the GitHub API (a full commit needs no request)."""
    if FULL_RE.match(ref):
        return ref
    url = f"https://api.github.com/repos/{UPSTREAM_REPO}/commits/{ref}"
    out = subprocess.run(["curl", "-sSfL", "-H", "Accept: application/vnd.github.sha", *_auth_args(), url], check=True, capture_output=True, text=True, timeout=60).stdout.strip()
    if not FULL_RE.match(out):
        raise PinError(f"{UPSTREAM_REPO} has no commit {ref!r}")
    return out


def tarball(ref_or_commit: str, tag: bool) -> Path:
    """Download (or reuse) the codeload tarball of a tag or commit."""
    directory = cache_dir() / "tarballs"
    directory.mkdir(parents=True, exist_ok=True)
    path = directory / f"{ref_or_commit}.tar.gz"
    if path.exists():
        return path
    url = f"https://codeload.github.com/{UPSTREAM_REPO}/tar.gz/" + ("refs/tags/" if tag else "") + ref_or_commit
    # Close mkstemp's descriptor: curl writes the file by name, and Windows refuses to rename a file this process holds open (WinError 32).
    descriptor, name = tempfile.mkstemp(prefix=path.name + ".", dir=directory)
    os.close(descriptor)
    temporary = Path(name)
    try:
        subprocess.run(["curl", "-sSfL", *_auth_args(), "-o", str(temporary), url], check=True, timeout=600)
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)
    return path


def tarball_identity(path: Path) -> tuple[str, str]:
    """(full commit from the wrapper directory name, coding-agent package version)."""
    with tarfile.open(path) as archive:
        root = archive.next().name.split("/")[0]
        commit = root.rsplit("-", 1)[-1]
        member = archive.getmember(f"{root}/packages/coding-agent/package.json")
        version = json.loads(archive.extractfile(member).read())["version"]
    if not FULL_RE.match(commit):
        raise PinError(f"{path}: tarball root {root!r} does not end in a full commit")
    return commit, version


def _cached(ref: str) -> Pin | None:
    path = cache_dir() / "resolved" / f"{ref}.json"
    if not path.exists():
        return None
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
        return validate(Pin(data["ref"], data["commit"], data["package_version"], data["version"]), str(path))
    except (KeyError, ValueError, PinError):
        return None


def _remember(ref: str, pin: Pin) -> Pin:
    path = cache_dir() / "resolved" / f"{ref}.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(pin.fields()), encoding="utf-8")
    return pin


def resolve(ref: str, base: Pin | None = None) -> Pin:
    """Resolve a tag, branch, or (short) commit to a pin. The committed pin and
    refs resolved before need no network; branches are always resolved afresh."""
    if base is not None and (ref in (base.ref, base.commit, base.version, base.tag) or (HEX_RE.match(ref) and base.commit.startswith(ref))):
        return base
    match = TAG_RE.match(ref)
    if not match and not HEX_RE.match(ref):
        commit_hint = ls_remote(ref)
        return resolve(commit_hint, base)
    seen = _cached(ref)
    if seen is not None:
        return seen
    if match:
        tag = "v" + match.group(1)
        commit = ls_remote(tag)
        return _remember(ref, validate(Pin(tag, commit, match.group(1), match.group(1)), f"tag {tag}"))
    path = tarball(full_commit(ref), False)
    commit, package_version = tarball_identity(path)
    return _remember(ref, validate(Pin(commit, commit, package_version, f"{package_version}+{commit[:8]}"), f"commit {ref}"))


# pigversion.go -----------------------------------------------------------------

PIG_VERSION_RE = re.compile(r'^const PigVersion = "([^"]+)"$', re.M)


def pig_version(path: Path = PIGVERSION) -> str:
    match = PIG_VERSION_RE.search(path.read_text(encoding="utf-8"))
    if not match:
        raise PinError(f"{path}: no PigVersion")
    return match.group(1)


def render_pigversion(pin: Pin, pig: str) -> str:
    return f'''// Code generated by automation/pin/pin.py from .upstream/pin. DO NOT EDIT, except PigVersion, which `make set-version` writes.

// Package pigversion provides PiG and upstream Pi version pins to packages
// that cannot import coding.
package pigversion

// PigVersion is PiG's release line. It advances independently of the upstream
// Pi target.
const PigVersion = "{pig}"

// UpstreamRef is the pinned tag or commit of Pi.
const UpstreamRef = "{pin.ref}"

// UpstreamVersion is the pinned Pi build's display and ledger version: the
// release version for a tag, the release version plus +<commit[:8]> for a commit.
const UpstreamVersion = "{pin.version}"

// UpstreamPackageVersion is the version Pi's package.json files and `pi --version` report.
const UpstreamPackageVersion = "{pin.package_version}"

// UpstreamCommit is the exact Pi commit of the pin.
const UpstreamCommit = "{pin.commit}"

// Version combines the PiG release line with the upstream Pi build as semver
// build metadata.
const Version = PigVersion + "+{pin.build_metadata}"
'''


def write_shared(path: Path, text: str) -> None:
    """Write a file that concurrent runs of one pin share: untouched when it already has this content, else replaced atomically,
    so a `go` process reading it never sees a truncated file."""
    if path.exists() and path.read_text(encoding="utf-8") == text:
        return
    staging = path.with_name(f".{path.name}.{os.getpid()}.tmp")
    staging.write_text(text, encoding="utf-8")
    os.replace(staging, path)


def overlay(pin: Pin) -> str | None:
    """Write a Go build overlay that swaps pigversion.go for `pin`, and return the GOFLAGS word."""
    pig = pig_version()
    text = render_pigversion(pin, pig)
    import hashlib

    digest = hashlib.sha256(text.encode()).hexdigest()[:16]
    directory = Path(os.environ.get("TMPDIR") or "/tmp") / f"pig-pin-overlay-{os.getuid() if hasattr(os, 'getuid') else 0}" / digest
    directory.mkdir(parents=True, exist_ok=True)
    config = directory / "overlay.json"
    write_shared(directory / "pigversion.go", text)
    write_shared(config, json.dumps({"Replace": {str(PIGVERSION): str(directory / "pigversion.go")}}))
    return f"-overlay={config}"
