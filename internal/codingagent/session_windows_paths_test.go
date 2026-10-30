package codingagent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// Pi 1be312b4 (#10488): with a custom session directory, --continue, resume by id and the session list hid sessions recorded with different cwd casing on Windows;
// sessionCwdMatches, the default-directory test and usesDefaultSessionDir compare with pathsEqual.
func TestSessionCwdAndDefaultDirectoryComparisonsIgnoreCaseOnWindows(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "Project")
	t.Run("sessionCwdMatches", func(t *testing.T) {
		if sessionCwdMatches(strings.ToLower(cwd), cwd) {
			t.Fatal("a header cwd in other casing matched on a case-sensitive platform")
		}
		t.Cleanup(resolvepath.OverridePlatform("windows"))
		if !sessionCwdMatches(strings.ToLower(cwd), cwd) {
			t.Fatal("a header cwd in other casing did not match on Windows")
		}
		if sessionCwdMatches("", cwd) {
			t.Fatal("an empty header cwd matched")
		}
	})
	t.Run("default session directory", func(t *testing.T) {
		t.Setenv(ENV_AGENT_DIR, filepath.Join(t.TempDir(), "Agent"))
		sm := NewSessionManagerWithDir(cwd, strings.ToLower(defaultSessionDir(cwd)))
		if sm.sessionDirIsCwdScoped() {
			t.Fatal("a session directory in other casing was the default one on a case-sensitive platform")
		}
		session := NewSession("windows-paths", cwd)
		session.path = filepath.Join(strings.ToLower(defaultSessionDir(cwd)), "s.jsonl")
		view := ExtensionSessionView{CWD: cwd, SessionDir: strings.ToLower(defaultSessionDir(cwd)), Session: session}
		if view.usesDefaultSessionDir() {
			t.Fatal("usesDefaultSessionDir matched other casing on a case-sensitive platform")
		}
		t.Cleanup(resolvepath.OverridePlatform("windows"))
		if !sm.sessionDirIsCwdScoped() || !view.usesDefaultSessionDir() {
			t.Fatal("a session directory in other casing was not the default one on Windows")
		}
	})
}
