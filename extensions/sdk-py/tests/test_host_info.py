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


if __name__ == "__main__":
    unittest.main()
