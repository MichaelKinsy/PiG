// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type gitRepo struct {
	t    *testing.T
	root string
}

func (r gitRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.root, "-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes the files and commits them; it returns the full hash.
func (r gitRepo) commit(msg string, files map[string]string) string {
	r.t.Helper()
	for name, text := range files {
		path := filepath.Join(r.root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func newGitRepo(t *testing.T) gitRepo {
	t.Helper()
	saved := legacyList
	t.Cleanup(func() { legacyList = saved })
	legacyList = ""
	r := gitRepo{t: t, root: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.commit("Release", map[string]string{"README.md": "x\n"})
	return r
}

func (r gitRepo) check() string {
	r.t.Helper()
	dir := filepath.Join(r.root, "changelog.d")
	fragments, problems := load(dir)
	problems = append(problems, checkHistory(dir, fragments)...)
	var msg []string
	for _, p := range problems {
		msg = append(msg, p.Error())
	}
	return strings.Join(msg, "\n")
}

func (r gitRepo) fragments(files map[string]string) {
	r.t.Helper()
	in := map[string]string{}
	for name, text := range files {
		in["changelog.d/"+name] = text
	}
	r.commit("Write the fragments", in)
}

// The failure of train #255: five fragments whose "### Fixed" bullets named no commit, so the bug ledger attributed the
// 0.5.0 squash and its ~11,000 tests to each. The check names every one of them, and passes once each bullet names its
// fix.
func TestEveryFixedBulletNeedsAFixMarker(t *testing.T) {
	r := newGitRepo(t)
	var fix [5]string
	files := map[string]string{}
	names := []string{"claude", "tern", "next-main", "tern", "claude"}
	for i := range fix {
		fix[i] = r.commit("fix: the fifth bug", map[string]string{"code" + string(rune('a'+i)) + ".go": "package x\n"})
		files[names[i]+"-"+fix[i][:10]+".md"] = "### Fixed\n\n- Bug " + string(rune('A'+i)) + " no longer happens.\n"
	}
	r.fragments(files)

	got := r.check()
	for i := range fix {
		if !strings.Contains(got, "Bug "+string(rune('A'+i))+" no longer happens.") {
			t.Errorf("the unmarked bullet %c is not reported:\n%s", 'A'+i, got)
		}
	}
	if n := strings.Count(got, "has no fix marker"); n != 5 {
		t.Errorf("%d bullets reported, want 5:\n%s", n, got)
	}
	if !strings.Contains(got, "<!-- fix: <sha> -->") || !strings.Contains(got, "squash") {
		t.Errorf("the message does not say how to fix it:\n%s", got)
	}

	for i := range fix {
		files[names[i]+"-"+fix[i][:10]+".md"] = "### Fixed\n\n- Bug " + string(rune('A'+i)) + " no longer happens. <!-- fix: " + fix[i][:10] + " -->\n"
	}
	r.fragments(files)
	if got := r.check(); got != "" {
		t.Fatalf("marked fragments must pass:\n%s", got)
	}
}

// A bullet of a section the ledger does not read needs no marker, and a bullet that continues on indented lines takes
// the marker on its last line.
func TestOnlyTheSectionsTheLedgerReadsNeedAMarker(t *testing.T) {
	r := newGitRepo(t)
	fix := r.commit("fix: wrapped", map[string]string{"a.go": "package x\n"})
	r.fragments(map[string]string{"tern-" + fix[:10] + ".md": "### Added\n\n- A command.\n\n### Changed\n\n- A default.\n\n### Fixed\n\n- A wrapped bullet\n  that ends here. <!-- fix: " + fix[:10] + " -->\n"})
	if got := r.check(); got != "" {
		t.Fatalf("problems:\n%s", got)
	}
}

// A marker may name several commits; each must be reachable.
func TestEveryCommitOfAMarkerMustBeReachable(t *testing.T) {
	r := newGitRepo(t)
	first := r.commit("fix: one", map[string]string{"a.go": "package x\n"})
	second := r.commit("fix: two", map[string]string{"b.go": "package x\n"})
	r.fragments(map[string]string{"tern-" + second[:10] + ".md": "### Fixed\n\n- Both. <!-- fix: " + first[:10] + ", " + second[:10] + " -->\n"})
	if got := r.check(); got != "" {
		t.Fatalf("problems:\n%s", got)
	}
	r.fragments(map[string]string{"tern-" + second[:10] + ".md": "### Fixed\n\n- Both. <!-- fix: " + first[:10] + ", deadbeef00 -->\n"})
	if got := r.check(); !strings.Contains(got, "deadbeef00") || !strings.Contains(got, "no commit reachable from HEAD") {
		t.Fatalf("a marker commit that does not exist must be reported:\n%s", got)
	}
	r.fragments(map[string]string{"tern-" + second[:10] + ".md": "### Fixed\n\n- Short. <!-- fix: abc12 -->\n"})
	if got := r.check(); !strings.Contains(got, "fewer than 7 hex digits") {
		t.Fatalf("an abbreviation that short must be reported:\n%s", got)
	}
}

// A marker that names a commit that exists but sits on another branch, or that a rebase replaced, is not reachable.
func TestAMarkerNamingAnUnreachableCommitFails(t *testing.T) {
	r := newGitRepo(t)
	r.git("checkout", "-q", "-b", "side")
	side := r.commit("fix: on the side", map[string]string{"side.go": "package x\n"})
	r.git("checkout", "-q", "main")
	kept := r.commit("fix: on main", map[string]string{"main.go": "package x\n"})
	r.fragments(map[string]string{"tern-" + kept[:10] + ".md": "### Fixed\n\n- On the side. <!-- fix: " + side[:10] + " -->\n"})
	got := r.check()
	if !strings.Contains(got, side[:10]) || !strings.Contains(got, "no commit reachable from HEAD") {
		t.Fatalf("a commit of another branch must be reported:\n%s", got)
	}
	if !strings.Contains(got, "rebase, squash or amend") {
		t.Fatalf("the message must say what happened and what to do:\n%s", got)
	}
	r.fragments(map[string]string{"tern-" + kept[:10] + ".md": "### Fixed\n\n- On the side. <!-- fix: " + kept[:10] + " -->\n"})
	if got := r.check(); got != "" {
		t.Fatalf("problems:\n%s", got)
	}
}

// The pre-rebase name: a fragment named after a commit that a rebase replaced. Its marker is fine; only the name
// names a commit that is gone.
func TestAFragmentNamedAfterAPreRebaseCommitFails(t *testing.T) {
	r := newGitRepo(t)
	old := r.commit("fix: the bug", map[string]string{"a.go": "package x\n"})
	r.git("commit", "-q", "--amend", "-m", "fix: the bug, reworded")
	fix := r.git("rev-parse", "HEAD")
	if fix == old {
		t.Fatal("the amend did not replace the commit")
	}
	r.fragments(map[string]string{"tern-" + old[:10] + ".md": "### Fixed\n\n- The bug no longer happens. <!-- fix: " + fix[:10] + " -->\n"})
	got := r.check()
	if !strings.Contains(got, "tern-"+old[:10]+".md") || !strings.Contains(got, "the name <lane>-<sha>.md names "+old[:10]) || !strings.Contains(got, "git mv") {
		t.Fatalf("a fragment named after a replaced commit must be reported with the rename:\n%s", got)
	}
	if strings.Contains(got, "fix marker") {
		t.Fatalf("the marker is reachable and must not be reported:\n%s", got)
	}
	r.git("mv", "changelog.d/tern-"+old[:10]+".md", "changelog.d/tern-"+fix[:10]+".md")
	r.git("commit", "-q", "-m", "Rename the fragment")
	if got := r.check(); got != "" {
		t.Fatalf("the renamed fragment must pass:\n%s", got)
	}
}

// A legacy fragment, whose commits a squash replaced, is exempt from the reachability checks, not from the marker: the fold
// copies its bullets into CHANGELOG.md, where the ledger refuses a bullet that has none (the 0.5.2 fold carried 42 of them
// out of fragments that legacy.txt exempted). A marker naming a commit that no longer exists passes.
func TestLegacyFragmentsAreExemptFromReachabilityButNotFromTheMarker(t *testing.T) {
	r := newGitRepo(t)
	r.fragments(map[string]string{"tern-0123abcdef.md": "### Fixed\n\n- Old fix.\n"})
	if got := r.check(); got == "" {
		t.Fatal("an unmarked fragment named after no commit must fail")
	}
	legacyList = "tern-0123abcdef.md\n"
	got := r.check()
	if n := strings.Count(got, "has no fix marker"); n != 1 || !strings.Contains(got, "tern-0123abcdef.md") || strings.Contains(got, "reachable") {
		t.Fatalf("an unmarked legacy bullet must fail on the marker alone:\n%s", got)
	}
	r.fragments(map[string]string{"tern-0123abcdef.md": "### Fixed\n\n- Old fix. <!-- fix: 89abcdef01 -->\n"})
	if got := r.check(); got != "" {
		t.Fatalf("a marked legacy fragment is exempt from reachability:\n%s", got)
	}
}

// With only legacy fragments there is nothing to resolve in the history, so the marker rule must not need a git checkout.
func TestAnUnmarkedLegacyBulletFailsWithoutAGitCheckout(t *testing.T) {
	saved := legacyList
	defer func() { legacyList = saved }()
	legacyList = "old-sections.md\n"
	dir := writeFragments(t, map[string]string{"old-sections.md": "### Fixed\n\n- Old fix.\n\n### Added\n\n- A command.\n"})
	fragments, problems := load(dir)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	problems = checkHistory(dir, fragments)
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), `old-sections.md: the "### Fixed" bullet "Old fix." has no fix marker`) {
		t.Fatalf("problems = %v, want the one unmarked Fixed bullet (the Added bullet needs none)", problems)
	}
}

// A shallow clone cannot say whether a commit is reachable; the check says so instead of reporting every commit missing.
func TestAShallowCloneFailsTheCheckInsteadOfGuessing(t *testing.T) {
	r := newGitRepo(t)
	fix := r.commit("fix: the bug", map[string]string{"a.go": "package x\n"})
	r.fragments(map[string]string{"tern-" + fix[:10] + ".md": "### Fixed\n\n- A fix. <!-- fix: " + fix[:10] + " -->\n"})
	shallow := t.TempDir()
	cmd := exec.Command("git", "clone", "-q", "--depth=1", "file://"+r.root, shallow)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	dir := filepath.Join(shallow, "changelog.d")
	fragments, problems := load(dir)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	problems = checkHistory(dir, fragments)
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), "shallow") {
		t.Fatalf("problems = %v, want the shallow-clone message", problems)
	}
}

// With no fragment to check the history is never opened, so a fold that empties changelog.d outside a checkout still passes.
func TestNoFragmentsNeedNoHistory(t *testing.T) {
	if problems := checkHistory(t.TempDir(), nil); len(problems) != 0 {
		t.Fatal(problems)
	}
}

// squashed commits a fix on a side branch, then lands it on main as one squash commit whose message message(side) builds,
// and drops the side branch, as a squash merge does: the side commit stays out of main's history. It returns the side
// commit's full sha.
func (r gitRepo) squashed(message func(side string) string) (side string) {
	r.t.Helper()
	r.git("checkout", "-q", "-b", "side")
	side = r.commit("fix: the squashed bug", map[string]string{"fixed.go": "package x\n"})
	r.git("checkout", "-q", "main")
	r.git("branch", "-q", "-D", "side")
	r.commit(message(side), map[string]string{"fixed.go": "package x\n"})
	return side
}

// A squash commit that carries "Fix-Commit: <full sha>" for a commit it replaced lets a marker or a fragment name keep
// naming that commit; every other spelling of the same claim still fails.
func TestFixCommitTrailerResolvesAMarkerTheSquashRemoved(t *testing.T) {
	const bullet = "### Fixed\n\n- The squashed bug no longer happens. <!-- fix: %s -->\n"
	other := strings.Repeat("ab", 20)
	for _, tc := range []struct {
		name    string
		message func(side string) string
		marker  func(side string) string
		ok      bool
	}{
		{"unreachable marker with a matching trailer resolves", func(s string) string { return "Squash\n\nFix-Commit: " + s }, func(s string) string { return s[:10] }, true},
		{"a full-length marker resolves too", func(s string) string { return "Squash\n\nFix-Commit: " + s }, func(s string) string { return s }, true},
		{"a trailer among others resolves", func(s string) string {
			return "Squash\n\nSigned-off-by: T <t@example.com>\nFix-Commit: " + other + "\nFix-Commit: " + s
		}, func(s string) string { return s[:10] }, true},
		{"unreachable marker without a trailer fails", func(string) string { return "Squash" }, func(s string) string { return s[:10] }, false},
		{"a trailer naming a different sha fails", func(string) string { return "Squash\n\nFix-Commit: " + other }, func(s string) string { return s[:10] }, false},
		{"an abbreviated trailer fails", func(s string) string { return "Squash\n\nFix-Commit: " + s[:10] }, func(s string) string { return s[:10] }, false},
		{"a trailer that is not at a line start fails", func(s string) string { return "Squash\n\nSee Fix-Commit: " + s }, func(s string) string { return s[:10] }, false},
		{"a marker shorter than the minimum fails", func(s string) string { return "Squash\n\nFix-Commit: " + s }, func(s string) string { return s[:6] }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newGitRepo(t)
			side := r.squashed(tc.message)
			r.fragments(map[string]string{"tern-" + side[:10] + ".md": strings.Replace(bullet, "%s", tc.marker(side), 1)})
			got := r.check()
			if tc.ok && got != "" {
				t.Fatalf("the marker must resolve through the trailer:\n%s", got)
			}
			if !tc.ok && !strings.Contains(got, "reachable from HEAD") && !strings.Contains(got, "fewer than") {
				t.Fatalf("the marker must not resolve:\n%q", got)
			}
		})
	}
}

// A fragment name resolves through the trailer the same way a marker does.
func TestFixCommitTrailerResolvesAFragmentName(t *testing.T) {
	const bullet = "### Fixed\n\n- The squashed bug no longer happens. <!-- fix: %s -->\n"
	r := newGitRepo(t)
	side := r.squashed(func(s string) string { return "Squash\n\nFix-Commit: " + s })
	r.fragments(map[string]string{"tern-" + side[:10] + ".md": strings.Replace(bullet, "%s", side[:10], 1)})
	if got := r.check(); got != "" {
		t.Fatalf("name and marker must resolve through the trailer:\n%s", got)
	}
	r = newGitRepo(t)
	side = r.squashed(func(string) string { return "Squash with no trailer" })
	r.fragments(map[string]string{"tern-" + side[:10] + ".md": strings.Replace(bullet, "%s", side[:10], 1)})
	if got := r.check(); !strings.Contains(got, "the name <lane>-<sha>.md names") {
		t.Fatalf("without the trailer the name must fail:\n%s", got)
	}
}
