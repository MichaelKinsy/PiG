package coding

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type runtimeTestOptions struct {
	cwd              string
	agentDir         string
	memory           bool
	noBootstrapModel bool
	extension        func() extension.Extension
	tools            []agent.AgentTool
	// recordFactoryOptions observes each replacement's factory options.
	recordFactoryOptions func(CreateAgentSessionRuntimeOptions)
}

type runtimeTestHarness struct {
	runtime  *Runtime
	provider *scriptedProvider
	models   []*ai.Model
}

func newRuntimeTestHarness(t *testing.T, options runtimeTestOptions) *runtimeTestHarness {
	t.Helper()
	if options.cwd == "" {
		options.cwd = t.TempDir()
	}
	if options.agentDir == "" {
		options.agentDir = t.TempDir()
	}
	provider := &scriptedProvider{responses: []scriptedResponse{fauxReply("one", ai.StopReasonStop, 0), fauxReply("two", ai.StopReasonStop, 0), fauxReply("three", ai.StopReasonStop, 0)}}
	models := []*ai.Model{
		{ID: "faux-1", DisplayName: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{ProviderID: "faux"}, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000, MaxThinking: ai.ThinkingLevelHigh}},
		{ID: "faux-2", DisplayName: "faux-2", Provider: provider, ProviderMeta: ai.ProviderMetadata{ProviderID: "faux"}, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000}},
	}
	factory := func(_ context.Context, target CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		if options.recordFactoryOptions != nil {
			options.recordFactoryOptions(target)
		}
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: target.CWD, AgentDir: target.AgentDir})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		t.Cleanup(services.Close)
		if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		services.Registry().RegisterExtensionProvider("faux", extension.ProviderConfig{API: "openai-completions", APIKey: "faux-key", BaseURL: "https://faux.invalid", Models: []extension.ProviderModelConfig{
			{ID: "faux-1", Name: "faux-1", Reasoning: true, ContextWindow: 128_000, MaxTokens: 4096, Input: []string{"text"}},
			{ID: "faux-2", Name: "faux-2", Reasoning: false, ContextWindow: 128_000, MaxTokens: 4096, Input: []string{"text"}},
		}})
		// The provider boundary is scripted; Session construction, model restoration and persistence remain production paths.
		services.modelRuntime.prepare = func(_ context.Context, model *ai.Model, opts ai.StreamOptions) (*ai.Model, ai.Provider, ai.StreamOptions, error) {
			return model, provider, opts, nil
		}
		var extensions []extension.Extension
		if options.extension != nil {
			extensions = append(extensions, options.extension())
		}
		runner := inproc.NewRunner(extensions, target.CWD)
		var model *ai.Model
		if !options.noBootstrapModel {
			model = models[0]
		}
		session, err := NewSession(services, SessionOptions{SessionManager: target.SessionManager, Model: model, Runner: runner, Tools: options.tools,
			InitialActiveToolNames: []string{"read", "bash", "edit", "write"}})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		done := make(chan struct{})
		eventsCh := session.Events()
		go func() {
			defer close(done)
			for event := range eventsCh {
				AcknowledgeEvent(event)
			}
		}()
		t.Cleanup(func() { _ = session.Close(); <-done })
		return CreateAgentSessionRuntimeResult{Session: session, Services: services}, nil
	}
	manager, err := NewInMemorySessionManager(options.cwd)
	if err != nil {
		t.Fatal(err)
	}
	if !options.memory {
		manager, err = icodingagent.NewSessionManagerWithDir(options.cwd, filepath.Join(options.agentDir, "sessions")).Create(manager.ID(), "")
		if err != nil {
			t.Fatal(err)
		}
	}
	runtime, err := CreateAgentSessionRuntime(t.Context(), factory, CreateAgentSessionRuntimeOptions{CWD: options.cwd, AgentDir: options.agentDir, SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err := runtime.Session().BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	return &runtimeTestHarness{runtime: runtime, provider: provider, models: models}
}

func runtimePrompt(t *testing.T, runtime *Runtime, text string) {
	t.Helper()
	if err := runtime.Session().Prompt(t.Context(), text); err != nil {
		t.Fatal(err)
	}
}

func runtimeMessageView(messages []agent.AgentMessage) []map[string]string {
	result := make([]map[string]string, 0, len(messages))
	for _, message := range messages {
		row := map[string]string{"role": message.Role()}
		if message.User != nil {
			row["text"] = extractUserMessageText(message.User.Content)
		}
		result = append(result, row)
	}
	return result
}

func runtimeEventRecorder(events *[]any, cancelNew *string, cancelFork *bool) extension.Extension {
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{}}
	for _, name := range []string{"session_before_switch", "session_before_fork", "session_shutdown", "session_start"} {
		ext.Handlers[name] = []extension.HandlerFn{func(args ...any) (any, error) {
			*events = append(*events, args[0])
			if event, ok := args[0].(extension.SessionBeforeSwitchEvent); ok && cancelNew != nil && event.Reason == *cancelNew {
				return extension.SessionBeforeSwitchResult{Cancel: true}, nil
			}
			if _, ok := args[0].(extension.SessionBeforeForkEvent); ok && cancelFork != nil && *cancelFork {
				*cancelFork = false
				return extension.SessionBeforeForkResult{Cancel: true}, nil
			}
			return nil, nil
		}}
	}
	return ext
}

