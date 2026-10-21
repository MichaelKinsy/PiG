"""get_agent_dir, get_config_dir_name and get_version read the host's PIG_EXT_HOST_INFO entry, as Go's sdk.AgentDir/ConfigDirName/Version and Rust's
get_agent_dir/get_config_dir_name/get_version do (extensions/sdk/host_info_test.go). A process without a host entry gets an error, never a made-up path."""

from __future__ import annotations

import os
import unittest
from unittest import mock

import pig_sdk


class HostInfo(unittest.TestCase):
    def test_reads_the_hosts_environment_entry(self) -> None:
        entry = '{"agentDir":"/home/u/.pig/agent","configDirName":".pig","version":"1.1.0"}'
        with mock.patch.dict(os.environ, {"PIG_EXT_HOST_INFO": entry}):
            self.assertEqual(pig_sdk.get_agent_dir(), "/home/u/.pig/agent")
            self.assertEqual(pig_sdk.get_config_dir_name(), ".pig")
            self.assertEqual(pig_sdk.get_version(), "1.1.0")

    def test_unavailable_without_a_host_that_provides_it(self) -> None:
        for name, value in {"unset": "", "not JSON": "{", "no agent directory": '{"configDirName":".pig"}'}.items():
            with self.subTest(name), mock.patch.dict(os.environ, {"PIG_EXT_HOST_INFO": value}):
                with self.assertRaises(pig_sdk.HostInfoUnavailable):
                    pig_sdk.get_agent_dir()

    def test_package_dir_and_default_shell_come_from_the_host(self) -> None:
        entry = '{"agentDir":"/a","packageDir":"/opt/pig","shell":{"shell":"/bin/bash","args":["-c"]}}'
        with mock.patch.dict(os.environ, {"PIG_EXT_HOST_INFO": entry}):
            self.assertEqual(pig_sdk.get_package_dir(), "/opt/pig")
            self.assertEqual(pig_sdk.get_shell_config(), pig_sdk.ShellConfig("/bin/bash", ("-c",)))
        with mock.patch.dict(os.environ, {"PIG_EXT_HOST_INFO": '{"agentDir":"/a","shell":{"error":"No bash shell found."}}'}):
            with self.assertRaisesRegex(OSError, "No bash shell found."):
                pig_sdk.get_shell_config()
            with self.assertRaises(pig_sdk.HostInfoUnavailable):
                pig_sdk.get_package_dir()
        with mock.patch.dict(os.environ, {"PIG_EXT_HOST_INFO": ""}):
            with self.assertRaises(pig_sdk.HostInfoUnavailable):
                pig_sdk.get_shell_config()

    def test_a_custom_shell_path_is_checked_here_as_pi_does(self) -> None:
        import tempfile

        with mock.patch.dict(os.environ, {"PIG_EXT_HOST_INFO": ""}), tempfile.TemporaryDirectory() as existing:
            self.assertEqual(pig_sdk.get_shell_config(existing), pig_sdk.ShellConfig(existing, ("-c",)))
            with self.assertRaisesRegex(OSError, "Custom shell path not found: " + existing + "/missing"):
                pig_sdk.get_shell_config(existing + "/missing")
        from pig_sdk.host_info import _is_legacy_wsl_bash_path

        legacy = {
            "C:\\Windows\\System32\\bash.exe": True, "c:/windows/sysnative/bash.exe": True, "D:\\WINDOWS\\SYSTEM32\\BASH.EXE": True, "\u212a:\\windows\\system32\\bash.exe": True,
            "C:\\Windows\\System32\\bash.exe ": False, "C:\\Windows\\bash.exe": False, "C:\\Program Files\\Git\\bin\\bash.exe": False,
            "1:\\windows\\system32\\bash.exe": False, "\u0130:\\windows\\system32\\bash.exe": False, "C:\\W\u0130ndows\\System32\\bash.exe": False,
        }
        for shell, want in legacy.items():
            self.assertEqual(_is_legacy_wsl_bash_path(shell), want, shell)


if __name__ == "__main__":
    unittest.main()
