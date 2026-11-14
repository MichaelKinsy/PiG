//go:build !windows

package codingagent

import (
	"bytes"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// stateChild re-executes this test binary as another process on the same agent directory with its own scratch HOME and Pi/PiG variables.
func stateChild(t *testing.T, scratch, agentDir, role string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	cmd.Env = []string{"STATE_TEST_ROLE=" + role, "STATE_TEST_DIR=" + agentDir, "HOME=" + scratch, "PIG_HOME=" + scratch, "PI_HOME=" + scratch,
		"PIG_CODING_AGENT_DIR=" + agentDir, "PI_CODING_AGENT_DIR=" + agentDir, "PATH=" + os.Getenv("PATH")}
	return cmd
}

// limitFileSize makes every write past 64 bytes in this process fail with EFBIG after the first 64 bytes were written, as a full disk does.
func limitFileSize(t *testing.T) {
	t.Helper()
	signal.Ignore(syscall.SIGXFSZ)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 64, Max: 64}); err != nil {
		t.Fatal(err)
	}
}

func noTemporaryFiles(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") {
			t.Errorf("a temporary file is left in %s: %s", dir, entry.Name())
		}
	}
}

// A settings write that fails once it has begun (a full disk, a size limit, a kill) must leave settings.json as it was. The in-place write truncated the file first, so the next process read no settings at all and its next change saved that one setting alone.
func TestSettingsWriteThatFailsKeepsEverySetting(t *testing.T) {
	if role := os.Getenv("STATE_TEST_ROLE"); role != "" {
		agentDir := os.Getenv("STATE_TEST_DIR")
		manager := NewSettingsManager(filepath.Join(agentDir, "project"), agentDir)
		if role == "limited-write" {
			limitFileSize(t)
		}
		setErr := manager.SetTheme("light")
		_ = manager.Flush()
		failed := setErr != nil || len(manager.DrainErrors()) > 0
		if role == "limited-write" && !failed {
			t.Fatal("a settings write past the file size limit succeeded")
		}
		if role == "write" && failed {
			t.Fatal("the settings write failed")
		}
		return
	}
	scratch := t.TempDir()
	agentDir := filepath.Join(scratch, "agent")
	if err := os.MkdirAll(filepath.Join(agentDir, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(agentDir, "settings.json")
	before := []byte("{\n  \"defaultProvider\": \"anthropic\",\n  \"defaultModel\": \"claude-sonnet-4-5\",\n  \"defaultThinkingLevel\": \"high\",\n  \"packages\": [\n    \"npm:pi-demo\"\n  ]\n}")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := stateChild(t, scratch, agentDir, "limited-write").CombinedOutput(); err != nil {
		t.Fatalf("limited-write process: %v\n%s", err, out)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
		t.Fatalf("settings.json after the failed write is %d bytes %q, want the %d bytes it had", len(after), after, len(before))
	}
	if out, err := stateChild(t, scratch, agentDir, "write").CombinedOutput(); err != nil {
		t.Fatalf("write process: %v\n%s", err, out)
	}
	after, _ := os.ReadFile(path)
	for _, want := range []string{`"defaultProvider": "anthropic"`, `"defaultModel": "claude-sonnet-4-5"`, `"npm:pi-demo"`, `"theme": "light"`} {
		if !bytes.Contains(after, []byte(want)) {
			t.Errorf("settings.json after the failed write and a later change lacks %s:\n%s", want, after)
		}
	}
	noTemporaryFiles(t, agentDir)
}

// The same for the project trust store: a torn write lost every trust decision, and the next decision wrote a store that held that one alone.
func TestTrustWriteThatFailsKeepsEveryDecision(t *testing.T) {
	yes := true
	if role := os.Getenv("STATE_TEST_ROLE"); role != "" {
		agentDir := os.Getenv("STATE_TEST_DIR")
		if role == "limited-write" {
			limitFileSize(t)
		}
		err := NewProjectTrustStore(agentDir).Set("/work/new", &yes)
		if role == "limited-write" && err == nil {
			t.Fatal("a trust write past the file size limit succeeded")
		}
		if role == "write" && err != nil {
			t.Fatal(err)
		}
		return
	}
	scratch := t.TempDir()
	agentDir := filepath.Join(scratch, "agent")
	store := NewProjectTrustStore(agentDir)
	for _, dir := range []string{"/work/a", "/work/b", "/work/c", "/work/d"} {
		if err := store.Set(dir, &yes); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(agentDir, "trust.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := stateChild(t, scratch, agentDir, "limited-write").CombinedOutput(); err != nil {
		t.Fatalf("limited-write process: %v\n%s", err, out)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
		t.Fatalf("trust.json after the failed write is %d bytes %q, want the %d bytes it had", len(after), after, len(before))
	}
	if out, err := stateChild(t, scratch, agentDir, "write").CombinedOutput(); err != nil {
		t.Fatalf("write process: %v\n%s", err, out)
	}
	after, _ := os.ReadFile(path)
	for _, want := range []string{`"/work/a": true`, `"/work/d": true`, `"/work/new": true`} {
		if !bytes.Contains(after, []byte(want)) {
			t.Errorf("trust.json after the failed write and a later decision lacks %s:\n%s", want, after)
		}
	}
	noTemporaryFiles(t, agentDir)
}
