"""Pi's create<Tool>ToolDefinition options (core/tools/*.ts) as the SDK sends them to the host.

The behavior of the tools is the shared conformance row TestConformance_BuiltinToolDelegationAcrossSDKs; these cases pin what the SDK does before and after the
host call: which options cross the wire, which functions stay, the refusal of an incomplete operations object and the tool-result shape of a host result.
"""

from __future__ import annotations

import base64
import unittest

import pig_sdk
from pig_sdk import builtin_tool


class BuiltinToolOptionsTest(unittest.TestCase):
    def test_serializable_options_cross_and_functions_stay(self) -> None:
        calls: list[str] = []
        spec = builtin_tool._read_spec("/c", pig_sdk.ReadToolOptions(
            auto_resize_images=False, resize_options=pig_sdk.ModelImageResizeOptions(max_width=5),
            operations=pig_sdk.ReadOperations(read_file=lambda path: calls.append(path) or b"\x00\xffbytes", access=lambda path: None),
        ))
        self.assertEqual(spec.options, {"autoResizeImages": False, "resizeOptions": {"maxWidth": 5}, "operations": ["readFile", "access"]})
        handle = builtin_tool._Registry.register(spec.callbacks)
        try:
            encoded = builtin_tool.dispatch_callback({"handle": handle, "member": "operations.readFile", "args": {"path": "/a"}})
            self.assertEqual(base64.b64decode(encoded), b"\x00\xffbytes")
            self.assertEqual(calls, ["/a"])
        finally:
            builtin_tool._Registry.release(handle)
        with self.assertRaises(RuntimeError):
            builtin_tool.dispatch_callback({"handle": handle, "member": "operations.readFile", "args": {"path": "/a"}})

    def test_optional_members_are_announced_only_when_given(self) -> None:
        grep = builtin_tool._grep_spec("/c", pig_sdk.GrepToolOptions(operations=pig_sdk.GrepOperations(is_directory=lambda path: True)))
        self.assertEqual(grep.options["operations"], ["isDirectory"])
        grep = builtin_tool._grep_spec("/c", pig_sdk.GrepToolOptions(operations=pig_sdk.GrepOperations(is_directory=lambda path: True, read_file=lambda path: "x")))
        self.assertEqual(grep.options["operations"], ["isDirectory", "readFile"])

    def test_an_operations_object_missing_a_member_is_refused(self) -> None:
        with self.assertRaisesRegex(TypeError, "operations.access is required"):
            builtin_tool._read_spec("/c", {"operations": {"read_file": lambda path: b""}})
        with self.assertRaisesRegex(TypeError, "operations.exec is required"):
            builtin_tool._shell_spec("bash", "/c", pig_sdk.BashToolOptions(operations=pig_sdk.BashOperations(exec=None)))

    def test_a_spawn_hook_and_the_shell_options_are_announced(self) -> None:
        spec = builtin_tool._shell_spec("bash", "/c", pig_sdk.BashToolOptions(command_prefix="set -e", expose_session_environment=False, spawn_hook=lambda context: context))
        self.assertEqual(spec.options, {"exposeSessionEnvironment": False, "commandPrefix": "set -e", "spawnHook": True})
        self.assertIn("spawnHook", spec.callbacks)

    def test_a_host_result_is_the_dict_a_tool_returns(self) -> None:
        wire = builtin_tool._wire_result({"content": [{"type": "text", "text": ""}], "details": {"a": 1}, "structuredContent": {"exit_code": 0}, "isError": True})
        self.assertEqual(list(wire), ["content", "details", "structured_content", "is_error"])
        self.assertEqual(wire["structured_content"], {"exit_code": 0})
        self.assertIs(wire["is_error"], True)


if __name__ == "__main__":
    unittest.main()
