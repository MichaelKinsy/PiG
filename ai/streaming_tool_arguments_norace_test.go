//go:build !race

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// upstream: packages/ai/test/streaming-tool-arguments.test.ts "allocates linearly in the argument text". The race detector inflates allocations, so the measurement runs without it.

func TestStreamingToolArgumentsAllocateLinearlyInTheArgumentText(t *testing.T) {
	for _, path := range streamingArgumentsPaths {
		t.Run(path.name, func(t *testing.T) {
			run := func(size int) uint64 {
				arguments, text := streamingWriteCall(t, size)
				deltas := streamingDeltas(text, 32)
				body := path.body(deltas, arguments, text)
				// The server and the body are built outside the measured run; the run streams the body once.
				server := sequentialToolCallServer(body)
				defer server.Close()
				return allocatedBytes(func() {
					stream := sequentialToolCallStream(t, path.provider(server.URL))
					for range stream.Events(context.Background()) {
					}
					_ = stream.Result()
				})
			}
			run(5_000) // warm the provider's connection and caches
			small := run(25_000)
			large := run(250_000)
			// Ten times the text: linear growth is about 10x, re-parsing the accumulated text about 100x.
			if ratio := float64(large) / float64(small); ratio >= 25 {
				t.Fatalf("allocation grew %.1fx for ten times the text (%d to %d bytes)", ratio, small, large)
			}
			_, text := streamingWriteCall(t, 250_000)
			if perByte := float64(large) / float64(len(text)); perByte >= 5_000 {
				t.Fatalf("%.0f bytes allocated per byte of arguments", perByte)
			}
		})
	}
}

// upstream: constrained-sampling.test.ts "streams custom tool input in time and memory linear in its length" (#9265).
func TestCustomToolInputStreamsInMemoryLinearInItsLength(t *testing.T) {
	run := func(size int) uint64 {
		var frames []string
		frames = append(frames, responseScratchFrame("output_item.added", `"output_index":0,"item":{"type":"custom_tool_call","call_id":"call_1","id":"ctc_1","name":"sample_tool","input":""}`))
		for index := range size / 32 {
			delta := fmt.Sprintf("chunk %d \"quoted\" \\ \n", index)
			delta += strings.Repeat("x", 32-len(fmt.Sprintf("chunk %d \"quoted\" \\ \n", index)))
			encoded, _ := json.Marshal(delta)
			frames = append(frames, responseScratchFrame("custom_tool_call_input.delta", `"output_index":0,"item_id":"ctc_1","delta":`+string(encoded)))
		}
		frames = append(frames, responseScratchFrame("completed", `"response":{"status":"completed"}`))
		builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "scratch-provider", "scratch-model")
		reader := &providerScratchReader{frames: frames}
		return allocatedBytes(func() {
			p := &openAIResponsesProvider{}
			p.parseResponsesSSE(t.Context(), reader, builder, map[string]string{"sample_tool": "payload"})
		})
	}
	run(25_600)
	small := run(25_600)
	large := run(256_000)
	if ratio := float64(large) / float64(small); ratio >= 25 {
		t.Fatalf("allocation grew %.1fx for ten times the input (%d to %d bytes)", ratio, small, large)
	}
}
