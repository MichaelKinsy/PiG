package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

type contextCaptureProvider struct{ requests []ai.TranscriptContext }

func (*contextCaptureProvider) ID() string   { return "context-capture" }
func (*contextCaptureProvider) Close() error { return nil }
func (p *contextCaptureProvider) Stream(_ context.Context, request ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.requests = append(p.requests, request)
	message := sessionTestMessage(p.ID(), "done", ai.StopReasonStop, "")
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}

func TestSessionContextTransformReachesProviderAndTracksRunnerReplacement(t *testing.T) {
	p := &contextCaptureProvider{}
	runner := inproc.NewRunner([]extension.Extension{{Path: "first", Handlers: map[string][]extension.HandlerFn{
		"context": {func(args ...any) (any, error) {
			event := args[0].(extension.ContextEvent)
			for _, message := range event.Messages {
				if message.(agent.AgentMessage).System != nil {
					t.Fatal("conversation hook saw system")
				}
			}
			return &extension.ContextEventResult{Messages: []extension.AgentMessage{map[string]any{"role": "user", "content": "replacement", "timestamp": 0}}}, nil
		}},
		"context_with_system": {func(args ...any) (any, error) {
			event := args[0].(extension.ContextWithSystemEvent)
			head := event.Messages[0].(agent.AgentMessage).System
			head.Content = ai.SystemText("request only")
			head.Sections = nil
			head.ToolsAdded = nil
			return nil, nil
		}},
	}}}, t.TempDir())
	session, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModelWithProvider(p), SystemPrompt: "original", Runner: runner, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if _, err := session.Send(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 1 {
		t.Fatalf("requests=%d", len(p.requests))
	}
	request := p.requests[0]
	if ai.GetCurrentSystemPrompt(request.Messages()) != "request only" || len(ai.GetCurrentTools(request.Messages())) != 0 {
		t.Fatalf("request=%+v", request)
	}
	if len(request.Messages()) != 2 || request.Messages()[1].(ai.UserMessage).Content.(ai.UserContentBlocks)[0].(ai.TextContent).Text != "replacement" {
		t.Fatalf("messages=%+v", request.Messages())
	}
	if session.Agent().SystemPrompt() != "original" {
		t.Fatal("request hook mutated persistent system prompt")
	}
	session.ReplaceRunner(inproc.NewRunner(nil, t.TempDir()))
	if _, err := session.Send(t.Context(), "second"); err != nil {
		t.Fatal(err)
	}
	if ai.GetCurrentSystemPrompt(p.requests[1].Messages()) != "original" {
		t.Fatal("old runner still transformed request")
	}
}

func TestSessionCloneRetainsContextTransformAndTracksOwnRunner(t *testing.T) {
	provider := &contextCaptureProvider{}
	firstCalls := 0
	var firstSeen []string
	firstRunner := inproc.NewRunner([]extension.Extension{{Path: "first", Handlers: map[string][]extension.HandlerFn{
		"context_with_system": {func(args ...any) (any, error) {
			firstCalls++
			event := args[0].(extension.ContextWithSystemEvent)
			head := event.Messages[0].(agent.AgentMessage).System
			firstSeen = append(firstSeen, ai.GetCurrentSystemPrompt([]ai.Message{*head}))
			head.Content = ai.SystemText("request-first")
			head.Sections = nil
			return nil, nil
		}},
	}}}, t.TempDir())
	session, err := NewSession(newTestServices(t), SessionOptions{
		Model:                fakeModelWithProvider(provider),
		SystemPromptSections: ai.OrderedSections{{Name: "preamble", Value: new("stored")}},
		Runner:               firstRunner,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if _, err := session.Send(t.Context(), "seed"); err != nil {
		t.Fatal(err)
	}

	clone, err := session.Clone()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = clone.Close() }()
	clone.Agent().SetSystemPrompt("forced-clone")
	if _, err := clone.Send(t.Context(), "clone"); err != nil {
		t.Fatal(err)
	}
	if firstCalls != 2 || len(firstSeen) != 2 || firstSeen[1] != "stored" {
		t.Fatalf("clone context calls=%d seen=%v", firstCalls, firstSeen)
	}
	if got := ai.GetCurrentSystemPrompt(provider.requests[len(provider.requests)-1].Messages()); got != "forced-clone" {
		t.Fatalf("clone provider prompt = %q", got)
	}
	var persisted []ai.Message
	for _, message := range clone.Inner().BuildContext(nil) {
		if message.System != nil {
			persisted = append(persisted, *message.System)
		}
	}
	if len(persisted) != 1 || ai.GetCurrentSystemPrompt(persisted) != "stored" {
		t.Fatalf("clone persisted system = %#v", persisted)
	}

	secondCalls := 0
	secondRunner := inproc.NewRunner([]extension.Extension{{Path: "second", Handlers: map[string][]extension.HandlerFn{
		"context_with_system": {func(...any) (any, error) {
			secondCalls++
			return nil, nil
		}},
	}}}, t.TempDir())
	clone.ReplaceRunner(secondRunner)
	if _, err := clone.Send(t.Context(), "clone replacement"); err != nil {
		t.Fatal(err)
	}
	if secondCalls != 1 || firstCalls != 2 {
		t.Fatalf("clone replacement calls: first=%d second=%d", firstCalls, secondCalls)
	}
	if _, err := session.Send(t.Context(), "original still first"); err != nil {
		t.Fatal(err)
	}
	if firstCalls != 3 || secondCalls != 1 {
		t.Fatalf("runner ownership calls: first=%d second=%d", firstCalls, secondCalls)
	}
}

func TestSessionContextTransformCancellationDoesNotCallProvider(t *testing.T) {
	provider := &contextCaptureProvider{}
	entered := make(chan struct{})
	runner := inproc.NewRunner([]extension.Extension{{Path: "wait", Handlers: map[string][]extension.HandlerFn{"context_with_system": {func(args ...any) (any, error) {
		close(entered)
		ctx := args[1].(context.Context)
		<-ctx.Done()
		return nil, ctx.Err()
	}}}}}, t.TempDir())
	session, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModelWithProvider(provider), Runner: runner, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := session.Send(ctx, "hello"); done <- err }()
	<-entered
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled context transform succeeded")
	}
	if len(provider.requests) != 0 {
		t.Fatal("cancelled transform called provider")
	}
	messages := session.Messages()
	last := messages[len(messages)-1]
	if last.Assistant == nil || last.Assistant.StopReason != ai.StopReasonAborted {
		t.Fatalf("terminal message=%+v", last)
	}
}

