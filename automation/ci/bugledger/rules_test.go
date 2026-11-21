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
	"time"
)

// overlayNote is the release note of the Kitty overlay fix. It says an image is "drawn again when it closes"; the impact rule found "loses" inside "closes" and classed a display fix as lost data.
const overlayNote = "Kitty and iTerm2 images no longer cover an overlay drawn over them. An image that an overlay overlaps by row and column is removed while the overlay is open and drawn again when it closes, on the main screen and in fullscreen mode; an image beside an overlay, such as a sidebar, stays visible."

// A keyword of an impact or platform rule counts as a whole word: a longer word that only contains it says nothing about what went wrong.
func TestImpactAndPlatformKeywordsMatchWholeWordsOnly(t *testing.T) {
	const none = "Behavior differed from Pi"
	for _, c := range []struct{ note, impact string }{
		{overlayNote, "Wrong display"},
		{"The panel stays where it was when the dialog closes.", none},
		{"A fence that encloses the block is kept.", none},
		{"The message discloses the path of the file.", none},
		{"The session loses its last entry on resume.", "Lost or altered data"},
		{"The last entry was lost on resume.", "Lost or altered data"},
		{"It dropped an entry of the list.", "Lost or altered data"},
		{"It dropped anchors in every list.", none},
		{"The secretary role keeps its name.", none},
		{"The hangar view opens.", none},
		{"The run exited 10 times in a row.", none},
		{"The refusenik label is kept.", none},
		{"The server surrenders the lease.", none},
		{"The precursor event is delivered.", none},
		{"The drawer keeps its width.", none},
		{"A hieroglyph is kept as it is.", none},
		{"The border of the box is kept.", none},
		{"A recorder is attached to the session.", none},
		// The forms of each word still count.
		{"The host crashes when the extension exits.", "Crash, hang or runaway process"},
		{"The loop panicked on an empty list.", "Crash, hang or runaway process"},
		{"Stored permissions are checked again.", "Credential or permission exposure"},
		{"The request is refused with a message.", "An operation failed or was rejected"},
		{"The table renders with its header.", "Wrong display"},
		{"The list is re-rendered once.", "Wrong display"},
		{"Entries are reordered by date.", "Wrong output or ordering"},
		{"Unescaped quotes are written as they are.", "Wrong output or ordering"},
	} {
		if got := impactOf(c.note); got != c.impact {
			t.Errorf("impactOf(%q) = %q, want %q", c.note, got, c.impact)
		}
	}
	for _, c := range []struct{ note, platform string }{
		{"The Darwinian test is kept.", "All platforms"},
		{"On macOS and on other Darwin kernels the path is kept.", "macOS"},
		{"`pig.exe` is found again.", "Windows"},
		{"A script run by `pwsh` keeps its arguments.", "Windows"},
	} {
		if got := platformOf(c.note); got != c.platform {
			t.Errorf("platformOf(%q) = %q, want %q", c.note, got, c.platform)
		}
	}
}

