//go:build live

package coding

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// These are the credential-backed inputs from Pi 0.87.1 agent-session-branching.test.ts:90,110,131. The retained-system-message ruling is shared with TestRuntimeOriginalBranching; the original zero/two counts disagree with the pinned implementation.
func TestRuntimeBranchingLiveUpstream(t *testing.T) {
	for _, tc := range []struct {
		name    string
		memory  bool
		prompts []string
		pick    int
		roles   []string
		timeout time.Duration
	}{
		{"should allow forking from single message", false, []string{"Say hello"}, 0, []string{"system"}, 30 * time.Second},
		{"should support in-memory forking in --no-session mode", true, []string{"Say hi"}, 0, []string{"system"}, 30 * time.Second},
		{"should fork from middle of conversation", false, []string{"Say one", "Say two", "Say three"}, 1, []string{"system", "user", "assistant"}, 60 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// utilities.ts:32 prefers ANTHROPIC_OAUTH_TOKEN over ANTHROPIC_API_KEY.
			name := "ANTHROPIC_API_KEY"
			if os.Getenv("ANTHROPIC_OAUTH_TOKEN") != "" {
				name = "ANTHROPIC_OAUTH_TOKEN"
			}
			t.Log("live Anthropic: set ANTHROPIC_OAUTH_TOKEN or ANTHROPIC_API_KEY")
			key := testenv.RequireLiveEnv(t, name)
			ctx, cancel := context.WithTimeout(t.Context(), tc.timeout)
			defer cancel()
			runtime := liveBranchingRuntime(t, ctx, key, tc.memory)
			if tc.memory && runtime.Session().Path() != "" {
				t.Fatal("in-memory session has a file before prompting")
			}
			for _, prompt := range tc.prompts {
				if err := runtime.Session().Prompt(ctx, prompt); err != nil {
					t.Fatal(err)
				}
			}
			previous := runtime.Session()
			users := previous.UserMessagesForForking()
			if len(users) != len(tc.prompts) || users[tc.pick].Text != tc.prompts[tc.pick] {
				t.Fatalf("forkable users=%v, prompts=%q", users, tc.prompts)
			}
			if len(previous.Messages()) == 0 {
				t.Fatal("prompted session has no messages")
			}
			result, err := runtime.Fork(ctx, users[tc.pick].EntryID, nil)
			if err != nil || result.Cancelled || result.SelectedText == nil || *result.SelectedText != tc.prompts[tc.pick] {
				t.Fatalf("fork=%+v err=%v", result, err)
			}
			if runtime.Session() == previous {
				t.Fatal("fork retained the outgoing Session")
			}
			var roles []string
			for _, message := range runtime.Session().Messages() {
				roles = append(roles, message.Role())
			}
			if !slices.Equal(roles, tc.roles) {
				t.Fatalf("roles=%q want pinned implementation %q", roles, tc.roles)
			}
			if tc.memory {
				if runtime.Session().Path() != "" {
					t.Fatal("in-memory fork acquired a file")
				}
			} else if tc.pick == 0 {
				if runtime.Session().Path() == "" {
					t.Fatal("persisted fork has no selected file path")
				}
				if _, err := os.Stat(runtime.Session().Path()); !os.IsNotExist(err) {
					t.Fatalf("assistant-free fork was persisted: %v", err)
				}
			}
		})
	}
}

func liveBranchingRuntime(t *testing.T, ctx context.Context, key string, memory bool) *Runtime {
	t.Helper()
	directory := t.TempDir()
	manager, err := NewInMemorySessionManager(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !memory {
		manager, err = icodingagent.NewSessionManagerWithDir(directory, filepath.Join(directory, "sessions")).Create(manager.ID(), "")
		if err != nil {
			t.Fatal(err)
		}
	}
	factory := func(_ context.Context, target CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: target.CWD, AgentDir: target.AgentDir})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		if err := services.Auth().Set("anthropic", ai.Credential{Type: ai.CredentialAPIKey, Key: key}); err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		// agent-session-branching.test.ts:48 selects this exact model, without a fallback alias.
		model := services.ModelRuntime().GetModel("anthropic", "claude-sonnet-4-5")
		if model == nil {
			t.Fatal("pinned Anthropic claude-sonnet-4-5 model is unavailable")
		}
		session, err := NewSession(services, SessionOptions{SessionManager: target.SessionManager, Model: model,
			InitialActiveToolNames: []string{"read", "bash", "edit", "write"}})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			for event := range session.Events() {
				AcknowledgeEvent(event)
			}
		}()
		t.Cleanup(func() { _ = session.Close(); <-done })
		return CreateAgentSessionRuntimeResult{Session: session, Services: services}, nil
	}
	runtime, err := CreateAgentSessionRuntime(ctx, factory, CreateAgentSessionRuntimeOptions{CWD: directory, AgentDir: directory, SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err := runtime.Session().BindExtensions(ctx, ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	return runtime
}