func TestSessionStructuredContextTransformPrecedesForcedProjection(t *testing.T) {
	// agent-session.ts installs forced prompt projection after context handlers;
	// the provider sees forced text while the transcript retains section state.
	provider := &contextCaptureProvider{}
	var seen string
	runner := inproc.NewRunner([]extension.Extension{{Path: "context", Handlers: map[string][]extension.HandlerFn{
		"context_with_system": {func(args ...any) (any, error) {
			event := args[0].(extension.ContextWithSystemEvent)
			head := event.Messages[0].(agent.AgentMessage).System
			seen = ai.GetCurrentSystemPrompt([]ai.Message{*head})
			head.Sections = ai.OrderedSections{{Name: "preamble", Value: new("transformed")}}
			return nil, nil
		}},
	}}}, t.TempDir())
	session, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModelWithProvider(provider), SystemPromptSections: ai.OrderedSections{{Name: "preamble", Value: new("stored")}}, Runner: runner, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	session.Agent().SetSystemPrompt("forced")
	if _, err := session.Send(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	if seen != "stored" {
		t.Fatalf("context saw %q", seen)
	}
	if len(provider.requests) != 1 || ai.GetCurrentSystemPrompt(provider.requests[0].Messages()) != "forced" {
		t.Fatalf("provider requests = %#v", provider.requests)
	}
	var persisted []ai.Message
	for _, message := range session.Messages() {
		if message.System != nil {
			persisted = append(persisted, *message.System)
		}
	}
	if len(persisted) != 1 || ai.GetCurrentSystemPrompt(persisted) != "stored" {
		t.Fatalf("persisted system = %#v", persisted)
	}
}