// subagentsMessage is the message of the fix of the p2 subagents race, as git gives it (<< and >> stand for the backticks of its code spans, and the sign-off names the test author). Its "Tests" paragraph holds the cause words "did not", so the ledger took that paragraph for the root cause, and it names two tests only as places where another, open failure was seen, which the ledger listed as tests of this fix.
var subagentsMessage = strings.NewReplacer("<<", "`", ">>", "`").Replace(`fix(p2): one lock for what the subagent reports saw of each subagent (a session worker died of a concurrent map read and write)

The worker's subagent tools keep, between reports, what the last report saw of each subagent (<<seen>>, by
conversation). A report (childrenOf, detailsOf) reads and writes it from the reporter's push, which runs off the host's
loop (host.AwaitingCtx, the commit loop, SubagentControl.archive); the task graph's routine asks on the loop which
subagents it holds (workingChildren). Pi's one thread runs both. Here they ran at once on a plain map, and Go ended the
session's worker with <<fatal error: concurrent map read and map write>> (worker.go:706), as seen in
TestSessionRelocationFakeProvider/moves_following_subagents_with_the_session,_not_one_with_its_own_location: the
worker restarted and the subagent's bash came back as interrupted (relocate_test.go:562).

<<seen>> is a seenChildren now: the map with a lock every access takes, as the worker's other state shared by the two
sides has (<<endings>>, the one-shot set). The reporter's own lock is held for the whole push (the session's reads, the
server's call), which the loop must not wait for.

Read for the same shape (state read in a loop routine and written from a push or a timer) through worker.go,
tasks.go, team.go, tool.go and place.go: <<ended>> (endings, locked), <<oneShots>> (oneShotsMu), <<enabled>>, the reporter
and the cached natives (atomic pointers), <<working>> (the graph routine alone); the goroutines of tasks.go share
channels only. <<seen>> was the one without an owner.

Tests (go1.27.4, job race1, on this commit's tree).
- TestTheTaskGraphRoutineAsksForTheSeenSubagentsWhileAReportWritesThem (seen_test.go) drives pushes of childrenOf
  while workingChildren scans the graph, in no order with each other. Red on the base 09bc434bde7f under -race (the
  test with the base's plain map): the detector reports the write at worker.go:223 (detailsOf, from childrenOf :250)
  against the read at worker.go:706 (workingChildren). Green with the fix, and
  <<go test -race ./plugins/subagents/...>> is ok. Mutants: has() without its lock is the detector's report again;
  prune() keeping every entry fails seen_test.go:81.
- The real wiring, e2e TestSessionRelocationFakeProvider under -race with every process's reports in files (GORACE
  log_path, which the workers inherit): the base, x2, passes and leaves 12 reports in 6 files, all of
  worker.go:223/250 against :706; the fix, x20, leaves none.
- Not green: of those 20 runs 19 pass and one fails at relocate_test.go:507, a restarted worker (generation 2)
  ending <<p2.harness-host: did not become live within 60000 ms>>. No goroutine of the worker that was dumped waits on
  this lock; the failing worker left no stacks. The same end of a worker restart was seen on trees without this
  change (TestSearchFakeProvider search_test.go:182, TestM1WorkingChat m1_test.go:411); its cause is open and is not
  found here.
- <<make lint-changed LINT_BASE=09bc434bde7f>>: 0 issues. <<go build ./...>> and <<go vet ./plugins/subagents/...>> in
  apps/p2: ok.

Signed-off-by: T <t@example.com>`)

const subagentsNote = "p2: a session's worker no longer dies with `fatal error: concurrent map read and map write` when a subagent's task ends while the list of subagents is being reported to the server. The worker restarted, and a tool the subagent was running came back as interrupted."

// A paragraph that reports how a fix was checked is not its cause, whatever words it holds; what a message states outright wins over its prose.
func TestTheCauseIsNeverAParagraphAboutHowTheFixWasChecked(t *testing.T) {
	for _, c := range []struct{ name, message, want string }{
		{"a Tests paragraph with a cause word", "Fix x\n\nTests (go1.27.4, job race1, on this commit's tree).\n- TestA did not pass before.", ""},
		{"a Tests label after the cause", "Fix x\n\nThe map had no lock.\n\nTests: TestA was not run before.", "The map had no lock."},
		{"what was run", "Fix x\n\nRan: go test ./... because the suite was not run before.", ""},
		{"evidence", "Fix x\n\nEvidence: the job did not fail.", ""},
		{"where it was verified", "Fix x\n\nVerified on linux: the loop did not hang.", ""},
		{"a job name", "Fix x\n\nJob race1: the race job did not fail.", ""},
		{"a test name first", "Fix x\n\nTestLoopExits did not pass before.", ""},
		{"the items of a test list", "Fix x\n\nTests:\n\n- the suite was not run before\n\n- the race job did not fail", ""},
		{"a Cause label with a full stop", "Fix x\n\nSome prose.\n\nCause. The signal goroutine gave the descriptors back late.", "The signal goroutine gave the descriptors back late."},
		{"prose that opens with a call", "Fix x\n\nrun() awaited the prompt because the queue was not drained.", "run() awaited the prompt because the queue was not drained."},
		{"prose whose subject is a result", "Fix x\n\nResults were dropped because the channel was closed.", "Results were dropped because the channel was closed."},
		{"a Root-Cause trailer", "Fix x\n\nSome prose.\n\nRoot-Cause: the reader took no lock.\nSigned-off-by: T <t@example.com>", "the reader took no lock."},
		{"the trailer block is not prose", "Fix x\n\nFix-Commit: " + strings.Repeat("ab", 20) + "\nSigned-off-by: T <t@example.com>", ""},
	} {
		if got, _ := rootCauseOf([]string{c.message}, ""); got != c.want {
			t.Errorf("%s: rootCauseOf = %q, want %q", c.name, got, c.want)
		}
	}
	stated, _ := rootCauseOf([]string{"Fix a\n\nIt failed because the lock was held.", "Fix b\n\nRoot cause: the timer was never armed."}, "")
	if stated != "the timer was never armed." {
		t.Errorf("a cause the second commit states must win over the prose of the first, got %q", stated)
	}
	cause, from := rootCauseOf([]string{subagentsMessage}, subagentsNote)
	if !strings.HasPrefix(cause, "The worker's subagent tools keep, between reports, what the last report saw of each subagent") || from != "commit" {
		t.Errorf("the cause of the subagents race = %q from %q, want the paragraph that describes the race", cause, from)
	}
	if strings.Contains(cause, "job race1") || strings.Contains(cause, "TestSearchFakeProvider") {
		t.Errorf("the cause of the subagents race is its Tests paragraph: %q", cause)
	}
}

