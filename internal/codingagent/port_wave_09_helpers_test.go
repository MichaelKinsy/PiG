package codingagent

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// isolatePathTestHome keeps path expansion and all effective agent/session roots inside this test's temporary directory.
func isolatePathTestHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	for key, dir := range map[string]string{
		"HOME":                         home,
		"USERPROFILE":                  home,
		"PIG_HOME":                     filepath.Join(root, "pig-home"),
		"PIG_CODING_AGENT_DIR":         filepath.Join(root, "pig-agent"),
		"PI_CODING_AGENT_DIR":          filepath.Join(root, "pi-agent"),
		"PIG_CODING_AGENT_SESSION_DIR": filepath.Join(root, "pig-sessions"),
		"PI_CODING_AGENT_SESSION_DIR":  filepath.Join(root, "pi-sessions"),
		"XDG_CONFIG_HOME":              filepath.Join(root, "config"),
		"XDG_DATA_HOME":                filepath.Join(root, "data"),
		"XDG_STATE_HOME":               filepath.Join(root, "state"),
		"XDG_CACHE_HOME":               filepath.Join(root, "cache"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	t.Setenv("PI_SESSION_FILE", filepath.Join(root, "pi-sessions", "session.jsonl"))
	return home
}

func requirePathResult[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func realPathForTest(t *testing.T, path string) string {
	t.Helper()
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return realPath
}

// fileURLForTest constructs the node:pathToFileURL fixture for an absolute native temporary path, including the leading slash before a Windows drive.
func fileURLForTest(path string) *url.URL {
	pathname := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		pathname = "/" + pathname
	}
	return &url.URL{Scheme: "file", Path: pathname}
}
