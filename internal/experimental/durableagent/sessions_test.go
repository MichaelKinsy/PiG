package durableagent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// sessionRoot isolates the agent directory and returns the directory of the sessions of cwd.
func sessionRoot(t *testing.T, kind, cwd string) string {
	t.Helper()
	agentDir := filepath.Join(t.TempDir(), "agent")
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_USE_PI_DIRS", "")
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(resolved))
	return filepath.Join(agentDir, "experimental", kind+"-sessions", hex.EncodeToString(digest[:])[:24])
}

// sessions.ts:15-45: a new session is a locked directory named by time and UUID under the hash of the real path of the working directory.
func TestSelectSessionCreatesALockedSessionForTheRealPathOfTheDirectory(t *testing.T) {
	project := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	testenv.RequireDirectoryLink(t, project, link)
	root := sessionRoot(t, "durable", project)
	before := time.Now().UnixMilli()
	location, err := SelectSession(t.Context(), "durable", link, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = location.Release() }()
	resolved, _ := filepath.EvalSymlinks(project)
	if location.CWD != resolved || !location.Created {
		t.Fatalf("cwd %q created %v; want the real path %q, created", location.CWD, location.Created, resolved)
	}
	if location.Directory != filepath.Join(root, location.ID) || location.Database != filepath.Join(location.Directory, "session.sqlite") {
		t.Fatalf("location = %+v, want a session directory under %s", location, root)
	}
	if !regexp.MustCompile(`^\d{13}-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(location.ID) {
		t.Fatalf("id = %q", location.ID)
	}
	if stamp, err := strconv.ParseInt(location.ID[:13], 10, 64); err != nil || stamp < before || stamp > time.Now().UnixMilli() {
		t.Fatalf("the directory time %q is not the creation time (%d..%d)", location.ID[:13], before, time.Now().UnixMilli())
	}
	if info, err := os.Stat(location.Directory); err != nil || !info.IsDir() {
		t.Fatalf("the session directory does not exist: %v", err)
	}
	if _, err := os.Stat(location.Directory + ".lock"); err != nil {
		t.Fatalf("the session directory is not locked: %v", err)
	}
	if err := location.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(location.Directory + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("the lock outlives its release: %v", err)
	}
}

// sessions.ts:27-37: --continue opens the newest session directory by name; other entries are not sessions.
func TestSelectSessionContinuesTheNewestSession(t *testing.T) {
	project := t.TempDir()
	root := sessionRoot(t, "durable", project)
	older := "1700000000000-" + "11111111-1111-1111-1111-111111111111"
	newer := "1700000000001-" + "00000000-0000-0000-0000-000000000000"
	for _, name := range []string{older, newer, "1800000000000-not-a-session", "9999999999999"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A file that matches the name is not a directory.
	if err := os.WriteFile(filepath.Join(root, "1900000000000-22222222-2222-2222-2222-222222222222"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	location, err := SelectSession(t.Context(), "durable", project, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = location.Release() }()
	if location.ID != newer || location.Created {
		t.Fatalf("id %q created %v, want the newest %q, not created", location.ID, location.Created, newer)
	}
}

func TestSelectSessionContinueWithoutASessionFails(t *testing.T) {
	for _, kind := range []string{"durable", "vacation"} {
		project := t.TempDir()
		sessionRoot(t, kind, project)
		resolved, _ := filepath.EvalSymlinks(project)
		_, err := SelectSession(t.Context(), kind, project, true)
		if want := "No " + kind + " session exists for " + resolved; err == nil || err.Error() != want {
			t.Fatalf("%s: error = %v, want %q", kind, err, want)
		}
	}
}

// sessions.ts:39-49: a second process finds the lock held, waits, and then fails with the directory named; after the holder releases, the session opens.
func TestSelectSessionRefusesASessionOpenElsewhere(t *testing.T) {
	previous := sessionLockWait
	sessionLockWait = 50 * time.Millisecond
	t.Cleanup(func() { sessionLockWait = previous })
	project := t.TempDir()
	sessionRoot(t, "durable", project)
	first, err := SelectSession(t.Context(), "durable", project, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = SelectSession(t.Context(), "durable", project, true)
	// sessions.ts:46-48: the message is exactly the directory; the lock failure is only the cause.
	if err == nil || err.Error() != "Session is already open in another process: "+first.Directory || errors.Unwrap(err) == nil {
		t.Fatalf("error = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := SelectSession(t.Context(), "durable", project, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID {
		t.Fatalf("continued %q, want %q", again.ID, first.ID)
	}
	_ = again.Release()
}

// The vacation planner keeps its sessions apart from the coding agent's.
func TestSelectSessionKindsDoNotShareDirectories(t *testing.T) {
	project := t.TempDir()
	durableRoot := sessionRoot(t, "durable", project)
	location, err := SelectSession(t.Context(), "vacation", project, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = location.Release() }()
	if filepath.Dir(location.Directory) == durableRoot || !strings.Contains(location.Directory, "vacation-sessions") {
		t.Fatalf("directory = %s", location.Directory)
	}
}

// sessions.ts:39-45: a second process retries while the holder lets go, so a short overlap does not fail it.
func TestSelectSessionWaitsForTheHolderToRelease(t *testing.T) {
	previous := sessionLockWait
	sessionLockWait = 10 * time.Second
	t.Cleanup(func() { sessionLockWait = previous })
	project := t.TempDir()
	sessionRoot(t, "durable", project)
	first, err := SelectSession(t.Context(), "durable", project, false)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	time.AfterFunc(100*time.Millisecond, func() { released <- first.Release() })
	second, err := SelectSession(t.Context(), "durable", project, true)
	if err != nil {
		t.Fatalf("the second open must wait for the release: %v", err)
	}
	defer func() { _ = second.Release() }()
	if err := <-released; err != nil {
		t.Fatal(err)
	}
}
