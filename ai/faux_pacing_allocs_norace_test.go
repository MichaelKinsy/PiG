//go:build !race

package ai

import (
	"context"
	"strings"
	"testing"
)

// fauxStreamAllocs is the allocations of one faux stream whose text splits into chunks one-token chunks, paced at tokensPerSecond (0 is unpaced).
func fauxStreamAllocs(t *testing.T, tokensPerSecond, chunks int) float64 {
	one := 1
	provider := NewFauxProvider(FauxConfig{TokensPerSecond: tokensPerSecond, TokenSize: &FauxTokenSize{Min: &one, Max: &one}})
	text := strings.Repeat("abcd", chunks)
	return testing.AllocsPerRun(3, func() {
		provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText(text)}, StopReason: "stop"})})
		stream, err := provider.Stream(context.Background(), emptyTranscript(), StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if result := stream.Result(); result.StopReason != StopReasonStop {
			t.Fatalf("stop=%q error=%q", result.StopReason, result.ErrorMessage)
		}
	})
}

// fauxChunkAllocs is what one more chunk of a faux stream allocates.
func fauxChunkAllocs(t *testing.T, tokensPerSecond int) float64 {
	const chunks = 32
	return (fauxStreamAllocs(t, tokensPerSecond, 2*chunks) - fauxStreamAllocs(t, tokensPerSecond, chunks)) / chunks
}

// A paced faux stream waits for each chunk on one timer that it resets, with its callbacks bound once: a paced chunk allocates what an unpaced one does, not a new timer and two closures.
func TestFauxPacedChunkAllocatesNoTimer(t *testing.T) {
	unpaced, paced := fauxChunkAllocs(t, 0), fauxChunkAllocs(t, 1_000_000)
	t.Logf("allocations per chunk: unpaced %.2f, paced %.2f", unpaced, paced)
	if paced > unpaced+0.5 {
		t.Fatalf("a paced chunk allocates %.2f times, an unpaced one %.2f: pacing must allocate nothing per chunk", paced, unpaced)
	}
}