// only returns the one entry of the ledger of r, built from its history.
func only(t *testing.T, r testRepo) Entry {
	t.Helper()
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 {
		t.Fatalf("entries = %+v, want one", l.Entries)
	}
	return l.Entries[0]
}

// The fix of the subagents race adds one test. Its message names three more tests of the tree: the e2e test where the race was seen, and two where another failure was seen. None of them is a test of the fix.
func TestTheSubagentsRaceFixListsTheTestItAddsAndNoSighting(t *testing.T) {
	r := newFixture(t)
	r.write("apps/p2/e2e/relocate_test.go", "package e2e\n\nfunc TestSessionRelocationFakeProvider(t *testing.T) {}\n")
	r.write("apps/p2/e2e/search_test.go", "package e2e\n\nfunc TestSearchFakeProvider(t *testing.T) {}\n")
	r.write("apps/p2/e2e/m1_test.go", "package e2e\n\nfunc TestM1WorkingChat(t *testing.T) {}\n")
	r.commit("p2: the e2e suite", "apps/p2/e2e")
	r.write("apps/p2/plugins/subagents/worker.go", "package subagents\n")
	r.write("apps/p2/plugins/subagents/seen_test.go", "package subagents\n\nfunc TestTheTaskGraphRoutineAsksForTheSeenSubagentsWhileAReportWritesThem(t *testing.T) {}\n")
	fix := r.commit(subagentsMessage, "apps/p2/plugins/subagents")
	r.write("changelog.d/p2-subagents-seen-race-"+fix[:10]+".md", "### Fixed\n\n- "+subagentsNote+" <!-- fix: "+fix[:10]+" -->\n")
	r.commit("changelog.d: the subagents race", "changelog.d")
	e := only(t, r)
	if !strings.HasPrefix(e.RootCause, "The worker's subagent tools keep") || e.CauseFrom != "commit" {
		t.Errorf("root cause = %q from %q", e.RootCause, e.CauseFrom)
	}
	if want := []string{"apps/p2/plugins/subagents/seen_test.go::TestTheTaskGraphRoutineAsksForTheSeenSubagentsWhileAReportWritesThem"}; !slices.Equal(e.Tests, want) {
		t.Errorf("tests = %v, want %v", e.Tests, want)
	}
}

// A test a message mentions in a file the commit leaves alone is no test of the fix; a Regression-Test trailer names one outright.
func TestOnlyANamedRegressionTestIsListed(t *testing.T) {
	r := newFixture(t)
	r.write("a.go", "package x\n")
	mentioned := r.commit("Fix a crash in a\n\nThe list was read before it was filled. TestOld showed the same crash on another branch.", "a.go")
	r.write("b.go", "package x\n")
	named := r.commit("Fix a crash in b\n\nThe list was read before it was filled.\n\nRegression-Test: TestOld\nSigned-off-by: T <t@example.com>", "b.go")
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]string{}
	for _, e := range l.Entries {
		tests[e.Commits[0].Hash] = e.Tests
	}
	if got := tests[mentioned[:10]]; len(got) != 0 {
		t.Errorf("a mentioned test is listed as a test of the fix: %v", got)
	}
	if got := tests[named[:10]]; !slices.Equal(got, []string{"old_test.go::TestOld"}) {
		t.Errorf("tests of the commit that names its regression test = %v", got)
	}
}

