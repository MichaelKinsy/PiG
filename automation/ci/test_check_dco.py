"""Regression tests for check-dco.sh against disposable git repositories; no network or real home access."""
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest

CI = Path(__file__).resolve().parent
SCRIPT = CI / "check-dco.sh"
IDENTITIES = CI / "dco-identities.txt"

ALICE = ("Alice Example", "alice@example.invalid")
ALICE_ALIAS = ("Alice Example", "alice@alias.invalid")  # mapped to ALICE by the fixture's dco-identities.txt
ALICE_OTHER = ("Alice Example", "alice@other.invalid")  # same name, not mapped
BOB = ("Bob Example", "bob@example.invalid")
MAP = "# fixture map\n\n%s = %s\n" % ("%s <%s>" % ALICE, "%s <%s>" % ALICE_ALIAS)


def fixture_environment():
    """Keep inherited Git repository/config overrides out of disposable fixtures."""
    environment = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    environment["GIT_CONFIG_GLOBAL"] = os.devnull
    environment["GIT_CONFIG_NOSYSTEM"] = "1"
    return environment


def signoff(identity):
    return "Signed-off-by: %s <%s>" % identity


def remediation(identity, *commits):
    """The remediation block: a lead line, one '<sha> <subject>' row per commit, a blank line, a sign-off."""
    rows = "\n".join("%s %s" % (sha, subject) for sha, subject in commits)
    return "I, %s, retroactively sign off on these commits:\n%s\n\n%s" % (identity[0], rows, signoff(identity))


class CheckDCOTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        # The script reads dco-identities.txt next to itself, so each fixture gets its own copy and map.
        self.tools = tempfile.TemporaryDirectory()
        self.addCleanup(self.tools.cleanup)
        self.script = Path(self.tools.name) / "check-dco.sh"
        shutil.copy(SCRIPT, self.script)
        (Path(self.tools.name) / "dco-identities.txt").write_text(MAP)
        self.env = fixture_environment()
        self.git("init", "-q", "-b", "main")
        self.commit(ALICE, "base\n\n" + signoff(ALICE))
        self.base = self.git("rev-parse", "HEAD").strip()

    def git(self, *args, env=None):
        done = subprocess.run(
            ["git", "-C", self.directory.name, "-c", "commit.gpgsign=false", *args],
            env=env or self.env, check=True, capture_output=True, text=True,
        )
        return done.stdout

    def commit(self, identity, message):
        """Commit an empty tree change authored and committed by identity; returns the full SHA."""
        env = dict(self.env)
        env.update(
            GIT_AUTHOR_NAME=identity[0], GIT_AUTHOR_EMAIL=identity[1],
            GIT_COMMITTER_NAME=identity[0], GIT_COMMITTER_EMAIL=identity[1],
        )
        self.git("commit", "-q", "--allow-empty", "-m", message, env=env)
        return self.git("rev-parse", "HEAD").strip()

    def check(self):
        return subprocess.run(
            ["bash", str(self.script), self.base, "HEAD"],
            cwd=self.directory.name, env=self.env, capture_output=True, text=True,
        )

    def assertPasses(self, count):
        done = self.check()
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertEqual(done.stdout, "DCO: %d non-merge commit(s) signed off\n" % count)

    def assertFails(self, *unsigned):
        done = self.check()
        self.assertEqual(done.returncode, 1, done.stdout)
        self.assertIn("The following commits lack a DCO sign-off matching the author or committer:", done.stderr)
        self.assertIn("Create commits with: git commit --signoff", done.stderr)
        self.assertEqual(
            [line.split()[0] for line in done.stderr.splitlines() if line.startswith("  ")],
            [self.git("rev-parse", "--short", sha).strip() for sha in unsigned],
        )

    def test_signed_commit_passes(self):
        self.commit(ALICE, "feature\n\n" + signoff(ALICE))
        self.assertPasses(1)

    def test_unsigned_commit_fails(self):
        unsigned = self.commit(ALICE, "feature")
        self.assertFails(unsigned)

    def test_remediated_by_same_author_passes(self):
        unsigned = self.commit(ALICE, "feature")
        self.commit(ALICE, remediation(ALICE, (unsigned, "feature")))
        self.assertPasses(2)

    def test_remediation_may_use_a_ten_character_prefix(self):
        unsigned = self.commit(ALICE, "feature")
        self.commit(ALICE, remediation(ALICE, (unsigned[:10], "feature")))
        self.assertPasses(2)

    def test_remediation_with_a_short_prefix_fails(self):
        unsigned = self.commit(ALICE, "feature")
        self.commit(ALICE, remediation(ALICE, (unsigned[:9], "feature")))
        self.assertFails(unsigned)

    def test_remediation_by_different_author_fails(self):
        unsigned = self.commit(ALICE, "feature")
        self.commit(BOB, remediation(BOB, (unsigned, "feature")))
        self.assertFails(unsigned)

    def test_remediation_naming_the_author_in_the_lead_line_by_a_different_author_fails(self):
        unsigned = self.commit(ALICE, "feature")
        self.commit(BOB, remediation(ALICE, (unsigned, "feature")).replace(signoff(ALICE), signoff(BOB)))
        self.assertFails(unsigned)

    def test_remediation_by_same_name_different_email_fails(self):
        unsigned = self.commit(ALICE, "feature")
        self.commit(ALICE_OTHER, remediation(ALICE_OTHER, (unsigned, "feature")))
        self.assertFails(unsigned)

    def test_remediation_naming_the_wrong_sha_fails(self):
        unsigned = self.commit(ALICE, "feature")
        other = self.commit(ALICE, "other\n\n" + signoff(ALICE))
        self.commit(ALICE, remediation(ALICE, (other, "other")))
        self.assertFails(unsigned)

    def test_remediation_without_its_own_sign_off_fails(self):
        unsigned = self.commit(ALICE, "feature")
        unsigned_remediation = self.commit(
            ALICE, remediation(ALICE, (unsigned, "feature")).replace(signoff(ALICE), "")
        )
        self.assertFails(unsigned, unsigned_remediation)

    def test_remediation_sign_off_by_another_person_fails(self):
        unsigned = self.commit(ALICE, "feature")
        forged = self.commit(ALICE, remediation(ALICE, (unsigned, "feature")).replace(signoff(ALICE), signoff(BOB)))
        self.assertFails(unsigned, forged)

    def test_remediation_before_the_unsigned_commit_fails(self):
        # A remediation can only vouch for commits that precede it; the SHA of a later commit cannot be known anyway.
        first = self.commit(ALICE, "first")
        self.commit(ALICE, remediation(ALICE, (first, "first")))
        late = self.commit(ALICE, "late")
        self.assertFails(late)

    def test_remediation_names_only_the_commits_it_lists(self):
        first = self.commit(ALICE, "first")
        second = self.commit(ALICE, "second")
        self.commit(ALICE, remediation(ALICE, (first, "first")))
        self.assertFails(second)

    def test_one_remediation_covers_several_commits(self):
        first = self.commit(ALICE, "first")
        second = self.commit(ALICE, "second")
        self.commit(ALICE, remediation(ALICE, (first, "first"), (second, "second")))
        self.assertPasses(3)

    def test_each_identity_needs_its_own_remediation(self):
        alice = self.commit(ALICE, "alice")
        bob = self.commit(BOB, "bob")
        self.commit(ALICE, remediation(ALICE, (alice, "alice"), (bob, "bob")))
        self.assertFails(bob)
        self.commit(BOB, remediation(BOB, (bob, "bob")))
        self.assertPasses(4)

    def test_remediation_by_a_mapped_identity_passes(self):
        unsigned = self.commit(ALICE, "feature")
        self.commit(ALICE_ALIAS, remediation(ALICE_ALIAS, (unsigned, "feature")))
        self.assertPasses(2)

    def test_mapping_works_in_both_directions(self):
        unsigned = self.commit(ALICE_ALIAS, "feature")
        self.commit(ALICE, remediation(ALICE, (unsigned, "feature")))
        self.assertPasses(2)

    def test_mapped_remediation_still_needs_its_own_sign_off(self):
        unsigned = self.commit(ALICE, "feature")
        # Signed off as the unsigned commit's identity, but authored as the alias.
        forged = self.commit(ALICE_ALIAS, remediation(ALICE, (unsigned, "feature")))
        self.assertFails(unsigned, forged)

    def test_mapping_does_not_extend_to_other_people(self):
        unsigned = self.commit(ALICE, "feature")
        self.commit(BOB, remediation(BOB, (unsigned, "feature")))
        self.assertFails(unsigned)

    def test_the_map_ignores_comments_and_blank_lines(self):
        (Path(self.tools.name) / "dco-identities.txt").write_text("# %s <%s> = %s <%s>\n\n" % (*ALICE, *ALICE_ALIAS))
        unsigned = self.commit(ALICE, "feature")
        self.commit(ALICE_ALIAS, remediation(ALICE_ALIAS, (unsigned, "feature")))
        self.assertFails(unsigned)

    def test_remediation_outside_the_range_does_not_count(self):
        unsigned = self.commit(ALICE, "feature")
        self.commit(ALICE, remediation(ALICE, (unsigned, "feature")))
        self.base = unsigned
        self.assertPasses(1)
        self.base = self.git("rev-parse", "HEAD~2").strip()
        self.assertPasses(2)
        self.base = self.git("rev-parse", "HEAD").strip()
        self.assertPasses(0)


class CommittedIdentityMapTests(unittest.TestCase):
    """The repository's own dco-identities.txt: every identity on a line can remediate every other."""

    def test_every_mapped_identity_can_remediate_its_peers(self):
        groups = [
            [part.strip() for part in line.split(" = ")]
            for line in IDENTITIES.read_text().splitlines()
            if line.strip() and not line.startswith("#")
        ]
        self.assertTrue(groups, "dco-identities.txt maps no one")
        pairs = 0
        for group in groups:
            parsed = [re.fullmatch(r"(.+) <([^<>]+)>", identity) for identity in group]
            self.assertTrue(all(parsed), group)
            identities = [m.groups() for m in parsed]
            for unsigned_identity in identities:
                for remediating_identity in identities:
                    if remediating_identity == unsigned_identity:
                        continue
                    pairs += 1
                    case = CheckDCOTests()
                    case.setUp()
                    try:
                        case.script = SCRIPT  # the committed script reads the committed map
                        unsigned = case.commit(unsigned_identity, "feature")
                        case.commit(remediating_identity, remediation(remediating_identity, (unsigned, "feature")))
                        case.assertPasses(2)
                    finally:
                        case.directory.cleanup()
                        case.tools.cleanup()
        self.assertTrue(pairs, "dco-identities.txt has no line with two identities")


if __name__ == "__main__":
    unittest.main()
