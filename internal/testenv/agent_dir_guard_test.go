package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// automation/ci/agent-dir-guard.sh fails a `make test` group when a test rewrites a credential file or adds a session under the agent directory the environment names. Each mutation below is one way the 2026-10-06 lane credential wipe could recur.
func TestAgentDirGuardFailsWhenATestTouchesTheSeededDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the guard is a bash script run by make test on Unix runners")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "automation", "ci", "agent-dir-guard.sh"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(root, mode string) (string, error) {
		out, err := exec.Command(bash, script, mode, root).CombinedOutput()
		return string(out), err
	}
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, root string)
		want   string
	}{
		{"untouched", func(*testing.T, string) {}, ""},
		{"credentials emptied", func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "agent", "auth.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "agent/auth.json"},
		{"credentials removed", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "pi-agent", "auth.json")); err != nil {
				t.Fatal(err)
			}
		}, "pi-agent/auth.json"},
		{"session written", func(t *testing.T, root string) {
			dir := filepath.Join(root, "agent", "sessions", "--cwd--")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "agent/sessions/--cwd--/s.jsonl"},
		{"session directory created", func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, "agent", "sessions", "--cwd--"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, "agent/sessions/--cwd--"},
		{"lock directory created and removed", func(t *testing.T, root string) {
			lock := filepath.Join(root, "agent", "settings.json.lock")
			if err := os.Mkdir(lock, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(lock); err != nil {
				t.Fatal(err)
			}
		}, "modified: ./agent\n"},
		{"credentials rewritten with the same bytes", func(t *testing.T, root string) {
			path := filepath.Join(root, "agent", "auth.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "modified: ./agent/auth.json"},
		{"home written", func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "pig-home", "new"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "pig-home/new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "guard")
			exports, err := run(root, "seed")
			if err != nil {
				t.Fatalf("seed: %v: %s", err, exports)
			}
			for _, key := range []string{"PIG_CODING_AGENT_DIR=", "PI_CODING_AGENT_DIR=", "PIG_HOME=", "PI_HOME="} {
				if !strings.Contains(exports, key) {
					t.Errorf("seed output lacks %s: %s", key, exports)
				}
			}
			tc.mutate(t, root)
			out, err := run(root, "check")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("untouched directories failed the guard: %v: %s", err, out)
				}
				return
			}
			if err == nil || !strings.Contains(out, tc.want) {
				t.Fatalf("guard result = %v, output %q, want a failure naming %s", err, out, tc.want)
			}
		})
	}
}
