//go:build !windows

package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A models-store write that fails once it has begun (a full disk, a size limit, a kill) must leave the store as it was: the catalog every provider cached is in that one file. The in-place write truncated
// the file and wrote part of the new content, so the next start read a truncated catalog and refused it. The child process limits every write to 64 bytes, as a full disk does.
func TestModelsStoreWriteThatFailsKeepsTheStore(t *testing.T) {
	if os.Getenv("MODELS_STORE_TEST_ROLE") == "limited-write" {
		signal.Ignore(syscall.SIGXFSZ)
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 64, Max: 64}); err != nil {
			t.Fatal(err)
		}
		checkedAt := 1700000000000.0
		err := NewFileModelsStore(os.Getenv("MODELS_STORE_TEST_PATH")).Write(context.Background(), "llama.cpp", ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{json.RawMessage(`{"id":"m","name":"a model with a name long enough to pass the limit"}`)}), CheckedAt: &checkedAt})
		if err == nil {
			t.Fatal("a models-store write past the file size limit succeeded")
		}
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "models-store.json")
	before := []byte(`{"zeta":{"models":[{"id":"cached-model-of-another-provider"}],"etag":"\"a-validator-of-some-length-to-take-the-file-past-the-limit\""}}`)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	cmd.Env = []string{"MODELS_STORE_TEST_ROLE=limited-write", "MODELS_STORE_TEST_PATH=" + path, "HOME=" + dir, "PATH=" + os.Getenv("PATH")}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("the store after the failed write = %q (%v); want it as it was: %q", after, err, before)
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") {
			t.Errorf("a temporary file is left beside the store: %s", entry.Name())
		}
	}
}
