//go:build !race

package ai

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A producer outside the executor that streams one long tool-call argument costs each push in proportion to what the push added, not to the argument so far: the snapshot reads the strings of the arguments for validity only where they grew. The cost does not allocate, so only the CPU time shows it: 8x the pushes take about 8x the CPU time, where a snapshot that validated the whole argument at every push takes 64x.
func TestUnmanagedProducerSnapshotsDoNotRevalidateTheArgumentAtEveryPush(t *testing.T) {
	const piece = 4 << 10
	stream := func(pushes int) {
		full := strings.Repeat("x", piece*pushes)
		stream := NewAssistantMessageEventStream()
		partial := &AssistantMessage{API: APIOpenAICompletions, Provider: "p", Model: "m", StopReason: StopReasonPending, Content: []AssistantContentBlock{ToolCall{ID: "c", Name: "write", Arguments: JsonObject{"content": ""}}}}
		for push := 1; push <= pushes; push++ {
			// The argument is a longer prefix of one string, as a producer that appends to a buffer holds it.
			partial.Content[0] = ToolCall{ID: "c", Name: "write", Arguments: JsonObject{"content": full[:push*piece]}}
			if err := stream.Push(ToolCallDeltaEvent{ContentIndex: 0, Delta: full[(push-1)*piece : push*piece], Partial: partial}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The small stream runs 8x as often as the large one, so both rounds take about as long; 8x the pushes may take up to 20x the CPU time.
	const runs = 16
	smallCPU, largeCPU := cpuOf(t, 8*runs, func() { stream(128) }), cpuOf(t, runs, func() { stream(1024) })
	ratio := float64(largeCPU) / float64(max(smallCPU, 1))
	t.Logf("128 pushes of a 4 KB argument piece: %v CPU, 8x: %v (%.1fx)", smallCPU, largeCPU, ratio)
	testenv.TimingGate(t, ratio > 20, "8x the pushes took %.1fx the CPU time (%v vs %v); want at most 20x", ratio, largeCPU, smallCPU)
}
