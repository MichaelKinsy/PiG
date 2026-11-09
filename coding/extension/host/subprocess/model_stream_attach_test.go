package subprocess

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/streamattachhook/attachstall"
)

// A subprocess extension that calls Pi's streamSimple gets the stream and `for await`s it in one synchronous prefix, so the start event it reads holds the partial the provider built, however long the response takes. The host stream the modelStream call serves must forward start the same way: a forwarder that attached after the provider had finished would send the finished message as the start event's partial.
func TestModelStreamForwardsStartBeforeTheResponseFillsItsPartial(t *testing.T) {
	models, model := attachstall.ModelsOverOpenAI(t)
	attachstall.Install(t)
	actions := &HostCallbacks{StreamModel: func(ctx context.Context, _, _ map[string]any) (*ai.AssistantMessageEventStream, error) {
		return models.StreamSimple(ctx, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}, ai.StreamOptions{APIKey: "test"}), nil
	}}

	host, peer := net.Pipe()
	defer func() { _ = host.Close() }()
	defer func() { _ = peer.Close() }()
	conn := NewConn("model-stream", host)
	conn.Start(t.Context())
	defer func() { _ = conn.Close("test done") }()
	bridge := NewUIBridge(func() {})

	events := make(chan json.RawMessage, 16)
	go func() {
		defer close(events)
		for {
			env, err := readEnvelope(peer)
			if err != nil {
				return
			}
			if env.Notify == nil || env.Notify.Method != "model_stream_event" {
				continue
			}
			var args struct {
				Event json.RawMessage `json:"event"`
			}
			if err := json.Unmarshal(env.Notify.Args, &args); err == nil {
				events <- args.Event
			}
		}
	}()

	args, err := json.Marshal(ModelStreamCall{StreamID: "s1", Simple: true, Model: map[string]any{"provider": "probe", "id": "probe-1"}, Request: json.RawMessage(`{"messages":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := bridge.handleModelStream(t.Context(), conn, actions, args); err != nil || result == nil || result.Error != nil {
		t.Fatalf("handleModelStream = %v, %v", result, err)
	}
	_ = host.Close()
	first, ok := <-events
	if !ok {
		t.Fatal("no model_stream_event reached the extension")
	}
	var start struct {
		Type    string `json:"type"`
		Partial struct {
			StopReason string `json:"stopReason"`
			Content    []any  `json:"content"`
		} `json:"partial"`
	}
	if err := json.Unmarshal(first, &start); err != nil {
		t.Fatal(err)
	}
	if start.Type != "start" || start.Partial.StopReason != "pending" || len(start.Partial.Content) != 0 {
		t.Fatalf("first model_stream_event %s: want start with the pending, empty partial the provider built", first)
	}
}
