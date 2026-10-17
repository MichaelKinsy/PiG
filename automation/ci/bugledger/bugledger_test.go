// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSentencesKeepCodeSpansAndAbbreviationsWhole(t *testing.T) {
	got := sentences("The `a.b()` call (e.g. on Windows) failed. Before, it hung. It now returns.")
	want := []string{"The `a.b()` call (e.g. on Windows) failed.", "Before, it hung.", "It now returns."}
	if !slices.Equal(got, want) {
		t.Fatalf("sentences = %q, want %q", got, want)
	}
}

func TestSymptomIsTheBeforeSentence(t *testing.T) {
	symptom, fix := symptomOf("The editor keeps its cursor. Before, the cursor jumped to the start on paste.")
	if symptom != "The cursor jumped to the start on paste." || fix != "The editor keeps its cursor." {
		t.Fatalf("symptom %q, fix %q", symptom, fix)
	}
	if symptom, _ := symptomOf("Ops validate again. Before this fix, `Assert` reported an error."); symptom != "`Assert` reported an error." {
		t.Fatalf("symptom after \"Before this fix,\" = %q", symptom)
	}
	symptom, _ = symptomOf("Reading a file no longer fails when it grows.")
	if symptom != "Reading a file no longer fails when it grows." {
		t.Fatalf("symptom without a Before sentence = %q", symptom)
	}
}

