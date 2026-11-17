package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/session-manager.ts

// createAgentSession makes the session directory eagerly when it builds the default manager (sdk.ts:201 getDefaultSessionDir, session-manager.ts:597-602) with mkdirSync(dir, {recursive: true}) and lets Node's error escape unwrapped, so the caller sees `ENOTDIR: not a directory, mkdir '<dir>'` and nothing else. PiG's Create reports the same text, without a "sessionmanager: mkdir:" prefix. A read-only file system (EROFS) is not made by this test.
func TestSessionManagerCreateReportsNodesMkdirErrorForAnUnmakeableDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{"file where the directory should be": filepath.Join(file, "agent", "sessions", "--cwd--")}
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if probe, err := os.Create(filepath.Join(locked, "probe")); err == nil {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
	} else {
		cases["unwritable parent"] = filepath.Join(locked, "agent", "sessions", "--cwd--")
	}
	sessionsIsAFile := filepath.Join(root, "agent2")
	if err := os.MkdirAll(sessionsIsAFile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionsIsAFile, "sessions"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases["sessions is a file"] = filepath.Join(sessionsIsAFile, "sessions", "--cwd--")
	for name, dir := range cases {
		t.Run(name, func(t *testing.T) {
			var want string
			pioracle.Run(t, `
let message = "ok";
try { const { SessionManager } = await load("pi-coding-agent/core/session-manager.js"); SessionManager.create(input.cwd, input.dir); } catch (error) { message = error.message; }
emit(message);`, map[string]string{"cwd": root, "dir": dir}, &want)
			if want == "ok" {
				t.Fatalf("Pi created %s", dir)
			}
			_, err := NewSessionManagerWithDir(root, dir).Create("", "")
			if err == nil || err.Error() != want {
				t.Fatalf("Create error = %v, want Pi's %q", err, want)
			}
		})
	}
}

// A session directory that exists, even as a regular file, is skipped: the constructor makes it only when existsSync(dir) is false (session-manager.ts:1018-1020, getDefaultSessionDir :597-602), so SessionManager.create does not throw and the first write of the session file reports the failure.
func TestSessionManagerCreateSkipsASessionDirectoryThatExistsAsAFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var want string
	pioracle.Run(t, `
let message = "ok";
try { const { SessionManager } = await load("pi-coding-agent/core/session-manager.js"); SessionManager.create(input.cwd, input.dir); } catch (error) { message = error.message; }
emit(message);`, map[string]string{"cwd": filepath.Dir(dir), "dir": dir}, &want)
	if want != "ok" {
		t.Fatalf("Pi's SessionManager.create over a file = %q, want it to skip the directory", want)
	}
	if err := MkdirSessionDir(dir); err != nil {
		t.Fatalf("MkdirSessionDir over a file = %v, want nil as Pi's existsSync guard", err)
	}
}
