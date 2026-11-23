package codingagent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A response that arrives in one piece is one task of Pi's loop, and the owner loop keeps one message_update per part of the message until
// the task closes (collectEvent). The same response paced by a timer is one task per chunk, so every message_update is applied, most of them
// as tasks of one update that carry no markers. Both leave the same transcript: thinking, text, the cards of two tool calls in their order
// with their arguments and results, and the last answer.
func TestInteractiveResponseInOnePieceLeavesTheTranscriptOfItsChunks(t *testing.T) {
	transcript := func(t *testing.T, tokensPerSecond int) (chat string, updates, tasks int) {
		t.Helper()
		synctest.Test(t, func(t *testing.T) {
			provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", TokensPerSecond: tokensPerSecond})
			provider.SetResponses([]ai.FauxResponseStep{
				ai.FauxStaticStep(ai.FauxResponse{StopReason: string(ai.StopReasonToolUse), Content: []ai.FauxContentBlock{
					ai.FauxThinking("a thought that the provider streams in several chunks before the answer"),
					ai.FauxText("an answer that the provider streams in several chunks of text"),
					ai.FauxToolCall("read", map[string]any{"path": "notes.txt"}, &ai.FauxToolCallOptions{ID: "call-1"}),
					ai.FauxToolCall("read", map[string]any{"path": "notes.txt", "offset": 2, "limit": 1}, &ai.FauxToolCallOptions{ID: "call-2"}),
				}}),
				ai.FauxStaticStep(ai.FauxResponse{StopReason: string(ai.StopReasonStop), Content: []ai.FauxContentBlock{
					ai.FauxText("the last words of the run"),
				}}),
			})
			_, opts := replySessionOn(t, provider)
			if err := os.WriteFile(filepath.Join(opts.CWD, "notes.txt"), []byte("FILE-LINE-ONE\nFILE-LINE-TWO\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			marked := false
			h := icodingagent.NewTestHarnessWithOutput(t, opts, func(_ *icodingagent.TestHarness, ev agent.AgentEvent) {
				if _, ok := ev.(agent.MessageUpdateEvent); ok {
					updates++
					if !marked {
						// A message_update outside the markers is a task of its own.
						tasks++
					}
				}
				if marker, ok := ev.(interface{ EventRegion() (open, bounds bool) }); ok {
					if open, bounds := marker.EventRegion(); bounds {
						if marked = open; open {
							tasks++
						}
					}
				}
			}, &writeLog{})
			h.Do(h.OwnFrames)
			h.Do(func() { h.Enter("hello") })
			h.WaitIdle(t, 30*time.Second)
			synctest.Wait()
			chat = h.Chat()
		})
		return chat, updates, tasks
	}

	onePiece, updates, tasks := transcript(t, 0)
	if updates < 12 {
		t.Fatalf("the response has %d message_update events, want several chunks for each of its four blocks", updates)
	}
	chunks, _, chunkTasks := transcript(t, 400)
	if tasks > 4 || chunkTasks < 12 {
		t.Fatalf("the run in one piece is %d tasks and the run in chunks %d, want a task per response in one piece and a task for most of the %d updates in chunks", tasks, chunkTasks, updates)
	}
	for _, want := range []string{"a thought that the provider streams", "an answer that the provider streams in several chunks of text", "notes.txt:2-2", "the last words of the run"} {
		if !strings.Contains(onePiece, want) {
			t.Fatalf("the transcript of the response in one piece lacks %q:\n%s", want, onePiece)
		}
	}
	if cards := strings.Count(onePiece, "notes.txt"); cards != 2 {
		t.Fatalf("the transcript of the response in one piece has %d tool cards, want two:\n%s", cards, onePiece)
	}
	if onePiece != chunks {
		t.Fatalf("the response in one piece left another transcript than the same response in chunks\nin one piece:\n%s\nin chunks:\n%s", onePiece, chunks)
	}
}
