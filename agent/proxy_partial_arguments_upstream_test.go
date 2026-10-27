package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/agent/test/proxy.test.ts "shows growing partial arguments without keeping the raw text on the block, in linear time" (#9265).
func TestStreamProxyShowsGrowingPartialArgumentsWithoutKeepingTheRawTextOnTheBlockInLinearTime(t *testing.T) {
	content := strings.Repeat("line \"quoted\" \\ end\n", 20_000)
	encodedContent, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	text := `{"path":"a.ts","content":` + string(encodedContent) + `}`
	events := []ProxyAssistantMessageEvent{{Type: "start"}, {Type: "toolcall_start", ContentIndex: 0, ID: "call_1", ToolName: "write"}}
	for index := 0; index < len(text); index += 32 {
		events = append(events, ProxyAssistantMessageEvent{Type: "toolcall_delta", ContentIndex: 0, Delta: text[index:min(index+32, len(text))]})
	}
	events = append(events, ProxyAssistantMessageEvent{Type: "done", Reason: ai.StopReasonToolUse})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeProxyEvents(t, writer, events, true)
	}))
	defer server.Close()

	started := time.Now()
	stream := StreamProxy(t.Context(), proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{AuthToken: "test-token", ProxyURL: server.URL})
	shown, deltas := 0, 0
	for event := range stream.Events(t.Context()) {
		delta, ok := event.(ai.ToolCallDeltaEvent)
		if !ok {
			continue
		}
		// The projection reads the block's arguments in place: observing a copy of the partial on every delta would make this test itself quadratic.
		blocks, _, _ := delta.Partial.ObserveProjection(nil)
		partial, isString := blocks[0].Arguments["content"].(string)
		if deltas%500 == 0 {
			observed := delta.Partial.Observe()
			encoded, err := json.Marshal(observed.Content[0])
			if err != nil {
				t.Fatal(err)
			}
			var members map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &members); err != nil {
				t.Fatal(err)
			}
			if len(members) != 4 || members["arguments"] == nil || members["id"] == nil || members["name"] == nil || members["type"] == nil {
				t.Fatalf("block members %.200s, want arguments, id, name and type only", encoded)
			}
		}
		if !isString {
			continue
		}
		// Comparing the whole text on every delta would make this test itself quadratic.
		if deltas%500 == 0 && !strings.HasPrefix(content, partial) {
			t.Fatalf("partial content is not a prefix of the file")
		}
		deltas++
		if len(partial) < shown {
			t.Fatalf("partial content shrank from %d to %d bytes", shown, len(partial))
		}
		shown = len(partial)
	}
	result := stream.Result()
	if shown <= len(content)-64 {
		t.Fatalf("shown %d of %d bytes", shown, len(content))
	}
	call, ok := result.Content[0].(ai.ToolCall)
	if !ok || len(result.Content) != 1 || call.ID != "call_1" || call.Name != "write" || call.Arguments["content"] != content || call.Arguments["path"] != "a.ts" {
		t.Fatalf("result content = %#v", result.Content)
	}
	// Re-parsing the accumulated 520 KB of arguments on each of its 16,000 deltas took minutes.
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("took %s", elapsed)
	}
}
