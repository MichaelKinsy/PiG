package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/agent/src/agent-loop.ts:416; packages/coding-agent/src/core/agent-session.ts:916-919. Shutdown must release a reserved Session continuation that the worker never claimed.
func TestSessionObservationReservationCanceledBeforeAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session := &Session{rawEvents: make(chan agent.AgentEvent), closeDone: make(chan struct{})}
		stream := newSessionTestStream(ai.StartEvent{Partial: &ai.AssistantMessage{StopReason: ai.StopReasonPending}}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: &ai.AssistantMessage{StopReason: ai.StopReasonStop}})
		started := make(chan struct{})
		var once sync.Once
		a := agent.NewAgent(agent.AgentOptions{
			Model: &ai.Model{ID: "probe"},
			StreamFn: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				return stream, nil
			},
			OnEvent: func(event agent.AgentEvent) {
				if agent.EventObservation(event) == nil {
					return
				}
				once.Do(func() { close(started) })
				session.emitSynchronousSessionEvent(event, true)
			},
		})
		done := make(chan error, 1)
		go func() { _, err := a.Send(t.Context(), "probe"); done <- err }()
		<-started
		synctest.Wait()
		close(session.closeDone)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

// A claimed callback cannot be discarded as an unclaimed reservation. Close joins it, and its terminal result wait must still advance independently of the Agent sink.
func TestSessionObservationCloseJoinsClaimedCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session := &Session{rawEvents: make(chan agent.AgentEvent), events: make(chan agent.AgentEvent), closeDone: make(chan struct{})}
		stream := newSessionTestStream(ai.StartEvent{Partial: &ai.AssistantMessage{StopReason: ai.StopReasonPending}}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: &ai.AssistantMessage{StopReason: ai.StopReasonStop}})
		entered, release := make(chan struct{}), make(chan struct{})
		var originating *ai.StreamObservation
		unsubscribe := session.Subscribe(func(event agent.AgentEvent) {
			if _, ok := event.(agent.MessageStartEvent); !ok {
				return
			}
			observation := agent.EventObservation(event)
			if observation == nil || observation == originating {
				t.Error("Session subscriber did not receive its active child observation")
			}
			close(entered)
			if err := observation.Await(func() error { <-release; return nil }); err != nil {
				t.Error(err)
			}
			if final := stream.Result(); final.StopReason != ai.StopReasonStop {
				t.Error("held Session callback lost terminal result")
			}
		})
		defer unsubscribe()
		workerDone := make(chan struct{})
		go func() { defer close(workerDone); session.forwardAgentEvents() }()
		a := agent.NewAgent(agent.AgentOptions{
			Model: &ai.Model{ID: "probe"},
			StreamFn: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				return stream, nil
			},
			OnEvent: func(event agent.AgentEvent) {
				if agent.EventObservation(event) != nil {
					originating = agent.EventObservation(event)
					session.emitSynchronousSessionEvent(event, true)
				}
			},
		})
		done := make(chan error, 1)
		go func() { _, err := a.Send(t.Context(), "probe"); done <- err }()
		<-entered
		close(session.closeDone)
		synctest.Wait()
		select {
		case err := <-done:
			t.Errorf("producer escaped held Session callback: %v", err)
			close(release)
			<-workerDone
			return
		default:
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		<-workerDone
	})
}

