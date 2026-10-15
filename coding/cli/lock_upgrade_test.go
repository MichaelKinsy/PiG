package cli

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func TestCLIUpgradeFromV020(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pig.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/pig")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	root := t.TempDir()
	agent, cwd := filepath.Join(root, "agent"), filepath.Join(root, "work")
	for _, dir := range []string{agent, cwd} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("PIG_HOME", filepath.Join(root, "pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("PIG_USE_PI_DIRS", "")
	t.Setenv("PIG_TEST_FAUX", "1")
	t.Setenv("PIG_OFFLINE", "1")
	t.Setenv("PI_OFFLINE", "1")
	if err := os.WriteFile(filepath.Join(agent, "settings.json"), []byte(`{"defaultProvider":"test-faux","defaultModel":"faux-1","enableInstallTelemetry":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(bin string) []byte {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), bin, "--model", "test-faux/faux-1", "--no-session", "--print", "reply with exactly: UPGRADED")
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != "UPGRADED\n" {
			t.Fatalf("CLI: %v\n%s", err, out)
		}
		return out
	}
	// This optional cache is the verified v0.2.0 linux-amd64 release asset, not a network download or a developer build. Other hosts use the exact old lock fixture below.
	if cached := os.Getenv("PIG_TEST_V020_BIN"); cached != "" {
		data, err := os.ReadFile(cached)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != "432719982b7f218cff04ac901ade1eec096f5631d013d90cbe8c8704b09487a7" {
			t.Fatalf("unverified v0.2.0 release: %s", got)
		}
		run(cached)
	}
	// v0.2.0 c318b771: auth.go:394, models_store.go:278,
	// settings.go:2343, trust_manager.go:210 leave these empty OS-locked files.
	for _, name := range []string{"auth.json", "models-store.json", "settings.json", "trust.json"} {
		lock := flock.New(filepath.Join(agent, name+".lock"))
		ok, err := lock.TryLock()
		if err != nil || !ok {
			t.Fatalf("old protocol: %v %v", ok, err)
		}
		if err := lock.Unlock(); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Minute)
		if err := os.Chtimes(filepath.Join(agent, name+".lock"), old, old); err != nil {
			t.Fatal(err)
		}
	}
	// Force the production trust path even in a fresh project; an ordinary
	// print turn with no local resources need not consult the trust store.
	if err := os.MkdirAll(filepath.Join(cwd, ".agents", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	trust := fmt.Sprintf("{%q:false}", filepath.ToSlash(cwd))
	if err := os.WriteFile(filepath.Join(agent, "trust.json"), []byte(trust), 0o600); err != nil {
		t.Fatal(err)
	}
	run(binary)
	for _, name := range []string{"settings.json", "trust.json", "auth.json"} {
		if _, err := os.Lstat(filepath.Join(agent, name+".lock")); !os.IsNotExist(err) {
			t.Errorf("%s lock remains: %v", name, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(agent, "settings.json"))
	if err != nil || !strings.Contains(string(data), `"defaultModel":"faux-1"`) {
		t.Fatalf("settings were lost: %s %v", data, err)
	}
}