// A bullet states the cause and the tests of its fix with markers. They are the entry's as written, whatever the fix commit says, and they are not part of the note.
func TestABulletStatesItsCauseAndItsTests(t *testing.T) {
	r := newFixture(t)
	r.write("a.go", "package x\n")
	r.write("a_test.go", "package x\n\nfunc TestAFixed(t *testing.T) {}\n")
	fix := r.commit("a: handle empty input\n\nTests: TestAFixed did not pass before.", "a.go", "a_test.go")
	bullet := "### Fixed\n\n- Empty input no longer crashes. <!-- fix: " + fix[:10] + " --> <!-- cause: The parser read the first byte before it checked the length. -->"
	r.write("changelog.d/a.md", bullet+" <!-- tests: TestOld -->\n")
	r.commit("Note the fix", "changelog.d/a.md")
	e := only(t, r)
	if e.Note != "Empty input no longer crashes." || e.Title != "Empty input no longer crashes" {
		t.Errorf("note = %q, title = %q: the markers are not part of the text", e.Note, e.Title)
	}
	if e.RootCause != "The parser read the first byte before it checked the length." || e.CauseFrom != "release note" {
		t.Errorf("root cause = %q from %q", e.RootCause, e.CauseFrom)
	}
	if !slices.Equal(e.Tests, []string{"old_test.go::TestOld"}) {
		t.Errorf("tests = %v, want the test the bullet names", e.Tests)
	}
	// A fragment that names a test the tree does not have fails, and the failure names the fragment.
	r.write("changelog.d/a.md", bullet+" <!-- tests: TestGone -->\n")
	if _, err := build(r.root, "0.4.1"); err == nil || !strings.Contains(err.Error(), "changelog.d/a.md") || !strings.Contains(err.Error(), "TestGone") {
		t.Errorf("a tests marker that names no test must fail with the fragment's name, got %v", err)
	}
}

// A fix made since the rule (see causeRequiredFrom) whose cause no source gives is written as "Not recorded", and the check fails and says where to state the cause. Stating it in the bullet settles it.
func TestTheCheckAsksForTheCauseOfANewFix(t *testing.T) {
	r := newFixture(t)
	r.write("a.go", "package x\n")
	fix := commitAt(t, r, "2026-11-14T12:00:00Z", "a: handle empty input", "a.go")
	r.write("b.go", "package x\n")
	plain := commitAt(t, r, "2026-11-14T12:00:00Z", "Fix a crash in b", "b.go")
	bullet := "### Fixed\n\n- Empty input is handled. <!-- fix: " + fix[:10] + " -->"
	r.write("changelog.d/a.md", bullet+"\n")
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	r.commit("Note the fix", "changelog.d/a.md", jsonPath, pagePath)
	page, err := os.ReadFile(filepath.Join(r.root, pagePath))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(page), "- **Root cause:** Not recorded."); n != 2 {
		t.Fatalf("the page says %d times that no cause is recorded, want 2:\n%s", n, page)
	}
	err = run(r.root, "0.4.1", true, false)
	if err == nil {
		t.Fatal("the check must fail for a new fix without a cause")
	}
	for _, want := range []string{"has no root cause", "changelog.d/a.md", "<!-- cause:", "commit " + plain[:10], "exempt.txt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the check does not say %q:\n%v", want, err)
		}
	}
	r.write("changelog.d/a.md", bullet+" <!-- cause: The length was read before it was checked. -->\n")
	r.write("automation/ci/bugledger/exempt.txt", plain[:10]+" Follow-up to unreleased work: b was never released\n")
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	if err := run(r.root, "0.4.1", true, false); err != nil {
		t.Fatalf("with the cause stated the check must pass: %v", err)
	}
	l, _, err := readCommitted(r.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].RootCause != "The length was read before it was checked." {
		t.Fatalf("entries = %+v, want the stated cause", l.Entries)
	}
}

// A fix made before the rule keeps passing the check without a cause: its message cannot change.
func TestAFixMadeBeforeTheCauseRuleIsNotAsked(t *testing.T) {
	r := newFixture(t)
	r.write("b.go", "package x\n")
	commitAt(t, r, "2026-11-13T12:00:00Z", "Fix a crash in b", "b.go")
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	if err := run(r.root, "0.4.1", true, false); err != nil {
		t.Fatalf("a fix made before the rule must pass the check: %v", err)
	}
}