// packages/coding-agent/test/suite/agent-session-runtime.test.ts:218 (Pi #11060): "leaves no copy behind when an import fails on a missing
// session cwd".
func TestRuntimeOriginalImportMissingCwdLeavesNoCopy(t *testing.T) {
	recordRuntimeOriginal(t, 218)
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	dir := h.runtime.Session().inner.Load().GetSessionDir()
	importDir := filepath.Join(t.TempDir(), "import")
	if err := os.MkdirAll(importDir, 0o755); err != nil {
		t.Fatal(err)
	}
	importPath := filepath.Join(importDir, "orphan.jsonl")
	data, err := json.Marshal(icodingagent.SessionHeader{Type: "session", Version: 3, ID: "orphan", Timestamp: icodingagent.RFC3339NowNano(), CWD: filepath.Join(h.runtime.CWD(), "renamed-away")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(importPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = h.runtime.ImportFromJsonl(t.Context(), importPath)
	if _, ok := errors.AsType[*icodingagent.MissingSessionCwdError](err); !ok {
		t.Fatalf("import error = %v, want MissingSessionCwdError", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "orphan.jsonl")); !os.IsNotExist(statErr) {
		t.Fatalf("the failed import left its copy behind: %v", statErr)
	}
	if _, err := h.runtime.ImportFromJsonl(t.Context(), importPath, h.runtime.CWD()); err != nil {
		t.Fatal(err)
	}
	if got, want := h.runtime.Session().Path(), filepath.Join(dir, "orphan.jsonl"); got != want {
		t.Fatalf("session path = %q, want %q", got, want)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "orphan-1.jsonl")); !os.IsNotExist(statErr) {
		t.Fatalf("a second copy with the same id exists: %v", statErr)
	}
}

// packages/coding-agent/test/suite/agent-session-runtime.test.ts:242.
func TestRuntimeOriginalImportCollision(t *testing.T) {
	recordRuntimeOriginal(t, 242)
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	dir := h.runtime.Session().inner.Load().GetSessionDir()
	importDir := filepath.Join(t.TempDir(), "import")
	if err := os.MkdirAll(importDir, 0o755); err != nil {
		t.Fatal(err)
	}
	storedPath, importPath := filepath.Join(dir, "collision.jsonl"), filepath.Join(importDir, "collision.jsonl")
	header := func(id string) []byte {
		data, err := json.Marshal(icodingagent.SessionHeader{Type: "session", Version: 3, ID: id, Timestamp: icodingagent.RFC3339NowNano(), CWD: h.runtime.CWD()})
		if err != nil {
			t.Fatal(err)
		}
		return append(data, '\n')
	}
	stored, imported := header("stored"), header("imported")
	if err := os.WriteFile(storedPath, stored, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(importPath, imported, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runtime.ImportFromJsonl(t.Context(), importPath); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(storedPath)
	if err != nil || string(actual) != string(stored) {
		t.Fatalf("stored=%q error=%v", actual, err)
	}
	if h.runtime.Session().Path() == storedPath {
		t.Fatal("import overwrote existing path")
	}
	actual, err = os.ReadFile(h.runtime.Session().Path())
	if err != nil || !strings.Contains(string(actual), `"id":"imported"`) {
		t.Fatalf("imported=%q error=%v", actual, err)
	}
}

// packages/coding-agent/test/suite/agent-session-runtime.test.ts:275.
func TestRuntimeOriginalNewResumeEvents(t *testing.T) {
	recordRuntimeOriginal(t, 275)
	var events []any
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension { return runtimeEventRecorder(&events, nil, nil) }})
	if !reflect.DeepEqual(events, []any{extension.SessionStartEvent{Type: "session_start", Reason: "startup"}}) {
		t.Fatalf("startup=%#v", events)
	}
	events = nil
	runtimePrompt(t, h.runtime, "hello")
	original := h.runtime.Session()
	first := original.Path()
	result, err := h.runtime.NewSession(t.Context(), nil)
	if err != nil || result.Cancelled {
		t.Fatalf("new=%+v error=%v", result, err)
	}
	if err := h.runtime.Session().BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	if h.runtime.Session() == original || len(h.runtime.Session().Messages()) != 0 {
		t.Fatal("new did not replace the Session with an empty one")
	}
	second := h.runtime.Session().Path()
	want := []any{extension.SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: "new"}, extension.SessionShutdownEvent{Type: "session_shutdown", Reason: "new", TargetSessionFile: second}, extension.SessionStartEvent{Type: "session_start", Reason: "new", PreviousSessionFile: first}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("new events=%#v want=%#v", events, want)
	}
	events = nil
	result, err = h.runtime.SwitchSession(t.Context(), first)
	if err != nil || result.Cancelled {
		t.Fatalf("resume=%+v error=%v", result, err)
	}
	if err := h.runtime.Session().BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	want = []any{extension.SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: "resume", TargetSessionFile: first}, extension.SessionShutdownEvent{Type: "session_shutdown", Reason: "resume", TargetSessionFile: first}, extension.SessionStartEvent{Type: "session_start", Reason: "resume", PreviousSessionFile: second}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("resume events=%#v want=%#v", events, want)
	}
}

