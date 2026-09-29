package subprocess

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestNodeRawGoogleThinkingReachesHost(t *testing.T) {
	shortSockDir(t)
	host := newTestHost(t)
	bridge := NewUIBridge(func() {})
	var received map[string]any
	bridge.SetHostAction("streamModel", func(_ context.Context, _ map[string]any, request map[string]any) (*ai.AssistantMessageEventStream, error) {
		received = request
		message := &ai.AssistantMessage{StopReason: ai.StopReasonStop, Content: []ai.AssistantContentBlock{}}
		stream := ai.NewAssistantMessageEventStream()
		if err := stream.Push(ai.StartEvent{Partial: message}); err != nil {
			return nil, err
		}
		if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}); err != nil {
			return nil, err
		}
		return stream, nil
	})
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")
	ext, err := host.Load(t.Context(), ExtConfig{Name: "node-native-options", Source: filepath.Join("testdata", "node-native-options.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := ext.Commands["native-options"].Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if received["reasoning"] != "off" || received["reasoningEffort"] != "xhigh" || !reflect.DeepEqual(received["thinking"], map[string]any{"enabled": true, "budgetTokens": float64(0)}) {
		t.Fatalf("native options = %#v", received)
	}
}
