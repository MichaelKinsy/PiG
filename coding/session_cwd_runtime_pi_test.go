package coding

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pi: packages/coding-agent/src/core/session-cwd.ts

// Ports test/session-cwd.test.ts "throws a controlled error before runtime creation when the stored cwd is missing": a session file whose stored cwd is
// gone, opened without an override, makes createAgentSessionRuntime reject with MissingSessionCwdError before it calls the factory.
func TestCreateAgentSessionRuntimeThrowsAControlledErrorWhenTheStoredCwdIsMissing(t *testing.T) {
	fallback, missing, file := sessionCwdFixture(t)
	manager, err := icodingagent.NewSessionManagerWithDir(fallback, filepath.Dir(file)).Open(file)
	if err != nil {
		t.Fatal(err)
	}
	createRuntimeCalled := false
	createRuntime := func(context.Context, CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		createRuntimeCalled = true
		return CreateAgentSessionRuntimeResult{}, errors.New("should not be called")
	}
	_, err = CreateAgentSessionRuntime(t.Context(), createRuntime, CreateAgentSessionRuntimeOptions{CWD: fallback, AgentDir: fallback, SessionManager: manager})
	var missingErr *icodingagent.MissingSessionCwdError
	if !errors.As(err, &missingErr) {
		t.Fatalf("error = %v, want a *MissingSessionCwdError", err)
	}
	if createRuntimeCalled {
		t.Fatal("the runtime factory was called before the stored cwd was checked")
	}
	if want := (icodingagent.SessionCwdIssue{SessionFile: file, SessionCwd: missing, FallbackCwd: fallback}); missingErr.Issue != want {
		t.Fatalf("issue = %+v, want %+v", missingErr.Issue, want)
	}
}