// The cause rule starts at the instant causeRequiredFrom names and reads the date of the fix commit, never the clock: a fix dated before the instant passes the check without a cause, a fix dated at or after it fails with the rule's text, and a fix that states its cause passes on both sides. The rows are written from the constant, so they follow the rule if its instant moves; the test names no zone, and TestTheCauseRuleHoldsInEveryProcessZone runs it under three.
func TestTheCauseRuleStartsOnItsDay(t *testing.T) {
	at := func(d time.Duration) string { return causeRequiredFrom.Add(d).Format(time.RFC3339) }
	for _, tc := range []struct {
		name, date string
		asked      bool
	}{
		{"the last second before the rule", at(-time.Second), false},
		{"the first second of the rule", at(0), true},
		{"a later year", "2027-01-02T03:04:05Z", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newFixture(t)
			r.write("b.go", "package x\n")
			fix := commitAt(t, r, tc.date, "Fix a crash in b", "b.go")
			if err := run(r.root, "0.4.1", false, false); err != nil {
				t.Fatal(err)
			}
			err := run(r.root, "0.4.1", true, false)
			switch {
			case !tc.asked && err != nil:
				t.Fatalf("a fix dated %s without a cause must pass the check: %v", tc.date, err)
			case tc.asked && (err == nil || !strings.Contains(err.Error(), "has no root cause") || !strings.Contains(err.Error(), "commit "+fix[:10])):
				t.Fatalf("a fix dated %s without a cause must fail the check with the rule's text and its commit %s, got %v", tc.date, fix[:10], err)
			}
			stated := newFixture(t)
			stated.write("b.go", "package x\n")
			commitAt(t, stated, tc.date, "Fix a crash in b\n\nRoot-Cause: b read a nil map.\nSigned-off-by: T <t@example.com>", "b.go")
			if err := run(stated.root, "0.4.1", false, false); err != nil {
				t.Fatal(err)
			}
			if err := run(stated.root, "0.4.1", true, false); err != nil {
				t.Fatalf("a fix dated %s that states its cause must pass the check: %v", tc.date, err)
			}
			l, _, err := readCommitted(stated.root)
			if err != nil {
				t.Fatal(err)
			}
			if len(l.Entries) != 1 || l.Entries[0].RootCause != "b read a nil map." {
				t.Fatalf("entries = %+v, want the cause the commit states", l.Entries)
			}
		})
	}
}

// An entry the committed ledger keeps takes its impact from the rules as they are, and the check says when the committed class is not the one its text reads as.
func TestAKeptEntryTakesItsImpactFromTheRulesAsTheyAre(t *testing.T) {
	r := newFixture(t)
	r.write("a.go", "package x\n")
	fix := r.commit("tui: images under an overlay\n\nThe image was drawn after the overlay.", "a.go")
	r.write("changelog.d/a.md", "### Fixed\n\n- "+overlayNote+" <!-- fix: "+fix[:10]+" -->\n")
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	// The committed entry carries the class the rule that matched inside "closes" gave it.
	l, _, err := readCommitted(r.root)
	if err != nil {
		t.Fatal(err)
	}
	l.Entries[0].Impact = "Lost or altered data"
	writeLedger(t, r, l)
	r.commit("Note the fix", "changelog.d/a.md", jsonPath, pagePath)
	if err := run(r.root, "0.4.1", true, false); err == nil || !strings.Contains(err.Error(), "user impact") || !strings.Contains(err.Error(), "Wrong display") {
		t.Fatalf("the check must name the stale class, got %v", err)
	}
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	if l, _, err = readCommitted(r.root); err != nil || l.Entries[0].Impact != "Wrong display" {
		t.Fatalf("regenerated impact = %q (%v), want Wrong display", l.Entries[0].Impact, err)
	}
	if err := run(r.root, "0.4.1", true, false); err != nil {
		t.Fatalf("after regeneration the check must pass: %v", err)
	}
}

