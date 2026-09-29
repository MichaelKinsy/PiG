package ai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// BenchmarkGoogleStream10kDeltas streams 10,000 text records from one response body through the provider and a plain consumer, as the plan's 10k-delta profile does.
func BenchmarkGoogleStream10kDeltas(b *testing.B) {
	var body strings.Builder
	for i := range 10000 {
		fmt.Fprintf(&body, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"t%d \"}]}}]}\r\n\r\n", i)
	}
	body.WriteString("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"end\"}]},\"finishReason\":\"STOP\"}]}\r\n\r\n")
	payload := body.String()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()
	provider := NewGoogleProvider(GoogleConfig{BaseURL: server.URL, APIKey: "test", Model: "gemini-2.5-flash", ProviderID: "google"})
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
	b.ReportAllocs()
	for b.Loop() {
		stream, err := provider.Stream(context.Background(), transcript, StreamOptions{})
		if err != nil {
			b.Fatal(err)
		}
		events := 0
		for range stream.Events(context.Background()) {
			events++
		}
		if events < 10000 {
			b.Fatalf("events = %d", events)
		}
	}
}
