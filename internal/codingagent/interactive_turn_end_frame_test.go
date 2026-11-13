package codingagent_test

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's stream ends with the frame that agent_end requested: interactive-mode.ts:3778 clears the working indicator and calls requestRender
// once, and the end of the prompt requests nothing, in the same task of the loop. PiG's turn cleanup reaches the owner loop some iterations
// after agent_end (the Session's post-run handling and the FlushEvents barrier come first), and it painted unconditionally: when the frame of
// agent_end had run by then, that paint was a frame that changed no row, after the frame that carries the reply (scenario
// interactive-rendering/68 counts it). Here the frame deadline passes right after agent_end, which is that schedule made certain.
func TestInteractiveTurnCleanupDrawsNoFrameAfterTheFrameOfAgentEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ran := 0
		h, out := replyFrames(t, func(h *icodingagent.TestHarness, ev agent.AgentEvent) {
			if _, ok := ev.(agent.AgentEndEvent); ok {
				// The clock is the bubble's: the 16 ms deadline of the frame agent_end requested passes before the cleanup is posted.
				time.Sleep(20 * time.Millisecond)
				h.RunDueFrame()
				ran++
			}
		})
		before := len(out.snapshot())
		h.Do(func() { h.Enter("hello") })
		h.WaitIdle(t, 10*time.Second)
		time.Sleep(200 * time.Millisecond)
		if ran != 1 {
			t.Fatalf("the frame deadline passed %d times after agent_end, want once", ran)
		}
		writes := out.snapshot()[before:]
		last := -1
		for i, w := range writes {
			if strings.Contains(w, "REPLY-TEXT") {
				last = i
			}
		}
		if last < 0 {
			t.Fatalf("the reply was never drawn; writes: %q", writes)
		}
		// Pi 1.5.2 writes nothing after the frame that carries the reply but that frame's own cursor tail (TestInteractiveAnswerEndEmitsNoTrailingFrame).
		want := []string{"\x1b[3A\x1b[1G", "\x1b[?25l"}
		if got := writes[last+1:]; !slices.Equal(got, want) {
			t.Fatalf("writes after the write that draws the reply = %q, want only that frame's cursor tail %q", got, want)
		}
	})
}
