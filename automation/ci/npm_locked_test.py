import importlib.util
import os
from pathlib import Path
import shlex
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("npm_locked", Path(__file__).with_name("npm-locked.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


# Windows returns ERROR_PRIVILEGE_NOT_HELD when this process may not create
# symbolic links (no Developer Mode or elevation).
ERROR_PRIVILEGE_NOT_HELD = 1314


def symlink_or_skip(test, link, target):
    """Create a directory symlink, skipping the test only where the host forbids it."""
    try:
        link.symlink_to(target, target_is_directory=True)
    except OSError as error:
        if getattr(error, "winerror", None) == ERROR_PRIVILEGE_NOT_HELD:
            test.skipTest(f"creating symbolic links needs Developer Mode or elevation on Windows: {error}")
        raise


class NpmLockedTest(unittest.TestCase):
    def test_content_stamp_and_shared_tree(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            owner = root / "owner"
            owner.mkdir()
            (owner / "package.json").write_text('{}')
            (owner / "package-lock.json").write_text('{}')
            with patch.object(module.subprocess, "run", side_effect=lambda *args, **kwargs: (owner / "node_modules").mkdir(exist_ok=True)) as npm:
                module.ensure(owner)
                npm.assert_called_once()
                os.utime(owner / "package-lock.json", None)
                module.ensure(owner)
                npm.assert_called_once()
                (owner / "package-lock.json").write_text('{"changed":true}')
                module.ensure(owner)
                self.assertEqual(npm.call_count, 2)
                borrower = root / "borrower"
                borrower.mkdir()
                for name in ("package.json", "package-lock.json"):
                    (borrower / name).write_bytes((owner / name).read_bytes())
                symlink_or_skip(self, borrower / "node_modules", owner / "node_modules")
                module.ensure(borrower)
                self.assertEqual(npm.call_count, 2)
                (borrower / "package.json").write_text('{"changed":true}')
                with self.assertRaisesRegex(ValueError, "shared path"):
                    module.ensure(borrower)
                self.assertEqual(npm.call_count, 2)
                # A symlinked ancestor also belongs to another checkout.
                alias = root / "alias"
                symlink_or_skip(self, alias, owner)
                (owner / "package.json").write_text('{"changed":true}')
                with self.assertRaisesRegex(ValueError, "shared path"):
                    module.ensure(alias)
                self.assertEqual(npm.call_count, 2)

    def test_failed_or_racing_install_does_not_certify(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            for name in ("package.json", "package-lock.json"):
                (root / name).write_text('{}')
            with patch.object(module.subprocess, "run", side_effect=OSError("fixture failure")):
                with self.assertRaises(OSError):
                    module.ensure(root)
            self.assertFalse((root / "node_modules" / ".pig-deps-ready").exists())
            with patch.object(module.subprocess, "run", side_effect=lambda *args, **kwargs: (root / "package.json").write_text('{"changed":true}')):
                with self.assertRaisesRegex(ValueError, "changed during install"):
                    module.ensure(root)
            self.assertFalse((root / "node_modules" / ".pig-deps-ready").exists())

    def test_runs_npm_as_installed_on_path(self):
        # Node's Windows installer provides npm.cmd, not npm.exe. CreateProcess
        # does not apply PATHEXT, so the bare name "npm" must be resolved first.
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            bin_dir = root / "npm bin"
            bin_dir.mkdir()
            # PATH holds only bin_dir below, so the fake npm records its
            # arguments with this test's own interpreter, named absolutely,
            # instead of commands such as mkdir that PATH no longer reaches.
            recorder = bin_dir / "record_npm_args.py"
            recorder.write_text(
                "import os, sys\n"
                "os.makedirs('node_modules', exist_ok=True)\n"
                "with open(os.path.join('node_modules', 'npm-args.txt'), 'w') as out:\n"
                "    out.write(' '.join(sys.argv[1:]))\n"
            )
            if os.name == "nt":
                (bin_dir / "npm.cmd").write_text(f'@"{sys.executable}" "{recorder}" %*\n')
            else:
                npm = bin_dir / "npm"
                npm.write_text(f'#!/bin/sh\nexec {shlex.quote(sys.executable)} {shlex.quote(str(recorder))} "$@"\n')
                npm.chmod(0o755)
            project = root / "project"
            project.mkdir()
            for name in ("package.json", "package-lock.json"):
                (project / name).write_text('{}')
            with patch.dict(os.environ, {"PATH": str(bin_dir)}):
                module.ensure(project)
            self.assertEqual((project / "node_modules" / "npm-args.txt").read_text().split(), ["ci", "--ignore-scripts", "--no-audit", "--no-fund"])
            self.assertTrue((project / "node_modules" / ".pig-deps-ready").is_file())


if __name__ == "__main__":
    unittest.main()