// A branch is rebased: its fix is committed again on the new base and the fix marker follows, while the committed ledger still names the commit of the branch as it was. The entry is recorded under the commit the marker names, and until it is the check says which entry names a commit HEAD does not reach and which fragment holds its bullet.
func TestARebasedFixIsRecordedUnderTheCommitThatReplacedIt(t *testing.T) {
	r := newFixture(t)
	r.run("checkout", "-q", "-b", "lane")
	r.write("a.go", "package x\n")
	r.write("a_test.go", "package x\n\nfunc TestAFixed(t *testing.T) {}\n")
	old := r.commit("a: handle empty input\n\nThe length was read before it was checked.", "a.go", "a_test.go")
	r.write("changelog.d/a.md", "### Fixed\n\n- Empty input no longer crashes. <!-- fix: "+old[:10]+" -->\n")
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	r.commit("Note the fix", "changelog.d/a.md", jsonPath, pagePath)
	r.run("checkout", "-q", "main")
	r.write("other.go", "package x\n")
	r.commit("other: unrelated", "other.go")
	r.run("checkout", "-q", "-b", "rebased")
	r.run("cherry-pick", old)
	fix := r.run("rev-parse", "HEAD")
	r.run("checkout", "-q", "lane", "--", "changelog.d/a.md", jsonPath, pagePath)
	r.write("changelog.d/a.md", "### Fixed\n\n- Empty input no longer crashes. <!-- fix: "+fix[:10]+" -->\n")
	r.commit("Note the fix", "changelog.d/a.md", jsonPath, pagePath)
	if fix == old {
		t.Fatal("the rebased fix must be another commit")
	}

	err := run(r.root, "0.4.1", true, false)
	if err == nil || !strings.Contains(err.Error(), old[:10]) || !strings.Contains(err.Error(), "HEAD does not reach") || !strings.Contains(err.Error(), "changelog.d/a.md") {
		t.Fatalf("the check must name the commit HEAD does not reach and the fragment, got %v", err)
	}
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	l, data, err := readCommitted(r.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || len(l.Entries[0].Commits) != 1 || l.Entries[0].Commits[0].Hash != fix[:10] || l.Entries[0].ID != "BF-"+fix[:7] {
		t.Fatalf("entries = %+v, want one entry of commit %s", l.Entries, fix[:10])
	}
	if bytes.Contains(data, []byte(old[:10])) || !slices.Equal(l.Entries[0].Tests, []string{"a_test.go::TestAFixed"}) {
		t.Fatalf("the ledger still names %s, or lost the test of the fix:\n%s", old[:10], data)
	}
	if err := run(r.root, "0.4.1", true, true); err != nil {
		t.Fatalf("after regeneration the strict check must pass: %v", err)
	}
}

// cloneOf returns a clone of the branch main of r that holds only the commits main reaches.
func cloneOf(t *testing.T, r testRepo) testRepo {
	t.Helper()
	c := testRepo{t: t, root: filepath.Join(t.TempDir(), "clone")}
	cmd := exec.Command("git", "clone", "-q", "--no-local", "--single-branch", "--branch", "main", r.root, c.root)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	return c
}

// squashed lands a fix on main as one squash commit with the given message and deletes the branch that held the fix commit, which the repository still holds; a fragment then names the fix commit. It returns the fix commit and the squash commit.
func squashed(t *testing.T, r testRepo, message func(side string) string) (side, squash string) {
	t.Helper()
	r.run("checkout", "-q", "-b", "side")
	r.write("a.go", "package x\n")
	r.write("a_test.go", "package x\n\nfunc TestAFixed(t *testing.T) {}\n")
	side = r.commit("a: handle empty input", "a.go", "a_test.go")
	r.run("checkout", "-q", "main")
	r.run("branch", "-q", "-D", "side")
	r.write("a.go", "package x\n")
	r.write("a_test.go", "package x\n\nfunc TestAFixed(t *testing.T) {}\n")
	squash = r.commit(message(side), "a.go", "a_test.go")
	r.write("changelog.d/a-"+side[:10]+".md", "### Fixed\n\n- Empty input no longer crashes. <!-- fix: "+side[:10]+" -->\n")
	r.commit("Note the fix", "changelog.d")
	return side, squash
}

// The ledger of a commit is the same in every clone of it. A fix was squashed and its fragment still names the commit the squash replaced: a clone that happens to hold that commit (the repository where the branch was) and a clone that does not must give the same answer, which is the failure that names the fragment, or, when the squash commit carries the Fix-Commit trailer, the same entry under the squash commit.
func TestTheLedgerOfASquashedFixIsTheSameInEveryClone(t *testing.T) {
	t.Run("no trailer", func(t *testing.T) {
		r := newFixture(t)
		side, _ := squashed(t, r, func(string) string { return "Squash (#9)" })
		pure := cloneOf(t, r)
		if _, held := (repo{root: r.root}).resolve(side); !held {
			t.Fatal("the repository must still hold the squashed commit")
		}
		if _, held := (repo{root: pure.root}).resolve(side); held {
			t.Fatal("the clone must not hold the squashed commit")
		}
		_, errHeld := build(r.root, "0.4.1")
		_, errPure := build(pure.root, "0.4.1")
		if errHeld == nil || errPure == nil || errHeld.Error() != errPure.Error() {
			t.Fatalf("the two clones differ:\n  holding the commit: %v\n  without it: %v", errHeld, errPure)
		}
		if name := "changelog.d/a-" + side[:10] + ".md"; !strings.Contains(errPure.Error(), name) || !strings.Contains(errPure.Error(), "not a commit HEAD reaches") {
			t.Fatalf("the failure must name the fragment %s, got %v", name, errPure)
		}
	})
	t.Run("Fix-Commit trailer", func(t *testing.T) {
		r := newFixture(t)
		_, squash := squashed(t, r, func(side string) string { return "Squash (#9)\n\nFix-Commit: " + side })
		var written [][]byte
		for _, c := range []testRepo{r, cloneOf(t, r)} {
			for range 2 {
				if err := run(c.root, "0.4.1", false, false); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(c.root, jsonPath))
				if err != nil {
					t.Fatal(err)
				}
				page, err := os.ReadFile(filepath.Join(c.root, pagePath))
				if err != nil {
					t.Fatal(err)
				}
				written = append(written, append(data, page...))
			}
		}
		for i, w := range written {
			if !bytes.Equal(w, written[0]) {
				t.Fatalf("run %d wrote another ledger:\n%s\nwant\n%s", i, w, written[0])
			}
		}
		if !bytes.Contains(written[0], []byte(`"hash": "`+squash[:10]+`"`)) {
			t.Fatalf("the entry must name the squash commit %s:\n%s", squash[:10], written[0])
		}
	})
}

