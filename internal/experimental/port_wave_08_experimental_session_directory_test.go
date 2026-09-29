package experimental

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestPortWave08SessionDirectory(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "PIG_HOME", "PI_HOME"} {
		t.Setenv(name, home)
	}
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "pig-agent"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "pi-agent"))
	// D2: select Pi's environment namespace explicitly so the upstream input is preserved.
	t.Setenv("PIG_USE_PI_DIRS", "1")
	t.Chdir(t.TempDir())
	// upstream: packages/coding-agent/test/experimental-session-directory.test.ts:9
	t.Run("uses the experimental directory under the configured agent directory by default", func(t *testing.T) {
		// Pi's input and expectation are the POSIX literals below. A rooted POSIX path is drive-relative on Windows, so Windows uses a platform-derived absolute path instead.
		agentDir, want := "/tmp/pi-agent-config", "/tmp/pi-agent-config/experimental/sessions"
		if runtime.GOOS == "windows" {
			agentDir = filepath.Join(t.TempDir(), "pi-agent-config")
			want = filepath.Join(agentDir, "experimental", "sessions")
		}
		t.Setenv("PI_CODING_AGENT_DIR", agentDir)
		got, err := ResolveSessionDirectory(nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("directory = %q, want %q", got, want)
		}
	})
	// upstream: packages/coding-agent/test/experimental-session-directory.test.ts:15
	t.Run("resolves an explicit relative directory from the current working directory", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", "/tmp/pi-agent-config")
		got, err := ResolveSessionDirectory(new("relative/sessions"))
		if err != nil {
			t.Fatal(err)
		}
		want, err := filepath.Abs("relative/sessions")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("directory = %q, want %q", got, want)
		}
	})
	// upstream: packages/coding-agent/test/experimental-session-directory.test.ts:21
	t.Run("expands a tilde in an explicit directory", func(t *testing.T) {
		got, err := ResolveSessionDirectory(new("~/custom-sessions"))
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, "custom-sessions")
		if got != want {
			t.Fatalf("directory = %q, want %q", got, want)
		}
	})
}