// packages/coding-agent/test/suite/agent-session-runtime.test.ts:320.
func TestRuntimeOriginalSwitchCancellation(t *testing.T) {
	recordRuntimeOriginal(t, 320)
	var events []any
	var cancel string
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension { return runtimeEventRecorder(&events, &cancel, nil) }})
	runtimePrompt(t, h.runtime, "hello")
	original := h.runtime.Session()
	cancel = "new"
	result, err := h.runtime.NewSession(t.Context(), nil)
	if err != nil || !result.Cancelled || h.runtime.Session() != original {
		t.Fatalf("new=%v err=%v", result, err)
	}
	otherDir := t.TempDir()
	other, err := icodingagent.NewSessionManagerWithDir(otherDir, t.TempDir()).Create("other", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "other"}}}}); err != nil {
		t.Fatal(err)
	}
	cancel = "resume"
	result, err = h.runtime.SwitchSession(t.Context(), other.Path())
	if err != nil || !result.Cancelled || h.runtime.Session() != original {
		t.Fatalf("resume=%v err=%v", result, err)
	}
}

// packages/coding-agent/test/suite/agent-session-runtime.test.ts:355.
func TestRuntimeOriginalForkEventsAndCancellation(t *testing.T) {
	recordRuntimeOriginal(t, 355)
	var events []any
	cancel := false
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension { return runtimeEventRecorder(&events, nil, &cancel) }})
	events = nil
	runtimePrompt(t, h.runtime, "hello")
	user := h.runtime.Session().UserMessagesForForking()[0]
	previous := h.runtime.Session().Path()
	result, err := h.runtime.Fork(t.Context(), user.EntryID, nil)
	if err != nil || result.Cancelled || result.SelectedText == nil || *result.SelectedText != "hello" {
		t.Fatalf("fork=%+v error=%v", result, err)
	}
	if err := h.runtime.Session().BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	want := []any{extension.SessionBeforeForkEvent{Type: "session_before_fork", EntryID: user.EntryID, Position: "before"}, extension.SessionShutdownEvent{Type: "session_shutdown", Reason: "fork", TargetSessionFile: h.runtime.Session().Path()}, extension.SessionStartEvent{Type: "session_start", Reason: "fork", PreviousSessionFile: previous}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("fork events=%#v want=%#v", events, want)
	}
	if !strings.HasSuffix(strings.TrimSuffix(filepath.Base(h.runtime.Session().Path()), ".jsonl"), "_"+h.runtime.Session().ID()) {
		t.Fatal("fork filename does not match Session ID")
	}
	for _, selection := range []struct{ id, position string }{{user.EntryID, "before"}, {"missing-entry", "at"}} {
		events = nil
		cancel = true
		result, err := h.runtime.Fork(t.Context(), selection.id, &extension.ForkOptions{Position: selection.position})
		if err != nil || result != (RuntimeForkResult{Cancelled: true}) {
			t.Fatalf("cancel=%+v error=%v", result, err)
		}
		if !reflect.DeepEqual(events, []any{extension.SessionBeforeForkEvent{Type: "session_before_fork", EntryID: selection.id, Position: selection.position}}) {
			t.Fatalf("cancel events=%#v", events)
		}
	}
}

// packages/coding-agent/test/suite/agent-session-runtime.test.ts:404.
func TestRuntimeOriginalUnflushedForkDiagnostic(t *testing.T) {
	recordRuntimeOriginal(t, 404)
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	s := h.runtime.Session()
	if s.Path() == "" || s.LeafID() == nil {
		t.Fatal("missing unflushed path/leaf")
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Fatalf("fresh file exists: %v", err)
	}
	_, err := h.runtime.Fork(t.Context(), *s.LeafID(), &extension.ForkOptions{Position: "at"})
	if err == nil || err.Error() != "This session has not been saved yet. Send a message before cloning or forking it." {
		t.Fatalf("fork error=%v", err)
	}
}

