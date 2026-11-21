"""Fail-closed regression tests for automation/ci/test-warm.sh --rust-only."""
import os
from pathlib import Path
import shutil
import stat
import subprocess
import tempfile
import unittest

REPO_ROOT = Path(__file__).resolve().parents[2]
SCRIPT = REPO_ROOT / "automation" / "ci" / "test-warm.sh"


def _copy_script_tree(root: Path, with_daemon_manifest: bool = False) -> Path:
    """Create a minimal repo layout holding test-warm.sh (and optionally the daemon Cargo.toml)."""
    ci_dir = root / "automation" / "ci"
    ci_dir.mkdir(parents=True)
    dst = ci_dir / "test-warm.sh"
    shutil.copy2(SCRIPT, dst)
    dst.chmod(dst.stat().st_mode | stat.S_IXUSR)
    sdk_dir = root / "extensions" / "sdk-rs"
    sdk_dir.mkdir(parents=True)
    (sdk_dir / "Cargo.toml").write_text('[package]\nname = "pig-sdk"\nversion = "0.0.0"\n', encoding="utf-8")
    (sdk_dir / "Cargo.lock").write_text("# lock\n", encoding="utf-8")
    if with_daemon_manifest:
        daemon = root / ".upstream" / "current" / "packages" / "env" / "daemon"
        daemon.mkdir(parents=True)
        (daemon / "Cargo.toml").write_text('[package]\nname = "env-daemon"\nversion = "0.0.0"\n', encoding="utf-8")
        (daemon / "Cargo.lock").write_text("# lock\n", encoding="utf-8")
    return dst


def _write_stub(bin_dir: Path, name: str, body: str) -> None:
    path = bin_dir / name
    path.write_text("#!/usr/bin/env bash\n" + body + "\n", encoding="utf-8")
    path.chmod(0o755)


def _minimal_bin(tmp: Path, include_cargo: bool, go_body: str = "exit 0", cargo_body: str = "exit 0") -> str:
    """Build a PATH directory containing symlinks to standard tools (excluding cargo unless requested)."""
    bin_dir = tmp / "bin"
    bin_dir.mkdir()
    for tool in ("bash", "dirname", "mktemp", "rm", "tr", "cat", "uname", "mkdir"):
        found = shutil.which(tool)
        if found:
            (bin_dir / tool).symlink_to(found)
    _write_stub(bin_dir, "go", go_body)
    if include_cargo:
        _write_stub(bin_dir, "cargo", cargo_body)
    return str(bin_dir)


class TestWarmRustOnlyFailClosed(unittest.TestCase):
    def test_missing_cargo_fails_closed_in_rust_only_mode(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            tree = tmp / "repo"
            script = _copy_script_tree(tree)
            env = {
                "HOME": str(tmp),
                "PATH": _minimal_bin(tmp, include_cargo=False),
            }
            proc = subprocess.run(
                [str(script), "--rust-only", "cli"],
                cwd=tree,
                env=env,
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(proc.returncode, 0, f"expected non-zero exit when cargo is absent; stdout={proc.stdout!r} stderr={proc.stderr!r}")
            self.assertIn("cargo", proc.stderr.lower())

    def test_missing_cargo_still_skips_without_rust_only(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            tree = tmp / "repo"
            (tree / "piglets" / "porter" / "extensions" / "pig-porter").mkdir(parents=True)
            script = _copy_script_tree(tree)
            env = {
                "HOME": str(tmp),
                "PATH": _minimal_bin(tmp, include_cargo=False, go_body="exit 0"),
            }
            proc = subprocess.run(
                [str(script)],
                cwd=tree,
                env=env,
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertEqual(proc.returncode, 0, f"expected exit 0 without --rust-only; stdout={proc.stdout!r} stderr={proc.stderr!r}")
            self.assertIn("skipping Rust caches", proc.stderr)

    def test_unknown_target_name_fails_closed(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            tree = tmp / "repo"
            script = _copy_script_tree(tree)
            env = {
                "HOME": str(tmp),
                "PATH": _minimal_bin(tmp, include_cargo=True),
            }
            proc = subprocess.run(
                [str(script), "--rust-only", "bogus-target"],
                cwd=tree,
                env=env,
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(proc.returncode, 0, f"expected non-zero exit for unknown target; stdout={proc.stdout!r} stderr={proc.stderr!r}")
            self.assertIn("bogus-target", proc.stderr)
            for known in ("subprocess", "runtimecell", "cli", "conformance", "env-oracle"):
                self.assertIn(known, proc.stderr)

    def test_env_oracle_with_absent_daemon_manifest_fails_closed(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            tree = tmp / "repo"
            script = _copy_script_tree(tree, with_daemon_manifest=False)
            env = {
                "HOME": str(tmp),
                "PATH": _minimal_bin(tmp, include_cargo=True),
            }
            proc = subprocess.run(
                [str(script), "--rust-only", "env-oracle"],
                cwd=tree,
                env=env,
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(proc.returncode, 0, f"expected non-zero exit when daemon Cargo.toml is absent; stdout={proc.stdout!r} stderr={proc.stderr!r}")
            self.assertIn("Cargo.toml", proc.stderr)

    def test_cargo_build_failure_fails_closed(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            tree = tmp / "repo"
            script = _copy_script_tree(tree)
            env = {
                "HOME": str(tmp),
                "PATH": _minimal_bin(
                    tmp,
                    include_cargo=True,
                    go_body='if [ "$1" = "run" ] && [ "$2" = "./automation/ci/cargotarget" ]; then echo "/tmp/fake-target"; fi; exit 0',
                    cargo_body="echo 'simulated cargo failure' >&2; exit 101",
                ),
            }
            proc = subprocess.run(
                [str(script), "--rust-only", "cli"],
                cwd=tree,
                env=env,
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(proc.returncode, 0, f"expected non-zero exit when cargo fails; stdout={proc.stdout!r} stderr={proc.stderr!r}")

    def test_prints_target_dir_and_seconds_per_target(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            tree = tmp / "repo"
            script = _copy_script_tree(tree, with_daemon_manifest=True)
            env = {
                "HOME": str(tmp),
                "PATH": _minimal_bin(
                    tmp,
                    include_cargo=True,
                    go_body='if [ "$1" = "run" ] && [ "$2" = "./automation/ci/cargotarget" ]; then echo "/tmp/fake-$3"; fi; exit 0',
                    cargo_body="exit 0",
                ),
            }
            proc = subprocess.run(
                [str(script), "--rust-only", "cli", "env-oracle"],
                cwd=tree,
                env=env,
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertEqual(proc.returncode, 0, f"stdout={proc.stdout!r} stderr={proc.stderr!r}")
            self.assertIn("[test-warm] Rust SDK cell dependencies -> /tmp/fake-cli", proc.stdout)
            self.assertRegex(proc.stdout, r"\[test-warm\] cli warmed in [0-9]+ s")
            self.assertIn("[test-warm] Pi env daemon -> /tmp/fake-env-oracle", proc.stdout)
            self.assertRegex(proc.stdout, r"\[test-warm\] env-oracle warmed in [0-9]+ s")


if __name__ == "__main__":
    unittest.main()
