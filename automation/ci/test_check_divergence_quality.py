"""Tests for the approval-binds-text rule of check-divergence-quality.py, on disposable ledgers; no network."""
import importlib.util
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

CI = Path(__file__).resolve().parent
SCRIPT = CI / "check-divergence-quality.py"
spec = importlib.util.spec_from_file_location("check_divergence_quality", SCRIPT)
gate = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = gate
spec.loader.exec_module(gate)

CORE = """## D1 A difference

What: PiG prints the reason in the first line, where Pi prints it in the last line of
the failure report.

Why: the first line is the one a terminal keeps when the report is cut.

Evidence: `TestReasonFirst` in `ai/reason_test.go`; scenario `slash/01-reason.toml`.

Remove when: Pi prints the reason in its first line.

SCRUTINIZED:approved
"""
ADDITIVE = """## D2 An addition

Summary: PiG adds a probe.

Stock disposition: inert capability.

What: the probe reports its state.

Remove when: the probe is removed.

Locked by: `TestProbe`.

SCRUTINIZED:approved
"""


class Fixture(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.write("internal/coding/pigversion/pigversion.go", 'package pigversion\nconst UpstreamPackageVersion = "1.5.2"\n')
        self.write("ai/reason_test.go", "package ai\n")
        self.write("docs/parity/divergences/D1.md", CORE)
        self.write("docs/additive-features.md", ADDITIVE)

    def write(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")

    def read(self, name):
        return (self.root / name).read_text(encoding="utf-8")

    def run_gate(self, *args):
        return subprocess.run([sys.executable, "-B", str(SCRIPT), "--root", str(self.root), *args], capture_output=True, text=True)

    def stamp_all(self):
        done = self.run_gate("--initial-stamp")
        self.assertEqual(done.returncode, 0, done.stderr)

    def edit(self, name, old, new):
        text = self.read(name)
        self.assertIn(old, text)
        self.write(name, text.replace(old, new, 1))

    def passes(self):
        done = self.run_gate()
        self.assertEqual(done.returncode, 0, done.stderr + done.stdout)
        return done.stdout

    def fails(self, *wants):
        done = self.run_gate()
        self.assertNotEqual(done.returncode, 0, done.stdout)
        for want in wants:
            self.assertIn(want, done.stderr)
        return done.stderr


class BindingTests(Fixture):
    def test_unbound_approval_fails_and_names_the_hash_to_bind(self):
        self.fails("D1", "binds no text hash", "make divergence-approve ID=D1")

    def test_stamped_records_pass(self):
        self.stamp_all()
        self.assertIn("2 bound to their text", self.passes())
        self.assertRegex(self.read("docs/parity/divergences/D1.md"), r"(?m)^SCRUTINIZED:approved text-sha256:[0-9a-f]{16}$")
        self.assertRegex(self.read("docs/additive-features.md"), r"(?m)^SCRUTINIZED:approved text-sha256:[0-9a-f]{16}$")

    def test_a_changed_word_fails_naming_record_bound_and_current_hash(self):
        self.stamp_all()
        bound = self.run_gate("--print-hash", "D1").stdout.strip()
        self.edit("docs/parity/divergences/D1.md", "in the first line, where", "in the second line, where")
        current = self.run_gate("--print-hash", "D1").stdout.strip()
        self.assertNotEqual(bound, current)
        self.fails("D1", f"binds text-sha256:{bound}", f"hashes to {current}", "make divergence-approve ID=D1")

    def test_a_changed_additive_word_fails(self):
        self.stamp_all()
        self.edit("docs/additive-features.md", "reports its state", "reports its size")
        self.fails("D2", "the approved text changed")

    def test_a_new_approval_line_makes_it_pass_again(self):
        self.stamp_all()
        self.edit("docs/parity/divergences/D1.md", "in the first line, where", "in the second line, where")
        self.assertEqual(self.run_gate("--stamp", "D1", "--date", "2026-11-17").returncode, 0)
        self.passes()

    def test_numbers_durations_flags_and_names_stay_in_the_hash(self):
        self.edit("docs/parity/divergences/D1.md", "Why: the first line", "Why: after 30s `--no-stdin` and `gpt-5` and `PIG_HOME` and 5 retries, the first line")
        self.stamp_all()
        for old, new in (("30s", "60s"), ("`--no-stdin`", "`--stdin`"), ("`gpt-5`", "`gpt-6`"), ("`PIG_HOME`", "`PI_HOME`"), ("5 retries", "6 retries")):
            self.edit("docs/parity/divergences/D1.md", old, new)
            self.fails("D1", "the approved text changed")
            self.edit("docs/parity/divergences/D1.md", new, old)
            self.passes()

    def test_a_changed_inline_path_or_test_name_keeps_the_hash(self):
        self.edit("docs/parity/divergences/D1.md", "Why: the first line", "Why: `ai/reason_test.go` and `TestReasonFirst/sub_case` show the first line")
        self.stamp_all()
        self.write("coding/other_test.go", "package coding\n")
        self.edit("docs/parity/divergences/D1.md", "`ai/reason_test.go` and `TestReasonFirst/sub_case`", "`coding/other_test.go` and `TestReasonOther`")
        self.passes()

    def test_a_reflow_keeps_the_hash(self):
        self.stamp_all()
        text = self.read("docs/parity/divergences/D1.md")
        self.write("docs/parity/divergences/D1.md", text.replace("PiG prints the reason in the first line, where Pi", "PiG  prints\nthe reason in\n  the first line,\nwhere Pi"))
        self.passes()

    def test_a_changed_evidence_list_keeps_the_hash(self):
        self.stamp_all()
        self.edit("docs/parity/divergences/D1.md", "`TestReasonFirst` in `ai/reason_test.go`; scenario `slash/01-reason.toml`.", "`TestReasonLast`, `TestReasonAgain`.\n\n- `ai/reason_test.go`\n- `ai/other_test.go`")
        self.write("ai/other_test.go", "package ai\n")
        self.passes()
        self.edit("docs/additive-features.md", "Locked by: `TestProbe`.", "Locked by: `TestProbeOther`.\n\n1. `TestThird`")
        self.passes()

    def test_inline_test_names_paths_links_and_pin_versions_keep_the_hash(self):
        self.edit("docs/parity/divergences/D1.md", "Why: the first line", "Why: Pi 1.4.0 and `TestOld` (`ai/reason_test.go`) show [the report](docs/a.md) and the first line")
        self.stamp_all()
        self.edit("docs/parity/divergences/D1.md", "Pi 1.4.0 and `TestOld` (`ai/reason_test.go`) show [the report](docs/a.md)", "Pi 1.5.2 and `TestNew` show [the report](docs/b.md)")
        self.passes()

    def test_a_changed_pin_in_the_title_keeps_the_hash(self):
        self.edit("docs/parity/divergences/D1.md", "## D1 A difference", "## D1 A difference (Pi 1.4.0 throws)")
        self.stamp_all()
        self.edit("docs/parity/divergences/D1.md", "Pi 1.4.0 throws", "Pi 1.5.2 throws")
        self.passes()

    def test_a_changed_remove_when_version_fails(self):
        self.edit("docs/parity/divergences/D1.md", "Remove when: Pi prints", "Remove when: Pi 1.6.0 prints")
        self.stamp_all()
        self.edit("docs/parity/divergences/D1.md", "Pi 1.6.0", "Pi 1.7.0")
        self.fails("D1", "the approved text changed")

    def test_a_field_the_gate_does_not_know_is_hashed(self):
        self.edit("docs/parity/divergences/D1.md", "Remove when:", "Quirk: the first line is cut at 80 columns.\n\nRemove when:")
        self.stamp_all()
        self.edit("docs/parity/divergences/D1.md", "cut at 80 columns", "cut at 100 columns")
        self.fails("D1", "the approved text changed")

    def test_a_paragraph_after_an_evidence_block_is_hashed(self):
        self.stamp_all()
        self.edit("docs/parity/divergences/D1.md", "scenario `slash/01-reason.toml`.", "scenario `slash/01-reason.toml`.\n\nPiG also truncates the reason at 80 columns.")
        self.fails("D1", "the approved text changed")

    def test_a_removed_approval_fails_as_before(self):
        self.stamp_all()
        text = self.read("docs/parity/divergences/D1.md")
        self.write("docs/parity/divergences/D1.md", text[: text.index("SCRUTINIZED")])
        self.fails("D1: missing SCRUTINIZED:approved")

    def test_a_hash_for_the_wrong_text_fails(self):
        self.stamp_all()
        other = self.run_gate("--print-hash", "D2").stdout.strip()
        text = self.read("docs/parity/divergences/D1.md")
        self.write("docs/parity/divergences/D1.md", text[: text.index("text-sha256:")] + f"text-sha256:{other}\n")
        self.fails("D1", f"binds text-sha256:{other}")

    def test_a_malformed_hash_fails(self):
        self.edit("docs/parity/divergences/D1.md", "SCRUTINIZED:approved", "SCRUTINIZED:approved text-sha256:abc")
        self.fails("D1", "malformed text-sha256:abc")

    def test_an_edit_of_the_source_file_shows_before_the_ledger_is_regenerated(self):
        self.write("docs/parity/DIVERGENCES.md", "## D1 A difference\n\nstale roll-up\n")
        self.stamp_all()
        self.edit("docs/parity/divergences/D1.md", "Why: the first line", "Why: the last line")
        self.fails("D1", "the approved text changed")

    def test_the_approval_line_keeps_its_date_and_prose_when_stamped(self):
        self.edit("docs/parity/divergences/D1.md", "SCRUTINIZED:approved", "SCRUTINIZED:approved 2026-11-16. The owner approved it.")
        self.stamp_all()
        self.assertIn("SCRUTINIZED:approved 2026-11-16 text-sha256:", self.read("docs/parity/divergences/D1.md"))
        self.assertIn(". The owner approved it.", self.read("docs/parity/divergences/D1.md"))
        self.passes()
        self.edit("docs/parity/divergences/D1.md", "The owner approved it.", "The owner approved it twice.")
        self.fails("D1", "the approved text changed")

    def test_print_hash_text_shows_what_is_hashed(self):
        shown = self.run_gate("--print-hash", "D1", "--text").stdout
        self.assertIn("D1 A difference What: PiG prints the reason in the first line", shown)
        self.assertNotIn("TestReasonFirst", shown)
        self.assertIn("Remove when: Pi prints the reason in its first line.", shown)


class PendingTests(Fixture):
    PENDING = "automation/ci/divergence-reapproval-pending.txt"

    def bind_additive(self):
        self.edit("docs/additive-features.md", "SCRUTINIZED:approved", "SCRUTINIZED:approved text-sha256:" + "0" * 16)
        self.edit("docs/additive-features.md", "0" * 16, self.run_gate("--print-hash", "D2").stdout.strip())

    def test_a_pending_record_passes_and_is_printed(self):
        self.write(self.PENDING, "# shrink only\nD1 behaviour changed after the first marker abc1234 (+3/-1 words)\n")
        self.bind_additive()
        text = self.passes()
        self.assertIn("1 pending re-approval", text)
        self.assertIn("pending re-approval: D1 [behaviour]: changed after the first marker abc1234 (+3/-1 words)", text)

    def test_an_edited_bound_record_passes_only_with_a_pending_entry(self):
        self.stamp_all()
        self.edit("docs/parity/divergences/D1.md", "in the first line, where", "in the second line, where")
        self.fails("D1", "the approved text changed", "Only the owner approves", "An agent or a lane never runs it", "divergence-reapproval-pending.txt")
        self.write(self.PENDING, "D1 behaviour the reason moved to the second line\n")
        text = self.passes()
        self.assertIn("pending re-approval: D1 [behaviour]: the reason moved to the second line", text)

    def test_an_unbound_record_that_is_not_listed_fails(self):
        self.write(self.PENDING, "# none\n")
        self.fails("D1", "binds no text hash")

    def test_a_pending_entry_for_a_bound_record_is_stale(self):
        self.stamp_all()
        self.write(self.PENDING, "D1 behaviour old\n")
        self.fails("D1", "listed in automation/ci/divergence-reapproval-pending.txt but its approval binds the current text", "delete the stale entry")

    def test_a_pending_entry_for_a_missing_record_fails(self):
        self.stamp_all()
        self.write(self.PENDING, "D9 behaviour gone\n")
        self.fails("D9 is not a record")

    def test_a_pending_record_still_needs_its_approval_marker(self):
        self.write(self.PENDING, "D1 behaviour changed\n")
        self.edit("docs/parity/divergences/D1.md", "SCRUTINIZED:approved", "SCRUTINIZED:pending")
        self.fails("D1: missing SCRUTINIZED:approved")

    def test_every_class_is_accepted_and_an_unknown_one_fails(self):
        self.bind_additive()
        for kind in ("behaviour", "scope", "remove-when", "owner-field", "editorial", "new"):
            self.write(self.PENDING, f"D1 {kind} reason\n")
            self.passes()
        self.write(self.PENDING, "D1 minor reason\n")
        self.fails("want `D<N> <class> <reason>`")
        self.write(self.PENDING, "D1 behaviour\n")
        self.fails("want `D<N> <class> <reason>`")

    def test_entries_must_be_sorted_by_class_then_number(self):
        self.write("docs/parity/divergences/D3.md", CORE.replace("D1", "D3"))
        self.bind_additive()
        self.write(self.PENDING, "D1 scope a\nD3 behaviour b\n")
        self.fails("entries must be sorted by class")
        self.write(self.PENDING, "D3 behaviour b\nD1 scope a\n")
        self.passes()

    def test_initial_stamp_skips_pending_records_and_adds_no_date(self):
        self.write(self.PENDING, "D1 new added by a member\n")
        self.assertEqual(self.run_gate("--initial-stamp").returncode, 0)
        self.assertNotIn("text-sha256", self.read("docs/parity/divergences/D1.md"))
        self.assertRegex(self.read("docs/additive-features.md"), r"(?m)^SCRUTINIZED:approved text-sha256:[0-9a-f]{16}$")
        self.passes()

    def test_initial_stamp_refuses_when_any_record_is_bound(self):
        self.stamp_all()
        self.edit("docs/parity/divergences/D1.md", "in the first line, where", "in the second line, where")
        before = self.read("docs/parity/divergences/D1.md")
        done = self.run_gate("--initial-stamp")
        self.assertEqual(done.returncode, 2)
        self.assertIn("refuses to run", done.stderr)
        self.assertEqual(before, self.read("docs/parity/divergences/D1.md"))

    def test_there_is_no_general_stamp_of_unbound_records(self):
        self.assertNotEqual(self.run_gate("--stamp-unbound").returncode, 0)

    def test_stamp_is_the_owners_approval_with_a_date_and_ends_the_pending_state(self):
        self.write(self.PENDING, "D1 behaviour changed\nD2 editorial reworded\n")
        self.assertEqual(self.run_gate("--stamp", "D1", "--date", "2026-11-17").returncode, 0)
        self.assertRegex(self.read("docs/parity/divergences/D1.md"), r"(?m)^SCRUTINIZED:approved 2026-11-17 text-sha256:[0-9a-f]{16}$")
        self.assertEqual(self.read(self.PENDING), "D2 editorial reworded\n")
        self.edit("docs/additive-features.md", "SCRUTINIZED:approved", "SCRUTINIZED:approved 2026-10-01")
        self.assertEqual(self.run_gate("--stamp", "D2", "--date", "2026-11-18").returncode, 0)
        self.assertRegex(self.read("docs/additive-features.md"), r"(?m)^SCRUTINIZED:approved 2026-11-18 text-sha256:[0-9a-f]{16}$")
        self.assertEqual(self.read(self.PENDING), "")
        self.passes()


if __name__ == "__main__":
    unittest.main()
