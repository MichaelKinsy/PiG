package coding

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/core/sdk.ts — the supplied SessionManager is the actual log, not a copied projection.
func TestSDKManagerOverrideUsesOneLiveLog(t *testing.T) {
	services, model, cwd, _ := sessionManagerFixture(t)
	manager, err := NewInMemorySessionManager(cwd)
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{Model: model, SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	if session.Inner() != manager {
		t.Fatal("Session persisted through a different manager")
	}
	if err := session.SetSessionName("shared"); err != nil {
		t.Fatal(err)
	}
	if manager.GetSessionName() != "shared" {
		t.Fatal("Session append missing from supplied manager")
	}
	if _, err := manager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "retained"}}, Timestamp: 0}}); err != nil {
		t.Fatal(err)
	}
	session.RefreshContext()
	if !slices.ContainsFunc(session.Messages(), func(message agent.AgentMessage) bool {
		return message.User != nil && extractUserMessageText(message.User.Content) == "retained"
	}) {
		t.Fatal("supplied manager append missing from Session projection")
	}
	if manager.IsPersisted() || manager.GetSessionFile() != nil || manager.GetSessionDir() != "" {
		t.Fatal("in-memory manager acquired a persistence path")
	}
}

// upstream: packages/coding-agent/src/core/sdk.ts — explicit thinking wins over restored and configured preferences.
func TestSDKExplicitThinkingLevelPrecedesConfiguredDefaults(t *testing.T) {
	services, model, _, _ := sessionManagerFixture(t)
	if err := services.SettingsManager().SetDefaultThinkingLevel("low"); err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{Model: model, ThinkingLevel: ai.ThinkingHigh})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	if session.ThinkingLevel() != ai.ThinkingHigh {
		t.Fatalf("thinking=%s want=high", session.ThinkingLevel())
	}
	var levels []string
	for _, entry := range session.SessionManager().Entries() {
		if entry.Base.Type != "thinking_level_change" {
			continue
		}
		var value struct {
			ThinkingLevel string `json:"thinkingLevel"`
		}
		if err := json.Unmarshal(entry.Raw(), &value); err != nil {
			t.Fatal(err)
		}
		levels = append(levels, value.ThinkingLevel)
	}
	if !slices.Equal(levels, []string{"high"}) {
		t.Fatalf("persisted thinking=%v want=[high]", levels)
	}
}

// upstream: packages/coding-agent/src/core/sdk.ts — skip saved-model lookup when a model is supplied, and fill missing initial thinking metadata.
func TestSDKInjectedHistoryKeepsExplicitModelAndFillsThinkingMetadata(t *testing.T) {
	services, model, cwd, _ := sessionManagerFixture(t)
	marker := filepath.Join(t.TempDir(), "unused-credential-command")
	if err := services.Registry().RegisterProvider("sdk-unused", extension.ProviderConfig{BaseURL: "http://localhost:0", API: "openai-completions", APIKey: fmt.Sprintf("!printf seen > '%s'; printf unused-key", marker), Models: []extension.ProviderModelConfig{{ID: "old", Name: "Old", Input: []string{"text"}, ContextWindow: 128000, MaxTokens: 4096}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("fixture resolved credentials before construction: %v", err)
	}
	manager, err := NewInMemorySessionManager(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AppendModelSwitch("sdk-unused", "old", "Old"); err != nil {
		t.Fatal(err)
	}
	for _, message := range []agent.AgentMessage{
		{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "old"}}, Timestamp: 0}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonError, ErrorMessage: "old error", Timestamp: 0}},
	} {
		if _, err := manager.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	session, err := NewSession(services, SessionOptions{Model: model, ThinkingLevel: ai.ThinkingHigh, SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	if session.Model() != model {
		t.Fatal("saved model displaced the explicit model")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("unused saved model resolved its credential command: %v", err)
	}
	var levels []string
	for _, entry := range manager.Entries() {
		if entry.Base.Type != "thinking_level_change" {
			continue
		}
		var value struct {
			ThinkingLevel string `json:"thinkingLevel"`
		}
		if err := json.Unmarshal(entry.Raw(), &value); err != nil {
			t.Fatal(err)
		}
		levels = append(levels, value.ThinkingLevel)
	}
	if !slices.Equal(levels, []string{"high"}) {
		t.Errorf("missing initial thinking metadata: %v", levels)
	}
	messages := session.Messages()
	if len(messages) != 2 || messages[1].Assistant == nil || messages[1].Assistant.StopReason != ai.StopReasonError {
		t.Fatalf("poisoned history changed at construction: %+v", messages)
	}
	_, markerErr := os.Stat(marker)
	roles := make([]string, len(messages))
	for i, message := range messages {
		roles[i] = message.Role()
	}
	printSessionManagerProbe(t, 5, []any{os.IsNotExist(markerErr), levels, session.Model() == model, roles})
}

// upstream: packages/coding-agent/src/core/session-manager.ts — an explicit storage directory remains independent of the opened file's parent.
func TestSDKManagerReportsConfiguredDirectory(t *testing.T) {
	services, model, _, _ := sessionManagerFixture(t)
	source, err := NewSession(services, SessionOptions{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "saved"}}, StopReason: ai.StopReasonStop}}); err != nil {
		t.Fatal(err)
	}
	path := source.Path()
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "selected-sessions")
	resumed, err := NewSession(services, SessionOptions{Model: model, ResumePath: path, SessionDir: directory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := resumed.Close(); err != nil {
			t.Error(err)
		}
	})
	if resumed.SessionManager().GetSessionDir() != directory {
		t.Fatalf("manager directory=%q want=%q", resumed.SessionManager().GetSessionDir(), directory)
	}
	if resumed.SessionManager().GetSessionFile() == nil || *resumed.SessionManager().GetSessionFile() != path {
		t.Fatalf("opening with a directory override changed file: %v", resumed.SessionManager().GetSessionFile())
	}
	printSessionManagerProbe(t, 6, []any{resumed.SessionManager().GetSessionDir() == directory, *resumed.SessionManager().GetSessionFile() == path})
}

func TestRuntimeKeepsSuppliedSessionManager(t *testing.T) {
	services := newTestServices(t)
	manager, err := NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(RuntimeOptions{Services: services})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	session, err := runtime.New(SessionStartOptions{SessionManager: manager, Model: fakeModel(), ThinkingLevel: ai.ThinkingOff})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	if session.SessionManager() != manager || manager.IsPersisted() || manager.GetSessionFile() != nil {
		t.Fatalf("Runtime.New did not retain the supplied memory log: same=%v file=%v", session.SessionManager() == manager, manager.GetSessionFile())
	}
}

func TestSessionManagerCWDDefaultAndExplicitOverride(t *testing.T) {
	_, model, cwd, agentDir := sessionManagerFixture(t)
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			manager, err := NewInMemorySessionManager(cwd)
			if err != nil {
				t.Fatal(err)
			}
			options := ServicesOptions{AgentDir: agentDir, SessionManager: manager}
			want := cwd
			if explicit {
				want = t.TempDir()
				options.CWD = want
			}
			session := createSessionWithServicesOptions(t, options, SessionOptions{Model: model, SessionManager: manager})
			// upstream system-prompt.ts:170 renders the cwd with forward slashes.
			if session.Services().CWD() != want || session.SessionManager() != manager || !strings.Contains(session.SystemPrompt(), "<cwd>\n"+strings.ReplaceAll(want, `\`, "/")+"\n</cwd>") {
				t.Fatalf("CWD/log mismatch: cwd=%q want=%q sameManager=%v", session.Services().CWD(), want, session.SessionManager() == manager)
			}
		})
	}
}
