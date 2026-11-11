package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// BenchmarkServeBridgeStream serves one 200-delta response per iteration through serveBridgeStream, the bridge's whole consumer path, so the cost of creating the stream and attaching to it in a stream continuation shows in ns/op and allocs/op against the straight-line attach.
func BenchmarkServeBridgeStream(b *testing.B) {
	var body strings.Builder
	for index := range 200 {
		fmt.Fprintf(&body, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"w%d \"}}]}\n\n", index)
	}
	body.WriteString("data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":200,\"total_tokens\":203}}\n\ndata: [DONE]\n\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, body.String())
	}))
	b.Cleanup(server.Close)
	var line providerBridgeRequest
	if err := json.Unmarshal([]byte(bridgeLine("bench", "stream", true, server.URL)), &line); err != nil {
		b.Fatal(err)
	}
	events := 0
	send := func(reply providerBridgeReply) bool {
		if reply.Type == "event" {
			events++
		}
		return true
	}
	var callbacks bridgeCallbacks
	b.ReportAllocs()
	for b.Loop() {
		events = 0
		serveBridgeStream(context.Background(), line, "", send, &callbacks)
		if events < 200 {
			b.Fatalf("%d events", events)
		}
	}
}
