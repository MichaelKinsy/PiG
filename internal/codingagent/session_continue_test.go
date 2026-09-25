package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// FindMostRecentForContinue must not resume another project's session when
// the session dir is a custom (non-cwd-encoded) directory shared across
// projects: it filters to the newest session whose header cwd matches the
// runtime cwd, even when a different project's session is newer. Mirrors
// upstream continueRecent's filterCwd branch.
func TestFindMostRecentForContinue_FiltersByCwd(t *testing.T) {
	dir := t.TempDir() // custom shared session dir, not the cwd-encoded default

	write := func(name, headerCwd, text string, mtime time.Time) string {
		path := filepath.Join(dir, name)
		header := fmt.Sprintf(`{"type":"session","version":%d,"id":%q,"timestamp":"2025-01-01T00:00:00.000Z","cwd":%q}`,
			CurrentSessionVersion, name, headerCwd)
		msg := fmt.Sprintf(`{"type":"message","id":"m1","parentId":null,"timestamp":"2025-01-01T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`, text)
		if err := os.WriteFile(path, []byte(header+"\n"+msg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return path
	}

	now := time.Now()
	projA := "/projects/alpha"
	projB := "/projects/beta"
	wantA := write("a.jsonl", projA, "alpha", now.Add(-time.Minute))
	write("b.jsonl", projB, "beta", now) // newer, but a different project

	smA := NewSessionManagerWithDir(projA, dir)
	if got := smA.FindMostRecentForContinue(); got != wantA {
		t.Errorf("FindMostRecentForContinue=%q want %q (cwd-matched alpha, not newer beta)", got, wantA)
	}

	// No session matches the cwd: resume nothing rather than another project's.
	smGamma := NewSessionManagerWithDir("/projects/gamma", dir)
	if got := smGamma.FindMostRecentForContinue(); got != "" {
		t.Errorf("FindMostRecentForContinue=%q want empty for unmatched cwd", got)
	}

	// Plain FindMostRecent stays cwd-agnostic (newest wins) so the
	// picker and other callers are unaffected by the continue filter.
	if got := smGamma.FindMostRecent(); got == "" {
		t.Errorf("FindMostRecent should still return the newest session regardless of cwd")
	}
}

// A session header cwd in canonical form (/private/tmp/x) must match a
// runtime cwd in logical form (/tmp/x) for the same real directory, since
// Go's os.Getwd returns the logical path. Exercised with a real temp dir
// so EvalSymlinks resolves an actual symlinked path on macOS (/var ->
// /private/var, /tmp -> /private/tmp).
func TestListSessionsCustomDirFiltersCurrentCWDAndListAllDoesNot(t *testing.T) {
	dir := t.TempDir()
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	writeSessionInfoFixture(t, dir, "a", cwdA)
	writeSessionInfoFixture(t, dir, "b", cwdB)

	manager := NewSessionManagerWithDir(cwdA, dir)
	current, err := manager.ListCurrentSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(current) != 1 || current[0].ID != "a" {
		t.Fatalf("current sessions = %+v, want only a", current)
	}
	all, err := manager.ListAllSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all sessions = %+v, want two", all)
	}
}

func TestListSessionsAcrossRootDiscoversDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	writeSessionInfoFixture(t, target, "linked", t.TempDir())
	alias := filepath.Join(root, "--linked--")
	testenv.Symlink(t, target, alias)

	sessions, err := listSessionsAcrossRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "linked" {
		t.Fatalf("sessions = %+v, want linked", sessions)
	}
	if want := filepath.Join(alias, "linked.jsonl"); sessions[0].Path != want {
		t.Fatalf("path = %q, want alias path %q", sessions[0].Path, want)
	}
}

func TestListSessionsAcrossRootIgnoresBrokenDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "--regular--")
	if err := os.Mkdir(regular, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSessionInfoFixture(t, regular, "regular", t.TempDir())
	target := filepath.Join(t.TempDir(), "removed-sessions")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, target, filepath.Join(root, "--broken--"))
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}

	sessions, err := listSessionsAcrossRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "regular" {
		t.Fatalf("sessions = %+v, want regular", sessions)
	}
}

func TestListSessionsAcrossRootIgnoresSymlinkToFile(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "--regular--")
	if err := os.Mkdir(regular, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSessionInfoFixture(t, regular, "regular", t.TempDir())
	target := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, target, filepath.Join(root, "--file--"))

	sessions, err := listSessionsAcrossRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "regular" {
		t.Fatalf("sessions = %+v, want regular", sessions)
	}
}

