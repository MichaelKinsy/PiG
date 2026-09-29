package coding

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestRuntimeReplacementAwaitsRebindAndReturnsItsError(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	previous := h.runtime.Session()
	entered, release := make(chan struct{}), make(chan struct{})
	failure := errors.New("rebind failed")
	h.runtime.SetRebindSession(func(_ context.Context, session *Session) error {
		if session == previous || session != h.runtime.Session() {
			return errors.New("rebind received the wrong Session")
		}
		close(entered)
		<-release
		return failure
	})
	done := make(chan error, 1)
	go func() { _, err := h.runtime.NewSession(t.Context(), nil); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		close(release)
		t.Fatalf("replacement did not enter rebind: %v", err)
	}
	select {
	case err := <-done:
		close(release)
		t.Fatalf("replacement returned before rebind completed: %v", err)
	default:
	}
	close(release)
	if err := <-done; !errors.Is(err, failure) {
		t.Fatalf("replacement error=%v", err)
	}
	if h.runtime.Session() == previous {
		t.Fatal("failed rebind rolled back the replacement")
	}
}

func TestRuntimeFactoryFailureFollowsOutgoingDisposal(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	previous := h.runtime.Session()
	failure := errors.New("factory failed")
	disposedBeforeFactory := false
	h.runtime.replacement.createRuntime = func(context.Context, CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		select {
		case <-previous.closeDone:
			disposedBeforeFactory = previous.currentRunner().IsStale()
		default:
		}
		return CreateAgentSessionRuntimeResult{}, failure
	}
	_, err := h.runtime.NewSession(t.Context(), nil)
	if !errors.Is(err, failure) || !disposedBeforeFactory {
		t.Fatalf("error=%v disposedBeforeFactory=%v", err, disposedBeforeFactory)
	}
	if h.runtime.Session() != previous {
		t.Fatal("failed construction installed a fabricated replacement")
	}
}

func TestRuntimeImportDirectoryDoesNotLeaveDestination(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	previous := h.runtime.Session()
	source := t.TempDir()
	destination := filepath.Join(previous.SessionManager().GetSessionDir(), filepath.Base(source))
	if _, err := h.runtime.ImportFromJsonl(t.Context(), source); err == nil {
		t.Fatal("directory import succeeded")
	}
	if h.runtime.Session() != previous {
		t.Fatal("failed import replaced the Session")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed copy retained destination: %v", err)
	}
}

func TestRuntimeImportFileURLAndMissingInput(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	source := filepath.Join(t.TempDir(), "import with spaces.jsonl")
	data, err := json.Marshal(icodingagent.SessionHeader{Type: "session", Version: 3, ID: "imported", CWD: h.runtime.CWD()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	uriPath := filepath.ToSlash(source)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	input := (&url.URL{Scheme: "file", Path: uriPath}).String()
	if result, err := h.runtime.ImportFromJsonl(t.Context(), input); err != nil || result.Cancelled {
		t.Fatalf("import=%v error=%v", result, err)
	}
	if h.runtime.Session().ID() != "imported" {
		t.Fatalf("id=%s", h.runtime.Session().ID())
	}
	before, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(h.runtime.Session().Path())
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode().Perm() != before.Mode().Perm() {
		t.Fatalf("import permissions=%v source=%v", after.Mode().Perm(), before.Mode().Perm())
	}
	current := h.runtime.Session()
	missing := filepath.Join(t.TempDir(), "missing.jsonl")
	_, err = h.runtime.ImportFromJsonl(t.Context(), missing)
	var notFound *SessionImportFileNotFoundError
	if !errors.As(err, &notFound) || notFound.FilePath != missing || err.Error() != "File not found: "+missing {
		t.Fatalf("missing input error=%v", err)
	}
	if h.runtime.Session() != current {
		t.Fatal("missing import replaced the Session")
	}
}

// Pi session-cwd.test.ts:67 rejects before invoking the supplied runtime factory.
func TestRuntimeMissingCWDPrecedesFactory(t *testing.T) {
	fallback := t.TempDir()
	missing := filepath.Join(fallback, "does-not-exist")
	manager, err := icodingagent.NewSessionManagerWithDir(missing, t.TempDir()).Create("session-id", "")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = CreateAgentSessionRuntime(t.Context(), func(context.Context, CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		called = true
		return CreateAgentSessionRuntimeResult{}, errors.New("should not be called")
	}, CreateAgentSessionRuntimeOptions{CWD: fallback, AgentDir: fallback, SessionManager: manager})
	var issue *icodingagent.MissingSessionCwdError
	if !errors.As(err, &issue) || called {
		t.Fatalf("error=%v factoryCalled=%v", err, called)
	}
	if issue.Issue.SessionFile != manager.Path() || issue.Issue.SessionCwd != missing || issue.Issue.FallbackCwd != fallback {
		t.Fatalf("issue=%+v", issue.Issue)
	}
}
