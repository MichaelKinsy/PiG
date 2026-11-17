//go:build !windows

package ai

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The store keeps credentials by provider id without knowing any provider, so the fixtures use neutral ids in the two shapes a store holds: OAuth credentials (three live and one expired, the one a refresh replaces) and API keys.
const (
	fixtureExpiredOAuth = "oauth-expired"
	fixtureAPIKey       = "key-a"
)

var (
	fixtureLiveOAuth = []string{"oauth-a", "oauth-b", "oauth-c"}
	fixtureAPIKeys   = []string{fixtureAPIKey, "key-b"}
	// credentialFixtureProviders are the entries every test below seeds, sorted as the store lists them.
	credentialFixtureProviders = []string{"key-a", "key-b", "oauth-a", "oauth-b", "oauth-c", "oauth-expired"}
)

// credentialShape is one shape of credential a write replaces: an OAuth credential whose tokens a refresh rotates, or an API key that is replaced.
type credentialShape struct {
	name     string
	provider string
	rotate   func(Credential) Credential
	rotated  func(Credential) bool
}

var credentialShapes = []credentialShape{
	{
		name:     "oauth refresh",
		provider: fixtureExpiredOAuth,
		rotate: func(c Credential) Credential {
			c.Access, c.Refresh = "access-rotated", "refresh-rotated"
			return c
		},
		rotated: func(c Credential) bool { return c.Access == "access-rotated" && c.Refresh == "refresh-rotated" },
	},
	{
		name:     "api key replaced",
		provider: fixtureAPIKey,
		rotate: func(c Credential) Credential {
			c.Key = "key-rotated"
			return c
		},
		rotated: func(c Credential) bool { return c.Key == "key-rotated" },
	},
}

// credentialShapeOf is the shape whose provider a child process was told to rewrite.
func credentialShapeOf(t *testing.T, provider string) credentialShape {
	t.Helper()
	for _, shape := range credentialShapes {
		if shape.provider == provider {
			return shape
		}
	}
	t.Fatalf("no credential shape for provider %q", provider)
	return credentialShape{}
}

func seedCredentialFixture(t *testing.T, path string) {
	t.Helper()
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	soon := time.Now().Add(time.Hour).UnixMilli()
	for _, id := range fixtureLiveOAuth {
		if err := store.Set(id, Credential{Type: CredentialOAuth, Access: "access-" + id, Refresh: "refresh-" + id, Expires: soon}); err != nil {
			t.Fatal(err)
		}
	}
	// An expired credential: the one a model switch refreshes.
	if err := store.Set(fixtureExpiredOAuth, Credential{Type: CredentialOAuth, Access: "access-old", Refresh: "refresh-old", Expires: time.Now().Add(-time.Second).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	for _, id := range fixtureAPIKeys {
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

// credentialChild re-executes this test binary as another process on the same agent directory, with its own scratch HOME (testenv.ChildHomeEnv: pinned Go caches and the go command's telemetry off in it) and Pi/PiG variables. The child runs the top-level test, whose first lines act on the role and the provider it is given.
func credentialChild(t *testing.T, scratch, path, role, provider string) *exec.Cmd {
	t.Helper()
	top, _, _ := strings.Cut(t.Name(), "/")
	cmd := exec.Command(os.Args[0], "-test.run=^"+top+"$", "-test.count=1")
	agentDir := filepath.Dir(path)
	cmd.Env = append(testenv.ChildHomeEnv(t, scratch), "AUTH_TEST_ROLE="+role, "AUTH_TEST_PATH="+path, "AUTH_TEST_PROVIDER="+provider, "PIG_HOME="+scratch, "PI_HOME="+scratch,
		"PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PATH="+os.Getenv("PATH"))
	return cmd
}

// The credential tests' child is this test binary with a scratch HOME that its parent's cleanup removes. Its TestMain runs testenv.ScopeTempDir, whose KeepGoBuildCaches runs `go env` unless the caches arrive in the environment, and the first go command in a home with telemetry on forks a sidecar that keeps writing under the home after the cleanup removed it (macOS CI: TestRefreshedCredentialSurvivesALockTakenMeanwhile, "unlinkat .../Library/Application Support/go/telemetry: directory not empty"). The go command creates telemetry/local before it exits, so the home must hold nothing but the mode file once the child is done.
func TestCredentialChildLeavesNoGoTelemetryInItsHome(t *testing.T) {
	if os.Getenv("AUTH_TEST_ROLE") == "probe" {
		return
	}
	scratch := t.TempDir()
	if out, err := credentialChild(t, scratch, filepath.Join(scratch, "agent", "auth.json"), "probe", "").CombinedOutput(); err != nil {
		t.Fatalf("probe child: %v\n%s", err, out)
	}
	testenv.RequireNoGoTelemetryActivity(t, scratch)
}

// rotateCredential replaces the stored credential of provider with what its shape stores after a refresh or a replacement.
func rotateCredential(t *testing.T, store *AuthStorage, provider string) error {
	t.Helper()
	shape := credentialShapeOf(t, provider)
	_, err := store.Modify(context.Background(), provider, func(current *Credential) (*Credential, error) {
		return new(shape.rotate(*current)), nil
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
		provider := os.Getenv("AUTH_TEST_PROVIDER")
		if role == "limited-write" {
			signal.Ignore(syscall.SIGXFSZ)
			if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 64, Max: 64}); err != nil {
				t.Fatal(err)
			}
			if err := rotateCredential(t, store, provider); err == nil {
				t.Fatal("a write past the file size limit succeeded")
			}
			return
		}
		if err := rotateCredential(t, store, provider); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, shape := range credentialShapes {
		t.Run(shape.name, func(t *testing.T) {
			scratch := t.TempDir()
			path := filepath.Join(scratch, "agent", "auth.json")
			seedCredentialFixture(t, path)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			// Process 1: a session whose credential write fails after it began.
			if out, err := credentialChild(t, scratch, path, "limited-write", shape.provider).CombinedOutput(); err != nil {
				t.Fatalf("limited-write process: %v\n%s", err, out)
			}
			if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
				t.Fatalf("auth.json after the failed write is %d bytes %q, want the %d bytes it had", len(after), after, len(before))
			}
			// Process 2: another session writes the same provider again (an OAuth token lasts minutes, so every session refreshes).
			if out, err := credentialChild(t, scratch, path, "refresh", shape.provider).CombinedOutput(); err != nil {
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
		})
	}
}