func writeSessionInfoFixture(t *testing.T, dir, id, cwd string) {
	t.Helper()
	path := filepath.Join(dir, id+".jsonl")
	header := fmt.Sprintf("{\"type\":\"session\",\"version\":3,\"id\":%q,\"timestamp\":\"2026-08-04T00:00:00Z\",\"cwd\":%q}\n", id, cwd)
	if err := os.WriteFile(path, []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCwdMatches_ResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	canonical, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("evalsymlinks: %v", err)
	}
	if !sessionCwdMatches(canonical, real) {
		t.Errorf("sessionCwdMatches(%q, %q) = false, want true (same real dir)", canonical, real)
	}
	if sessionCwdMatches("", real) {
		t.Errorf("empty header cwd must never match")
	}
	if sessionCwdMatches("/no/such/dir-a", "/no/such/dir-b") {
		t.Errorf("distinct nonexistent dirs must not match")
	}
}

// TestFindMostRecentTieBreaksByCreated covers a CI flake: on a coarse-mtime
// filesystem two sessions written back-to-back share an identical mtime, so a
// pure-mtime sort leaves their order undefined and FindMostRecent can return
// the older one. The header creation timestamp (RFC3339Nano) breaks the tie
// deterministically. Filenames are chosen so the lexically-first file is the
// OLDER session, so the unfixed mtime-only sort returns it (red) and the
// Created tie-break returns the newer one (green).
func TestFindMostRecentTieBreaksByCreated(t *testing.T) {
	dir := t.TempDir()
	sameMtime := time.Now().Truncate(time.Second)

	write := func(name, created string) string {
		path := filepath.Join(dir, name)
		header := fmt.Sprintf(`{"type":"session","version":%d,"id":%q,"timestamp":%q,"cwd":"/p"}`,
			CurrentSessionVersion, name, created)
		msg := `{"type":"message","id":"m1","parentId":null,"timestamp":"2025-01-01T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"x"}]}}`
		if err := os.WriteFile(path, []byte(header+"\n"+msg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, sameMtime, sameMtime); err != nil {
			t.Fatal(err)
		}
		return path
	}

	write("a.jsonl", "2025-01-01T00:00:00.000000001Z")              // lexically first, older
	wantNewer := write("b.jsonl", "2025-01-01T00:00:00.000000002Z") // lexically second, newer

	sm := NewSessionManagerWithDir("/p", dir)
	if got := sm.FindMostRecent(); got != wantNewer {
		t.Errorf("FindMostRecent=%q want %q (later-created wins the equal-mtime tie)", got, wantNewer)
	}
}

// compareSessionRecencyDesc must be a strict total order: slices.SortFunc is
// not stable, so two distinct sessions that tie on both mtime and Created must
// still compare non-zero and antisymmetric, or their order is undefined. This
// is the deeper invariant behind the equal-mtime flake: Created resolves the
// realistic case (nanosecond session headers), Path guarantees determinism even
// in the degenerate mtime==Created tie. Red without the Path fallback (returns
// 0 for distinct paths).
func TestCompareSessionRecencyDescIsTotalOrder(t *testing.T) {
	mt := time.Unix(1000, 0)
	ct := time.Unix(2000, 0)
	a := SessionInfo{Path: "/x/a.jsonl", Modified: mt, Created: ct}
	b := SessionInfo{Path: "/x/b.jsonl", Modified: mt, Created: ct}

	if got := compareSessionRecencyDesc(a, b); got >= 0 {
		t.Errorf("equal mtime+created: compareSessionRecencyDesc(a,b)=%d, want <0 (path breaks the tie)", got)
	}
	if compareSessionRecencyDesc(a, b) != -compareSessionRecencyDesc(b, a) {
		t.Errorf("comparator not antisymmetric under the path tie-break")
	}

	// mtime dominates Created and Path: a newer mtime sorts first even with an
	// older Created and a lexically larger path.
	newer := SessionInfo{Path: "/x/z.jsonl", Modified: mt.Add(time.Second), Created: time.Unix(1, 0)}
	if got := compareSessionRecencyDesc(newer, a); got >= 0 {
		t.Errorf("newer mtime must sort first: got %d, want <0", got)
	}
	// Created dominates Path when mtime ties.
	createdNewer := SessionInfo{Path: "/x/z.jsonl", Modified: mt, Created: ct.Add(time.Second)}
	if got := compareSessionRecencyDesc(createdNewer, a); got >= 0 {
		t.Errorf("later Created must sort first when mtime ties: got %d, want <0", got)
	}
}
