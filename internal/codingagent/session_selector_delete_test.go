package codingagent

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// installFakeTrash copies this test binary into a fresh directory as the platform's `trash` executable and returns that directory. TestMain turns it into the fake selected by fakeTrashEnv.
func installFakeTrash(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "trash"
	if runtime.GOOS == "windows" {
		name = "trash.exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), binary, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// nodeUnlinkDirectoryError is Node's fs.promises.unlink message for a directory: libuv reports EPERM on Windows and Darwin and the kernel reports EISDIR on Linux. Node 24 on Windows was probed for this text.
func nodeUnlinkDirectoryError(path string) string {
	if runtime.GOOS == "linux" {
		return "EISDIR: illegal operation on a directory, unlink '" + path + "'"
	}
	return "EPERM: operation not permitted, unlink '" + path + "'"
}

// Pi deleteSessionFile (session-selector.ts:654-689) runs `trash` through spawnSync with literal arguments, passing "--" before a dash-prefixed path. Exit status 0, or a path that no longer exists, counts as a trash deletion. Otherwise it unlinks, and a failed unlink reports Node's error text with a hint built from the spawn error and the first stderr line, limited to 200 UTF-16 units.
func TestDeleteSessionFileMatchesPi(t *testing.T) {
	fake := installFakeTrash(t)
	absent := t.TempDir()
	type outcome struct {
		result sessionDeleteResult
		argv   []string
		exists bool
	}
	run := func(t *testing.T, pathDir, mode, target string) outcome {
		t.Helper()
		record := filepath.Join(t.TempDir(), "argv.json")
		t.Setenv("PATH", pathDir)
		t.Setenv(fakeTrashEnv, mode)
		t.Setenv(fakeTrashArgvEnv, record)
		out := outcome{result: deleteSessionFile(target)}
		if data, err := os.ReadFile(record); err == nil {
			if err := json.Unmarshal(data, &out.argv); err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		_, err := os.Lstat(target)
		out.exists = err == nil
		return out
	}
	file := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "victim.jsonl")
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	directory := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "victim.jsonl")
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	trash := sessionDeleteResult{ok: true, method: sessionDeleteTrash}
	unlinked := sessionDeleteResult{ok: true, method: sessionDeleteUnlink}
	failed := func(message string) sessionDeleteResult {
		return sessionDeleteResult{method: sessionDeleteUnlink, error: message}
	}
	for _, tc := range []struct {
		name, mode string
		absent     bool
		target     func(*testing.T) string
		want       func(path string) sessionDeleteResult
		exists     bool
		argv       bool
	}{
		{name: "trash succeeds", mode: "move", target: file, want: func(string) sessionDeleteResult { return trash }, argv: true},
		{name: "trash fails and unlink succeeds", mode: "fail", target: file, want: func(string) sessionDeleteResult { return unlinked }, argv: true},
		{name: "trash fails after the file is gone", mode: "fail-removed", target: file, want: func(string) sessionDeleteResult { return trash }, argv: true},
		{name: "trash absent and unlink succeeds", absent: true, target: file, want: func(string) sessionDeleteResult { return unlinked }},
		{name: "trash absent and the file is already gone", absent: true, target: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing.jsonl") }, want: func(string) sessionDeleteResult { return trash }},
		{name: "trash and unlink both fail", mode: "fail", target: directory, exists: true, argv: true, want: func(path string) sessionDeleteResult {
			return failed(nodeUnlinkDirectoryError(path) + " (trash: trash: fake refusal\r)")
		}},
		{name: "trash absent and unlink fails", absent: true, target: directory, exists: true, want: func(path string) sessionDeleteResult {
			return failed(nodeUnlinkDirectoryError(path) + " (trash: spawnSync trash ENOENT)")
		}},
		{name: "trash stdout exceeds spawnSync maxBuffer", mode: "overflow-stdout", target: directory, exists: true, argv: true, want: func(path string) sessionDeleteResult {
			return failed(nodeUnlinkDirectoryError(path) + " (trash: spawnSync trash ENOBUFS)")
		}},
		{name: "trash stderr exceeds spawnSync maxBuffer", mode: "overflow-stderr", target: directory, exists: true, argv: true, want: func(path string) sessionDeleteResult {
			return failed(nodeUnlinkDirectoryError(path) + " (trash: spawnSync trash ENOBUFS · " + strings.Repeat("x", 200-len([]rune("spawnSync trash ENOBUFS · "))) + ")")
		}},
		{name: "trash combined output exceeds spawnSync maxBuffer", mode: "overflow-combined", target: directory, exists: true, argv: true, want: func(path string) sessionDeleteResult {
			return failed(nodeUnlinkDirectoryError(path) + " (trash: spawnSync trash ENOBUFS · " + strings.Repeat("x", 200-len([]rune("spawnSync trash ENOBUFS · "))) + ")")
		}},
		{name: "trash hint is limited to 200 UTF-16 units", mode: "fail-long", target: directory, exists: true, argv: true, want: func(path string) sessionDeleteResult {
			return failed(nodeUnlinkDirectoryError(path) + " (trash: " + strings.Repeat("\U0001F600", 100) + ")")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pathDir, mode := fake, tc.mode
			if tc.absent {
				pathDir, mode = absent, "move"
			}
			target := tc.target(t)
			got := run(t, pathDir, mode, target)
			if want := tc.want(target); got.result != want {
				t.Errorf("result=%#v\nwant    %#v", got.result, want)
			}
			if got.exists != tc.exists {
				t.Errorf("target exists=%v, want %v", got.exists, tc.exists)
			}
			var wantArgv []string
			if tc.argv {
				wantArgv = []string{target}
			}
			if !reflect.DeepEqual(got.argv, wantArgv) {
				t.Errorf("trash argv=%q, want %q", got.argv, wantArgv)
			}
		})
	}
	t.Run("dash-prefixed path follows --", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		if err := os.WriteFile(filepath.Join(dir, "-victim.jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got := run(t, fake, "move", "-victim.jsonl")
		if got.result != trash || got.exists || !reflect.DeepEqual(got.argv, []string{"--", "-victim.jsonl"}) {
			t.Fatalf("got %#v", got)
		}
	})
	if runtime.GOOS == "windows" {
		t.Run("explicit relative PATH entry runs trash", func(t *testing.T) {
			t.Chdir(fake)
			target := file(t)
			got := run(t, ".", "move", target)
			if got.result != trash || got.exists || !reflect.DeepEqual(got.argv, []string{target}) {
				t.Fatalf("got %#v", got)
			}
		})
		// Node 24's spawnSync("trash") on Windows resolves only trash.com and trash.exe on PATH; it neither runs batch files nor implicitly searches the current directory.
		t.Run("batch files and the current directory are not trash", func(t *testing.T) {
			shims := t.TempDir()
			marker := filepath.Join(shims, "ran.txt")
			for _, name := range []string{"trash.cmd", "trash.bat"} {
				script := "@echo ran> \"" + marker + "\"\r\n@del /f /q \"%~1\"\r\n@exit /b 0\r\n"
				if err := os.WriteFile(filepath.Join(shims, name), []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			cwd := installFakeTrash(t)
			t.Chdir(cwd)
			target := file(t)
			got := run(t, shims, "move", target)
			if got.result != unlinked || got.exists || got.argv != nil {
				t.Fatalf("got %#v", got)
			}
			if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("a batch trash shim ran: %v", err)
			}
		})
		t.Run("unlink of an open file reports EBUSY", func(t *testing.T) {
			target := file(t)
			handle, err := os.Open(target)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := handle.Close(); err != nil {
					t.Error(err)
				}
			}()
			got := run(t, absent, "move", target)
			want := failed("EBUSY: resource busy or locked, unlink '" + target + "' (trash: spawnSync trash ENOENT)")
			if got.result != want || !got.exists {
				t.Fatalf("got %#v\nwant %#v", got, want)
			}
		})
	}
}

// Both pickers use deleteSessionFile, so the selector shows Pi's trash status for a real listed session.
func TestSessionSelectorTrashDeletionStatus(t *testing.T) {
	t.Setenv(ENV_AGENT_DIR, t.TempDir())
	cwd := t.TempDir()
	path := listingPersistedSession(t, defaultSessionDir(cwd), cwd, "trash me")
	t.Setenv("PATH", installFakeTrash(t))
	t.Setenv(fakeTrashEnv, "move")
	sm := NewSessionManager(cwd)
	s := newSessionSelectorFromListers(
		func(o SessionListOptions) ([]SessionInfo, error) { return sm.ListCurrentSessions(o) },
		func(o SessionListOptions) ([]SessionInfo, error) { return sm.ListAllSessions(o) },
		nil, sm.deleteListedSession, "", sessionSelectorInputBindings(t))
	t.Cleanup(s.close)
	for s.scopeLoad(sessionScopeCurrent) != nil {
		select {
		case update := <-s.work.updates:
			update()
		case result := <-s.loadResult(sessionScopeCurrent):
			s.finishLoad(sessionScopeCurrent, result)
		}
	}
	if len(s.filtered) != 1 || s.filtered[0].Session.Path != path {
		t.Fatalf("listed %+v, want %s", s.filtered, path)
	}
	s.HandleInput("\x04")
	s.HandleInput("\r")
	if s.statusState.message != "Session moved to trash" || s.statusState.error {
		t.Fatalf("status=%+v", s.statusState)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("session remains: %v", err)
	}
}
