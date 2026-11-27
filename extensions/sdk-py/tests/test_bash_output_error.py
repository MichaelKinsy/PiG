import unittest

from pig_sdk import _user_bash_event_result
from pig_sdk.kit import BashExecution
from pig_sdk.llm_convert import convert_to_llm


class TestBashOutputErrorParity(unittest.TestCase):
    def test_convert_to_llm_truncation_notes(self) -> None:
        cases = [
            (
                {"role": "bashExecution", "command": "ls", "output": "a", "truncated": True, "fullOutputPath": "/tmp/out.log"},
                "[Output truncated. Full output: /tmp/out.log]",
            ),
            (
                {"role": "bashExecution", "command": "ls", "output": "a", "truncated": True, "fullOutputError": "ENOSPC: no space left"},
                "[Output truncated. Could not save the full output: ENOSPC: no space left]",
            ),
            (
                {"role": "bashExecution", "command": "ls", "output": "a", "truncated": True},
                "[Output truncated]",
            ),
        ]
        for msg, want in cases:
            converted = convert_to_llm([msg])
            self.assertEqual(len(converted), 1)
            text = converted[0]["content"][0]["text"]
            self.assertTrue(text.endswith("\n\n" + want), f"{text!r} does not end with {want!r}")

    def test_user_bash_event_result_drops_null_full_output_error(self) -> None:
        out = _user_bash_event_result({
            "result": {
                "output": "ok",
                "exitCode": 0,
                "cancelled": False,
                "truncated": True,
                "fullOutputPath": None,
                "fullOutputError": None,
            }
        })
        self.assertNotIn("fullOutputPath", out["result"])
        self.assertNotIn("fullOutputError", out["result"])

    def test_kit_bash_execution_set_complete_full_output_error(self) -> None:
        b = BashExecution("seq 3000")
        b.set_complete(0, False, True, "", "ENOSPC")
        self.assertEqual(b.complete.get("fullOutputError"), "ENOSPC")
        self.assertNotIn("fullOutputPath", b.complete)


if __name__ == "__main__":
    unittest.main()