// A commit message that refers to another commit by hash keeps a readable sentence, and tooling words of the project's gates stay private.
func TestPublicTextRewordsCommitReferencesAndDropsGateWording(t *testing.T) {
	for in, want := range map[string]string{
		"Port of pi-mono 6b07b4e5: a long stream was aborted.":                                 "Port of pi-mono: a long stream was aborted.",
		"bdb7ad8292 replaced the cap with math.MaxInt, so the buffer grows.":                   "An earlier change replaced the cap with math.MaxInt, so the buffer grows.",
		"It added a cap (a new divergence-guard hit). Pi has one dispatcher.":                  "Pi has one dispatcher.",
		"The divguard entries follow the move. The portlint entry is deleted. It works.":       "It works.",
		"It moved three baselined literals. It works.":                                         "It works.",
		"It carries D134 for the lead to confirm. It works.":                                   "It works.",
		"The output is byte-identical to Pi's. The cap holds. It matches byte for byte.":       "The cap holds.",
		"The cap of 1024 bytes holds.":                                                         "The cap of 1024 bytes holds.",
		"It returns { ...rest, edits } and reads :433 and :578.":                               "It returns { ...rest, edits } and reads :433 and :578.",
		"It failed 2 of 12 runs on " + "imla" + "dris with a high RSS. The timer kept the VM.": "The timer kept the VM.",
		"The cache under /Users/" + "x/.dev" + "cache grew. It works.":                         "It works.",
	} {
		if got := publicText(in); got != want {
			t.Errorf("publicText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPublicTextDropsPrivateSentencesVersionsAndHashes(t *testing.T) {
	in := "The loop never exited (aca35e7ff). Red on integrate-042: it failed. The lane owner agreed. Pi 1.0.4 differs. It grew 22% too. Closes 24 rows of shard 4. Review of batch-ctor-oracle found it. Contract coding-agent/tree-selector flipped. Mutation-checked twice. Per the lead's ruling it moved. S5k (rules/functions.go) judges it. A new oracle runs it."
	got := publicText(in)
	if got != "The loop never exited ()." && got != "The loop never exited ( )." && !strings.HasPrefix(got, "The loop never exited") {
		t.Fatalf("publicText = %q", got)
	}
	for _, bad := range []string{"integrate", "lane", "1.0.4", "22%", "aca35e7", "shard", "batch", "Contract", "Mutation", "ruling", "S5k", "oracle"} {
		if strings.Contains(got, bad) {
			t.Fatalf("publicText kept %q: %q", bad, got)
		}
	}
}

func TestRootCausePrefersALabelThenCauseWordsThenTheFirstParagraph(t *testing.T) {
	labelled, from := rootCauseOf([]string{"Fix x\n\nSomething first.\n\nRoot cause: the handler never armed the timer."}, "")
	if labelled != "the handler never armed the timer." || from != "commit" {
		t.Fatalf("labelled = %q from %q", labelled, from)
	}
	because, _ := rootCauseOf([]string{"Fix x\n\nFirst paragraph.\n\nIt failed because the lock was held."}, "")
	if because != "It failed because the lock was held." {
		t.Fatalf("because = %q", because)
	}
	first, _ := rootCauseOf([]string{"Fix x\n\nPi reads the file once."}, "")
	if first != "Pi reads the file once." {
		t.Fatalf("first = %q", first)
	}
	if none, from := rootCauseOf([]string{"Fix x"}, "It fails."); none != "" || from != "" {
		t.Fatalf("no source should give no cause, got %q from %q", none, from)
	}
}

func TestImpactAndPlatformComeFromTheWording(t *testing.T) {
	cases := []struct{ note, impact, platform string }{
		{"A deadlock froze the UI on macOS.", "Crash, hang or runaway process", "macOS"},
		{"A credential file was world-readable on Linux.", "Credential or permission exposure", "Linux"},
		{"The update fails with EPERM.", "An operation failed or was rejected", "Windows"},
		{"The block is drawn in the wrong colors.", "Wrong display", "All platforms"},
		{"A value is kept.", "Behavior differed from Pi", "All platforms"},
	}
	for _, c := range cases {
		if got := impactOf(c.note); got != c.impact {
			t.Errorf("impactOf(%q) = %q, want %q", c.note, got, c.impact)
		}
		if got := platformOf(c.note); got != c.platform {
			t.Errorf("platformOf(%q) = %q, want %q", c.note, got, c.platform)
		}
	}
}

func TestFixedBulletsReadOnlyTheFixedSectionOfLaterReleases(t *testing.T) {
	text := "## [Unreleased]\n\n### Fixed\n\n- New fix. <!-- fix: abc1234, def5678 -->\n- Wrapped\n  bullet.\n\n### Added\n\n- Not a fix.\n\n## [0.5.0] - 2026\n\n### Fixed\n\n- Fixed in 0.5.0.\n\n## [0.4.1] - 2026\n\n### Fixed\n\n- Old fix.\n"
	got := fixedBullets(text, "0.4.1", false)
	var texts []string
	for _, b := range got {
		texts = append(texts, b.Text)
	}
	want := []string{"New fix.", "Wrapped bullet.", "Fixed in 0.5.0."}
	if !slices.Equal(texts, want) {
		t.Fatalf("bullets = %q, want %q", texts, want)
	}
	if !slices.Equal(got[0].Fix, []string{"abc1234", "def5678"}) {
		t.Fatalf("fix marker = %v", got[0].Fix)
	}
	frag := fixedBullets("### Fixed\n\n- Fragment fix.\n", "0.4.1", true)
	if len(frag) != 1 || frag[0].Version != "Unreleased" {
		t.Fatalf("fragment bullets = %+v", frag)
	}
}

type testRepo struct {
	t    *testing.T
	root string
}

func (r testRepo) run(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.root, "-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r testRepo) write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r testRepo) commit(msg string, paths ...string) string {
	r.t.Helper()
	r.run(append([]string{"add", "--"}, paths...)...)
	r.run("commit", "-q", "-m", msg)
	return r.run("rev-parse", "HEAD")
}

const divergenceFixture = "# DIVERGENCES\n\n## D1 Old\n\nWhat: old.\n\nRemove when: never.\n\nSCRUTINIZED:approved\n"

func newFixture(t *testing.T) testRepo {
	t.Helper()
	r := testRepo{t: t, root: t.TempDir()}
	r.run("init", "-q", "-b", "main")
	r.write("CHANGELOG.md", "# Changelog\n\n## [Unreleased]\n\n## [0.4.1] - 2026-01-01\n")
	r.write("docs/parity/DIVERGENCES.md", divergenceFixture)
	r.write("old_test.go", "package x\n\nfunc TestOld(t *testing.T) {}\n")
	r.commit("Release 0.4.1", "CHANGELOG.md", "docs/parity/DIVERGENCES.md", "old_test.go")
	return r
}

func TestBuildLedgerFromHistory(t *testing.T) {
	r := newFixture(t)
	r.write("loop.go", "package x\n")
	r.write("loop_test.go", "package x\n\nfunc TestLoopExits(t *testing.T) {}\n")
	r.write("changelog.d/loop.md", "### Fixed\n\n- On Windows the loop no longer hangs. Before, it never exited.\n")
	r.commit("loop: exit the loop\n\nThe loop hung.\n\nRoot cause: the loop never checked the stop flag.\n\nTestLoopExits fails without the fix.", "loop.go", "loop_test.go", "changelog.d/loop.md")
	r.write("quick.go", "package x\n")
	quick := r.commit("Fix a quick crash", "quick.go")
	r.write("docs/parity/DIVERGENCES.md", divergenceFixture+"\n## D2 Aborted tools\n\nWhat: PiG stops.\n\nPi bug: Pi waits forever.\n\nPi issue: https://example.com/pi/issues/1\n\nRemove when: Pi stops waiting.\n\nEvidence: `TestLoopExits`.\n\nSCRUTINIZED:approved\n")
	r.commit("Record D2", "docs/parity/DIVERGENCES.md")

	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	var loop, crash, d2 *Entry
	for i := range l.Entries {
		e := &l.Entries[i]
		switch {
		case strings.Contains(e.Title, "loop no longer hangs"):
			loop = e
		case e.Origin == "commit":
			crash = e
		case e.Divergence == "D2":
			d2 = e
		}
	}
	if loop == nil || crash == nil || d2 == nil {
		t.Fatalf("missing entries: loop=%v crash=%v d2=%v in %+v", loop != nil, crash != nil, d2 != nil, l.Entries)
	}
	if loop.Platform != "Windows" || loop.Impact != "Crash, hang or runaway process" || loop.Class != ClassOwn {
		t.Errorf("loop entry fields: %+v", loop)
	}
	if loop.RootCause != "the loop never checked the stop flag." || loop.CauseFrom != "commit" {
		t.Errorf("loop root cause = %q from %q", loop.RootCause, loop.CauseFrom)
	}
	if !slices.Equal(loop.Tests, []string{"loop_test.go::TestLoopExits"}) {
		t.Errorf("loop tests = %v", loop.Tests)
	}
	if d2.Class != ClassAheadOfPi || d2.PiIssue != "https://example.com/pi/issues/1" || d2.RemoveWhen != "Pi stops waiting." {
		t.Errorf("D2 entry = %+v", d2)
	}
	if !slices.Equal(d2.Tests, []string{"loop_test.go::TestLoopExits"}) {
		t.Errorf("D2 tests = %v", d2.Tests)
	}
	if len(crash.Commits) != 1 || !strings.HasPrefix(quick, crash.Commits[0].Hash) {
		t.Errorf("crash entry commits = %+v, want %s", crash.Commits, quick)
	}
	for _, e := range l.Entries {
		if !strings.HasPrefix(e.ID, "BF-") {
			t.Errorf("entry ID %q", e.ID)
		}
	}

	// A fix-titled commit that is not a bug is listed under its category, not as an entry.
	r.write("automation/ci/bugledger/exempt.txt", quick[:10]+" Repair of a merge: not a release bug\n")
	l, err = build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if l.Exempt["Repair of a merge"] != 1 {
		t.Errorf("exempt = %v", l.Exempt)
	}
	for _, e := range l.Entries {
		if e.Origin == "commit" {
			t.Errorf("an exempt commit became an entry: %+v", e)
		}
	}
}

func TestVerifyFailsOnAStaleLedgerAndAMissingTest(t *testing.T) {
	r := newFixture(t)
	r.write("loop.go", "package x\n")
	r.write("loop_test.go", "package x\n\nfunc TestLoopExits(t *testing.T) {}\n")
	r.write("changelog.d/loop.md", "### Fixed\n\n- The loop no longer hangs.\n")
	r.commit("loop: exit the loop\n\nTestLoopExits guards it.", "loop.go", "loop_test.go", "changelog.d/loop.md")

	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	r.commit("Add the ledger", "docs/bug-ledger.json", "docs/site/docs/bug-ledger.md")
	if err := run(r.root, "0.4.1", true, true); err != nil {
		t.Fatalf("a freshly generated ledger must verify strictly: %v", err)
	}

	// A new fix with no ledger entry fails the check.
	r.write("changelog.d/other.md", "### Fixed\n\n- A second bug no longer happens.\n")
	r.commit("other: fix it", "changelog.d/other.md")
	err := run(r.root, "0.4.1", true, false)
	if err == nil || !strings.Contains(err.Error(), "no ledger entry for") || !strings.Contains(err.Error(), "A second bug no longer happens") {
		t.Fatalf("a fix without an entry must fail the check, got %v", err)
	}
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	if err := run(r.root, "0.4.1", true, false); err != nil {
		t.Fatalf("after regeneration the check must pass: %v", err)
	}

	// A divergence record added after the ledger was generated fails the check until the ledger accounts for it.
	r.write("docs/parity/DIVERGENCES.md", divergenceFixture+"\n## D9 A new difference\n\nWhat: new.\n\nRemove when: never.\n\nSCRUTINIZED:approved\n")
	r.commit("Record D9", "docs/parity/DIVERGENCES.md")
	err = run(r.root, "0.4.1", true, false)
	if err == nil || !strings.Contains(err.Error(), "divergence D9") || !strings.Contains(err.Error(), "not accounted for") {
		t.Fatalf("a divergence the ledger omits must fail the check, got %v", err)
	}
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}

	// An entry that names a test that no longer exists fails the check.
	r.write("loop_test.go", "package x\n")
	err = run(r.root, "0.4.1", true, false)
	if err == nil || !strings.Contains(err.Error(), "TestLoopExits") || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("a missing test must fail the check, got %v", err)
	}
}

func TestExplicitFixMarkerNamesTheFixCommit(t *testing.T) {
	r := newFixture(t)
	r.write("a.go", "package x\n")
	r.write("a_test.go", "package x\n\nfunc TestAFixed(t *testing.T) {}\n")
	fix := r.commit("a: handle empty input", "a.go", "a_test.go")
	r.write("changelog.d/a.md", "### Fixed\n\n- Empty input no longer crashes. <!-- fix: "+fix[:10]+" -->\n")
	r.commit("Note the fix", "changelog.d/a.md")
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 {
		t.Fatalf("entries = %+v", l.Entries)
	}
	e := l.Entries[0]
	if len(e.Commits) != 1 || !strings.HasPrefix(fix, e.Commits[0].Hash) || !slices.Equal(e.Tests, []string{"a_test.go::TestAFixed"}) {
		t.Fatalf("entry = %+v", e)
	}
	r.write("changelog.d/b.md", "### Fixed\n\n- Bad marker. <!-- fix: deadbeef00 -->\n")
	if _, err := build(r.root, "0.4.1"); err == nil || !strings.Contains(err.Error(), "not a commit") {
		t.Fatalf("a marker that names no commit must fail, got %v", err)
	}
}

func TestBuildNeedsTheReleaseCommit(t *testing.T) {
	r := testRepo{t: t, root: t.TempDir()}
	r.run("init", "-q", "-b", "main")
	r.write("CHANGELOG.md", "# Changelog\n")
	r.commit("start", "CHANGELOG.md")
	if _, err := build(r.root, "0.4.1"); err == nil || !strings.Contains(err.Error(), "fetch-depth") {
		t.Fatalf("a history without the baseline release must fail with a clear message, got %v", err)
	}
}

func TestReleaseNotesDraftComesFromTheLedgers(t *testing.T) {
	r := newFixture(t)
	r.write("internal/coding/pigversion/pigversion.go", "package pigversion\n\nconst UpstreamVersion = \"1.1.0\"\n")
	r.write("CHANGELOG.md", "# Changelog\n\n## [Unreleased]\n\n## [0.5.0] - 2026-02-01\n\n### Upgrading from 0.4.x\n\n- Change your call.\n\n### Added\n\n- A new flag.\n\n### Fixed\n\n- A hang on Windows no longer happens.\n\n## [0.4.1] - 2026-01-01\n\n### Added\n\n- Old thing.\n")
	r.write("loop.go", "package x\n")
	r.write("docs/parity/DIVERGENCES.md", divergenceFixture+"\n## D2 New\n\nWhat: new.\n\nRemove when: never.\n\nSCRUTINIZED:approved\n")
	r.commit("loop: stop hanging\n\nRoot cause: the loop never checked the stop flag.", "CHANGELOG.md", "loop.go", "docs/parity/DIVERGENCES.md", "internal/coding/pigversion/pigversion.go")
	r.write("changelog.d/x.md", "### Changed\n\n- A fragment change.\n")
	notes, err := releaseNotes(r.root, "0.4.1", "0.5.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# PiG 0.5.0 release notes (draft)", "Draft. Not published.", "## Upgrading from 0.4.x", "- Change your call.", "- A new flag.", "- A fragment change.", "PiG 0.5.0 follows Pi 1.1.0", "Crash, hang or runaway process: 1", "- D2: New"} {
		if !strings.Contains(notes, want) {
			t.Errorf("the draft lacks %q:\n%s", want, notes)
		}
	}
	if strings.Contains(notes, "Old thing") {
		t.Errorf("the draft includes a note of the previous release:\n%s", notes)
	}
}

// A fix that edits the body of an existing test, or a helper after it, names the test it edits and no other: git names the nearest preceding function in the hunk header, and only an indented added line sits inside that function.
func TestTestsOfCommitNamesAnEditedTestNotATrailingHelper(t *testing.T) {
	r := newFixture(t)
	r.write("edit_test.go", "package x\n\nfunc TestEdited(t *testing.T) {\n\tone()\n\ttwo()\n\tthree()\n}\n\nfunc TestOther(t *testing.T) {\n\tone()\n\ttwo()\n\tthree()\n}\n")
	r.commit("add tests", "edit_test.go")
	r.write("edit_test.go", "package x\n\nfunc TestEdited(t *testing.T) {\n\tone()\n\ttwo()\n\tthree()\n\tfour()\n}\n\nfunc TestOther(t *testing.T) {\n\tone()\n\ttwo()\n\tthree()\n}\n\nvar helper = 1\n")
	hash := r.commit("Fix an edge", "edit_test.go")
	idx, err := buildTestIndex(r.root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := testsOfCommit(repo{root: r.root}, idx, hash, "Fix an edge")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"edit_test.go::TestEdited"}; !slices.Equal(got, want) {
		t.Errorf("tests = %v, want %v (TestOther is untouched, and the helper after it is not its body)", got, want)
	}
}

// A note whose commit carries both its fragment and its test, in a lane of several unrelated commits, keeps that commit's tests: the commit stands in for the fix, and its own tests guard the bug.
func TestEntryMatchedByNoteTextKeepsTheFixCommitsTests(t *testing.T) {
	r := newFixture(t)
	for _, n := range []string{"a", "b", "c", "d"} {
		r.write("docs/"+n+".md", n+"\n")
		r.commit("docs: "+n, "docs/"+n+".md")
	}
	r.write("parse_test.go", "package x\n\nfunc TestParseKeepsEmptyList(t *testing.T) {}\n")
	r.write("changelog.d/parse.md", "### Fixed\n\n- The parser keeps an empty list instead of dropping it.\n")
	r.commit("Unrelated words", "parse_test.go", "changelog.d/parse.md")
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entries {
		if strings.Contains(e.Title, "parser keeps an empty list") {
			if !slices.Equal(e.Tests, []string{"parse_test.go::TestParseKeepsEmptyList"}) {
				t.Errorf("tests = %v (commits %+v)", e.Tests, e.Commits)
			}
			return
		}
	}
	t.Fatalf("no entry for the note in %+v", l.Entries)
}

// A record in the form the Pi-bug lanes write, with "Pi #N" in its title and no "Pi bug:" paragraph, is a Pi defect fixed ahead of Pi with that issue; a record that cites no Pi issue is a deliberate difference. Every record added since the baseline is accounted for.
func TestEveryNewDivergenceIsAccountedFor(t *testing.T) {
	r := newFixture(t)
	r.write("loop_test.go", "package x\n\nfunc TestNoStdin(t *testing.T) {}\n")
	r.write("docs/parity/DIVERGENCES.md", divergenceFixture+
		"\n## D3 `--no-stdin` skips the startup read (Pi #10415)\n\nWhat: PiG adds the flag.\n\nWhy: Pi waits for the end of an open pipe.\n\nObservable effect: PiG starts; Pi hangs.\n\nLocked by: `TestNoStdin`.\n\nRemove when: Pi fixes #10415.\n\nSCRUTINIZED:approved\n"+
		"\n## D5 A streamed call without an id (pi-mono #10299)\n\nWhat: PiG gives it an id.\n\nWhy: the session wedges.\n\nRemove when: Pi fixes it.\n\nSCRUTINIZED:approved\n"+
		"\n## D4 Terminal input\n\nWhat: the driver owns input.\n\nWhy: no blocking call on the loop.\n\nRemove when: never.\n\nSCRUTINIZED:approved\n")
	r.commit("Record D3 and D4", "docs/parity/DIVERGENCES.md", "loop_test.go")
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range l.Divergences {
		got = append(got, d.ID+"="+d.Entry)
	}
	if want := []string{"D3=BF-D3", "D4=", "D5=BF-D5"}; !slices.Equal(got, want) {
		t.Fatalf("divergences = %v, want %v", got, want)
	}
	for _, e := range l.Entries {
		if e.Divergence == "D3" {
			if e.PiIssue != "https://github.com/earendil-works/pi/issues/10415" || e.Class != ClassAheadOfPi || e.Symptom != "PiG starts; Pi hangs." || !slices.Equal(e.Tests, []string{"loop_test.go::TestNoStdin"}) {
				t.Errorf("D3 entry = %+v", e)
			}
			return
		}
	}
	t.Fatal("no entry for D3")
}

// squash copies the development repository dev into a new repository whose history is the release commit and one commit with the tree of HEAD, as a squash merge to a public branch leaves it. The development commits are not in the new repository.
func squash(t *testing.T, dev testRepo, release string) testRepo {
	t.Helper()
	pub := testRepo{t: t, root: t.TempDir()}
	pub.run("init", "-q", "-b", "main")
	extract := func(rev string) {
		t.Helper()
		cmd := exec.Command("sh", "-c", "git -C \"$1\" archive \"$2\" | tar -x -C \"$3\"", "sh", dev.root, rev, pub.root)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("archive %s: %v\n%s", rev, err, out)
		}
	}
	extract(release)
	pub.run("add", "-A")
	pub.run("commit", "-q", "-m", "Release 0.4.1")
	pub.run("rm", "-q", "-r", ".")
	extract("HEAD")
	pub.run("add", "-A")
	pub.run("commit", "-q", "-m", "Land the development branch (#7)")
	return pub
}