// packages/coding-agent/test/suite/agent-session-runtime.test.ts:417,431.
func TestRuntimeOriginalDuplicateCurrentBranch(t *testing.T) {
	for _, memory := range []bool{false, true} {
		t.Run(map[bool]string{false: "disk", true: "memory"}[memory], func(t *testing.T) {
			site := 417
			if memory {
				site = 457
			}
			recordRuntimeOriginal(t, site)
			h := newRuntimeTestHarness(t, runtimeTestOptions{memory: memory})
			runtimePrompt(t, h.runtime, "hello")
			runtimePrompt(t, h.runtime, "again")
			previous := h.runtime.Session()
			before := runtimeMessageView(previous.Messages())
			result, err := h.runtime.Fork(t.Context(), *previous.LeafID(), &extension.ForkOptions{Position: "at"})
			if err != nil || result != (RuntimeForkResult{}) {
				t.Fatalf("duplicate=%+v error=%v", result, err)
			}
			if previous == h.runtime.Session() {
				t.Fatal("fork kept the outgoing Session")
			}
			// Pi runtime.ts keeps the in-memory manager object while replacing its owning Session.
			if memory && previous.inner.Load() != h.runtime.Session().inner.Load() {
				t.Fatal("memory fork replaced the manager object")
			}
			if memory && h.runtime.Session().Path() != "" {
				t.Fatal("memory fork became persisted")
			}
			if !memory && previous.Path() == h.runtime.Session().Path() {
				t.Fatal("disk fork retained source path")
			}
			if after := runtimeMessageView(h.runtime.Session().Messages()); !reflect.DeepEqual(before, after) {
				t.Fatalf("after=%v before=%v", after, before)
			}
		})
	}
}

// packages/coding-agent/test/suite/agent-session-runtime.test.ts:569.
func TestRuntimeOriginalInvalidForkEntry(t *testing.T) {
	recordRuntimeOriginal(t, 569)
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	_, err := h.runtime.Fork(t.Context(), "missing-entry", nil)
	if err == nil || err.Error() != "Invalid entry ID for forking" {
		t.Fatalf("fork error=%v", err)
	}
}

// packages/coding-agent/test/suite/agent-session-runtime.test.ts:574.
func TestRuntimeOriginalCrossCWD(t *testing.T) {
	recordRuntimeOriginal(t, 574)
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	other := newRuntimeTestHarness(t, runtimeTestOptions{agentDir: h.runtime.Services().AgentDir()})
	runtimePrompt(t, other.runtime, "other")
	if result, err := h.runtime.SwitchSession(t.Context(), other.runtime.Session().Path()); err != nil || result.Cancelled {
		t.Fatalf("switch=%v error=%v", result, err)
	}
	if h.runtime.CWD() != other.runtime.CWD() || h.runtime.Session().SessionManager().GetCwd() != other.runtime.CWD() {
		t.Fatalf("runtime CWD=%q manager CWD=%q want=%q", h.runtime.CWD(), h.runtime.Session().CWD(), other.runtime.CWD())
	}
}

// packages/coding-agent/test/suite/agent-session-runtime.test.ts:646.
func TestRuntimeOriginalRestoreModelAndThinking(t *testing.T) {
	recordRuntimeOriginal(t, 646)
	h := newRuntimeTestHarness(t, runtimeTestOptions{noBootstrapModel: true})
	other := newRuntimeTestHarness(t, runtimeTestOptions{noBootstrapModel: true, agentDir: h.runtime.Services().AgentDir()})
	if err := other.runtime.Session().SetModel(other.models[1]); err != nil {
		t.Fatal(err)
	}
	if err := other.runtime.Session().SetThinkingLevel(ai.ThinkingOff); err != nil {
		t.Fatal(err)
	}
	// The upstream faux provider tags its reply with the requested model. The generic scripted reply defaults to faux-1, which would poison the saved assistant metadata after selecting faux-2.
	reply := other.provider.responses[0]
	other.provider.responses[0] = func(messages []ai.Message) *ai.AssistantMessage {
		message := reply(messages)
		message.Model = other.runtime.Session().Model().ID
		return message
	}
	runtimePrompt(t, other.runtime, "hello")
	if result, err := h.runtime.SwitchSession(t.Context(), other.runtime.Session().Path()); err != nil || result.Cancelled {
		t.Fatalf("switch=%v error=%v", result, err)
	}
	if model := h.runtime.Session().Model(); model == nil || model.ID != "faux-2" {
		t.Fatalf("model=%+v", model)
	}
	if h.runtime.Session().ThinkingLevel() != ai.ThinkingOff {
		t.Fatalf("thinking=%q", h.runtime.Session().ThinkingLevel())
	}
}
