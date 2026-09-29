package configvalue

import (
	"os"
	"strings"
	"testing"
)

func setupConfigValueTest(t *testing.T) string {
	t.Helper()
	tempDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	ClearCache()
	t.Cleanup(ClearCache)
	return tempDir
}

func assertResolvedConfigValue(t *testing.T, config, want string) {
	t.Helper()
	if got := Resolve(config, nil); got != want {
		t.Fatalf("resolveConfigValue(%q) = %q, want %q", config, got, want)
	}
}

func assertConfigCommandCounter(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != want {
		t.Fatalf("counter = %q, want %q", got, want)
	}
}