func TestASquashLandingKeepsTheCommittedLedger(t *testing.T) {
	dev := newFixture(t)
	release := dev.run("rev-parse", "HEAD")
	dev.write("a.go", "package x\n")
	dev.write("a_test.go", "package x\n\nfunc TestAFixed(t *testing.T) {}\n")
	fix := dev.commit("Port Pi 1.1.0 empty input handling\n\nPi 1.1.0 a.ts (#123) guards empty input.", "a.go", "a_test.go")
	dev.write("changelog.d/a.md", "### Fixed\n\n- Empty input no longer crashes. <!-- fix: "+fix[:10]+" -->\n- The loop no longer hangs.\n")
	dev.write("loop.go", "package x\n")
	dev.commit("loop: exit the loop", "changelog.d/a.md", "loop.go")
	dev.write("b.go", "package x\n")
	dev.commit("Fix a crash in b", "b.go")
	dev.write("b.go", "package x\n\n// follow-up\n")
	followUp := dev.commit("Fix the crash in b again", "b.go")
	dev.write("c.go", "package x\n")
	repair := dev.commit("Fix the merge fallout in c", "c.go")
	if err := run(dev.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	dev.write("automation/ci/bugledger/exempt.txt", followUp[:10]+" see "+dev.run("rev-parse", "HEAD~2")[:10]+"\n"+repair[:10]+" Repair of a merge: fallout\n")
	if err := run(dev.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	dev.commit("Add the ledger", "docs/bug-ledger.json", "docs/site/docs/bug-ledger.md", "automation/ci/bugledger/exempt.txt")
	if err := run(dev.root, "0.4.1", true, true); err != nil {
		t.Fatalf("the development history must verify strictly: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join(dev.root, jsonPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(committed), `"class": "pi-port"`) || !strings.Contains(string(committed), `"Repair of a merge": 1`) {
		t.Fatalf("the ported fix is not classed as a Pi port:\n%s", committed)
	}

	pub := squash(t, dev, release)
	if err := run(pub.root, "0.4.1", true, true); err != nil {
		t.Fatalf("a squash landing must keep the committed ledger valid: %v", err)
	}
	if err := run(pub.root, "0.4.1", false, false); err != nil {
		t.Fatalf("regenerating the ledger on a squash landing: %v", err)
	}
	regenerated, err := os.ReadFile(filepath.Join(pub.root, jsonPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(regenerated, committed) {
		t.Fatalf("regenerating on a squash landing changed the ledger:\n%s\nwant\n%s", regenerated, committed)
	}
}

func TestAPortCommitClassesItsReleaseNoteAsAPiPort(t *testing.T) {
	for _, c := range []struct {
		msg, class string
	}{
		{"Port Pi 1.1.0 empty input handling", ClassPiPort},
		{"Follow Pi 1.0.4 durable: growing reads", ClassPiPort},
		{"mcp: cancellable sign-in (Pi 1.1.0)", ClassPiPort},
		{"Retry a cancelled stream\n\nPi 1.0.4 packages/ai/src/utils/retry.ts (#10379).", ClassPiPort},
		{"fix(mcp/oauth): reject an iss parameter\n\nPort of Pi main c5f5b328 (RFC 9207).", ClassPiPort},
		{"Ports to the new API", ClassOwn},
		{"mcp: name the issuer\n\nPort of Pi mainline behavior to the cache.", ClassOwn},
		{"mcp: end stdio servers on exit\n\nPi's transport kills them.\n\nThe scenario compares with Pi 1.1.0 (#10562).", ClassOwn},
	} {
		if got := classOf("Empty input no longer crashes.", []string{c.msg}); got != c.class {
			t.Errorf("classOf(%q) = %q, want %q", c.msg, got, c.class)
		}
	}
}

func TestADivergenceJoinsTheNoteThatCitesItAlone(t *testing.T) {
	r := newFixture(t)
	r.write("tool.go", "package x\n")
	r.write("changelog.d/tool.md", "### Fixed\n\n- Escape lets a stuck tool go after a grace period (D2).\n")
	r.commit("tool: abandon a stuck tool", "tool.go", "changelog.d/tool.md")
	r.write("changelog.d/docs.md", "### Fixed\n\n- The divergence page lists D2 and D3.\n")
	r.write("docs/parity/DIVERGENCES.md", divergenceFixture+
		"\n## D2 Aborted tools\n\nWhat: PiG stops.\n\nPi bug: Pi waits forever.\n\nRemove when: Pi stops waiting.\n\nSCRUTINIZED:approved\n"+
		"\n## D3 Queued messages (Pi #9886)\n\nWhat: PiG keeps queued custom messages.\n\nRemove when: Pi keeps them.\n\nSCRUTINIZED:approved\n")
	r.commit("Document D2 and D3", "changelog.d/docs.md", "docs/parity/DIVERGENCES.md")
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	byNote := map[string]Entry{}
	var d3 *Entry
	for i, e := range l.Entries {
		byNote[e.Note] = e
		if e.Origin == "divergence" && e.Divergence == "D3" {
			d3 = &l.Entries[i]
		}
	}
	if e := byNote["Escape lets a stuck tool go after a grace period (D2)."]; e.Class != ClassAheadOfPi || e.Divergence != "D2" {
		t.Errorf("the fix note of D2 = %+v", e)
	}
	if e := byNote["The divergence page lists D2 and D3."]; e.Class != ClassOwn || e.Divergence != "" {
		t.Errorf("a note that lists several divergences took one: %+v", e)
	}
	if d3 == nil || d3.Class != ClassAheadOfPi || d3.PiIssue != piRepo+"/issues/9886" || d3.Title != "Queued messages (Pi #9886)" || d3.Symptom != "PiG keeps queued custom messages." {
		t.Errorf("a record whose heading names a Pi issue is a Pi defect fixed first: %+v", d3)
	}
}

// A fix that landed on the public branch first and reached the development branch twice (the public commit and the development commit it follows up) keeps its one entry after the squash landing, which drops the development commit.
func TestASquashLandingKeepsAFollowUpOfAMissingCommit(t *testing.T) {
	dev := newFixture(t)
	dev.run("checkout", "-q", "-b", "dev")
	dev.write("overlay.go", "package x\n")
	first := dev.commit("Fix a deadlock in the overlay", "overlay.go")
	dev.run("checkout", "-q", "main")
	dev.write("overlay.go", "package x\n\n// landed\n")
	public := dev.commit("Fix a deadlock in the overlay (#5)", "overlay.go")
	dev.run("checkout", "-q", "dev")
	dev.run("-c", "user.name=T", "-c", "user.email=t@example.com", "merge", "-q", "--no-edit", "-X", "theirs", "main")
	dev.write("automation/ci/bugledger/exempt.txt", public[:10]+" see "+first[:10]+"\n")
	if err := run(dev.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	dev.commit("Add the ledger", "docs/bug-ledger.json", "docs/site/docs/bug-ledger.md", "automation/ci/bugledger/exempt.txt")
	committed, err := os.ReadFile(filepath.Join(dev.root, jsonPath))
	if err != nil {
		t.Fatal(err)
	}

	pubRoot := filepath.Join(t.TempDir(), "pub")
	dev.run("clone", "-q", "--no-local", "--single-branch", "--branch", "main", dev.root, pubRoot)
	pub := testRepo{t: t, root: pubRoot}
	if out, err := exec.Command("sh", "-c", "git -C \"$1\" archive dev | tar -x -C \"$2\"", "sh", dev.root, pubRoot).CombinedOutput(); err != nil {
		t.Fatalf("archive: %v\n%s", err, out)
	}
	pub.run("add", "-A")
	pub.run("commit", "-q", "-m", "Land the development branch (#6)")
	if _, ok := (repo{root: pubRoot}).resolve(first); ok {
		t.Fatal("the squashed repository must not hold the development commit")
	}
	if err := run(pub.root, "0.4.1", true, true); err != nil {
		t.Fatalf("a follow-up whose target the squash dropped must keep the ledger valid: %v", err)
	}
	if err := run(pub.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	if regenerated, _ := os.ReadFile(filepath.Join(pub.root, jsonPath)); !bytes.Equal(regenerated, committed) {
		t.Fatalf("regenerating on a squash landing changed the ledger:\n%s\nwant\n%s", regenerated, committed)
	}
}

func TestACommitTitleKeepsItsPublicHead(t *testing.T) {
	r := newFixture(t)
	r.write("render.go", "package x\n")
	r.commit("fix: the renderer draws on the options terminal (review 14's reuse silenced it)", "render.go")
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Title != "fix: the renderer draws on the options terminal" {
		t.Fatalf("entries = %+v", l.Entries)
	}
}

// A pull request that fixes a bug regenerates the ledger before it merges, so the ledger names the pull request's commits. The squash merge replaces them with one commit titled after the pull request. That commit is the landing of the entries it carries, so the branch it lands on still passes the check, and regenerating there records the squash commit as a fix commit of those entries.
func TestAFixTitledSquashMergeJoinsTheEntriesItLands(t *testing.T) {
	for _, c := range []struct {
		name, msg string
		fragment  bool
	}{
		{name: "release note", msg: "b: stop the loop", fragment: true},
		{name: "fix-titled commit", msg: "Fix a hang in b"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dev := newFixture(t)
			release := dev.run("rev-parse", "HEAD")
			dev.write("b.go", "package x\n")
			paths := []string{"b.go"}
			if c.fragment {
				dev.write("changelog.d/b.md", "### Fixed\n\n- The b loop no longer hangs.\n")
				paths = append(paths, "changelog.d/b.md")
			}
			fix := dev.commit(c.msg, paths...)
			if err := run(dev.root, "0.4.1", false, false); err != nil {
				t.Fatal(err)
			}
			dev.commit("Regenerate the ledger", "docs/bug-ledger.json", "docs/site/docs/bug-ledger.md")
			if err := run(dev.root, "0.4.1", true, true); err != nil {
				t.Fatalf("the pull request's history must verify strictly: %v", err)
			}

			pub := testRepo{t: t, root: t.TempDir()}
			pub.run("init", "-q", "-b", "main")
			extract := func(rev string) {
				t.Helper()
				cmd := exec.Command("sh", "-c", "git -C \"$1\" archive \"$2\" | tar -x -C \"$3\"", "sh", dev.root, rev, pub.root)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("archive %s: %v\n%s", rev, err, out)
				}
			}
			extract(release)
			pub.run("add", "-A")
			pub.run("commit", "-q", "-m", "Release 0.4.1")
			pub.run("rm", "-q", "-r", ".")
			extract("HEAD")
			pub.run("add", "-A")
			squashed := pub.commit("Fix a hang in b (#7)", ".")
			if _, ok := (repo{root: pub.root}).resolve(fix); ok {
				t.Fatal("the squashed repository must not hold the pull request's commit")
			}
			if err := run(pub.root, "0.4.1", true, false); err != nil {
				t.Fatalf("a fix-titled squash merge must keep the check green: %v", err)
			}
			l, err := build(pub.root, "0.4.1")
			if err != nil {
				t.Fatal(err)
			}
			if len(l.Entries) != 1 {
				t.Fatalf("the squash commit must join the landed entry, not add one: %+v", l.Entries)
			}
			var hashes []string
			for _, cm := range l.Entries[0].Commits {
				hashes = append(hashes, cm.Hash)
			}
			if !slices.Contains(hashes, fix[:10]) || !slices.Contains(hashes, squashed[:10]) || l.Entries[0].Commits[slices.Index(hashes, squashed[:10])].PR != "7" {
				t.Fatalf("entry commits = %+v, want the pull request's %s and the squash %s (#7)", l.Entries[0].Commits, fix[:10], squashed[:10])
			}
			// The next pull request lands only its own entry: the earlier squash commit is not a fix commit of it.
			dev.write("c.go", "package x\n")
			fixC := dev.commit("Fix a crash in c", "c.go")
			if err := run(dev.root, "0.4.1", false, false); err != nil {
				t.Fatal(err)
			}
			dev.commit("Regenerate the ledger", "docs/bug-ledger.json", "docs/site/docs/bug-ledger.md")
			pub.run("rm", "-q", "-r", ".")
			extract("HEAD")
			pub.run("add", "-A")
			squashedC := pub.commit("Fix a crash in c (#8)", ".")
			if err := run(pub.root, "0.4.1", true, false); err != nil {
				t.Fatalf("a second fix-titled squash merge must keep the check green: %v", err)
			}
			if l, err = build(pub.root, "0.4.1"); err != nil {
				t.Fatal(err)
			}
			got := map[string][]string{}
			for _, e := range l.Entries {
				for _, cm := range e.Commits {
					got[e.Title] = append(got[e.Title], cm.Hash)
				}
			}
			titleB := c.msg
			if c.fragment {
				titleB = "The b loop no longer hangs"
			}
			for title, want := range map[string][]string{titleB: {fix[:10], squashed[:10]}, "Fix a crash in c": {fixC[:10], squashedC[:10]}} {
				g := slices.Sorted(slices.Values(got[title]))
				if !slices.Equal(g, slices.Sorted(slices.Values(want))) {
					t.Errorf("entry %q commits = %v, want %v", title, g, want)
				}
			}
			if len(l.Entries) != 2 {
				t.Errorf("entries = %+v, want two", l.Entries)
			}
			if err := run(pub.root, "0.4.1", false, false); err != nil {
				t.Fatal(err)
			}
			pub.commit("Regenerate the ledger", "docs/bug-ledger.json", "docs/site/docs/bug-ledger.md")
			if err := run(pub.root, "0.4.1", true, true); err != nil {
				t.Fatalf("the ledger regenerated after the squash must verify strictly: %v", err)
			}
		})
	}
}

// A fix-titled commit that changes only tests, documents, generated records or build tooling is not a bug in a release and needs no entry.
func TestAFixTitledCommitOutsideShippedCodeHasNoEntry(t *testing.T) {
	r := newFixture(t)
	r.write("x_test.go", "package x\n\nfunc TestX(t *testing.T) {}\n")
	r.write("docs/guide.md", "# Guide\n")
	r.write("Makefile", "all:\n")
	r.write("automation/make/ci.mk", "ci:\n")
	r.commit("Fix a flaky test, the guide and the build", "x_test.go", "docs/guide.md", "Makefile", "automation/make/ci.mk")
	r.write("x.go", "package x\n")
	shippedFix := r.commit("Fix the x crash", "x.go")
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || len(l.Entries[0].Commits) != 1 || !strings.HasPrefix(shippedFix, l.Entries[0].Commits[0].Hash) {
		t.Fatalf("entries = %+v, want only the fix of shipped code %s", l.Entries, shippedFix[:10])
	}
}

// A divergence record that gains a Pi defect after the ledger was written fails the check: the committed ledger calls it a deliberate difference.
func TestVerifyFailsWhenADeliberateDifferenceBecomesAPiDefect(t *testing.T) {
	r := newFixture(t)
	r.write("tool.go", "package x\n")
	record := "\n## D2 Aborted tools\n\nWhat: PiG stops.\n\nRemove when: Pi stops waiting.\n\nSCRUTINIZED:approved\n"
	r.write("docs/parity/DIVERGENCES.md", divergenceFixture+record)
	r.commit("tool: stop an aborted tool (D2)", "tool.go", "docs/parity/DIVERGENCES.md")
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	r.commit("Add the ledger", "docs/bug-ledger.json", "docs/site/docs/bug-ledger.md")
	if err := run(r.root, "0.4.1", true, false); err != nil {
		t.Fatal(err)
	}
	r.write("docs/parity/DIVERGENCES.md", divergenceFixture+strings.Replace(record, "Remove when:", "Pi bug: Pi waits forever.\n\nRemove when:", 1))
	r.commit("Record the Pi defect of D2", "docs/parity/DIVERGENCES.md")
	err := run(r.root, "0.4.1", true, false)
	if err == nil || !strings.Contains(err.Error(), "divergence D2 fixes a Pi defect but the ledger lists it as a deliberate difference") {
		t.Fatalf("err = %v", err)
	}
}

// A ledger written while a merge is in progress would read HEAD's history without the merged branch's fix commits and fail the check once the merge is committed, so the tool refuses until the merge is committed.
func TestTheLedgerWaitsForAPendingMergeToBeCommitted(t *testing.T) {
	r := newFixture(t)
	r.run("checkout", "-q", "-b", "side")
	r.write("side.go", "package x\n")
	r.commit("Fix a crash on the side branch", "side.go")
	r.run("checkout", "-q", "main")
	r.write("main.go", "package x\n")
	r.commit("Add main", "main.go")
	r.run("-c", "user.name=T", "-c", "user.email=t@example.com", "merge", "-q", "--no-commit", "--no-ff", "side")
	if err := run(r.root, "0.4.1", false, false); err == nil || !strings.Contains(err.Error(), "a merge is in progress") {
		t.Fatalf("regenerating during a merge: err = %v", err)
	}
	r.run("-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "--no-edit")
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Title != "Fix a crash on the side branch" {
		t.Fatalf("after the merge commit the side branch's fix has its entry: %+v", l.Entries)
	}
}

// A fix committed on the first-parent line and followed directly by a merge keeps its fix commit: the merge's first parent is the fix itself, so the merge did not bring it in.
func TestAFixFollowedByAMergeKeepsItsCommit(t *testing.T) {
	r := newFixture(t)
	r.run("checkout", "-q", "-b", "side")
	r.write("side.go", "package x\n")
	r.commit("Add side", "side.go")
	r.run("checkout", "-q", "main")
	r.write("tree.go", "package x\n")
	r.write("changelog.d/tree.md", "### Fixed\n\n- A request after tree navigation no longer fails.\n")
	fix := r.commit("tree: end the tool flow", "tree.go", "changelog.d/tree.md")
	r.run("-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "merge", "-q", "--no-ff", "--no-edit", "side")
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || len(l.Entries[0].Commits) != 1 || !strings.HasPrefix(fix, l.Entries[0].Commits[0].Hash) {
		t.Fatalf("entries = %+v, want the note fixed by %s", l.Entries, fix[:10])
	}
}

// A fragment of several notes in a short lane must not give every note every commit of the lane: a divergence number names one fix, so a note that cites D3 carries the commits that cite D3, their tests and their root cause, and nothing that belongs to the note that cites D4.
func TestNotesThatCiteDifferentDivergencesDoNotShareFixes(t *testing.T) {
	r := newFixture(t)
	r.write("a.go", "package x\n")
	r.write("a_test.go", "package x\n\nfunc TestAFix(t *testing.T) {}\n")
	r.commit("Fix a stalled stream (D3)\n\nRoot cause: the reader never saw the idle timer.", "a.go", "a_test.go")
	r.write("b.go", "package x\n")
	r.write("b_test.go", "package x\n\nfunc TestBFix(t *testing.T) {}\n")
	r.commit("Fix a lost signature (D4)\n\nRoot cause: the detail filter required text.", "b.go", "b_test.go")
	r.write("changelog.d/two.md", "### Fixed\n\n- A stalled stream ends after the idle timeout (D3).\n- A signature-only reasoning detail is kept (D4).\n")
	r.commit("changelog.d: two", "changelog.d/two.md")
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{ test, cause string }{
		"D3": {"a_test.go::TestAFix", "the reader never saw the idle timer."},
		"D4": {"b_test.go::TestBFix", "the detail filter required text."},
	}
	seen := 0
	for _, e := range l.Entries {
		for d, w := range want {
			if !strings.Contains(e.Note, "("+d+")") {
				continue
			}
			seen++
			if !slices.Equal(e.Tests, []string{w.test}) {
				t.Errorf("%s tests = %v, want only %s", d, e.Tests, w.test)
			}
			if e.RootCause != w.cause {
				t.Errorf("%s root cause = %q, want %q", d, e.RootCause, w.cause)
			}
			if len(e.Commits) != 1 {
				t.Errorf("%s commits = %+v, want only the commit that cites it", d, e.Commits)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("found %d of the 2 entries in %+v", seen, l.Entries)
	}
}

// A fix marker that names the commit of another divergence fails the build instead of publishing a wrong attribution.
func TestAMarkerNamingAnotherDivergencesCommitFailsTheBuild(t *testing.T) {
	r := newFixture(t)
	r.write("a.go", "package x\n")
	a := r.commit("Fix a stalled stream (D3)", "a.go")
	r.write("b.go", "package x\n")
	b := r.commit("Fix a lost signature (D4)", "b.go")
	r.write("changelog.d/two.md", "### Fixed\n\n- A stalled stream ends after the idle timeout (D3). <!-- fix: "+b[:10]+" -->\n- A signature-only reasoning detail is kept (D4). <!-- fix: "+b[:10]+" -->\n")
	r.commit("changelog.d: two", "changelog.d/two.md")
	_ = a
	_, err := build(r.root, "0.4.1")
	if err == nil || !strings.Contains(err.Error(), "cross-attribution") || !strings.Contains(err.Error(), "D4") {
		t.Fatalf("build must fail with a cross-attribution error naming D4, got %v", err)
	}
}

// A commit that writes several notes does not make its short lane part of every note: each note of the fragment would otherwise carry the fix commits of all the others.
func TestAFragmentOfSeveralNotesDoesNotShareItsShortLane(t *testing.T) {
	r := newFixture(t)
	r.write("alpha.go", "package x\n")
	alpha := r.commit("Fix the alpha parser crash", "alpha.go")
	r.write("beta.go", "package x\n")
	beta := r.commit("Fix the beta renderer", "beta.go")
	r.write("changelog.d/two.md", "### Fixed\n\n- The alpha parser no longer crashes on empty input.\n- The beta renderer draws the last line.\n")
	r.commit("changelog.d: two notes", "changelog.d/two.md")
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entries {
		if e.Origin != "release note" {
			continue
		}
		other := beta
		if strings.Contains(e.Note, "beta") {
			other = alpha
		}
		for _, c := range e.Commits {
			if strings.HasPrefix(other, c.Hash) {
				t.Errorf("%q carries the other note's fix %s: %+v", e.Note, c.Hash, e.Commits)
			}
		}
	}
}

// The commit that wrote a note stands in for its fix when no commit cites the note's divergence. Its message may cite another note's divergence; that is not a cross-attribution.
func TestTheNoteCommitMayCiteAnotherDivergence(t *testing.T) {
	r := newFixture(t)
	r.write("b.go", "package x\n")
	r.write("changelog.d/two.md", "### Fixed\n\n- A stalled stream ends after the idle timeout (D3).\n- A signature-only reasoning detail is kept (D4).\n")
	note := r.commit("Keep a signature-only detail (D4) and record both notes", "b.go", "changelog.d/two.md")
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatalf("the note commit's own divergence is not a cross-attribution: %v", err)
	}
	for _, e := range l.Entries {
		if strings.Contains(e.Note, "(D3)") && (len(e.Commits) != 1 || !strings.HasPrefix(note, e.Commits[0].Hash)) {
			t.Errorf("D3 entry commits = %+v, want the note commit %s", e.Commits, note[:10])
		}
	}
}

// A note that cites no divergence never takes a commit that cites a divergence another note claims, even from a short lane.
func TestANoteWithoutADivergenceSkipsAnotherNotesDivergenceCommit(t *testing.T) {
	r := newFixture(t)
	r.write("a.go", "package x\n")
	stalled := r.commit("Fix a stalled stream (D3)", "a.go")
	r.write("changelog.d/stream.md", "### Fixed\n\n- A stalled stream ends after the idle timeout (D3).\n")
	r.commit("changelog.d: stream", "changelog.d/stream.md")
	r.write("b.go", "package x\n")
	r.write("changelog.d/beta.md", "### Fixed\n\n- The beta renderer draws the last line.\n")
	r.commit("beta: draw the last line", "b.go", "changelog.d/beta.md")
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entries {
		if !strings.Contains(e.Note, "beta") {
			continue
		}
		for _, c := range e.Commits {
			if strings.HasPrefix(stalled, c.Hash) {
				t.Errorf("the beta note took the D3 fix %s: %+v", c.Hash, e.Commits)
			}
		}
	}
}

// A maintainer who lands a contributor's change inside a larger commit credits them with a Co-authored-by trailer
// (.github/CONTRIBUTING.md), so the release notes thank a co-author as they thank a commit author. One person credited by
// two names (same email) or two emails (same name) is thanked once, under the name of the first commit that credits them;
// the maintainer, bots, coding agents and a trailer without an email are not thanked.
func TestContributorsCountCoAuthoredByTrailers(t *testing.T) {
	r := newFixture(t)
	base := r.run("rev-parse", "HEAD")
	land := func(file, author, message string) {
		r.write(file, "package x\n")
		r.run("add", "--", file)
		r.run("commit", "-q", "--author", author, "-m", message)
	}
	land("a.go", "Yu LI <liyu1981@gmail.com>", "a: change")
	land("b.go", "Michael Kinsy <mrkinsy5@gmail.com>", "b: land a contributor's login carrier\n\nCo-authored-by: Dávid Balatoni <balcsida@gmail.com>\nSigned-off-by: Michael Kinsy <mrkinsy5@gmail.com>")
	land("c.go", "Michael Kinsy <mrkinsy5@gmail.com>", "c: more\n\n"+
		"Co-authored-by: balcsida <balcsida@gmail.com>\n"+
		"Co-authored-by: Yu LI <yu@example.com>\n"+
		"Co-authored-by: Volker Mauel <volkermauel@gmail.com>\n"+
		"Co-authored-by: Michael Kinsy <54590707+MichaelKinsy@users.noreply.github.com>\n"+
		"Co-authored-by: dependabot[bot] <49699333+dependabot[bot]@users.noreply.github.com>\n"+
		"Co-authored-by: Cursor <cursoragent@cursor.com>\n"+
		"Co-authored-by: a reviewer\n"+
		"Signed-off-by: Michael Kinsy <mrkinsy5@gmail.com>")
	var b strings.Builder
	if err := writeContributors(&b, repo{root: r.root}, base); err != nil {
		t.Fatal(err)
	}
	want := "## Contributors\n\nThanks to the contributors whose commits are in this release: Dávid Balatoni, Volker Mauel, Yu LI.\n\n"
	if b.String() != want {
		t.Fatalf("contributors =\n%q\nwant\n%q", b.String(), want)
	}
}
