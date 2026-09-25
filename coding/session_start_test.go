package coding

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Upstream sdk.ts constructs Agent with empty prompt/tools and existing messages;
// AgentSession.prompt prepends its initial system update only when a prompt runs.
func TestSessionStartupDefersSystemUntilPrompt(t *testing.T) {
	services := newTestServices(t)
	p := &scriptedProvider{responses: []scriptedResponse{fauxReply("first", ai.StopReasonStop, 0), fauxReply("second", ai.StopReasonStop, 0), fauxReply("resumed", ai.StopReasonStop, 0)}}
	model := fakeModelWithProvider(p)
	sess, err := NewSession(services, SessionOptions{Model: model, SystemPrompt: "configured instructions"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	if len(sess.Messages()) != 0 || sess.GetSessionStats().TotalMessages != 0 || len(sess.Inner().BuildSessionProjection().Messages) != 0 {
		t.Fatalf("fresh session already has transcript: %#v; stats=%+v", sess.Messages(), sess.GetSessionStats())
	}
	// Interactive uses the same Agent directly, so this must not depend on Session.Send.
	if _, err := sess.Agent().Send(context.Background(), "first prompt"); err != nil {
		t.Fatal(err)
	}
	assertStartupTranscript(t, sess, []string{"system", "user", "assistant"})
	if _, err := sess.Send(context.Background(), "second prompt"); err != nil {
		t.Fatal(err)
	}
	assertStartupTranscript(t, sess, []string{"system", "user", "assistant", "user", "assistant"})
	path := sess.Path()
	resumed, err := NewSession(services, SessionOptions{Model: model, SystemPrompt: "configured instructions", ResumePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resumed.Close(); err != nil {
			t.Error(err)
		}
	}()
	assertStartupTranscript(t, resumed, []string{"system", "user", "assistant", "user", "assistant"})
	if _, err := resumed.Send(context.Background(), "resume prompt"); err != nil {
		t.Fatal(err)
	}
	assertStartupTranscript(t, resumed, []string{"system", "user", "assistant", "user", "assistant", "user", "assistant"})
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, raw := range p.requests {
		var messages []struct {
			Role     string            `json:"role"`
			Content  any               `json:"content"`
			Sections map[string]string `json:"sections"`
		}
		if err := json.Unmarshal([]byte(raw), &messages); err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, message := range messages {
			if message.Role == "system" {
				count++
			}
		}
		if count != 1 || messages[0].Role != "system" || messages[0].Content != "" || messages[0].Sections["preamble"] != "configured instructions" {
			t.Fatalf("provider baseline %s", raw)
		}
	}
}
func assertStartupTranscript(t *testing.T, s *Session, want []string) {
	t.Helper()
	roles := func(messages []agent.AgentMessage) []string {
		out := []string{}
		for _, m := range messages {
			out = append(out, m.Role())
		}
		return out
	}
	if got := roles(s.Messages()); !reflect.DeepEqual(got, want) {
		t.Fatalf("live roles %v, want %v", got, want)
	}
	if got := roles(s.Inner().BuildSessionProjection().Messages); !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted roles %v, want %v", got, want)
	}
	system := s.Messages()[0].System
	if got := ai.GetCurrentSystemPrompt([]ai.Message{*system}); got != "configured instructions" {
		t.Fatalf("instructions %q", got)
	}
	if len(system.ToolsAdded) != len(s.Tools()) {
		t.Fatalf("system tools %d, active %d", len(system.ToolsAdded), len(s.Tools()))
	}
}
func TestSessionNewSessionClearsSystemUntilNextPrompt(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), SystemPrompt: "configured instructions"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := sess.Send(context.Background(), "before"); err != nil {
		t.Fatal(err)
	}
	if err := sess.NewSession(""); err != nil {
		t.Fatal(err)
	}
	if len(sess.Messages()) != 0 || sess.GetSessionStats().TotalMessages != 0 {
		t.Fatalf("new session retained transcript: %+v", sess.Messages())
	}
	sess.RefreshContext()
	if len(sess.Messages()) != 0 {
		t.Fatal("refresh restored previous baseline")
	}
	if _, err := sess.Send(context.Background(), "after"); err != nil {
		t.Fatal(err)
	}
	assertStartupTranscript(t, sess, []string{"system", "user", "assistant"})
}

func TestSessionRejectedFirstPromptKeepsTranscriptEmpty(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{SystemPrompt: "configured instructions"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := sess.Send(context.Background(), "no model"); err == nil {
		t.Fatal("expected missing-model failure")
	}
	if len(sess.Messages()) != 0 || sess.GetSessionStats().TotalMessages != 0 {
		t.Fatal("rejected prompt published system or user")
	}
}

func TestSessionFirstPromptWithoutToolsPublishesSystemLifecycleOnce(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), SystemPrompt: "configured instructions", SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	sess.Agent().QueueNextTurn(agent.AgentMessage{Custom: map[string]any{"role": "custom", "customType": "queued", "content": "context"}})
	if _, err := sess.Send(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	assertStartupTranscript(t, sess, []string{"system", "user", "custom", "assistant"})
	count := 0
	for {
		select {
		case event := <-sess.Events():
			if end, ok := event.(agent.MessageEndEvent); ok && end.Message.System != nil {
				count++
			}
			if _, ok := event.(agent.AgentSettledEvent); ok {
				if count != 1 {
					t.Fatalf("system message_end count %d", count)
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("missing settled event")
		}
	}
}

func BenchmarkSessionStartupWithoutTranscript(b *testing.B) {
	b.Setenv("PIG_HOME", b.TempDir())
	services, err := NewServices(ServicesOptions{CWD: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	model := fakeModel()
	b.ReportAllocs()
	for b.Loop() {
		sess, err := NewSession(services, SessionOptions{Model: model, SystemPrompt: "configured instructions", NoSession: true})
		if err != nil {
			b.Fatal(err)
		}
		if len(sess.Messages()) != 0 {
			b.Fatal("startup transcript is nonempty")
		}
		if err := sess.Close(); err != nil {
			b.Fatal(err)
		}
		for range sess.Events() {
		} // Join the owned forwarding goroutine each iteration.
	}
}

func TestSessionCloneBeforePromptRetainsDeferredInstructions(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), SystemPrompt: "configured instructions"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	cloned, err := sess.Clone()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cloned.Close(); err != nil {
			t.Error(err)
		}
	}()
	if len(cloned.Messages()) != 0 {
		t.Fatal("clone published unprompted system message")
	}
	if _, err := cloned.Send(context.Background(), "cloned first prompt"); err != nil {
		t.Fatal(err)
	}
	assertStartupTranscript(t, cloned, []string{"system", "user", "assistant"})
	if len(sess.Messages()) != 0 {
		t.Fatal("clone changed original transcript")
	}
}

func TestSessionResumeEmptySystemDoesNotInsertConfiguredBaseline(t *testing.T) {
	services := newTestServices(t)
	provider := &transcriptCaptureProvider{}
	model := fakeModelWithProvider(provider)
	options := SessionOptions{Model: model, SystemPrompt: "configured instructions", SkipBuiltinTools: true}
	sess, err := NewSession(services, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Inner().AppendMessage(agent.AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText(""), Timestamp: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "saved"}}, StopReason: ai.StopReasonStop}}); err != nil {
		t.Fatal(err)
	}
	options.ResumePath = sess.Path()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := NewSession(services, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resumed.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := resumed.SetModel(model); err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.Send(context.Background(), "preserve empty"); err != nil {
		t.Fatal(err)
	}
	if got := ai.GetCurrentSystemPrompt(provider.requests[0].Messages()); got != "" {
		t.Fatalf("empty resumed system replaced by %q", got)
	}
	systems := 0
	for _, message := range resumed.Messages() {
		if message.System != nil {
			systems++
		}
	}
	if systems != 1 {
		t.Fatalf("system entries %d, want only explicit empty baseline", systems)
	}
}

func TestSessionFirstHookOverridesProviderPromptWithoutDuplicatingBaseline(t *testing.T) {
	services := newTestServices(t)
	provider := &transcriptCaptureProvider{}
	runner := inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		event := args[0].(extension.BeforeAgentStartEvent)
		if event.SystemPrompt != "configured instructions" {
			t.Fatalf("hook baseline %q", event.SystemPrompt)
		}
		return &extension.BeforeAgentStartEventResult{SystemPrompt: new("forced instructions")}, nil
	}}}}}, t.TempDir())
	sess, err := NewSession(services, SessionOptions{Model: fakeModelWithProvider(provider), SystemPrompt: "configured instructions", Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := sess.Send(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	assertStartupTranscript(t, sess, []string{"system", "user", "assistant"})
	request := provider.requests[0].Messages()
	if got := ai.GetCurrentSystemPrompt(request); got != "forced instructions" {
		t.Fatalf("provider forced prompt %q", got)
	}
	systems := 0
	for _, message := range request {
		if _, ok := message.(ai.SystemMessage); ok {
			systems++
		}
	}
	if systems != 1 || len(ai.GetCurrentTools(request)) != len(sess.Tools()) {
		t.Fatalf("forced request systems=%d tools=%d", systems, len(ai.GetCurrentTools(request)))
	}
}