// upstream: packages/coding-agent/src/core/agent-session.ts:894-919. Public subscribers observe only after the extension's awaited handler returns.
func TestSessionObservesMessageAfterHeldExtension(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			release := make(chan struct{})
			releaseBody := sync.OnceFunc(func() { close(release) })
			defer releaseBody()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				if api == ai.APIOpenAICompletions {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"final\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				} else {
					_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg\",\"role\":\"assistant\",\"content\":[]}}\n\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"final\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
				}
			}))
			defer server.Close()
			var provider ai.Provider
			if api == ai.APIOpenAICompletions {
				provider = ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
			} else {
				provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{BaseURL: server.URL, APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
			}
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			var stream *ai.AssistantMessageEventStream
			var extensionReturned bool
			var before, after, subscriber []byte
			var directContent []ai.AssistantContentBlock
			var directStop ai.StopReason
			var result *ai.AssistantMessage
			ext := extension.Extension{Path: "held-observation", Handlers: map[string][]extension.HandlerFn{
				"message_start": {func(args ...any) (any, error) {
					event := args[0].(extension.MessageStartEvent)
					message := event.Message.(agent.AgentMessage)
					if message.Assistant == nil {
						return nil, nil
					}
					var err error
					before, err = json.Marshal(message)
					if err != nil {
						return nil, err
					}
					releaseBody()
					result = stream.Result()
					after, err = json.Marshal(message)
					extensionReturned = true
					return nil, err
				}},
			}}
			h := newRecoveryHarness(t, harnessOptions{extension: ext})
			h.session.Agent().SetStreamFunction(func(_ context.Context, _ *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				var err error
				stream, err = provider.Stream(ctx, transcript, options)
				return stream, err
			})
			unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
				start, ok := event.(agent.MessageStartEvent)
				if !ok || start.Message.Assistant == nil {
					return
				}
				if !extensionReturned {
					t.Error("subscriber overtook extension await")
				}
				// A JavaScript listener reads message fields directly at this tick; no observation call is needed.
				directContent, directStop = start.Message.Assistant.Content, start.Message.Assistant.StopReason
				var err error
				subscriber, err = json.Marshal(start.Message)
				if err != nil {
					t.Error(err)
				}
			})
			defer unsubscribe()
			if _, err := h.session.Send(ctx, "probe"); err != nil {
				t.Fatal(err)
			}
			var initial, observed agent.AgentMessage
			if err := json.Unmarshal(before, &initial); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(subscriber, &observed); err != nil {
				t.Fatal(err)
			}
			if initial.Assistant.StopReason != ai.StopReasonPending || len(initial.Assistant.Content) != 0 {
				t.Errorf("pre-await=%s; want empty pending", before)
			}
			if observed.Assistant.StopReason != ai.StopReasonPending || len(observed.Assistant.Content) != 1 || observed.Assistant.Content[0].(ai.TextContent).Text != "final" {
				t.Errorf("post-await subscriber=%s; want final content and pending", subscriber)
			}
			if directStop != ai.StopReasonPending || len(directContent) != 1 || directContent[0].(ai.TextContent).Text != "final" {
				t.Errorf("direct field reads = %v/%q; want final content and pending", directContent, directStop)
			}
			if !bytes.Equal(after, subscriber) {
				t.Errorf("extension/public observations differ: %s / %s", after, subscriber)
			}
			if result != stream.Result() || result.StopReason != ai.StopReasonStop {
				t.Errorf("terminal result identity/stopReason=%+v", result)
			}
			var persisted *agent.AssistantMessage
			for _, entry := range h.session.Inner().Entries() {
				message, ok := entry.AsMessage()
				if ok && message.Message.Assistant != nil {
					persisted = message.Message.Assistant
				}
			}
			if persisted == nil || persisted.StopReason != ai.StopReasonStop || len(persisted.Content) != 1 || persisted.Content[0].(ai.TextContent).Text != "final" {
				t.Fatalf("persisted message=%+v", persisted)
			}
		})
	}
}

// upstream: packages/coding-agent/src/core/agent-session.ts:_emitExtensionEvent. A message_update carries its own shallow message, not the retained message_start copy.
func TestSessionExtensionUpdateUsesCurrentMessage(t *testing.T) {
	var usage []int
	ext := extension.Extension{Path: "update-observation", Handlers: map[string][]extension.HandlerFn{
		"message_update": {func(args ...any) (any, error) {
			event := args[0].(extension.MessageUpdateEvent)
			message := event.Message.(agent.AgentMessage)
			usage = append(usage, message.Assistant.Usage.Output)
			return nil, nil
		}},
	}}
	h := newRecoveryHarness(t, harnessOptions{extension: ext})
	h.session.Agent().SetStreamFunction(func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		start := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonPending, Usage: ai.Usage{Output: 1}}
		update := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "delta"}}, StopReason: ai.StopReasonPending, Usage: ai.Usage{Output: 2}}
		final := &ai.AssistantMessage{Content: update.Content, StopReason: ai.StopReasonStop, Usage: ai.Usage{Output: 3}}
		return newSessionTestStream(ai.StartEvent{Partial: start}, ai.TextDeltaEvent{ContentIndex: 0, Delta: "delta", Partial: update}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: final}), nil
	})
	if _, err := h.session.Send(t.Context(), "probe"); err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || usage[0] != 2 {
		t.Fatalf("extension message_update usage=%v; want [2], not message_start [1]", usage)
	}
}
