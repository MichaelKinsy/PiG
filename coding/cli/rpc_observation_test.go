package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:916-923; packages/coding-agent/src/modes/rpc/rpc-mode.ts:354-363.
func TestRPCSubscriberObservesBeforePersistenceAndChannelDrain(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: &ai.Model{ID: "test", Provider: fakeProviderForRPC{id: "example"}}, SkipBuiltinTools: true, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	var frames []string
	unsubscribe := subscribeRPCEvents(session, func(frame any) {
		raw, marshalErr := json.Marshal(frame)
		if marshalErr != nil {
			t.Error(marshalErr)
			return
		}
		var record struct {
			Type    string             `json:"type"`
			Message agent.AgentMessage `json:"message"`
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Error(err)
			return
		}
		if record.Message.Assistant == nil || (record.Type != "message_start" && record.Type != "message_end") {
			return
		}
		frames = append(frames, record.Type+":"+string(record.Message.Assistant.StopReason))
		if record.Type == "message_end" {
			for _, entry := range session.Inner().GetEntries() {
				if message, ok := entry.(icodingagent.MessageEntry); ok && message.Message.Assistant != nil {
					t.Error("RPC serialized message_end only after Session persisted it")
				}
			}
		}
	}, func(err error) { t.Error(err) })
	defer unsubscribe()
	if _, err := session.Send(t.Context(), "probe"); err != nil {
		t.Fatal(err)
	}
	want := []string{"message_start:pending", "message_end:stop"}
	if !slices.Equal(frames, want) {
		t.Fatalf("RPC output before Events drain=%v; want %v", frames, want)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for event := range session.Events() {
			coding.AcknowledgeEvent(event)
		}
	}()
	if err := session.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	<-drained
	if !slices.Equal(frames, want) {
		t.Fatalf("channel drain duplicated RPC records: %v", frames)
	}
}

// upstream: packages/coding-agent/src/modes/rpc/rpc-mode.ts:354-363. Each conversion observes current retained data and owns its encoded frame.
func TestRPCObservationDuringHeldAgentSink(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
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
			var first, after, end []byte
			a := mustNewAgent(agent.AgentOptions{
				Model: &ai.Model{ID: "probe", Provider: provider},
				StreamFn: func(ctx context.Context, _ *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
					var err error
					stream, err = provider.Stream(ctx, transcript, options)
					return stream, err
				},
				OnEvent: func(event agent.AgentEvent) {
					switch value := event.(type) {
					case agent.MessageStartEvent:
						if value.Message.Assistant == nil {
							return
						}
						frame, err := rpcAgentEvent(event)
						if err != nil {
							t.Error(err)
							return
						}
						first, err = json.Marshal(frame)
						if err != nil {
							t.Error(err)
						}
						releaseBody()
						_ = stream.Result()
						frozen, err := json.Marshal(frame)
						if err != nil || !bytes.Equal(first, frozen) {
							t.Errorf("first frame revised after result: %s / %s (%v)", first, frozen, err)
						}
						frame, err = rpcAgentEvent(event)
						if err != nil {
							t.Error(err)
							return
						}
						after, err = json.Marshal(frame)
						if err != nil {
							t.Error(err)
						}
					case agent.MessageEndEvent:
						if value.Message.Assistant == nil {
							return
						}
						frame, err := rpcAgentEvent(event)
						if err != nil {
							t.Error(err)
							return
						}
						end, err = json.Marshal(frame)
						if err != nil {
							t.Error(err)
						}
					}
				},
			})
			if _, err := a.Send(ctx, "probe"); err != nil {
				t.Fatal(err)
			}
			decode := func(raw []byte) agent.AgentMessage {
				t.Helper()
				var records []struct {
					Message agent.AgentMessage `json:"message"`
				}
				if err := json.Unmarshal(raw, &records); err != nil {
					t.Fatal(err)
				}
				if len(records) != 1 || records[0].Message.Assistant == nil {
					t.Fatalf("invalid frame %s", raw)
				}
				return records[0].Message
			}
			initial, observed, terminal := decode(first), decode(after), decode(end)
			if initial.Assistant.StopReason != ai.StopReasonPending || len(initial.Assistant.Content) != 0 {
				t.Errorf("first frame=%s; want empty pending", first)
			}
			if observed.Assistant.StopReason != ai.StopReasonPending || len(observed.Assistant.Content) != 1 || observed.Assistant.Content[0].(ai.TextContent).Text != "final" {
				t.Errorf("held frame=%s; want final content and pending", after)
			}
			if terminal.Assistant.StopReason != ai.StopReasonStop || len(terminal.Assistant.Content) != 1 || terminal.Assistant.Content[0].(ai.TextContent).Text != "final" {
				t.Errorf("terminal frame=%s; want final content and stop", end)
			}
		})
	}
}
