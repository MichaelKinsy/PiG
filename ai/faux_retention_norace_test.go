//go:build !race

package ai

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fauxLongSession is the shape of the long-session row (bench long_session.py): each prompt is one user message and five model
// turns, four of which read a file. fileBytes sets the size of each read's result; payload is the bytes of text the messages carry.
func fauxLongSession(prompts, fileBytes int) (messages []Message, turnEnds []int, payload int) {
	file := strings.Repeat("0123456789abcdef fixture line of the file being read\n", fileBytes/53+1)[:fileBytes]
	for prompt := range prompts {
		user := fmt.Sprintf("Read the four fixture files (prompt %d)", prompt+1)
		messages = append(messages, UserMessage{Content: UserText(user)})
		payload += len(user)
		for read := range 4 {
			turnEnds = append(turnEnds, len(messages))
			path := fmt.Sprintf("fixture-%d.txt", read)
			id := fmt.Sprintf("call-%d-%d", prompt, read)
			// Each result is its own string, as each read returns a new one.
			content := strings.Clone(file)
			messages = append(messages,
				AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: id, Name: "read", Arguments: JsonObject{"path": path}}}, StopReason: StopReasonToolUse},
				ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: content}}},
			)
			payload += len(path) + len(content)
		}
		turnEnds = append(turnEnds, len(messages))
		answer := fmt.Sprintf("Done: read all four files (prompt %d).", prompt+1)
		messages = append(messages, AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: answer}}, StopReason: StopReasonStop})
		payload += len(answer)
	}
	return messages, turnEnds, payload
}

func liveHeap() uint64 {
	var stats runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// The memory row's session (75 prompts, about 4.2M tokens of context) driven through the faux provider, one model call per turn as
// the agent makes them: after the last call the provider keeps at most 0.3x the conversation's own bytes (the P7 bound, heap
// <= 1.3x payload). It kept the serialized prompt of the session and the storage of the one before it, about 2x.
func TestFauxSessionHeapRetentionAt4MTokens(t *testing.T) {
	messages, turnEnds, payload := fauxLongSession(75, 56<<10)
	if tokens := payload / 4; tokens < 4_200_000 {
		t.Fatalf("the history carries %d tokens, want at least 4.2M", tokens)
	}
	provider := NewFauxProvider(FauxConfig{})
	responses := make([]FauxResponseStep, len(turnEnds))
	for i := range responses {
		responses[i] = FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("ok")}, StopReason: string(StopReasonStop)})
	}
	provider.SetResponses(responses)
	options := StreamOptions{SessionID: "long-session"}
	before := liveHeap()
	var last *AssistantMessage
	for _, end := range turnEnds {
		transcript := NormalizeContext(Context{Messages: messages[:end], Frozen: true})
		stream, err := provider.Stream(context.Background(), transcript, options)
		if err != nil {
			t.Fatal(err)
		}
		last = stream.Result()
	}
	retained := int64(liveHeap()) - int64(before)
	runtime.KeepAlive(messages)
	runtime.KeepAlive(provider)
	if last.Usage.CacheRead == 0 || last.Usage.Input+last.Usage.CacheRead < payload/4 {
		t.Fatalf("the last call's usage %+v does not cover the %d-byte history", last.Usage, payload)
	}
	t.Logf("payload %.1f MB, provider retains %.2f MB (%.2fx payload)", float64(payload)/1e6, float64(retained)/1e6, float64(retained)/float64(payload))
	if limit := int64(payload) * 3 / 10; retained > limit {
		t.Fatalf("after %d model calls over a %d-byte history the faux provider retains %d bytes, want at most %d (0.3x payload)", len(turnEnds), payload, retained, limit)
	}
}

// A model call's usage estimate costs what the request adds to the session, not what the history holds: with the same number of
// messages, a history ten times larger in bytes must not cost ten times more per call. It did, as the whole transcript was
// serialized again and compared on every call.
func TestFauxEstimateCostDoesNotGrowWithHistoryBytes(t *testing.T) {
	perCall := func(fileBytes int) time.Duration {
		messages, turnEnds, _ := fauxLongSession(30, fileBytes)
		provider := NewFauxProvider(FauxConfig{})
		options := StreamOptions{SessionID: "s"}
		content := []FauxContentBlock{FauxText("ok")}
		// The session's first calls build its state; the timed calls each add the history's last turns.
		tail := turnEnds[len(turnEnds)-8:]
		for _, end := range turnEnds[:len(turnEnds)-8] {
			provider.estimateUsage(TranscriptContext{messages: messages[:end], validated: true}, options, content)
		}
		best := time.Duration(1 << 62)
		for _, end := range tail {
			transcript := TranscriptContext{messages: messages[:end], validated: true}
			start := time.Now()
			provider.estimateUsage(transcript, options, content)
			best = min(best, time.Since(start))
		}
		return best
	}
	small, large := perCall(4<<10), perCall(40<<10)
	t.Logf("per call: %v with 4 KiB reads, %v with 40 KiB reads", small, large)
	if large > 3*small+200*time.Microsecond {
		t.Fatalf("a call costs %v over 40 KiB reads and %v over 4 KiB reads: the estimate grows with the history's bytes", large, small)
	}
}
