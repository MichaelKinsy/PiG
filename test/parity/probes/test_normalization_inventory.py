import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch

import normalization_inventory as inventory


class NormalizationInventoryTest(unittest.TestCase):
    def test_each_rule_requires_its_own_reason(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = root / "test/parity/scenarios/rpc/probe.toml"
            path.parent.mkdir(parents=True)
            path.write_text('name="probe"\ndescription="wire"\n[assert]\nnormalize_replace=[{pattern="x",with="y",reason="D2 identity"},{pattern="bad",with="good"}]\n')
            with self.assertRaisesRegex(ValueError, "rule-level reason"):
                inventory.inventory(root)

    def test_inventory_retains_each_owner_pattern_and_reason(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(inventory, "MECHANISMS", {}):
            root = Path(directory)
            base = root / "test/parity/scenarios"
            base.mkdir(parents=True)
            for name in ("a", "b"):
                (base / f"{name}.toml").write_text(f'name="{name}"\ndescription="wire"\n[assert]\nnormalize_replace=[{{pattern="x",with="y",reason="{name} identity"}}]\n')
            result = inventory.inventory(root)
            self.assertEqual([x["transforms"][0]["reason"] for x in result["scenarios"]], ["a identity", "b identity"])
            before = inventory.render(root)
            (base / "b.toml").unlink()
            self.assertNotEqual(before, inventory.render(root))

    def test_a_floating_transcript_sequence_is_inventoried_with_its_reason(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(inventory, "MECHANISMS", {}):
            root = Path(directory)
            path = root / "test/parity/scenarios/startup/probe.toml"
            path.parent.mkdir(parents=True)
            floating = 'sequence="\\u001b[>4;2m"\nevidence_runs=20\nevidence_positions=[35,204]\n'
            path.write_text('name="probe"\ndescription="startup"\n[[assert.transcript_sequences_equal]]\nwindow="startup"\n[[assert.transcript_sequences_equal.floating]]\n' + floating)
            with self.assertRaisesRegex(ValueError, "floating requires a rule-level reason"):
                inventory.inventory(root)
            path.write_text('name="probe"\ndescription="startup"\n[[assert.transcript_sequences_equal]]\nwindow="startup"\n[[assert.transcript_sequences_equal.floating]]\nreason="answers the query late"\n' + floating)
            transform = inventory.inventory(root)["scenarios"][0]["transforms"][0]
            self.assertEqual((transform["kind"], transform["window"], transform["sequence"], transform["reason"]), ("transcript_sequences_floating", "startup", "\x1b[>4;2m", "answers the query late"))

    def test_json_cannot_be_filtered_by_regex(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = root / "test/parity/scenarios/probe.toml"
            path.parent.mkdir(parents=True)
            path.write_text('name="probe"\ndescription="wire"\n[assert]\njson_output_equal=true\nnormalize_replace=[{pattern="error",with="ok",reason="not permitted"}]\n')
            with self.assertRaisesRegex(ValueError, "cannot use text normalization"):
                inventory.inventory(root)


if __name__ == "__main__":
    unittest.main()