// A file git ignores is not part of the tree. A scratch copy of a test file under an ignored directory made the name of a moved test ambiguous, and the entry lost its test in that checkout alone.
func TestAnIgnoredCopyOfATestFileDoesNotChangeTheLedger(t *testing.T) {
	r := newFixture(t)
	r.write(".gitignore", "tmp/\n")
	r.write("a.go", "package x\n")
	r.write("a_test.go", "package x\n\nfunc TestAFixed(t *testing.T) {}\n")
	r.commit("Fix a crash in a", ".gitignore", "a.go", "a_test.go")
	r.write("a_test.go", "package x\n")
	r.write("b_test.go", "package x\n\nfunc TestAFixed(t *testing.T) {}\n")
	r.commit("tests: move the test of a", "a_test.go", "b_test.go")
	clean := only(t, r)
	r.write("tmp/scratch/b_test.go", "package x\n\nfunc TestAFixed(t *testing.T) {}\n")
	withCopy := only(t, r)
	if want := []string{"b_test.go::TestAFixed"}; !slices.Equal(clean.Tests, want) || !slices.Equal(withCopy.Tests, want) {
		t.Fatalf("tests = %v in a clean checkout and %v beside an ignored copy, want %v in both", clean.Tests, withCopy.Tests, want)
	}
}

// A pull request's fix was squash-merged, and the squash commit carries the ledger entry the pull request wrote, which names the pull request's own commit. The fix marker now names a commit of this history, so the entry is computed from the marker. The squash commit is titled as a fix; it is still the landing of that entry, and no bug of its own, on every run.
func TestAFixTitledSquashJoinsTheEntryItLandedWhenTheMarkerNamesAnotherCommit(t *testing.T) {
	r := newFixture(t)
	r.run("checkout", "-q", "-b", "pr")
	r.write("a.go", "package x\n")
	pr := r.commit("Fix a crash in a", "a.go")
	r.write("changelog.d/a.md", "### Fixed\n\n- Empty input no longer crashes. <!-- fix: "+pr[:10]+" -->\n")
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	r.commit("Note the fix", "changelog.d/a.md", jsonPath, pagePath)
	r.run("checkout", "-q", "main")
	r.run("checkout", "-q", "pr", "--", ".")
	squash := r.commit("Fix a crash in a (#7)", ".")
	r.run("branch", "-q", "-D", "pr")
	r.write("a.go", "package x\n\n// the rest of the fix\n")
	fix := r.commit("a: handle empty input", "a.go")
	r.write("changelog.d/a.md", "### Fixed\n\n- Empty input no longer crashes. <!-- fix: "+fix[:10]+" -->\n")
	r.commit("Name the fix", "changelog.d/a.md")
	var written [][]byte
	for range 2 {
		if err := run(r.root, "0.4.1", false, false); err != nil {
			t.Fatal(err)
		}
		written = append(written, committedBytes(t, r))
	}
	if !bytes.Equal(written[0], written[1]) {
		t.Fatalf("the second run wrote another ledger:\n%s\nwant\n%s", written[1], written[0])
	}
	l, data, err := readCommitted(r.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 {
		t.Fatalf("entries = %+v, want the one bug", l.Entries)
	}
	var got []string
	for _, c := range l.Entries[0].Commits {
		got = append(got, c.Hash)
	}
	want := []string{fix[:10], squash[:10]}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) || bytes.Contains(data, []byte(pr[:10])) {
		t.Fatalf("commits = %v, want the marker's commit and the squash %v, and not the pull request's commit %s", got, want, pr[:10])
	}
	if err := run(r.root, "0.4.1", true, false); err != nil {
		t.Fatalf("the check must pass: %v", err)
	}
}

