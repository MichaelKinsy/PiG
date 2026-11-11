package ai

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// BenchmarkFauxToolCallMultibyteArguments streams one faux tool call whose text arguments hold characters of every UTF-8 length, as the SDK conformance scenario that echoes 180 KB does (20000 repetitions). The time per byte is flat when each chunk costs what it adds and grows with the text when a chunk copies or checks the arguments again. Run it with and without -race to see the race detector's constant factor apart from any growth.
func BenchmarkFauxToolCallMultibyteArguments(b *testing.B) {
	for _, repeats := range []int{2000, 5000, 10000, 20000} {
		b.Run(fmt.Sprintf("%dKB", repeats*len("ab🦀é-")/1000), func(b *testing.B) {
			text := strings.Repeat("ab🦀é-", repeats)
			provider := NewFauxProvider(FauxConfig{})
			b.SetBytes(int64(len(text)))
			b.ReportAllocs()
			for b.Loop() {
				provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{
					Content: []FauxContentBlock{FauxToolCall("echo", map[string]any{"text": text}, &FauxToolCallOptions{ID: "call-1"})}, StopReason: "toolUse",
				})})
				stream, err := provider.Stream(context.Background(), emptyTranscript(), StreamOptions{})
				if err != nil {
					b.Fatal(err)
				}
				for range stream.Events(context.Background()) {
				}
				if result := stream.Result(); result.StopReason != StopReasonToolUse {
					b.Fatalf("stop reason %s: %s", result.StopReason, result.ErrorMessage)
				}
			}
		})
	}
}
