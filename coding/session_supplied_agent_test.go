package coding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// suppliedAgentFixture is a Session over an Agent the caller built, as sdk.ts builds one and hands it to `new AgentSession(config)`.
type suppliedAgentFixture struct {
	services *AgentSessionServices
	provider *scriptedProvider
	model    *ai.Model
	tool     *admissionTool
}

func newSuppliedAgentFixture(t *testing.T, responses ...scriptedResponse) *suppliedAgentFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	provider := &scriptedProvider{responses: responses}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000}}
	return &suppliedAgentFixture{services: services, provider: provider, model: model, tool: &admissionTool{name: "echo", parameter: "text", label: "Echo", description: "Echo text back"}}
}

func suppliedUserMessage(text string) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: time.Now().UnixMilli()}}
}

// upstream: packages/coding-agent/src/core/agent-session.ts:486,520-525 (`this.agent = config.agent`; _installAgentRequestProjection, _installAgentNextTurnRefresh, _installHiddenDeclarationsProjection chain the hook the Agent already had).
func TestNewAgentSessionDrivesTheSuppliedAgentAndChainsItsHooks(t *testing.T) {
	f := newSuppliedAgentFixture(t, admissionResponse("echo", "text", "one"), fauxReply("done", ai.StopReasonStop, 0))
	var mu sync.Mutex
	var streams, requests, nextTurns, transforms int
	supplied, err := agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentInitialState{Model: f.model, ThinkingLevel: "off"},
		StreamFn: func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			mu.Lock()
			streams++
			mu.Unlock()
			return model.Provider.Stream(ctx, transcript, options)
		},
		PrepareRequest: func(_ context.Context, request agent.PrepareRequestContext) (*agent.AgentRequestUpdate, error) {
			mu.Lock()
			requests++
			mu.Unlock()
			if len(request.Context.Messages) == 0 || len(request.Context.Tools) == 0 {
				t.Errorf("the supplied hook saw an empty canonical context: %d messages, %d tools", len(request.Context.Messages), len(request.Context.Tools))
			}
			context := request.Context
			context.Messages = append(append([]agent.AgentMessage(nil), context.Messages...), suppliedUserMessage("from-supplied-prepare-request"))
			return &agent.AgentRequestUpdate{Context: &context}, nil
		},
		PrepareNextTurnWithContext: func(_ context.Context, turn agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
			mu.Lock()
			nextTurns++
			mu.Unlock()
			return &agent.AgentLoopTurnUpdate{Messages: []agent.AgentMessage{suppliedUserMessage("from-supplied-next-turn")}}, nil
		},
		TransformLLMMessages: func(messages []ai.Message) []ai.Message {
			mu.Lock()
			transforms++
			mu.Unlock()
			return messages
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewAgentSession(SessionOptions{Services: f.services, Agent: supplied, NoSession: true, SkipBuiltinTools: true, Tools: []agent.AgentTool{f.tool}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range session.Events() {
			AcknowledgeEvent(event)
		}
	}()
	t.Cleanup(func() {
		_ = session.Close()
		<-done
	})
	if session.agent != supplied {
		t.Fatal("the Session built its own Agent instead of driving the supplied one")
	}
	if got := session.Model(); got != f.model {
		t.Fatalf("the Session model = %v, want the supplied Agent's", got)
	}
	if _, err := session.Send(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if streams != 2 || f.provider.callCount() != 2 {
		t.Fatalf("supplied stream function calls = %d, provider calls = %d, want 2 each: the Session replaced the caller's stream function", streams, f.provider.callCount())
	}
	if requests != 2 || nextTurns != 1 || transforms < 2 {
		t.Fatalf("supplied hooks ran prepareRequest %d, prepareNextTurn %d, transform %d times, want 2, 1, at least 2", requests, nextTurns, transforms)
	}
	if executions := f.tool.executions(); len(executions) != 1 || executions[0] != "one" {
		t.Fatalf("tool executions = %q, want the Session's tool to run once", executions)
	}
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	for i, request := range f.provider.requests {
		if !strings.Contains(request, "from-supplied-prepare-request") {
			t.Fatalf("request %d lacks the message the supplied prepareRequest added: its update was overwritten", i)
		}
	}
	if !strings.Contains(f.provider.requests[1], "from-supplied-next-turn") {
		t.Fatal("the second request lacks the message the supplied prepareNextTurn added")
	}
}

type fakeCacheWarmer struct {
	mu       sync.Mutex
	onWarmed func(context.Context, icodingagent.UsageEntry)
	settled  int
	modes    int
	canceled int
}

func (w *fakeCacheWarmer) Cancel() { w.mu.Lock(); w.canceled++; w.mu.Unlock() }
func (w *fakeCacheWarmer) Status() icodingagent.CacheWarmingStatus {
	return icodingagent.CacheWarmingStatus{State: "inactive", Reason: "supplied warmer"}
}
func (w *fakeCacheWarmer) OnAgentSettled() { w.mu.Lock(); w.settled++; w.mu.Unlock() }
func (w *fakeCacheWarmer) OnModeChanged()  { w.mu.Lock(); w.modes++; w.mu.Unlock() }
func (w *fakeCacheWarmer) SetOnWarmed(onWarmed func(context.Context, icodingagent.UsageEntry)) {
	w.mu.Lock()
	w.onWarmed = onWarmed
	w.mu.Unlock()
}

// upstream: packages/coding-agent/src/core/agent-session.ts:266,494-497,1079,1409-1411,1432,1438 (config.cacheWarmer: onWarmed emits entry_appended; cancel on dispose; onAgentSettled; onModeChanged; status).
func TestNewAgentSessionDrivesTheSuppliedCacheWarmer(t *testing.T) {
	f := newSuppliedAgentFixture(t, fauxReply("done", ai.StopReasonStop, 0))
	warmer := &fakeCacheWarmer{}
	session, err := NewAgentSession(SessionOptions{Services: f.services, Model: f.model, CacheWarmer: warmer, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var appended int
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range session.Events() {
			if _, ok := event.(agent.EntryAppendedEvent); ok {
				mu.Lock()
				appended++
				mu.Unlock()
			}
			AcknowledgeEvent(event)
		}
	}()
	closed := false
	closeSession := func() {
		if !closed {
			closed = true
			_ = session.Close()
			<-done
		}
	}
	t.Cleanup(closeSession)

	warmer.mu.Lock()
	onWarmed := warmer.onWarmed
	warmer.mu.Unlock()
	if onWarmed == nil {
		t.Fatal("the Session did not assign onWarmed on the supplied warmer")
	}
	if status := session.CacheWarmingStatus(); status == nil || status.Reason != "supplied warmer" {
		t.Fatalf("CacheWarmingStatus = %+v, want the supplied warmer's", status)
	}
	onWarmed(t.Context(), icodingagent.UsageEntry{})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := appended
		mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	if appended != 1 {
		t.Fatalf("entry_appended events = %d, want 1 from onWarmed", appended)
	}
	mu.Unlock()
	if _, err := session.Send(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}
	if err := session.SetCacheWarmingMode(icodingagent.CacheWarmingMode("off")); err != nil {
		t.Fatal(err)
	}
	closeSession()
	warmer.mu.Lock()
	defer warmer.mu.Unlock()
	if warmer.settled == 0 || warmer.modes != 1 || warmer.canceled == 0 {
		t.Fatalf("supplied warmer saw onAgentSettled %d, onModeChanged %d, cancel %d times; want >0, 1, >0", warmer.settled, warmer.modes, warmer.canceled)
	}
}

// acknowledgeSessionEvents acknowledges every Session event until the Session closes, and closes the Session at cleanup.
func acknowledgeSessionEvents(t *testing.T, session *Session) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range session.Events() {
			AcknowledgeEvent(event)
		}
	}()
	t.Cleanup(func() {
		_ = session.Close()
		<-done
	})
}

// upstream: packages/coding-agent/src/core/agent-session.ts:652-655 (_installAgentToolHooks assigns agent.beforeToolCall and agent.afterToolCall) and :1767-1769 (_installHiddenDeclarationsProjection runs the Agent's own transformContext first). transformContext and onPayload are sdk.ts createAgent options (sdk.ts:432-440), which AgentSession never assigns.
func TestNewAgentSessionKeepsTheSuppliedAgentsTransformContextAndReplacesItsToolHooks(t *testing.T) {
	f := newSuppliedAgentFixture(t, admissionResponse("echo", "text", "one"), fauxReply("done", ai.StopReasonStop, 0))
	var mu sync.Mutex
	var transforms, beforeCalls, afterCalls int
	supplied, err := agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentInitialState{Model: f.model, ThinkingLevel: "off"},
		StreamFn: func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return model.Provider.Stream(ctx, transcript, options)
		},
		TransformContext: func(_ context.Context, messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
			mu.Lock()
			transforms++
			mu.Unlock()
			return append(append([]agent.AgentMessage(nil), messages...), suppliedUserMessage("from-supplied-transform-context")), nil
		},
		BeforeToolCall: func(context.Context, agent.BeforeToolCallContext) *agent.BeforeToolCallResult {
			mu.Lock()
			beforeCalls++
			mu.Unlock()
			return &agent.BeforeToolCallResult{Block: true, Reason: "the supplied Agent's beforeToolCall ran"}
		},
		AfterToolCall: func(context.Context, agent.AfterToolCallContext) *agent.AfterToolCallResult {
			mu.Lock()
			afterCalls++
			mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewAgentSession(SessionOptions{Services: f.services, Agent: supplied, NoSession: true, SkipBuiltinTools: true, Tools: []agent.AgentTool{f.tool}})
	if err != nil {
		t.Fatal(err)
	}
	acknowledgeSessionEvents(t, session)
	if _, err := session.Send(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if transforms < 2 {
		t.Fatalf("the supplied transformContext ran %d times, want once per request: the Session replaced it", transforms)
	}
	if beforeCalls != 0 || afterCalls != 0 {
		t.Fatalf("the supplied beforeToolCall ran %d and afterToolCall %d times, want 0: AgentSession replaces both", beforeCalls, afterCalls)
	}
	if executions := f.tool.executions(); len(executions) != 1 || executions[0] != "one" {
		t.Fatalf("tool executions = %q, want the Session's tool to run once", executions)
	}
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	for i, request := range f.provider.requests {
		if !strings.Contains(request, "from-supplied-transform-context") {
			t.Fatalf("request %d lacks the message the supplied transformContext added", i)
		}
	}
}

// upstream: packages/coding-agent/src/core/agent-session.ts:812-815 (the model and thinking level the Agent's previous prepareRequest returned are the request's) and :899-903 (an Agent with only prepareNextTurn is chained through an adapter).
func TestNewAgentSessionHonorsThePreviousHooksModelAndLegacyNextTurn(t *testing.T) {
	f := newSuppliedAgentFixture(t)
	routed := &scriptedProvider{responses: []scriptedResponse{admissionResponse("echo", "text", "one"), fauxReply("done", ai.StopReasonStop, 0)}}
	routedModel := &ai.Model{ID: "faux-2", DisplayName: "faux-2", Provider: routed, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000}}
	high := agent.ThinkingLevel("high")
	var mu sync.Mutex
	var seen []string
	supplied, err := agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentInitialState{Model: f.model, ThinkingLevel: "off"},
		StreamFn: func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			mu.Lock()
			seen = append(seen, model.ID)
			mu.Unlock()
			return model.Provider.Stream(ctx, transcript, options)
		},
		PrepareRequest: func(context.Context, agent.PrepareRequestContext) (*agent.AgentRequestUpdate, error) {
			return &agent.AgentRequestUpdate{Model: routedModel, ThinkingLevel: &high}, nil
		},
		PrepareNextTurn: func(context.Context) (*agent.AgentLoopTurnUpdate, error) {
			return &agent.AgentLoopTurnUpdate{Messages: []agent.AgentMessage{suppliedUserMessage("from-legacy-next-turn")}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewAgentSession(SessionOptions{Services: f.services, Agent: supplied, NoSession: true, SkipBuiltinTools: true, Tools: []agent.AgentTool{f.tool}})
	if err != nil {
		t.Fatal(err)
	}
	acknowledgeSessionEvents(t, session)
	if _, err := session.Send(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}
	if f.provider.callCount() != 0 || routed.callCount() != 2 {
		t.Fatalf("selected provider calls = %d, routed provider calls = %d; want 0 and 2: the previous prepareRequest's model was dropped", f.provider.callCount(), routed.callCount())
	}
	mu.Lock()
	if len(seen) != 2 || seen[0] != "faux-2" || seen[1] != "faux-2" {
		t.Fatalf("requests = %q, want faux-2 twice", seen)
	}
	mu.Unlock()
	routed.mu.Lock()
	defer routed.mu.Unlock()
	if !strings.Contains(routed.requests[1], "from-legacy-next-turn") {
		t.Fatal("the second request lacks the message the supplied Agent's prepareNextTurn added")
	}
}

// upstream: packages/coding-agent/src/core/agent-session.ts:1409-1412 (dispose clears the warmer's onWarmed, then cancels it). PiG replaces a Session's inner session where Pi disposes the AgentSession and builds the next one.
func TestSessionReplacementAndCloseReleaseTheSuppliedCacheWarmer(t *testing.T) {
	f := newSuppliedAgentFixture(t)
	warmer := &fakeCacheWarmer{}
	session, err := NewAgentSession(SessionOptions{Services: f.services, Model: f.model, CacheWarmer: warmer, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	acknowledgeSessionEvents(t, session)
	session.ReplaceInner(icodingagent.NewSession("replacement", f.services.CWD()))
	warmer.mu.Lock()
	canceled, onWarmed := warmer.canceled, warmer.onWarmed
	warmer.mu.Unlock()
	if canceled != 1 || onWarmed != nil {
		t.Fatalf("after replacement the supplied warmer was canceled %d times and onWarmed is set = %v; want 1 and unset", canceled, onWarmed != nil)
	}
	if status := session.CacheWarmingStatus(); status == nil || status.Reason == "supplied warmer" {
		t.Fatalf("CacheWarmingStatus = %+v, want the replacement's own warmer", status)
	}
	_ = session.Close()
	warmer.mu.Lock()
	defer warmer.mu.Unlock()
	if warmer.canceled != 1 {
		t.Fatalf("Close canceled the released warmer again: %d cancels", warmer.canceled)
	}

	closed := &fakeCacheWarmer{}
	session, err = NewAgentSession(SessionOptions{Services: f.services, Model: f.model, CacheWarmer: closed, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	acknowledgeSessionEvents(t, session)
	_ = session.Close()
	closed.mu.Lock()
	defer closed.mu.Unlock()
	if closed.canceled != 1 || closed.onWarmed != nil {
		t.Fatalf("Close canceled the warmer %d times and left onWarmed set = %v; want 1 and unset", closed.canceled, closed.onWarmed != nil)
	}
}
