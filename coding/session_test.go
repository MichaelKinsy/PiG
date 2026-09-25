package coding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	codingcompaction "github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// fakeProvider is a minimal ai.Provider for tests that don't actually
// hit the LLM. NewSession needs a non-nil Model whose Provider is
// non-nil; we never call Stream in unit tests below.
type fakeProvider struct{}

func newSessionTestStream(events ...ai.AssistantMessageEvent) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	for _, event := range events {
		if err := stream.Push(event); err != nil {
			panic(err)
		}
	}
	return stream
}

func sessionTestMessage(provider, text string, reason ai.StopReason, errorMessage string) *ai.AssistantMessage {
	content := []ai.AssistantContentBlock(nil)
	if text != "" {
		content = append(content, ai.TextContent{Text: text})
	}
	return &ai.AssistantMessage{
		Content: content, Provider: provider, Model: "fake-1",
		StopReason: reason, ErrorMessage: errorMessage,
	}
}

type retryOnceProvider struct {
	calls int
}

func (p *retryOnceProvider) ID() string { return "retry-once" }
func (p *retryOnceProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.calls++
	if p.calls == 1 {
		message := sessionTestMessage(p.ID(), "", ai.StopReasonError, "please retry your request")
		return newSessionTestStream(
			ai.StartEvent{Partial: message},
			ai.ErrorEvent{Reason: ai.StopReasonError, Error: message},
		), nil
	}
	message := sessionTestMessage(p.ID(), "retry-ok", ai.StopReasonStop, "")
	return newSessionTestStream(
		ai.StartEvent{Partial: message},
		ai.DoneEvent{Reason: ai.StopReasonStop, Message: message},
	), nil
}
func (p *retryOnceProvider) Close() error { return nil }

