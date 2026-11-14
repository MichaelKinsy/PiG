//go:build !windows

package ai

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// credentialFixtureProviders are the entries every test below seeds: OAuth and API-key credentials of the providers a user is signed in to.
var credentialFixtureProviders = []string{"anthropic", "github-copilot", "google-gemini-cli", "openai", "openai-codex", "openrouter"}

func seedCredentialFixture(t *testing.T, path string) {
	t.Helper()
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	soon := time.Now().Add(time.Hour).UnixMilli()
	for _, id := range []string{"anthropic", "github-copilot", "google-gemini-cli"} {
		if err := store.Set(id, Credential{Type: CredentialOAuth, Access: "access-" + id, Refresh: "refresh-" + id, Expires: soon}); err != nil {
			t.Fatal(err)
		}
	}
	// An expired credential: the one a model switch refreshes.
	if err := store.Set("openai-codex", Credential{Type: CredentialOAuth, Access: "access-old", Refresh: "refresh-old", Expires: time.Now().Add(-time.Second).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"openai", "openrouter"} {
		if err := store.Set(id, Credential{Type: CredentialAPIKey, Key: "sk-" + id}); err != nil {
			t.Fatal(err)
		}
	}
}

func credentialFixtureEntries(t *testing.T, path string) []string {
	t.Helper()
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	infos, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("auth.json no longer reads: %v", err)
	}
	var ids []string
	for _, info := range infos {
		ids = append(ids, info.ProviderID)
	}
	slices.Sort(ids)
	return ids
}

// credentialChild re-executes this test binary as another process on the same agent directory, with its own scratch HOME and Pi/PiG variables.
func credentialChild(t *testing.T, scratch, path, role string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	agentDir := filepath.Dir(path)
	cmd.Env = []string{"AUTH_TEST_ROLE=" + role, "AUTH_TEST_PATH=" + path, "HOME=" + scratch, "PIG_HOME=" + scratch, "PI_HOME=" + scratch,
		"PIG_CODING_AGENT_DIR=" + agentDir, "PI_CODING_AGENT_DIR=" + agentDir, "PATH=" + os.Getenv("PATH")}
	return cmd
}

func refreshGitHubCopilot(store *AuthStorage) error {
	_, err := store.Modify(context.Background(), "github-copilot", func(current *Credential) (*Credential, error) {
		next := *current
		next.Access = "access-refreshed"
		return &next, nil
	})
	return err
}

// A credential write that fails once it has begun (a full disk, a size limit) must leave auth.json as it was. The in-place write truncated the file first: the next process then read {}, found no credential of any provider, and a login of one provider left the file with that provider alone.
func TestCredentialWriteThatFailsLeavesEveryProvider(t *testing.T) {
	if role := os.Getenv("AUTH_TEST_ROLE"); role != "" {
		store, err := NewAuthStorage(os.Getenv("AUTH_TEST_PATH"))
		if err != nil {
			t.Fatal(err)
		}
		if role == "limited-write" {
			signal.Ignore(syscall.SIGXFSZ)
			if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 64, Max: 64}); err != nil {
				t.Fatal(err)
			}
			if err := refreshGitHubCopilot(store); err == nil {
				t.Fatal("a write past the file size limit succeeded")
			}
			return
		}
		if err := refreshGitHubCopilot(store); err != nil {
			t.Fatal(err)
		}
		return
	}
	scratch := t.TempDir()
	path := filepath.Join(scratch, "agent", "auth.json")
	seedCredentialFixture(t, path)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Process 1: a session whose credential write fails after it began.
	if out, err := credentialChild(t, scratch, path, "limited-write").CombinedOutput(); err != nil {
		t.Fatalf("limited-write process: %v\n%s", err, out)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
		t.Fatalf("auth.json after the failed write is %d bytes %q, want the %d bytes it had", len(after), after, len(before))
	}
	// Process 2: another session refreshes github-copilot (its token lasts about 25 minutes, so every session does).
	if out, err := credentialChild(t, scratch, path, "refresh").CombinedOutput(); err != nil {
		t.Fatalf("refresh process: %v\n%s", err, out)
	}
	if got := credentialFixtureEntries(t, path); !slices.Equal(got, credentialFixtureProviders) {
		t.Fatalf("providers after the failed write and a refresh = %v, want %v", got, credentialFixtureProviders)
	}
	// No temporary file is left beside it.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") {
			t.Errorf("a temporary file is left in the agent directory: %s", entry.Name())
		}
	}
	_ = errors.ErrUnsupported
}
