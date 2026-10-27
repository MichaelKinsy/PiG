//go:build !race && !pig_strip_bedrock_converse_stream

package ai

import "testing"

// upstream: packages/ai/test/streaming-tool-arguments.test.ts "allocates linearly in the argument text" (Bedrock). The race detector inflates allocations, so the measurement runs without it.
func TestBedrockStreamingToolArgumentsAllocateLinearlyInTheArgumentText(t *testing.T) {
	run := func(size int) uint64 {
		_, text := streamingWriteCall(t, size)
		events := bedrockStreamStateEvents(text)
		return allocatedBytes(func() { runBedrockEvents(t, events...) })
	}
	run(5_000)
	small := run(25_000)
	large := run(250_000)
	if ratio := float64(large) / float64(small); ratio >= 25 {
		t.Fatalf("allocation grew %.1fx for ten times the text (%d to %d bytes)", ratio, small, large)
	}
	_, text := streamingWriteCall(t, 250_000)
	if perByte := float64(large) / float64(len(text)); perByte >= 5_000 {
		t.Fatalf("%.0f bytes allocated per byte of arguments", perByte)
	}
}