func retryOnceModel(p *retryOnceProvider) *ai.Model {
	return &ai.Model{ID: "retry-1", DisplayName: "retry-1", Provider: p, Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
}

func assistantText(msg *agent.AssistantMessage) string {
	if msg == nil {
		return ""
	}
	var b strings.Builder
	for _, block := range msg.Content {
		if text, ok := block.(ai.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

func (fakeProvider) ID() string { return "fake" }
func (fakeProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	message := sessionTestMessage("fake", "", ai.StopReasonStop, "")
	return newSessionTestStream(
		ai.StartEvent{Partial: message},
		ai.DoneEvent{Reason: ai.StopReasonStop, Message: message},
	), nil
}
func (fakeProvider) Close() error { return nil }

func fakeModel() *ai.Model {
	return &ai.Model{
		ID:          "fake-1",
		DisplayName: "fake-1",
		Provider:    fakeProvider{},
		Capabilities: ai.ModelCapabilities{
			ContextWindow: 8000,
		},
	}
}

func newTestServices(t *testing.T) *Services {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	srv, err := NewServices(ServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// newTestServicesSmallKeep is newTestServices with a tiny keepRecentTokens so
// short test sessions still have messages outside the recent-keep window to
// summarize. Without it, the 0.79.10 empty-compaction guard (PrepareCompaction
// returns nil when nothing is left to summarize) refuses to compact these
// sessions and the compaction never runs.
func newTestServicesSmallKeep(t *testing.T) *Services {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	agentDir := filepath.Join(tmp, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"),
		[]byte(`{"compaction":{"keepRecentTokens":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestNewSessionCreatesJSONL(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	if sess.ID() == "" {
		t.Errorf("session ID empty")
	}
	if !strings.HasSuffix(sess.Path(), ".jsonl") {
		t.Errorf("path %q does not end in .jsonl", sess.Path())
	}
	if got := sess.CWD(); got != svcs.CWD() {
		t.Errorf("session CWD = %q, want services CWD %q", got, svcs.CWD())
	}
	// File on disk should exist with at least the header line.
	if !filepath.IsAbs(sess.Path()) {
		t.Errorf("path should be absolute: %q", sess.Path())
	}
}

func TestSend_RetryableProviderErrorRetriesOnce(t *testing.T) {
	svcs := newTestServices(t)
	provider := &retryOnceProvider{}
	sess, err := NewSession(svcs, SessionOptions{Model: retryOnceModel(provider)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	msgs, err := sess.Send(context.Background(), "retry please")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	last := lastAssistantMessage(msgs)
	if last == nil || last.StopReason == "error" {
		t.Fatalf("last assistant = %+v, want successful retry", last)
	}
	if got := assistantText(last); got != "retry-ok" {
		t.Fatalf("assistant text = %q, want retry-ok", got)
	}
}

func TestResumeRestoresModelAndThinkingLevel(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test")
	services := newTestServices(t)
	model, err := BuildModel("openai/gpt-5", services)
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewSession(services, SessionOptions{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	if _, err := first.inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "saved"}}, StopReason: "stop",
	}}); err != nil {
		t.Fatal(err)
	}
	path := first.Path()
	if err := first.Close(); err != nil {
		t.Fatalf("close source session: %v", err)
	}

	fallback, err := BuildModel("openai/gpt-4o", services)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := NewSession(services, SessionOptions{Model: fallback, ResumePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resumed.Close(); err != nil {
			t.Errorf("close resumed session: %v", err)
		}
	}()
	if resumed.Model().ID != "gpt-5" {
		t.Fatalf("resumed model = %s, want gpt-5", resumed.Model().ID)
	}
	if resumed.ThinkingLevel() != ai.ThinkingHigh {
		t.Fatalf("resumed thinking level = %s, want high", resumed.ThinkingLevel())
	}
}

func TestNewSessionRejectsNilServices(t *testing.T) {
	_, err := NewSession(nil, SessionOptions{Model: fakeModel()})
	if !errors.Is(err, ErrNoServices) {
		t.Errorf("expected ErrNoServices, got %v", err)
	}
}

func TestNewSessionAcceptsNilModel(t *testing.T) {
	// Upstream pi's main.ts tolerates a nil model for interactive mode
	// (degraded "No models available" startup). pig mirrors that
	// behavior: NewSession must accept Model: nil and let the caller
	// (interactive TUI) wire SetModel later via /login or /model.
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: nil})
	if err != nil {
		t.Fatalf("expected NewSession to accept nil Model, got: %v", err)
	}
	defer func() { _ = sess.Close() }()
}

func TestNewSessionAddsCallerToolsOnTopOfDefaults(t *testing.T) {
	svcs := newTestServices(t)

	// A trivial extra tool to verify it's added.
	extra := &fakeTool{name: "test-tool-xyz"}
	sess, err := NewSession(svcs, SessionOptions{
		Model: fakeModel(),
		Tools: []agent.AgentTool{extra},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	names := toolNames(sess.Tools())
	if !slices.Contains(names, "test-tool-xyz") {
		t.Errorf("expected test-tool-xyz in tool list; got %v", names)
	}
	// Defaults should still be there.
	for _, want := range []string{"bash", "read", "write", "edit"} {
		if !slices.Contains(names, want) {
			t.Errorf("expected default tool %q in list; got %v", want, names)
		}
	}
}

func TestNewSessionAllowedToolsFiltersDefaultsAndExtras(t *testing.T) {
	svcs := newTestServices(t)
	extra := &fakeTool{name: "test-tool-abc"}

	sess, err := NewSession(svcs, SessionOptions{
		Model: fakeModel(),
		Tools: []agent.AgentTool{extra},
		AllowedTools: map[string]struct{}{
			"read":          {},
			"test-tool-abc": {},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	names := toolNames(sess.Tools())
	if !slices.Contains(names, "read") || !slices.Contains(names, "test-tool-abc") {
		t.Errorf("allowlisted tools missing: %v", names)
	}
	for _, blocked := range []string{"bash", "write", "edit"} {
		if slices.Contains(names, blocked) {
			t.Errorf("blocked tool %q should not be in allowed list; got %v", blocked, names)
		}
	}
}

func TestNewSessionActiveBuiltinToolsLimitsBuiltinsKeepsExtras(t *testing.T) {
	svcs := newTestServices(t)
	extra := &fakeTool{name: "test-tool-ext"}

	// Mirrors upstream defaultActiveToolNames (sdk.ts:244): only
	// read/bash/edit/write are active built-ins by default; grep/find/ls
	// are registered but inactive. Extension/caller tools are NOT gated by
	// ActiveBuiltinTools (only AllowedTools gates those).
	sess, err := NewSession(svcs, SessionOptions{
		Model: fakeModel(),
		Tools: []agent.AgentTool{extra},
		ActiveBuiltinTools: map[string]struct{}{
			"read": {}, "bash": {}, "edit": {}, "write": {},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	names := toolNames(sess.Tools())
	for _, want := range []string{"read", "bash", "edit", "write", "test-tool-ext"} {
		if !slices.Contains(names, want) {
			t.Errorf("expected active tool %q in list; got %v", want, names)
		}
	}
	for _, off := range []string{"grep", "find", "ls"} {
		if slices.Contains(names, off) {
			t.Errorf("tool %q must be inactive by default (opt-in via --tools); got %v", off, names)
		}
	}
}

// Upstream registers powershell on every platform (createAllTools) but no
// default active set includes it: the default session and the CLI default
// active set leave it out, while an allowlist or explicit active set that
// names it activates it, in upstream registry order.
func TestNewSessionPowerShellIsOptIn(t *testing.T) {
	svcs := newTestServices(t)
	for _, tc := range []struct {
		name string
		opts SessionOptions
		want []string
	}{
		{"default", SessionOptions{}, []string{"read", "bash", "edit", "write", "grep", "find", "ls"}},
		{"cli default active set", SessionOptions{ActiveBuiltinTools: map[string]struct{}{"read": {}, "bash": {}, "edit": {}, "write": {}}},
			[]string{"read", "bash", "edit", "write"}},
		{"allowlist", SessionOptions{AllowedTools: map[string]struct{}{"powershell": {}, "bash": {}}}, []string{"bash", "powershell"}},
		{"active set", SessionOptions{ActiveBuiltinTools: map[string]struct{}{"powershell": {}, "read": {}}}, []string{"read", "powershell"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Model = fakeModel()
			sess, err := NewSession(svcs, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sess.Close() }()
			if got := toolNames(sess.Tools()); !slices.Equal(got, tc.want) {
				t.Errorf("tools = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewSessionAllowedToolsEmptyBlocksAll(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{
		Model:        fakeModel(),
		AllowedTools: map[string]struct{}{}, // empty, NOT nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	if got := len(sess.Tools()); got != 0 {
		t.Errorf("empty allowlist should block all tools; got %d", got)
	}
}

// TestNewSessionExcludedToolsDenylistsBuiltinAndExtension: --exclude-tools is a
// denylist that makes a tool non-callable, gating built-in AND
// extension/caller tools (not merely hiding from the prompt). Mirrors upstream
// isAllowedTool's `!excludedToolNames?.has(name)` (agent-session.ts:2288).
func TestNewSessionExcludedToolsDenylistsBuiltinAndExtension(t *testing.T) {
	svcs := newTestServices(t)
	extBash := &fakeTool{name: "ext-only"}
	sess, err := NewSession(svcs, SessionOptions{
		Model: fakeModel(),
		Tools: []agent.AgentTool{extBash},
		ExcludedTools: map[string]struct{}{
			"bash":     {}, // built-in
			"ext-only": {}, // extension/caller tool
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	names := toolNames(sess.Tools())
	for _, off := range []string{"bash", "ext-only"} {
		if slices.Contains(names, off) {
			t.Errorf("excluded tool %q must be non-callable; got %v", off, names)
		}
	}
	// Non-excluded built-ins remain.
	for _, want := range []string{"read", "edit", "write"} {
		if !slices.Contains(names, want) {
			t.Errorf("non-excluded tool %q should remain; got %v", want, names)
		}
	}
}

func TestNewSessionSkipBuiltinToolsLeavesExtras(t *testing.T) {
	svcs := newTestServices(t)
	extra := &fakeTool{name: "test-tool-abc"}
	sess, err := NewSession(svcs, SessionOptions{
		Model:            fakeModel(),
		SkipBuiltinTools: true,
		Tools:            []agent.AgentTool{extra},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	names := toolNames(sess.Tools())
	if !slices.Equal(names, []string{"test-tool-abc"}) {
		t.Errorf("tools = %v, want only extra tool", names)
	}
}

func TestSessionMessagesReturnsDefensiveCopy(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	if _, err := sess.Send(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	got := sess.Messages()
	if len(got) != 3 || got[0].System == nil {
		t.Fatalf("first turn should contain the system, user and assistant; got %v", got)
	}
	got[0] = agent.AgentMessage{}
	if got2 := sess.Messages(); len(got2) != 3 || got2[0].System == nil {
		t.Errorf("mutation leaked back into session: %v", got2)
	}
}

func TestSessionEventsBufferedAndClosedOnClose(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	ch := sess.Events()
	if ch == nil {
		t.Fatal("Events() returned nil")
	}
	// Close should close the channel (drains to nil for receivers).
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Errorf("expected channel to be closed; got value with ok=true")
		}
	case <-time.After(250 * time.Millisecond):
		t.Errorf("channel not closed after Close()")
	}
}

func TestSessionAgentEndEventIncludesWillRetry(t *testing.T) {
	errorMessage := sessionTestMessage("fake", "", ai.StopReasonError, "429 Too Many Requests")
	retryProvider := staticProviderForSessionTests{events: []ai.AssistantMessageEvent{
		ai.StartEvent{Partial: errorMessage},
		ai.ErrorEvent{Reason: ai.StopReasonError, Error: errorMessage},
	}}
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(retryProvider)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, sendErr := sess.Send(ctx, "retry please")
		done <- sendErr
	}()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				t.Fatal("events channel closed before agent_end")
			}
			if end, ok := ev.(agent.AgentEndEvent); ok {
				if !end.WillRetry {
					t.Fatal("AgentEndEvent.WillRetry = false, want true")
				}
				cancel()
				<-done
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for agent_end event")
		}
	}
}

type staticProviderForSessionTests struct{ events []ai.AssistantMessageEvent }

func (p staticProviderForSessionTests) ID() string { return "fake" }
func (p staticProviderForSessionTests) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return newSessionTestStream(p.events...), nil
}
func (p staticProviderForSessionTests) Close() error { return nil }

type blockingProviderForSessionTests struct{ started chan struct{} }

func (p blockingProviderForSessionTests) ID() string { return "fake" }
func (p blockingProviderForSessionTests) Stream(ctx context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	close(p.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func (p blockingProviderForSessionTests) Close() error { return nil }

func fakeModelWithProvider(prov ai.Provider) *ai.Model {
	return &ai.Model{
		ID:          "fake-1",
		DisplayName: "fake-1",
		Provider:    prov,
		Capabilities: ai.ModelCapabilities{
			ContextWindow: 8000,
		},
	}
}

// TestSendDeadlocksWithoutEventDrainThenUnblocks reproduces the print-mode
// deadlock: a turn that emits more events than the rawEvents+events buffers hold
// (2×64) fills forwardAgentEvents' outbound channel; with no Events() consumer it
// blocks, the agent's emit() blocks, and Send() never returns. Print mode hit this
// on long findings output. Draining Events() (as print mode now does) unblocks it.
func TestSendDeadlocksWithoutEventDrainThenUnblocks(t *testing.T) {
	partial := sessionTestMessage("fake", "", ai.StopReasonPending, "")
	evs := []ai.AssistantMessageEvent{ai.StartEvent{Partial: partial}}
	for index := range 300 {
		partial = sessionTestMessage("fake", strings.Repeat("x", index+1), ai.StopReasonPending, "")
		evs = append(evs, ai.TextDeltaEvent{ContentIndex: 0, Delta: "x", Partial: partial})
	}
	final := sessionTestMessage("fake", strings.Repeat("x", 300), ai.StopReasonStop, "")
	evs = append(evs, ai.DoneEvent{Reason: ai.StopReasonStop, Message: final})
	prov := staticProviderForSessionTests{events: evs}
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(prov)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	ctx := t.Context()

	done := make(chan error, 1)
	go func() {
		_, e := sess.Send(ctx, "hi")
		done <- e
	}()

	// Without a drainer, Send must block: this is the deadlock condition.
	select {
	case <-done:
		t.Fatal("Send completed without draining events; the deadlock scenario was not reproduced")
	case <-time.After(500 * time.Millisecond):
	}

	// Draining Events() must unblock Send and let the turn finish.
	go func() {
		for range sess.Events() { //nolint:revive // intentional drain
		}
	}()
	select {
	case e := <-done:
		if e != nil {
			t.Fatalf("Send after drain returned error: %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send deadlocked even after draining events")
	}
}

func TestSessionResumeRebuildsAgentMessages(t *testing.T) {
	svcs := newTestServices(t)

	// Create a fresh session, append two messages directly via the
	// inner type so we don't need a real LLM call.
	sess1, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	path := sess1.Path()
	// Inject a user + assistant message via the inner session API.
	userMsg := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:      "user",
			Content:   []ai.UserContentBlock{ai.TextContent{Text: "hello"}},
			Timestamp: 1,
		},
	}
	asstMsg := agent.AgentMessage{
		Assistant: &agent.AssistantMessage{
			Role:      "assistant",
			Content:   []ai.AssistantContentBlock{ai.TextContent{Text: "hi back"}},
			Timestamp: 2,
		},
	}
	if _, err := sess1.inner.AppendMessage(userMsg); err != nil {
		t.Fatal(err)
	}
	if _, err := sess1.inner.AppendMessage(asstMsg); err != nil {
		t.Fatal(err)
	}
	_ = sess1.Close()

	// Resume the same session via NewSession with ResumePath.
	sess2, err := NewSession(svcs, SessionOptions{
		Model:      fakeModel(),
		ResumePath: path,
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	defer func() { _ = sess2.Close() }()

	got := sess2.Messages()
	if len(got) != 2 || got[0].User == nil || got[1].Assistant == nil {
		t.Fatalf("resumed transcript should contain only the directly persisted user and assistant: %+v", got)
	}
}

func TestAC54SessionReplaceRunnerRedirectsAgentLifecycle(t *testing.T) {
	newAgentStart := make(chan struct{}, 1)
	oldAgentStart := make(chan struct{}, 1)
	makeRunner := func(signal chan<- struct{}) *inproc.Runner {
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"agent_start": {func(...any) (any, error) {
				select {
				case signal <- struct{}{}:
				default:
				}
				return nil, nil
			}},
		}}
		return inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	}

	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), Runner: makeRunner(oldAgentStart)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	sess.ReplaceRunner(makeRunner(newAgentStart))
	if _, err := sess.Send(context.Background(), "route lifecycle to replacement"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-newAgentStart:
	case <-time.After(time.Second):
		t.Fatal("replacement runner did not receive agent_start")
	}
	select {
	case <-oldAgentStart:
		t.Fatal("stale runner received agent_start after replacement")
	default:
	}
}

func TestSessionSendPersistsUserPromptBeforeAgentLoop(t *testing.T) {
	// Verify that even when the agent's stream returns nothing
	// useful (our fakeProvider yields a closed channel: zero events),
	// the user prompt is persisted to the JSONL on disk before the
	// agent is even called. This is the F-2 contract: persistence
	// happens BEFORE the LLM call, so an aborted Send doesn't lose
	// the user input.
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()

	ctx := context.Background()
	_, _ = sess.Send(ctx, "persist-me-please")

	// Re-open the session from disk and confirm the user prompt is there.
	sess2, err := NewSession(svcs, SessionOptions{
		Model:      fakeModel(),
		ResumePath: sess.Path(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess2.Close() }()

	msgs := sess2.Messages()
	if len(msgs) == 0 {
		t.Fatal("expected at least 1 persisted message after Send")
	}
	found := false
	for _, m := range msgs {
		if m.User == nil {
			continue
		}
		for _, c := range m.User.Content {
			if tc, ok := c.(ai.TextContent); ok && tc.Text == "persist-me-please" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("persisted messages don't contain the user prompt; got %+v", msgs)
	}
}

// TestPersistenceFollowsReplaceInner guards against the session/agent desync:
// after a session switch (ReplaceInner: used by /resume, /new, /clone), the
// agent's OnMessagePersist hook must record into the NEW session, not the one
// captured when the session was created. The old wiring captured a local
// `inner`, so post-switch turns persisted to the original session while
// /session, /tree, and /compact displayed the new (empty) one: orphaning a
// whole conversation's worth of work and leaving the displayed session with 0
// entries (no on-disk file).
func TestPersistenceFollowsReplaceInner(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	oldInner := sess.Inner()
	oldEntriesBefore := len(oldInner.Entries())

	// Swap in a fresh empty session, exactly as /new, /clone, and /resume do.
	sm := newSessionManagerForDir(svcs.CWD(), "")
	newInner, err := sm.Create("sess-replaced", "")
	if err != nil {
		t.Fatal(err)
	}
	sess.ReplaceInner(newInner)

	// A turn after the swap. fakeModel yields no assistant events, but the user
	// prompt is persisted via OnMessagePersist before the agent loop.
	_, _ = sess.Send(context.Background(), "after-swap-prompt")

	newMsgs := 0
	for _, e := range newInner.Entries() {
		if e.Base.Type == "message" {
			newMsgs++
		}
	}
	oldEntriesAfter := len(oldInner.Entries())
	if newMsgs == 0 {
		t.Fatal("persistence did not follow ReplaceInner: post-swap turn was orphaned to the old session (new session has 0 message entries)")
	}
	if oldEntriesAfter != oldEntriesBefore {
		t.Errorf("post-swap turn changed OLD session entries: %d to %d", oldEntriesBefore, oldEntriesAfter)
	}
}

// ─── helpers ───────────────────────────────────────────────────────────────

type fakeTool struct {
	name string
}

func (t *fakeTool) Name() string          { return t.name }
func (t *fakeTool) Label() string         { return "" }
func (t *fakeTool) Schema() ai.ToolSchema { return ai.ToolSchema{Name: t.name} }
func (t *fakeTool) ExecutionMode() agent.ToolExecutionMode {
	return agent.ToolModeParallel
}
func (t *fakeTool) Execute(_ context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{}, nil
}

func toolNames(in []agent.AgentTool) []string {
	out := make([]string, len(in))
	for i, t := range in {
		out[i] = t.Name()
	}
	return out
}

// ─── compaction / NavigateTree tests ──────────────────────────────────────────

// fakeCompleter implements compaction.SimpleCompleter for tests.
type fakeCompleter struct {
	summary string
	err     error
	called  atomic.Bool
}

func (c *fakeCompleter) CompleteSimple(_ context.Context, _ *ai.Model, _ string, _ []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
	c.called.Store(true)
	return c.summary, nil, c.err
}

type blockingCompactionCompleter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func newBlockingCompactionCompleter() *blockingCompactionCompleter {
	return &blockingCompactionCompleter{started: make(chan struct{}), release: make(chan struct{})}
}

func (c *blockingCompactionCompleter) CompleteSimple(ctx context.Context, _ *ai.Model, _ string, _ []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
	c.calls.Add(1)
	c.once.Do(func() { close(c.started) })
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	case <-c.release:
		return "summary", nil, nil
	}
}

// buildSessionWithMessages creates a session and injects n plain user/assistant
// message pairs directly into the inner JSONL (no LLM call).
func buildSessionWithMessages(t *testing.T, svcs *Services, n int) *Session {
	t.Helper()
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		userMsg := agent.AgentMessage{
			User: &agent.UserMessage{
				Role:    "user",
				Content: []ai.UserContentBlock{ai.TextContent{Text: fmt.Sprintf("q%d", i)}},
			},
		}
		asstMsg := agent.AgentMessage{
			Assistant: &agent.AssistantMessage{
				Role:    "assistant",
				Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("a%d", i)}},
			},
		}
		if _, err := sess.inner.AppendMessage(userMsg); err != nil {
			t.Fatal(err)
		}
		if _, err := sess.inner.AppendMessage(asstMsg); err != nil {
			t.Fatal(err)
		}
	}
	// Rebuild agent messages to match inner state.
	sess.agent.SetMessages(sess.inner.BuildContext(nil))
	return sess
}

// drainEvents waits for an ordered marker after the operation's events.
func drainEvents(t *testing.T, sess *Session) []agent.AgentEvent {
	t.Helper()
	marker := &agent.AgentSettledEvent{}
	sess.emitOrderedEvent(marker)
	var events []agent.AgentEvent
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-sess.events:
			if event == marker {
				return events
			}
			events = append(events, event)
		case <-timer.C:
			t.Fatal("session event funnel did not drain")
			return nil
		}
	}
}

func TestCompact_Smoke(t *testing.T) {
	svcs := newTestServicesSmallKeep(t)
	sess := buildSessionWithMessages(t, svcs, 3)
	defer func() { _ = sess.Close() }()

	sess.completer = &fakeCompleter{summary: "summary text"}

	if err := sess.Compact(context.Background(), ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Tree should contain a compaction entry.
	leafID := sess.inner.LeafID()
	if leafID == nil {
		t.Fatal("leaf is nil after Compact")
		return
	}
	entries := sess.inner.Branch(*leafID)
	haveCompaction := false
	for _, e := range entries {
		if e.Base.Type == "compaction" {
			haveCompaction = true
		}
	}
	if !haveCompaction {
		t.Error("expected a compaction entry in the session branch after Compact")
	}

	// Events should include a CompactionStartEvent and CompactionEndEvent with
	// the summary text.
	evs := drainEvents(t, sess)
	var start, end agent.AgentEvent
	for _, ev := range evs {
		switch ev.(type) {
		case agent.CompactionStartEvent:
			start = ev
		case agent.CompactionEndEvent:
			end = ev
		}
	}
	if start == nil {
		t.Error("CompactionStartEvent not emitted")
	}
	if end == nil {
		t.Fatal("CompactionEndEvent not emitted")
	}
	endEv := end.(agent.CompactionEndEvent)
	if !strings.Contains(endEv.Summary, "summary text") {
		t.Errorf("CompactionEndEvent.Summary = %q; want it to contain 'summary text'", endEv.Summary)
	}
	if endEv.ErrorMessage != "" {
		t.Errorf("unexpected error in CompactionEndEvent: %q", endEv.ErrorMessage)
	}
	wantEstimatedAfter := 0
	for _, msg := range sess.agent.Messages() {
		wantEstimatedAfter += codingcompaction.EstimateTokens(msg)
	}
	if endEv.EstimatedTokensAfter != wantEstimatedAfter {
		t.Errorf("CompactionEndEvent.EstimatedTokensAfter = %d, want %d", endEv.EstimatedTokensAfter, wantEstimatedAfter)
	}
}

func TestCompact_ExtensionOverrideAndLifecycle(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	fallback := &fakeCompleter{summary: "fallback must not run"}
	sess.completer = fallback
	var sequence []string
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"session_before_compact": {func(args ...any) (any, error) {
			sequence = append(sequence, "before")
			event, ok := args[0].(extension.SessionBeforeCompactEvent)
			if !ok {
				t.Fatalf("session_before_compact event = %T", args[0])
			}
			prep, ok := event.Preparation.(*codingcompaction.CompactionPreparation)
			if !ok || prep.FirstKeptEntryID == "" {
				t.Fatalf("compaction preparation = %#v", event.Preparation)
			}
			if event.Reason != "manual" || event.WillRetry || event.CustomInstructions != "keep decisions" {
				t.Fatalf("session_before_compact metadata = %#v", event)
			}
			if len(event.BranchEntries) == 0 || event.Signal == nil {
				t.Fatalf("session_before_compact omitted branch or signal: %#v", event)
			}
			return extension.SessionBeforeCompactResult{Compaction: map[string]any{
				"summary":          "extension summary",
				"firstKeptEntryId": prep.FirstKeptEntryID,
				"tokensBefore":     prep.TokensBefore,
				"details":          map[string]any{"source": "extension"},
			}}, nil
		}},
		"session_compact": {func(args ...any) (any, error) {
			sequence = append(sequence, "compact")
			event, ok := args[0].(extension.SessionCompactEvent)
			if !ok {
				t.Fatalf("session_compact event = %T", args[0])
			}
			if !event.FromExtension || event.Reason != "manual" || event.WillRetry {
				t.Fatalf("session_compact metadata = %#v", event)
			}
			if event.CompactionEntry == nil {
				t.Fatal("session_compact omitted persisted entry")
			}
			return nil, nil
		}},
	}}
	sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{ext}, t.TempDir()))

	result, err := sess.CompactResult(context.Background(), "keep decisions")
	if err != nil {
		t.Fatal(err)
	}
	if fallback.called.Load() {
		t.Fatal("extension override called the summarization completer")
	}
	if result.Summary != "extension summary" {
		t.Fatalf("summary = %q", result.Summary)
	}
	details, ok := result.Details.(map[string]any)
	if !ok || details["source"] != "extension" {
		t.Fatalf("details = %#v", result.Details)
	}
	if !slices.Equal(sequence, []string{"before", "compact"}) {
		t.Fatalf("extension event order = %v", sequence)
	}
}

func TestCompact_ExtensionCancellationIsAborted(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	fallback := &fakeCompleter{summary: "fallback must not run"}
	sess.completer = fallback
	var failed *extension.SessionCompactFailedEvent
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"session_before_compact": {func(...any) (any, error) {
			return extension.SessionBeforeCompactResult{Cancel: true}, nil
		}},
		"session_compact_failed": {func(args ...any) (any, error) {
			event := args[0].(extension.SessionCompactFailedEvent)
			failed = &event
			return nil, nil
		}},
	}}
	sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{ext}, t.TempDir()))

	_, err := sess.CompactResult(context.Background(), "")
	if err == nil || err.Error() != "Compaction cancelled" {
		t.Fatalf("CompactResult error = %v", err)
	}
	if fallback.called.Load() {
		t.Fatal("cancelled extension compaction called the completer")
	}
	if failed == nil || failed.Type != "session_compact_failed" || failed.Reason != "manual" || !failed.Aborted || failed.ErrorMessage != "" || failed.WillRetry || failed.FromExtension {
		t.Fatalf("session_compact_failed = %#v", failed)
	}
	var end *agent.CompactionEndEvent
	for _, event := range drainEvents(t, sess) {
		if value, ok := event.(agent.CompactionEndEvent); ok {
			end = &value
		}
	}
	if end == nil || !end.Aborted || end.ErrorMessage != "" {
		t.Fatalf("compaction end = %#v", end)
	}
}

func TestAutoCompaction_ExtensionLifecycleMetadata(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	var beforeReason, compactReason string
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"session_before_compact": {func(args ...any) (any, error) {
			event := args[0].(extension.SessionBeforeCompactEvent)
			beforeReason = event.Reason
			prep := event.Preparation.(*codingcompaction.CompactionPreparation)
			return extension.SessionBeforeCompactResult{Compaction: map[string]any{
				"summary": "auto extension summary", "firstKeptEntryId": prep.FirstKeptEntryID,
				"tokensBefore": prep.TokensBefore,
			}}, nil
		}},
		"session_compact": {func(args ...any) (any, error) {
			event := args[0].(extension.SessionCompactEvent)
			compactReason = event.Reason
			if !event.FromExtension || event.WillRetry {
				t.Fatalf("session_compact metadata = %#v", event)
			}
			return nil, nil
		}},
	}}
	sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{ext}, t.TempDir()))

	if !sess.runAutoCompaction(context.Background(), "threshold", false) {
		t.Fatal("auto compaction did not run")
	}
	if beforeReason != "threshold" || compactReason != "threshold" {
		t.Fatalf("reasons = before:%q compact:%q", beforeReason, compactReason)
	}
}

func TestSessionSubscribePreservesOrderAndUnsubscribes(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	var calls []int
	unsubscribeFirst := sess.Subscribe(func(agent.AgentEvent) { calls = append(calls, 1) })
	sess.Subscribe(func(agent.AgentEvent) { calls = append(calls, 2) })

	sess.notifyListeners(agent.AgentSettledEvent{})
	unsubscribeFirst()
	sess.notifyListeners(agent.AgentSettledEvent{})
	if want := []int{1, 2, 2}; !slices.Equal(calls, want) {
		t.Fatalf("listener calls = %v, want %v", calls, want)
	}
}

func TestSessionNotifyListenersDoesNotAllocate(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	var calls atomic.Int64
	sess.Subscribe(func(agent.AgentEvent) { calls.Add(1) })
	event := agent.AgentSettledEvent{}

	if allocations := testing.AllocsPerRun(100, func() { sess.notifyListeners(event) }); allocations != 0 {
		t.Fatalf("notifyListeners allocations = %v, want 0", allocations)
	}
	if calls.Load() != 101 {
		t.Fatalf("listener calls = %d, want 101", calls.Load())
	}
}

func TestCompactCancelsSynchronouslyFromStartEvent(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	completer := &fakeCompleter{summary: "must not run"}
	sess.completer = completer

	unsubscribe := sess.Subscribe(func(event agent.AgentEvent) {
		if _, ok := event.(agent.CompactionStartEvent); ok {
			sess.AbortCompaction()
		}
	})
	defer unsubscribe()

	_, err := sess.CompactResult(t.Context(), "")
	if !errors.Is(err, errCompactionCancelled) {
		t.Fatalf("CompactResult error = %v, want %v", err, errCompactionCancelled)
	}
	if completer.called.Load() {
		t.Fatal("summarization provider was called, want 0 calls")
	}
	var end *agent.CompactionEndEvent
	for _, event := range drainEvents(t, sess) {
		if value, ok := event.(agent.CompactionEndEvent); ok {
			end = &value
		}
	}
	if end == nil || !end.Aborted {
		t.Fatalf("compaction_end = %#v, want aborted=true", end)
	}
}

func TestAutoCompactionCancelsSynchronouslyFromStartEvent(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	completer := &fakeCompleter{summary: "must not run"}
	sess.completer = completer
	failed := make(chan extension.SessionCompactFailedEvent, 1)
	sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Path: "compact-failed", Handlers: map[string][]extension.HandlerFn{
		"session_compact_failed": {func(args ...any) (any, error) {
			failed <- args[0].(extension.SessionCompactFailedEvent)
			return nil, nil
		}},
	}}}, t.TempDir()))

	startHandled := make(chan struct{})
	unsubscribe := sess.Subscribe(func(event agent.AgentEvent) {
		if _, ok := event.(agent.CompactionStartEvent); !ok {
			return
		}
		sess.AbortCompaction()
		close(startHandled)
	})
	defer unsubscribe()

	sess.runAutoCompaction(t.Context(), "threshold", false)
	select {
	case <-startHandled:
	case <-time.After(5 * time.Second):
		t.Fatal("compaction_start handler did not run")
	}
	if completer.called.Load() {
		t.Fatal("summarization provider was called, want 0 calls")
	}

	var end *agent.CompactionEndEvent
	for _, event := range drainEvents(t, sess) {
		if value, ok := event.(agent.CompactionEndEvent); ok {
			end = &value
		}
	}
	if end == nil || !end.Aborted {
		t.Fatalf("compaction_end = %#v, want aborted=true", end)
	}
	select {
	case event := <-failed:
		if event.Reason != "threshold" || !event.Aborted || event.ErrorMessage != "" {
			t.Fatalf("session_compact_failed = %#v", event)
		}
	default:
		t.Fatal("session_compact_failed was not emitted for cancellation at compaction_start")
	}
}

func TestAutoCompactionProviderErrorsAreNotCancellation(t *testing.T) {
	for _, message := range []string{"Compaction cancelled", "auth failed"} {
		t.Run(message, func(t *testing.T) {
			sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
			defer func() { _ = sess.Close() }()
			sess.completer = &fakeCompleter{err: errors.New(message)}

			if sess.runAutoCompaction(t.Context(), "threshold", false) {
				t.Fatal("failed auto-compaction reported success")
			}
			var end *agent.CompactionEndEvent
			for _, event := range drainEvents(t, sess) {
				if value, ok := event.(agent.CompactionEndEvent); ok {
					end = &value
				}
			}
			if end == nil || end.Aborted || !strings.Contains(end.ErrorMessage, message) {
				t.Fatalf("compaction_end = %#v, want aborted=false with %q", end, message)
			}
		})
	}
}

func TestAutoCompactionFailureAlwaysReportsWillRetryFalse(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	sess.completer = &fakeCompleter{err: errors.New("overflow failed")}
	failed := make(chan extension.SessionCompactFailedEvent, 1)
	sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Path: "compact-failed", Handlers: map[string][]extension.HandlerFn{
		"session_compact_failed": {func(args ...any) (any, error) {
			failed <- args[0].(extension.SessionCompactFailedEvent)
			return nil, nil
		}},
	}}}, t.TempDir()))

	if sess.runAutoCompaction(t.Context(), "overflow", true) {
		t.Fatal("failed overflow compaction reported success")
	}
	select {
	case event := <-failed:
		if event.Reason != "overflow" || event.WillRetry || event.Aborted || !strings.Contains(event.ErrorMessage, "overflow failed") {
			t.Fatalf("session_compact_failed = %#v", event)
		}
	default:
		t.Fatal("missing session_compact_failed")
	}
}

func TestAutoCompaction_NothingToCompactIsSilent(t *testing.T) {
	svcs := newTestServices(t)
	sess := buildSessionWithMessages(t, svcs, 3)
	defer func() { _ = sess.Close() }()

	fakeComp := &fakeCompleter{summary: "must not be called"}
	sess.completer = fakeComp

	sess.runAutoCompaction(context.Background(), "threshold", false)

	if fakeComp.called.Load() {
		t.Fatal("auto compaction called completer despite nothing to compact")
	}
	if evs := drainEvents(t, sess); len(evs) != 0 {
		t.Fatalf("auto compaction emitted %d events despite nothing to compact: %#v", len(evs), evs)
	}
}

func TestAutoCompaction_SuccessEmitsEstimatedTokensAfter(t *testing.T) {
	svcs := newTestServicesSmallKeep(t)
	sess := buildSessionWithMessages(t, svcs, 3)
	defer func() { _ = sess.Close() }()

	sess.completer = &fakeCompleter{summary: "auto summary"}
	sess.runAutoCompaction(context.Background(), "threshold", false)

	var end *agent.CompactionEndEvent
	for _, ev := range drainEvents(t, sess) {
		if ev, ok := ev.(agent.CompactionEndEvent); ok {
			end = &ev
		}
	}
	if end == nil {
		t.Fatal("CompactionEndEvent not emitted")
	}
	wantEstimatedAfter := 0
	for _, msg := range sess.agent.Messages() {
		wantEstimatedAfter += codingcompaction.EstimateTokens(msg)
	}
	if end.EstimatedTokensAfter != wantEstimatedAfter {
		t.Errorf("CompactionEndEvent.EstimatedTokensAfter = %d, want %d", end.EstimatedTokensAfter, wantEstimatedAfter)
	}
}

func TestCompact_ForwardsStreamFn(t *testing.T) {
	svcs := newTestServicesSmallKeep(t)
	sess := buildSessionWithMessages(t, svcs, 3)
	defer func() { _ = sess.Close() }()

	fallback := &fakeCompleter{summary: "fallback"}
	var streamCalls atomic.Int64
	sess.completer = fallback
	sess.streamFn = func(_ context.Context, _ *ai.Model, _ string, _ []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
		streamCalls.Add(1)
		return "streamed summary", nil, nil
	}

	if err := sess.Compact(context.Background(), ""); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if streamCalls.Load() == 0 {
		t.Fatal("expected streamFn to be called")
	}
	if fallback.called.Load() {
		t.Fatal("expected fallback completer to be bypassed when streamFn is set")
	}
}

func TestCompact_NothingToCompact(t *testing.T) {
	svcs := newTestServices(t)
	// Empty session (no entries) → PrepareCompaction returns nil.
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	sess.completer = &fakeCompleter{summary: "should not be called"}
	var failed *extension.SessionCompactFailedEvent
	sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{
		"session_compact_failed": {func(args ...any) (any, error) {
			event := args[0].(extension.SessionCompactFailedEvent)
			failed = &event
			return nil, nil
		}},
	}}}, t.TempDir()))

	if err := sess.Compact(context.Background(), ""); err != nil {
		t.Fatalf("Compact: unexpected error: %v", err)
	}
	if failed == nil || failed.Reason != "manual" || failed.Aborted || !strings.Contains(failed.ErrorMessage, "Nothing to compact") || failed.WillRetry || failed.FromExtension {
		t.Fatalf("session_compact_failed = %#v", failed)
	}

	evs := drainEvents(t, sess)
	var endEv *agent.CompactionEndEvent
	for _, ev := range evs {
		if ce, ok := ev.(agent.CompactionEndEvent); ok {
			endEv = &ce
		}
	}
	if endEv == nil {
		t.Fatal("CompactionEndEvent not emitted")
		return
	}
	if !strings.Contains(strings.ToLower(endEv.ErrorMessage), "nothing") {
		t.Errorf("expected 'nothing' in ErrorMessage; got %q", endEv.ErrorMessage)
	}
}

func TestNavigateTree_NoSummary(t *testing.T) {
	svcs := newTestServices(t)
	// Build a branching session: add 2 messages, record entry A, add 1 more.
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	// Append first message and record its ID.
	userMsg := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:    "user",
			Content: []ai.UserContentBlock{ai.TextContent{Text: "first"}},
		},
	}
	if _, err := sess.inner.AppendMessage(userMsg); err != nil {
		t.Fatal(err)
	}
	firstLeaf := sess.inner.LeafID()
	if firstLeaf == nil {
		t.Fatal("leaf is nil after first message")
		return
	}
	branchPoint := *firstLeaf // ID of node we'll navigate back to.

	// Append a second message (extends the branch beyond branchPoint).
	userMsg2 := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:    "user",
			Content: []ai.UserContentBlock{ai.TextContent{Text: "second"}},
		},
	}
	if _, err := sess.inner.AppendMessage(userMsg2); err != nil {
		t.Fatal(err)
	}

	// Navigate back to branchPoint without summarize.
	res, err := sess.NavigateTree(context.Background(), branchPoint, NavigateTreeOptions{Summarize: false})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if res.Aborted {
		t.Error("NavigateTree returned Aborted=true unexpectedly")
	}

	// branchPoint is a user-role message entry. Upstream navigateTree sets leaf
	// to the parent entry, which in a fresh session is the bootstrap
	// thinking-level audit entry.
	newLeaf := sess.inner.LeafID()
	if newLeaf == nil {
		t.Fatal("leaf should point at bootstrap audit entry")
	}
	entry, ok := sess.inner.EntryByID(*newLeaf)
	if !ok || entry.Base.Type != "thinking_level_change" {
		t.Fatalf("leaf entry = %v, want bootstrap thinking-level audit", entry.Base.Type)
	}
	if res.EditorText != "first" {
		t.Errorf("EditorText = %q, want %q", res.EditorText, "first")
	}
}

func TestNavigateTree_WithSummary(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	sess.completer = &fakeCompleter{summary: "branch summary text"}

	// Append first user message; record its ID as branchPoint.
	userMsg := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:    "user",
			Content: []ai.UserContentBlock{ai.TextContent{Text: "root"}},
		},
	}
	if _, err := sess.inner.AppendMessage(userMsg); err != nil {
		t.Fatal(err)
	}
	branchPoint := *sess.inner.LeafID()

	// Extend the branch.
	userMsg2 := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:    "user",
			Content: []ai.UserContentBlock{ai.TextContent{Text: "diverged"}},
		},
	}
	if _, err := sess.inner.AppendMessage(userMsg2); err != nil {
		t.Fatal(err)
	}

	res, err := sess.NavigateTree(context.Background(), branchPoint, NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatalf("NavigateTree (with summary): %v", err)
	}
	if res.Aborted {
		t.Error("NavigateTree returned Aborted=true unexpectedly")
	}

	// A branch_summary entry should exist on the CURRENT LEAF'S branch
	// (root → ... → branch_summary → leaf). Before the 3.2l fix, the entry
	// was written to the abandoned branch and was invisible after navigation.
	leaf := sess.inner.LeafID()
	if leaf == nil {
		t.Fatal("expected non-nil leaf after NavigateTree with Summarize=true")
	}
	branch := sess.inner.Branch(*leaf)
	foundOnBranch := false
	for _, e := range branch {
		if e.Base.Type == "branch_summary" {
			foundOnBranch = true
			break
		}
	}
	if !foundOnBranch {
		t.Error("branch_summary entry not found on current leaf's branch (would be invisible in rebuildChatFromSession)")
	}
}

func TestNavigateTreeRejectsStreamingSession(t *testing.T) {
	provider := blockingProviderForSessionTests{started: make(chan struct{})}
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModelWithProvider(provider)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	go func() {
		for range sess.Events() { //nolint:revive // the test must drain the production event funnel
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())
	sendDone := make(chan error, 1)
	go func() {
		_, sendErr := sess.Send(ctx, "stream")
		sendDone <- sendErr
	}()
	<-provider.started

	_, navigateErr := sess.NavigateTree(t.Context(), *sess.inner.LeafID(), NavigateTreeOptions{})
	if navigateErr == nil || navigateErr.Error() != "Wait for the current response to finish before navigating the session tree." {
		t.Fatalf("NavigateTree error = %v", navigateErr)
	}
	cancel()
	select {
	case <-sendDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Send did not stop after cancellation")
	}
}

func TestCompactAbortsInFlightManualCompactionAndRestarts(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	completer := newBlockingCompactionCompleter()
	sess.completer = completer
	originalLeafID := *sess.inner.LeafID()

	firstDone := make(chan error, 1)
	go func() { firstDone <- sess.Compact(t.Context(), "first") }()
	<-completer.started
	secondDone := make(chan error, 1)
	go func() { secondDone <- sess.Compact(t.Context(), "second") }()

	if err := <-firstDone; !errors.Is(err, errCompactionCancelled) {
		close(completer.release)
		t.Fatalf("first Compact error = %v, want cancellation", err)
	}
	close(completer.release)
	if err := <-secondDone; err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	assertRestartedCompaction(t, sess, originalLeafID)
}

func TestCompactAbortsInFlightAutoCompactionAndRestarts(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	completer := newBlockingCompactionCompleter()
	sess.completer = completer
	originalLeafID := *sess.inner.LeafID()

	autoDone := make(chan bool, 1)
	go func() { autoDone <- sess.runAutoCompaction(t.Context(), "threshold", false) }()
	<-completer.started
	manualDone := make(chan error, 1)
	go func() { manualDone <- sess.Compact(t.Context(), "manual") }()

	if compacted := <-autoDone; compacted {
		close(completer.release)
		t.Fatal("cancelled auto-compaction reported success")
	}
	close(completer.release)
	if err := <-manualDone; err != nil {
		t.Fatalf("manual Compact: %v", err)
	}
	assertRestartedCompaction(t, sess, originalLeafID)
}

func assertRestartedCompaction(t *testing.T, sess *Session, originalLeafID string) {
	t.Helper()
	entries := sess.inner.Entries()
	compactionEntry := entries[len(entries)-1]
	if compactionEntry.Base.Type != "compaction" || compactionEntry.Base.ParentID == nil || *compactionEntry.Base.ParentID != originalLeafID {
		t.Fatalf("compaction entry = %#v, want parent %q", compactionEntry.Base, originalLeafID)
	}
	var aborted bool
	for _, event := range drainEvents(t, sess) {
		if end, ok := event.(agent.CompactionEndEvent); ok && end.Aborted {
			aborted = true
		}
	}
	if !aborted {
		t.Fatal("missing aborted compaction_end for replaced compaction")
	}
}

func TestCompactAbortsTreeNavigationThenCompactsOriginalLeaf(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	completer := newBlockingCompactionCompleter()
	sess.completer = completer

	var targetID string
	for _, entry := range sess.inner.Entries() {
		if message, ok := entry.AsMessage(); ok && message.Message.Assistant != nil {
			targetID = entry.Base.ID
			break
		}
	}
	originalLeafID := *sess.inner.LeafID()
	navigationDone := make(chan NavigateTreeResult, 1)
	navigationErr := make(chan error, 1)
	go func() {
		result, err := sess.NavigateTree(t.Context(), targetID, NavigateTreeOptions{Summarize: true})
		navigationDone <- result
		navigationErr <- err
	}()
	<-completer.started

	compactDone := make(chan error, 1)
	go func() { compactDone <- sess.Compact(t.Context(), "") }()
	var navigation NavigateTreeResult
	select {
	case navigation = <-navigationDone:
	case err := <-compactDone:
		sess.AbortBranchSummary()
		close(completer.release)
		t.Fatalf("Compact returned before tree navigation settled: %v", err)
	case <-time.After(5 * time.Second):
		sess.AbortBranchSummary()
		close(completer.release)
		t.Fatal("manual compaction did not abort tree navigation")
	}
	if err := <-navigationErr; err != nil {
		close(completer.release)
		t.Fatalf("NavigateTree: %v", err)
	}
	if !navigation.Aborted {
		close(completer.release)
		t.Fatalf("navigation result = %#v, want aborted", navigation)
	}
	close(completer.release)
	if err := <-compactDone; err != nil {
		t.Fatalf("Compact: %v", err)
	}
	entries := sess.inner.Entries()
	compactionEntry := entries[len(entries)-1]
	if compactionEntry.Base.Type != "compaction" || compactionEntry.Base.ParentID == nil || *compactionEntry.Base.ParentID != originalLeafID {
		t.Fatalf("compaction entry = %#v, want parent %q", compactionEntry.Base, originalLeafID)
	}
}

func TestNavigateTreeRejectsManualCompactionBeforeLeafChanges(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	completer := newBlockingCompactionCompleter()
	sess.completer = completer

	var targetID string
	for _, entry := range sess.inner.Entries() {
		if message, ok := entry.AsMessage(); ok && message.Message.Assistant != nil {
			targetID = entry.Base.ID
			break
		}
	}
	if targetID == "" {
		t.Fatal("missing assistant navigation target")
	}
	originalLeafID := *sess.inner.LeafID()
	compactDone := make(chan error, 1)
	go func() { compactDone <- sess.Compact(t.Context(), "") }()
	<-completer.started

	if !sess.IsCompacting() {
		t.Fatal("IsCompacting = false during manual compaction")
	}
	_, navigateErr := sess.NavigateTree(t.Context(), targetID, NavigateTreeOptions{})
	if navigateErr == nil || navigateErr.Error() != "Wait for the current compaction or tree navigation to finish before navigating the session tree." {
		t.Fatalf("NavigateTree error = %v", navigateErr)
	}
	if leaf := sess.inner.LeafID(); leaf == nil || *leaf != originalLeafID {
		t.Fatalf("leaf = %v, want original leaf %q", leaf, originalLeafID)
	}

	close(completer.release)
	if err := <-compactDone; err != nil {
		t.Fatalf("Compact: %v", err)
	}
	entries := sess.inner.Entries()
	compactionEntry := entries[len(entries)-1]
	if compactionEntry.Base.Type != "compaction" || compactionEntry.Base.ParentID == nil || *compactionEntry.Base.ParentID != originalLeafID {
		t.Fatalf("compaction entry = %#v, want parent %q", compactionEntry.Base, originalLeafID)
	}
	if got := assistantMessageTexts(sess.Messages()); !slices.Contains(got, "a2") {
		t.Fatalf("assistant texts = %v, want original leaf message a2", got)
	}
}

func TestNavigateTreeRejectsSecondNavigationWhileFirstWaits(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServices(t), 2)
	defer func() { _ = sess.Close() }()
	completer := newBlockingCompactionCompleter()
	sess.completer = completer

	entries := sess.inner.Entries()
	var firstTargetID, secondTargetID string
	for _, entry := range entries {
		message, ok := entry.AsMessage()
		if !ok {
			continue
		}
		if secondTargetID == "" && message.Message.User != nil {
			secondTargetID = entry.Base.ID
		}
		if firstTargetID == "" && message.Message.Assistant != nil {
			firstTargetID = entry.Base.ID
		}
	}
	if firstTargetID == "" || secondTargetID == "" {
		t.Fatal("missing navigation targets")
	}
	originalLeafID := *sess.inner.LeafID()
	firstDone := make(chan error, 1)
	go func() {
		_, err := sess.NavigateTree(t.Context(), firstTargetID, NavigateTreeOptions{Summarize: true})
		firstDone <- err
	}()
	<-completer.started

	if !sess.IsCompacting() {
		t.Fatal("IsCompacting = false during branch summary")
	}
	_, secondErr := sess.NavigateTree(t.Context(), secondTargetID, NavigateTreeOptions{})
	if secondErr == nil || secondErr.Error() != "Wait for the current compaction or tree navigation to finish before navigating the session tree." {
		t.Fatalf("second NavigateTree error = %v", secondErr)
	}
	if leaf := sess.inner.LeafID(); leaf == nil || *leaf != originalLeafID {
		t.Fatalf("leaf = %v while first navigation waits, want %q", leaf, originalLeafID)
	}

	close(completer.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first NavigateTree: %v", err)
	}
	leaf := sess.inner.LeafID()
	if leaf == nil {
		t.Fatal("first navigation left a nil leaf")
	}
	summary, ok := sess.inner.EntryByID(*leaf)
	if !ok || summary.Base.Type != "branch_summary" || summary.Base.ParentID == nil || *summary.Base.ParentID != firstTargetID {
		t.Fatalf("first navigation leaf = %#v, want branch summary under %q", summary.Base, firstTargetID)
	}
}

func assistantMessageTexts(messages []agent.AgentMessage) []string {
	var texts []string
	for _, message := range messages {
		if message.Assistant == nil {
			continue
		}
		for _, block := range message.Assistant.Content {
			if text, ok := block.(ai.TextContent); ok {
				texts = append(texts, text.Text)
			}
		}
	}
	return texts
}

// ─── 3.2g: Auto-compaction trigger tests ──────────────────────────────────────

// TestCheckCompactionThreshold verifies that a session with high context
// usage emits a CompactionStartEvent when checkCompaction is called.
func TestCheckCompactionThreshold(t *testing.T) {
	svcs := newTestServicesSmallKeep(t)
	sess := buildSessionWithMessages(t, svcs, 5)
	defer func() { _ = sess.Close() }()
	sess.completer = &fakeCompleter{summary: "compact summary"}

	// Override model context window so threshold arithmetic is predictable.
	// contextWindow=128000, reserve=16384 → threshold at 111616 tokens.
	m := fakeModel()
	m.Capabilities.ContextWindow = 128000
	sess.model.Store(m)

	// Fake assistant message: usage that pushes total tokens above threshold.
	// input=100000 + output=15000 = 115000 > 111616 → should compact.
	msg := &agent.AssistantMessage{
		Role:       "assistant",
		StopReason: "stop",
		Provider:   "fake",
		ModelID:    "fake-1",
		Usage: &ai.Usage{
			Input:  100000,
			Output: 15000,
		},
	}

	if _, err := sess.checkCompaction(context.Background(), msg, true, nil); err != nil {
		t.Fatal(err)
	}

	// Should emit start and end, and threshold compaction must not retry the
	// interrupted turn: upstream 0.79.10 only sets willRetry for overflow
	// compact-and-retry recovery, not for over-window successful responses.
	evs := drainEvents(t, sess)
	hasStart := false
	var endEv *agent.CompactionEndEvent
	for _, ev := range evs {
		switch ev := ev.(type) {
		case agent.CompactionStartEvent:
			hasStart = true
		case agent.CompactionEndEvent:
			endEv = &ev
		}
	}
	if !hasStart {
		t.Error("expected CompactionStartEvent after threshold trigger; not found in events")
	}
	if endEv == nil {
		t.Fatal("expected CompactionEndEvent after threshold trigger; not found in events")
	}
	if endEv.Reason != "threshold" {
		t.Errorf("CompactionEndEvent.Reason = %q, want threshold", endEv.Reason)
	}
	if endEv.WillRetry {
		t.Error("threshold compaction set WillRetry=true; want false")
	}
}

// TestCheckCompactionOverflow verifies that an overflow error persistently
// omits the failed attempt with a context_edit, compacts, and asks the post-run
// loop to retry (agent-session.ts _checkCompaction case 1).
func TestCheckCompactionOverflow(t *testing.T) {
	svcs := newTestServicesSmallKeep(t)
	sess := buildSessionWithMessages(t, svcs, 3)
	defer func() { _ = sess.Close() }()
	sess.completer = &fakeCompleter{summary: "overflow summary"}

	m := fakeModel()
	m.Capabilities.ContextWindow = 8000
	sess.model.Store(m)

	overflowMsg := &agent.AssistantMessage{
		Role:         "assistant",
		StopReason:   "error",
		ErrorMessage: "prompt token count of 9000 exceeds the limit of 8000",
		Provider:     "fake",
		ModelID:      "fake-1",
		Timestamp:    time.Now().UnixMilli(),
	}
	overflowID, err := sess.inner.AppendMessage(agent.AgentMessage{Assistant: overflowMsg})
	if err != nil {
		t.Fatal(err)
	}
	sess.refreshContext()
	retryMsg := lastAssistantMessage(sess.agent.Messages())

	continueRun, err := sess.checkCompaction(context.Background(), retryMsg, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !continueRun {
		t.Fatal("overflow recovery did not request a retry")
	}
	omitted := false
	for _, entry := range sess.inner.Entries() {
		var edit icodingagent.ContextEditEntry
		if entry.Base.Type == "context_edit" && json.Unmarshal(entry.Raw(), &edit) == nil && edit.TargetID == overflowID && edit.Replacement == nil {
			omitted = true
		}
	}
	if !omitted {
		t.Fatal("overflow attempt was not omitted with a context_edit")
	}
	for _, m := range sess.agent.Messages() {
		if m.Assistant != nil && m.Assistant.ErrorMessage == overflowMsg.ErrorMessage {
			t.Error("overflow error assistant message still present in agent state")
		}
	}
	if !sess.overflowRecoveryAttempted.Load() {
		t.Error("overflow recovery was not marked as attempted")
	}

	var start, end bool
	for _, ev := range drainEvents(t, sess) {
		switch ev := ev.(type) {
		case agent.CompactionStartEvent:
			start = ev.Reason == "overflow"
		case agent.CompactionEndEvent:
			end = ev.Reason == "overflow" && ev.WillRetry && ev.ErrorMessage == ""
		}
	}
	if !start || !end {
		t.Errorf("overflow compaction events: start=%v end=%v", start, end)
	}
}

// TestOverflowNoDoubleRetry verifies that a second overflow when
// overflowRecoveryAttempted is already true emits a CompactionEndEvent
// with an error message, not another compaction attempt.
func TestOverflowNoDoubleRetry(t *testing.T) {
	svcs := newTestServices(t)
	sess := buildSessionWithMessages(t, svcs, 3)
	defer func() { _ = sess.Close() }()
	sess.completer = &fakeCompleter{summary: "should not be called"}
	var failed *extension.SessionCompactFailedEvent
	sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{
		"session_compact_failed": {func(args ...any) (any, error) {
			event := args[0].(extension.SessionCompactFailedEvent)
			failed = &event
			return nil, nil
		}},
	}}}, t.TempDir()))

	m := fakeModel()
	m.Capabilities.ContextWindow = 8000
	sess.model.Store(m)

	// Simulate that the first recovery was already attempted.
	sess.overflowRecoveryAttempted.Store(true)

	overflowMsg := &agent.AssistantMessage{
		Role:         "assistant",
		StopReason:   "error",
		ErrorMessage: "prompt token count of 9000 exceeds the limit of 8000",
		Provider:     "fake",
		ModelID:      "fake-1",
	}

	if _, err := sess.checkCompaction(context.Background(), overflowMsg, true, nil); err != nil {
		t.Fatal(err)
	}

	evs := drainEvents(t, sess)
	var endEv *agent.CompactionEndEvent
	for _, ev := range evs {
		if ce, ok := ev.(agent.CompactionEndEvent); ok {
			endEv = &ce
		}
	}
	if endEv == nil {
		t.Fatal("expected CompactionEndEvent; not found in events")
	}
	if !strings.Contains(endEv.ErrorMessage, "Context overflow recovery failed") {
		t.Errorf("CompactionEndEvent.ErrorMessage = %q; want 'Context overflow recovery failed'", endEv.ErrorMessage)
	}
	if failed == nil || failed.Reason != "overflow" || failed.Aborted || failed.WillRetry || failed.FromExtension || failed.ErrorMessage != endEv.ErrorMessage {
		t.Fatalf("session_compact_failed = %#v; compaction_end = %#v", failed, endEv)
	}
	// No CompactionStartEvent should have been emitted.
	for _, ev := range evs {
		if _, ok := ev.(agent.CompactionStartEvent); ok {
			t.Error("CompactionStartEvent should NOT be emitted on double-overflow guard")
		}
	}
}

// TestOverflowRecoveryFlagResetsOnNewTurn verifies that overflowRecoveryAttempted
// is cleared at the start of each new Send call and on non-error assistant turns.
// Without these resets a second overflow later in the same session would fail
// immediately with the double-retry guard instead of retrying.
func TestOverflowRecoveryFlagResetsOnNewTurn(t *testing.T) {
	svcs := newTestServices(t)
	sess := buildSessionWithMessages(t, svcs, 3)
	defer func() { _ = sess.Close() }()
	sess.completer = &fakeCompleter{summary: "recovery summary"}

	m := fakeModel()
	m.Capabilities.ContextWindow = 8000
	sess.model.Store(m)

	// Simulate that a prior overflow recovery was attempted.
	sess.overflowRecoveryAttempted.Store(true)

	// A length stop persisted at message_end keeps the guard; a response that
	// is neither an error nor a length stop resets it (agent-session.ts
	// message_end handling).
	lengthMsg := &agent.AssistantMessage{Role: "assistant", StopReason: "length", Provider: "fake", ModelID: "fake-1"}
	if err := sess.persistMessage(agent.AgentMessage{Assistant: lengthMsg}); err != nil {
		t.Fatalf("persist length message: %v", err)
	}
	if !sess.overflowRecoveryAttempted.Load() {
		t.Error("a length stop must not reset overflowRecoveryAttempted")
	}
	nonErrMsg := &agent.AssistantMessage{
		Role:       "assistant",
		StopReason: "stop",
		Provider:   "fake",
		ModelID:    "fake-1",
		Usage:      &ai.Usage{Input: 100, Output: 10},
	}
	if err := sess.persistMessage(agent.AgentMessage{Assistant: nonErrMsg}); err != nil {
		t.Fatalf("persist non-error message: %v", err)
	}
	if sess.overflowRecoveryAttempted.Load() {
		t.Error("overflowRecoveryAttempted should be reset after a non-error assistant turn")
	}

	// Set it again and verify Send() resets it (mirrors agent-session.ts:498
	// reset on message_start role=user).
	sess.overflowRecoveryAttempted.Store(true)
	// Send with background context; fakeProvider returns empty immediately.
	_, _ = sess.Send(context.Background(), "new prompt")
	if sess.overflowRecoveryAttempted.Load() {
		t.Error("overflowRecoveryAttempted should be reset at the start of Send()")
	}
}

// TestPreSendCompactionCheck verifies that Send() checks the last assistant
// message for overflow BEFORE calling agent.Send, even when the previous
// assistant was aborted (Ctrl+C). This mirrors upstream agent-session.ts:1028
// _checkCompaction(lastAssistant, skipAbortedCheck=false).
//
// Scenario: user Ctrl+C'd an overflow turn, then types a new message.
// Without the pre-send check, pig would send into another overflow.
// With it, compaction runs first.
func TestPreSendCompactionCheck(t *testing.T) {
	svcs := newTestServicesSmallKeep(t)
	sess := buildSessionWithMessages(t, svcs, 3)
	defer func() { _ = sess.Close() }()

	fakeComp := &fakeCompleter{summary: "pre-send compact summary"}
	sess.completer = fakeComp

	m := fakeModel()
	m.Capabilities.ContextWindow = 8000
	sess.model.Store(m)

	// Inject an overflow error assistant message into agent state.
	// Scenario: the last turn returned an overflow error. The post-send check
	// would have handled this if the turn completed normally, but if the user
	// aborted or the session was resumed, the overflow sits unprocessed.
	// The pre-send check (skipAbortedCheck=false) catches this.
	overflowMsg := agent.AgentMessage{
		Assistant: &agent.AssistantMessage{
			Role:         "assistant",
			StopReason:   "error",
			ErrorMessage: "This model's maximum context length is 8000 tokens",
			Provider:     "fake",
			ModelID:      "fake-1",
			Timestamp:    time.Now().UnixMilli(),
		},
	}
	if _, err := sess.inner.AppendMessage(overflowMsg); err != nil {
		t.Fatal(err)
	}
	sess.refreshContext()

	// Send a new prompt. The pre-send check detects the overflow, compacts
	// synchronously, and then sends the new prompt. Pi 0.84 awaits
	// _checkCompaction and continues through prompt construction.
	_, _ = sess.Send(context.Background(), "follow up")

	// Verify compaction was called (fakeCompleter.Complete invoked).
	if !fakeComp.called.Load() {
		t.Error("pre-send compaction check should trigger compaction for aborted overflow, but completer was not called")
	}
	foundFollowUp := false
	for _, entry := range sess.inner.Entries() {
		if strings.Contains(string(entry.Raw()), "follow up") {
			foundFollowUp = true
		}
	}
	if !foundFollowUp {
		t.Fatal("pre-send compaction did not continue with the accepted prompt")
	}
}

// TestPreSendCompactionSkipsAborted verifies that the pre-send check does NOT
// skip aborted messages (skipAbortedCheck=false), while the post-send check
// (skipAbortedCheck=true) DOES skip them. This distinction is the reason the
// pre-send check exists.
func TestPreSendCompactionSkipsAborted(t *testing.T) {
	svcs := newTestServicesSmallKeep(t)
	sess := buildSessionWithMessages(t, svcs, 3)
	defer func() { _ = sess.Close() }()

	fakeComp := &fakeCompleter{summary: "aborted threshold compact"}
	sess.completer = fakeComp

	m := fakeModel()
	m.Capabilities.ContextWindow = 8000
	sess.model.Store(m)

	// Aborted message with high usage: exceeds threshold but stopReason is aborted.
	abortedHighUsage := &agent.AssistantMessage{
		Role:       "assistant",
		StopReason: "aborted",
		Provider:   "fake",
		ModelID:    "fake-1",
		Timestamp:  time.Now().UnixMilli(),
		Usage:      &ai.Usage{Input: 7500, Output: 200},
	}

	// Post-send check (skipAbortedCheck=true) should skip.
	_, _ = sess.checkCompaction(context.Background(), abortedHighUsage, true, nil)
	if fakeComp.called.Load() {
		t.Error("post-send check (skipAbortedCheck=true) should skip aborted messages")
	}

	// Pre-send check (skipAbortedCheck=false) should NOT skip.
	_, _ = sess.checkCompaction(context.Background(), abortedHighUsage, false, nil)
	if !fakeComp.called.Load() {
		t.Error("pre-send check (skipAbortedCheck=false) should process aborted messages and trigger threshold compaction")
	}
}

func TestNavigateTree_SummaryJoinsDestinationFromAbandonedLeaf(t *testing.T) {
	// A summary must join the destination branch, not the abandoned branch.
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	sess.completer = &fakeCompleter{summary: "summary of old branch"}

	// Append first user message (parentID=nil after this = first entry).
	firstMsg := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:    "user",
			Content: []ai.UserContentBlock{ai.TextContent{Text: "first question"}},
		},
	}
	if _, err := sess.inner.AppendMessage(firstMsg); err != nil {
		t.Fatal(err)
	}
	firstID := *sess.inner.LeafID() // ID of "first question"

	// Append a second user message to build out the "old branch".
	secondMsg := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:    "user",
			Content: []ai.UserContentBlock{ai.TextContent{Text: "second question"}},
		},
	}
	if _, err := sess.inner.AppendMessage(secondMsg); err != nil {
		t.Fatal(err)
	}
	// Session now: [firstID → secondID]; leaf = secondID.
	abandonedLeafID := *sess.inner.LeafID()

	// Navigate to firstID (user message) with summarize=true. The bootstrap entries precede firstID, so its parent is the destination leaf.
	res, err := sess.NavigateTree(context.Background(), firstID, NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if res.Aborted {
		t.Error("NavigateTree returned Aborted=true unexpectedly")
	}
	if res.EditorText != "first question" {
		t.Errorf("EditorText = %q, want %q", res.EditorText, "first question")
	}

	// The current leaf's branch must contain the bootstrap audit entries and the
	// new branch_summary entry, but not the abandoned user-message path.
	leaf := sess.inner.LeafID()
	if leaf == nil {
		t.Fatal("leaf must not be nil after summarize navigation")
	}
	branch := sess.inner.Branch(*leaf)
	if len(branch) != 3 {
		types := make([]string, len(branch))
		for i, e := range branch {
			types[i] = e.Base.Type
		}
		t.Fatalf("branch len = %d, want 3 (bootstrap audit and branch_summary); types: %v", len(branch), types)
	}
	gotTypes := []string{branch[0].Base.Type, branch[1].Base.Type, branch[2].Base.Type}
	if want := []string{"model_change", "thinking_level_change", "branch_summary"}; !slices.Equal(gotTypes, want) {
		t.Errorf("branch types = %v, want %v", gotTypes, want)
	}
	var summary icodingagent.BranchSummaryEntry
	if err := json.Unmarshal(branch[2].Raw(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.ParentID == nil {
		t.Fatal("branch summary has no destination parent")
	}
	// Upstream branchWithSummary: parentId is the destination and fromId is
	// the abandoned leaf (branch-summary-extensions.test.ts).
	if summary.FromID != abandonedLeafID {
		t.Errorf("branch summary fromId = %q, want abandoned leaf %q", summary.FromID, abandonedLeafID)
	}
}

// TestIsRetryableError verifies that the Session's retry classifier correctly
// classifies transient errors vs non-retryable errors and context overflow.
func TestIsRetryableError(t *testing.T) {
	cases := []struct {
		name      string
		msg       agent.AssistantMessage
		ctxWindow int
		want      bool
	}{
		{
			name: "rate limit error",
			msg:  agent.AssistantMessage{StopReason: "error", ErrorMessage: "429 Too Many Requests"},
			want: true,
		},
		{
			name: "overloaded error",
			msg:  agent.AssistantMessage{StopReason: "error", ErrorMessage: "overloaded_error from Anthropic"},
			want: true,
		},
		{
			name: "503 service unavailable",
			msg:  agent.AssistantMessage{StopReason: "error", ErrorMessage: "503 service unavailable"},
			want: true,
		},
		{
			name: "timeout",
			msg:  agent.AssistantMessage{StopReason: "error", ErrorMessage: "request timed out"},
			want: true,
		},
		{
			name: "no error (normal stop)",
			msg:  agent.AssistantMessage{StopReason: "stop"},
			want: false,
		},
		{
			name: "error but wrong stop reason",
			msg:  agent.AssistantMessage{StopReason: "stop", ErrorMessage: "overloaded"},
			want: false,
		},
		{
			name: "nil message",
			msg:  agent.AssistantMessage{}, // handled via pointer nil below
			want: false,
		},
		{
			name:      "context overflow: not retryable",
			msg:       agent.AssistantMessage{StopReason: "error", ErrorMessage: "maximum context length exceeded"},
			ctxWindow: 100,
			want:      false,
		},
		{
			name: "auth error: not retryable",
			msg:  agent.AssistantMessage{StopReason: "error", ErrorMessage: "invalid api key"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ptr *agent.AssistantMessage
			if tc.msg.StopReason != "" || tc.msg.ErrorMessage != "" {
				cp := tc.msg
				ptr = &cp
			}
			got := icodingagent.IsRetryableError(ptr, tc.ctxWindow)
			if got != tc.want {
				t.Errorf("IsRetryableError(%+v, %d) = %v, want %v",
					tc.msg, tc.ctxWindow, got, tc.want)
			}
		})
	}
}

// TestAutoRetryUsesContinueNotSend verifies that the retry path pops the
// error assistant message and leaves exactly 1 user message, proving
// that Continue (not Send) semantics are correct: no duplicate user messages.
// Correctness fix: upstream uses agent.continue() not agent.send(prompt)
// (agent-session.ts:2501).
func TestAutoRetryUsesContinueNotSend(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	// Simulate the state that exists mid-retry:
	// agent has [user_msg, error_assistant_msg].
	userMsg := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:    "user",
			Content: []ai.UserContentBlock{ai.TextContent{Text: "hello"}},
		},
	}
	errAssistant := agent.AgentMessage{
		Assistant: &agent.AssistantMessage{
			Role:         "assistant",
			StopReason:   "error",
			ErrorMessage: "429 Too Many Requests",
		},
	}
	sess.agent.SetMessages([]agent.AgentMessage{userMsg, errAssistant})

	// Apply the retry-path transform: pop the trailing error assistant message.
	msgs := sess.agent.Messages()
	if last := msgs[len(msgs)-1]; last.Assistant != nil {
		sess.agent.SetMessages(msgs[:len(msgs)-1])
	}

	// After the pop: exactly 1 message (the user_msg) must remain.
	// If Send were called here instead of Continue, it would add a second user_msg.
	after := sess.agent.Messages()
	userCount := 0
	for _, m := range after {
		if m.User != nil {
			userCount++
		}
	}
	if userCount != 1 {
		t.Errorf("after error-pop: expected 1 user message, got %d (len=%d)", userCount, len(after))
	}
	if len(after) != 1 {
		t.Errorf("after error-pop: expected exactly 1 message total, got %d", len(after))
	}
}

// ─── Session metadata + stats tests (3.8) ─────────────────────────────────────

func TestSessionSetGetName(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	if name := sess.SessionName(); name != "" {
		t.Errorf("initial session name should be empty, got %q", name)
	}
	if err := sess.SetSessionName("  my-session  "); err != nil {
		t.Fatalf("SetSessionName: %v", err)
	}
	// Leading/trailing spaces are trimmed on write.
	if name := sess.SessionName(); name != "my-session" {
		t.Errorf("session name after set: got %q, want %q", name, "my-session")
	}
}

func TestSessionSetSessionNameEmptyClearsName(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	if err := sess.SetSessionName("named"); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetSessionName(""); err != nil {
		t.Fatalf("clear Session name: %v", err)
	}
	if name := sess.SessionName(); name != "" {
		t.Fatalf("SessionName after clear = %q", name)
	}
}

func TestSessionSetNameEmptyRejected(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	if err := sess.SetName(""); err == nil {
		t.Error("expected error for empty slash-command name, got nil")
	}
}

func TestLastAssistantTextNilWhenNoMessages(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	if got := sess.LastAssistantText(); got != nil {
		t.Errorf("expected nil, got %q", *got)
	}
}

func TestGetSessionStatsEmpty(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	stats := sess.GetSessionStats()
	if stats.SessionID != sess.ID() {
		t.Errorf("stats.SessionID = %q, want %q", stats.SessionID, sess.ID())
	}
	if stats.TotalMessages != 0 || stats.UserMessages != 0 || stats.AssistantMessages != 0 {
		t.Errorf("expected empty transcript stats, got %+v", stats)
	}
	if stats.Tokens.Total != 0 {
		t.Errorf("expected 0 total tokens, got %d", stats.Tokens.Total)
	}
}

func TestGetSessionStatsFollowsReplaceInner(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()
	if _, err := sess.inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser}}); err != nil {
		t.Fatal(err)
	}

	replacement := icodingagent.NewSession("replacement-stats", t.TempDir())
	if _, err := replacement.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: agent.RoleAssistant,
		Usage: &ai.Usage{
			Input:  7,
			Output: 3,
			Cost:   ai.UsageCost{Total: 0.25},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	sess.ReplaceInner(replacement)

	stats := sess.GetSessionStats()
	if stats.SessionID != "replacement-stats" || stats.TotalMessages != 1 || stats.UserMessages != 0 || stats.AssistantMessages != 1 {
		t.Fatalf("replacement stats = %#v", stats)
	}
	if stats.Tokens.Total != 10 || stats.Cost != 0.25 {
		t.Fatalf("replacement usage = %#v", stats)
	}
}

func TestGetSessionStatsIncludesToolUsageAndCost(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()
	assistantUsage := &ai.Usage{
		Input: 10, Output: 4, CacheRead: 3, CacheWrite: 2,
		Cost: ai.UsageCost{Total: 0.25},
	}
	toolUsage := &ai.Usage{
		Input: 7, Output: 1, CacheRead: 2, CacheWrite: 1,
		Cost: ai.UsageCost{Total: 0.5},
	}
	if _, err := sess.inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role:    agent.RoleAssistant,
		Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call-1", Name: "nested", Arguments: ai.JsonObject{}}},
		Usage:   assistantUsage, StopReason: "toolUse",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.inner.AppendMessage(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role: agent.RoleToolResult, ToolCallID: "call-1", ToolName: "nested", Usage: toolUsage,
	}}); err != nil {
		t.Fatal(err)
	}

	stats := sess.GetSessionStats()
	if stats.AssistantMessages != 1 || stats.ToolCalls != 1 || stats.ToolResults != 1 || stats.TotalMessages != 2 {
		t.Fatalf("message stats = %#v", stats)
	}
	if got, want := stats.Tokens.Total, 30; got != want {
		t.Fatalf("total tokens = %d, want %d", got, want)
	}
	if got, want := stats.Cost, 0.75; got != want {
		t.Fatalf("cost = %v, want %v", got, want)
	}
}

func TestUserMessagesForForkingEmpty(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	msgs := sess.UserMessagesForForking()
	if len(msgs) != 0 {
		t.Errorf("expected 0 fork messages, got %d", len(msgs))
	}
}

func TestRecordBashResultPreservesTruncationProvenance(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	sess.recordBashResult("large output", BashResult{
		Output: "tail", ExitCode: 0, Truncated: true, FullOutputPath: "/tmp/original-output",
	}, false)
	messages := sess.Inner().BuildSessionProjection().Messages
	if len(messages) != 1 || messages[0].Custom["truncated"] != true || messages[0].Custom["fullOutputPath"] != "/tmp/original-output" {
		t.Fatalf("persisted bash projection = %#v", messages)
	}
}

func TestAppendBashFallbackPreservesTruncationProvenance(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServices(t), 1)
	defer func() { _ = sess.Close() }()
	if err := os.Chmod(sess.Path(), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sess.Path(), 0o600) })

	sess.mu.Lock()
	sess.appendBashLocked(pendingBashRecord{command: "large output", result: BashResult{
		Output: "tail", ExitCode: 0, Truncated: true, FullOutputPath: "/tmp/original-output",
	}})
	sess.mu.Unlock()
	messages := sess.Messages()
	last := messages[len(messages)-1]
	if last.Custom["truncated"] != true || last.Custom["fullOutputPath"] != "/tmp/original-output" {
		t.Fatalf("fallback bash message = %#v", last.Custom)
	}
}

func TestExecuteBashBasic(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	ctx := context.Background()
	result, err := sess.ExecuteBash(ctx, "echo hello_from_bash", false)
	if err != nil {
		t.Fatalf("ExecuteBash: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code: got %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Output, "hello_from_bash") {
		t.Errorf("output does not contain expected string: %q", result.Output)
	}
	if result.Cancelled {
		t.Error("result.Cancelled should be false for successful execution")
	}
}

func TestExecuteBashRecordsResultInContext(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()
	ctx := context.Background()

	// excludeFromContext=false: result is recorded into live agent state and
	// persisted as a bash_execution entry (idle path: no turn streaming).
	if _, err := sess.ExecuteBash(ctx, "echo recorded_output", false); err != nil {
		t.Fatalf("ExecuteBash: %v", err)
	}
	msgs := sess.Agent().Messages()
	if len(msgs) == 0 {
		t.Fatal("expected a bashExecution message in agent state, got none")
	}
	last := msgs[len(msgs)-1]
	if last.Custom == nil {
		t.Fatalf("last message is not a custom bashExecution message: %+v", last)
	}
	if role, _ := last.Custom["role"].(string); role != agent.RoleBashExecution {
		t.Fatalf("role: got %q want %q", role, agent.RoleBashExecution)
	}
	if cmd, _ := last.Custom["command"].(string); cmd != "echo recorded_output" {
		t.Fatalf("command not recorded: %q", cmd)
	}
	if out, _ := last.Custom["output"].(string); !strings.Contains(out, "recorded_output") {
		t.Fatalf("output not recorded: %q", out)
	}
	if excl, _ := last.Custom["excludeFromContext"].(bool); excl {
		t.Fatalf("excludeFromContext should be false")
	}
	if !hasBashEntry(sess) {
		t.Fatal("bash_execution entry not persisted to session")
	}

	// excludeFromContext=true: still recorded (so reload/transcript shows it)
	// but flagged so bashExecutionToText drops it from LLM context.
	if _, err := sess.ExecuteBash(ctx, "echo hidden_output", true); err != nil {
		t.Fatalf("ExecuteBash (excluded): %v", err)
	}
	msgs = sess.Agent().Messages()
	last = msgs[len(msgs)-1]
	if excl, _ := last.Custom["excludeFromContext"].(bool); !excl {
		t.Fatalf("excludeFromContext should be true for !!cmd-style bash")
	}
}

func hasBashEntry(sess *Session) bool {
	for _, e := range sess.Inner().Entries() {
		if e.Base.Type == "bash_execution" {
			return true
		}
	}
	return false
}

// When an agent turn holds s.mu (streaming), recordBashResult must buffer the
// record instead of blocking, and flushPendingBashLocked drains it into agent
// state at turn end. Mirrors upstream _pendingBashMessages deferral.
func TestRecordBashResultDeferredWhileStreaming(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	before := len(sess.Agent().Messages())

	// Simulate an in-flight turn holding s.mu. recordBashResult's TryLock
	// must fail and the record must be queued, not block this goroutine.
	sess.mu.Lock()
	done := make(chan struct{})
	go func() {
		sess.recordBashResult("echo deferred", BashResult{
			Output: "deferred_out", ExitCode: 0, Truncated: true, FullOutputPath: "/tmp/deferred-output",
		}, false)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		sess.mu.Unlock()
		t.Fatal("recordBashResult blocked while a turn held s.mu (deferral broken)")
	}

	sess.pendingBashMu.Lock()
	queued := len(sess.pendingBashMessages)
	sess.pendingBashMu.Unlock()
	if queued != 1 {
		sess.mu.Unlock()
		t.Fatalf("expected 1 buffered bash record while streaming, got %d", queued)
	}
	if got := len(sess.Agent().Messages()); got != before {
		sess.mu.Unlock()
		t.Fatalf("deferred bash must not touch agent state mid-turn: before=%d now=%d", before, got)
	}

	// End-of-turn flush (caller holds s.mu).
	sess.flushPendingBashLocked()
	sess.mu.Unlock()

	if got := len(sess.Agent().Messages()); got != before+1 {
		t.Fatalf("flush did not append buffered bash record: before=%d after=%d", before, got)
	}
	last := sess.Agent().Messages()[before]
	if last.Custom["truncated"] != true || last.Custom["fullOutputPath"] != "/tmp/deferred-output" {
		t.Fatalf("flushed bash message = %#v", last.Custom)
	}
}

func TestExecuteBashNonZeroExit(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	ctx := context.Background()
	result, err := sess.ExecuteBash(ctx, "exit 42", false)
	if err != nil {
		t.Fatalf("ExecuteBash: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("exit code: got %d, want 42", result.ExitCode)
	}
}

func TestAbortBashCancels(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	done := make(chan BashResult, 1)
	go func() {
		ctx := context.Background()
		result, _ := sess.ExecuteBash(ctx, "sleep 30", false)
		done <- result
	}()

	// Give the goroutine time to start the process.
	time.Sleep(50 * time.Millisecond)
	sess.AbortBash()

	select {
	case result := <-done:
		if !result.Cancelled {
			t.Error("expected Cancelled=true after AbortBash")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("AbortBash did not unblock ExecuteBash within 3s")
	}
}

func TestReplaceInnerDoesNotRetainOutgoingSystemMessage(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if _, err := sess.Inner().AppendMessage(agent.AgentMessage{System: &ai.SystemMessage{
		Content: ai.SystemText("previous session instructions"),
	}}); err != nil {
		t.Fatal(err)
	}
	sess.RefreshContext()

	replacement := icodingagent.NewSession("replacement", sess.Inner().CWD())
	if _, err := replacement.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{
		Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "existing imported user"}},
	}}); err != nil {
		t.Fatal(err)
	}
	sess.ReplaceInner(replacement)

	projection := replacement.BuildSessionProjection().Messages
	if actual := sess.Messages(); !reflect.DeepEqual(actual, projection) {
		t.Fatalf("replacement messages = %#v, want canonical projection %#v", actual, projection)
	}
}

func TestRefreshContextRetainsSameSessionInstructionBaseline(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	system := agent.AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText("same session instructions")}}
	sess.agent.SetMessages([]agent.AgentMessage{system})
	if _, err := sess.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{
		Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "conversation"}},
	}}); err != nil {
		t.Fatal(err)
	}

	sess.RefreshContext()
	messages := sess.Messages()
	if len(messages) != 2 || messages[0].System == nil || messages[0].System.Content != ai.SystemText("same session instructions") {
		t.Fatalf("same-session refresh messages = %#v", messages)
	}
}

// TestReplaceInnerAbortsOutgoingControllers verifies that switching the
// underlying session (the /resume path) aborts any in-flight compaction and
// branch-summary LLM calls bound to the outgoing session, mirroring upstream
// dispose()-on-switch. Without this, a running compaction would write its
// result into a session that has already been replaced.
func TestReplaceInnerAbortsOutgoingControllers(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	compactCtx, compactCancel := context.WithCancel(context.Background())
	branchCtx, branchCancel := context.WithCancel(context.Background())
	sess.compactMu.Lock()
	sess.compactCancel = compactCancel
	sess.branchSumCancel = branchCancel
	sess.compactMu.Unlock()

	// Swap in the same inner: the test only asserts the abort side effect.
	sess.ReplaceInner(sess.Inner())

	if compactCtx.Err() == nil {
		t.Error("ReplaceInner did not abort the in-flight compaction")
	}
	if branchCtx.Err() == nil {
		t.Error("ReplaceInner did not abort the in-flight branch summary")
	}
}

// extensionWithHandler builds an in-proc extension.Extension whose only job is
// to run fn for the given event type. Used to observe lifecycle dispatch.
func extensionWithHandler(path, event string, fn func(args ...any) (any, error)) extension.Extension {
	return extension.Extension{
		Path:         path,
		ResolvedPath: path,
		Handlers:     map[string][]extension.HandlerFn{event: {fn}},
	}
}

// TestSession_EmitSessionStart_FiresHandler locks the parity fix: the one-shot
// modes (print/json/rpc) call sess.EmitSessionStart, and it must reach the
// extension's session_start handler with the given reason. Before the fix,
// session_start fired only in interactive mode; nothing dispatched it here.
func TestSession_EmitSessionStart_FiresHandler(t *testing.T) {
	svcs := newTestServices(t)
	var gotReason string
	var calls int
	exts := []extension.Extension{
		extensionWithHandler("/fixture/lifecycle", "session_start", func(args ...any) (any, error) {
			calls++
			if evt, ok := args[0].(extension.SessionStartEvent); ok {
				gotReason = evt.Reason
			}
			return nil, nil
		}),
	}
	rt, err := NewRuntime(RuntimeOptions{Services: svcs, NewExtensions: exts})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	sess, err := rt.New(SessionStartOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	sess.EmitSessionStart("startup")

	if calls != 1 {
		t.Fatalf("session_start handler fired %d times, want 1", calls)
	}
	if gotReason != "startup" {
		t.Errorf("session_start reason = %q, want startup", gotReason)
	}
}

// TestSession_EmitSessionShutdown_FiresHandler is the shutdown counterpart:
// the one-shot modes defer sess.EmitSessionShutdown so start/shutdown stay
// paired and extensions don't leak start-without-shutdown.
func TestSession_EmitSessionShutdown_FiresHandler(t *testing.T) {
	svcs := newTestServices(t)
	var gotReason string
	var calls int
	exts := []extension.Extension{
		extensionWithHandler("/fixture/lifecycle", "session_shutdown", func(args ...any) (any, error) {
			calls++
			if evt, ok := args[0].(extension.SessionShutdownEvent); ok {
				gotReason = evt.Reason
			}
			return nil, nil
		}),
	}
	rt, err := NewRuntime(RuntimeOptions{Services: svcs, NewExtensions: exts})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	sess, err := rt.New(SessionStartOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	sess.EmitSessionShutdown("quit")

	if calls != 1 {
		t.Fatalf("session_shutdown handler fired %d times, want 1", calls)
	}
	if gotReason != "quit" {
		t.Errorf("session_shutdown reason = %q, want quit", gotReason)
	}
}

// TestSession_EmitSessionStart_NilRunnerSafe locks that the emit helpers are
// no-ops (not panics) when no extension runner is present.
func TestSession_EmitSessionStart_NilRunnerSafe(t *testing.T) {
	s := &Session{}
	s.EmitSessionStart("startup") // must not panic
	s.EmitSessionShutdown("quit") // must not panic
}

// TestIsRetryableCompactionError verifies the summarization retry classifier:
// transient stream errors are retryable, quota/billing errors are not, and the
// empty string is not. Mirrors upstream isRetryableAssistantError applied to
// compaction (regression #6647).
func TestIsRetryableCompactionError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"", false},
		{"terminated", true},
		{"socket connection was closed", true},
		{"stream ended before a terminal response event", true},
		{"insufficient_quota", false},
		{"Monthly usage limit reached", false},
		{"available balance is too low", false},
		{"some unrelated validation error", false},
	}
	for _, tc := range cases {
		if got := isRetryableCompactionError(tc.msg); got != tc.want {
			t.Errorf("isRetryableCompactionError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

// Abort must surface the manual-operation cancellation, not the summarizer's
// wrapped transport error. The event suppresses that lower-level error.
type abortingCompactionCompleter struct{ abort func() }

func (c abortingCompactionCompleter) CompleteSimple(ctx context.Context, _ *ai.Model, _ string, _ []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
	c.abort()
	return "", nil, ctx.Err()
}
func TestCompactAbortReturnsOperationCancellation(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	sess.completer = abortingCompactionCompleter{abort: sess.AbortCompaction}
	before := len(sess.Inner().Entries())
	_, err := sess.CompactResult(context.Background(), "")
	if err == nil || err.Error() != "Compaction cancelled" {
		t.Fatalf("error = %v", err)
	}
	if len(sess.Inner().Entries()) != before {
		t.Fatal("cancelled summary was persisted")
	}
	var end *agent.CompactionEndEvent
	for _, event := range drainEvents(t, sess) {
		if ev, ok := event.(agent.CompactionEndEvent); ok {
			end = &ev
		}
	}
	if end == nil || !end.Aborted || end.ErrorMessage != "" {
		t.Fatalf("end = %#v", end)
	}
}