// A fix-titled commit that also commits the regenerated ledger writes the hash of an earlier fix into it. That fix is in the history, so the commit landed nothing: it is a fix of its own and keeps its entry.
func TestAFixThatCommitsTheLedgerOfAnEarlierFixKeepsItsOwnEntry(t *testing.T) {
	r := newFixture(t)
	r.write("a.go", "package x\n")
	first := r.commit("a: handle empty input", "a.go")
	r.write("changelog.d/a.md", "### Fixed\n\n- Empty input no longer crashes. <!-- fix: "+first[:10]+" -->\n")
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	r.write("b.go", "package x\n")
	second := r.commit("Fix a crash in b", "b.go", "changelog.d/a.md", jsonPath, pagePath)
	l, err := build(r.root, "0.4.1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, e := range l.Entries {
		for _, c := range e.Commits {
			got[e.Origin] = append(got[e.Origin], c.Hash)
		}
	}
	if len(l.Entries) != 2 || !slices.Equal(got["release note"], []string{first[:10]}) || !slices.Equal(got["commit"], []string{second[:10]}) {
		t.Fatalf("entries = %+v, want the note under %s and the second fix under %s", l.Entries, first[:10], second[:10])
	}
}

// Maintainer HTML comments inside a release note (such as <!-- evidence: ... -->) are not user-facing text and must not affect the published platform or impact. Comments inside Markdown code spans are user text and stay, and an unterminated <!-- fails open and keeps the text.
func TestClassifyIgnoresHTMLComments(t *testing.T) {
	note := "On Windows, X was lost. <!-- evidence: proven red on Linux by T -->"
	if got := platformOf(note); got != "Windows" {
		t.Errorf("platformOf(%q) = %q, want %q", note, got, "Windows")
	}

	multiline := "On Windows, X was lost. <!-- evidence:\nproven red on Linux\nand checked on macOS\n-->"
	if got := platformOf(multiline); got != "Windows" {
		t.Errorf("platformOf(multiline) = %q, want %q", got, "Windows")
	}

	// A comment naming a crash or error word must not override impactOf.
	impactNote := "A value is kept. <!-- evidence: did not crash or fail on Linux -->"
	if got := impactOf(impactNote); got != "Behavior differed from Pi" {
		t.Errorf("impactOf(%q) = %q, want %q", impactNote, got, "Behavior differed from Pi")
	}

	// A literal comment inside a Markdown code span is user text and is kept whole.
	codeSpan := "On macOS, a line with `<!-- fix: abc -->` typed in a prompt was dropped."
	if got := platformOf(codeSpan); got != "macOS" {
		t.Errorf("platformOf(%q) = %q, want %q", codeSpan, got, "macOS")
	}
	codeSpanKeywords := "On macOS, a line with `<!-- Windows crash -->` typed in a prompt is kept."
	if got := platformOf(codeSpanKeywords); got != "Windows, macOS" {
		t.Errorf("platformOf(%q) = %q, want %q", codeSpanKeywords, got, "Windows, macOS")
	}
	if got := impactOf(codeSpanKeywords); got != "Crash, hang or runaway process" {
		t.Errorf("impactOf(%q) = %q, want %q", codeSpanKeywords, got, "Crash, hang or runaway process")
	}

	// A real trailing comment after a code span is stripped while the code span stays.
	codeSpanThenComment := "On macOS, a line with `<!-- Windows -->` typed in a prompt is kept. <!-- evidence: proven red on Linux -->"
	if got := platformOf(codeSpanThenComment); got != "Windows, macOS" {
		t.Errorf("platformOf(%q) = %q, want %q", codeSpanThenComment, got, "Windows, macOS")
	}

	// An unterminated <!-- fails open and keeps the user text.
	unterminated := "On Windows, an unclosed <!-- comment mentions macOS."
	if got := platformOf(unterminated); got != "Windows, macOS" {
		t.Errorf("platformOf(%q) = %q, want %q", unterminated, got, "Windows, macOS")
	}
}
