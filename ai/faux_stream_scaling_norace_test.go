//go:build !race

package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// A faux tool call streams its arguments in chunks, and each chunk publishes the call's partial arguments, so what a chunk costs must not grow with the arguments it extends. The text holds characters of every UTF-8 length, so chunks that end inside a character (as byte offsets do) would show a broken string that no later copy may share, and every such chunk would copy and check the whole text again: allocations quadratic in the text. The race detector inflates allocations, so the measurement runs without it.
func TestScalingFauxToolCallWithMultibyteArguments(t *testing.T) {
	testscaling.Check(t, testscaling.Case{Name: "faux tool call, multibyte text arguments", Size: 2000, Bound: testscaling.Linear, Prepare: func(repeats int) func() {
		text := strings.Repeat("ab🦀é-", repeats)
		provider := NewFauxProvider(FauxConfig{})
		provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{
			Content: []FauxContentBlock{FauxToolCall("echo", map[string]any{"text": text}, &FauxToolCallOptions{ID: "call-1"})}, StopReason: "toolUse",
		})})
		return func() {
			stream, err := provider.Stream(context.Background(), emptyTranscript(), StreamOptions{})
			if err != nil {
				panic(err)
			}
			for range stream.Events(context.Background()) {
			}
			result := stream.Result()
			if result.StopReason != StopReasonToolUse {
				panic("stop reason " + string(result.StopReason) + ": " + result.ErrorMessage)
			}
			if call, ok := result.Content[0].(ToolCall); !ok || call.Arguments["text"] != text {
				panic("the streamed arguments are not the text the call carried")
			}
		}
	}})
}
