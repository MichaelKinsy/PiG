//go:build !race

package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// encodeChurnHistory is a long-session history of turns turns: a prompt, one assistant message with four read calls, their four
// distinct 48 KiB results (large strings the request cache splices) and a 1.5 KB answer (small strings encoded on every request).
func encodeChurnHistory(turns int) []Message {
	const reads, size = 4, 48 << 10
	answer := strings.Repeat("The module wires the session state into the renderer and streams tool results back. ", 18)
	messages := make([]Message, 0, turns*(reads+3))
	for turn := range turns {
		messages = append(messages, UserMessage{Content: UserText(fmt.Sprintf("Prompt %d: read the next four fixture files and explain how they wire the session into the renderer.", turn))})
		calls := make([]AssistantContentBlock, 0, reads+1)
		calls = append(calls, TextContent{Text: "I'll read the four files."})
		for read := range reads {
			calls = append(calls, ToolCall{ID: fmt.Sprintf("call_%d_%d", turn, read), Name: "read", Arguments: JsonObject{"path": fmt.Sprintf("src/file-%d-%d.ts", turn, read)}})
		}
		messages = append(messages, AssistantMessage{StopReason: StopReasonToolUse, Content: calls})
		for read := range reads {
			id := fmt.Sprintf("call_%d_%d", turn, read)
			messages = append(messages, ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: encodeChurnText(turn*reads+read, size)}}})
		}
		messages = append(messages, AssistantMessage{StopReason: StopReasonStop, Content: []AssistantContentBlock{TextContent{Text: fmt.Sprintf("%d. %s", turn, answer)}}})
	}
	return messages
}

// encodeChurnText is the index-th read result of size bytes: source text with tabs, quotes, a backslash and newlines on every line.
func encodeChurnText(index, size int) string {
	head := fmt.Sprintf("// fixture/file-%d.ts\n", index)
	line := "\tconst value = this.session.get(\"key\") ?? `fallback ${index}`; // <tag> & \\path\n"
	return head + strings.Repeat(line, size/len(line)+1)[:size-len(head)]
}

// Every completions request re-sends the whole history. The large tool results come from the request string cache, so what the
// encode writes per request is the skeleton: every small string and every key of the history. Encoding it should allocate about
// one skeleton per request (the message cache serves the small messages, so a build allocates less than that) (the body bytes it keeps); at e85ec79e85 encoding/json/v2 grows its buffer by doubling
// (jsonwire.AppendQuote -> slices.Grow) and then copies the result out (bytes.Clone in v2.Marshal), and the long-session profile
// charges 236 MB + 70 MB of that to 75 prompts. The bound is 1.5 skeletons per build at 75 turns.
func TestCompletionsRequestEncodeAllocatesAboutItsSkeleton(t *testing.T) {
	provider := NewOpenAIProvider(OpenAIConfig{Model: "probe", ProviderID: "probe-provider", APIKey: "test", BaseURL: "http://127.0.0.1:1"})
	t.Cleanup(func() { _ = provider.Close() })
	measure := func(turns int) (allocated, skeleton, body float64) {
		var payload any
		options := StreamOptions{OnPayload: func(value any, _ *Model) (any, error) {
			payload = value
			return nil, errors.New("payload captured")
		}}
		if stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: encodeChurnHistory(turns), Frozen: true}), options); err == nil && stream != nil {
			_ = stream.Result()
		}
		if payload == nil {
			t.Fatal("the provider built no payload")
		}
		var built *requestBody
		build := func() {
			var err error
			if built, err = marshalSpliced(payload, requestStrings, marshalRequestBody); err != nil {
				t.Fatal(err)
			}
		}
		_, allocated = requestBuildCost(build) // the first build fills the cache; the measured ones are steady-state requests
		// The skeleton is what a build writes when it takes only the large strings from the cache: the small messages, encoded, are in the odd segments of a build that takes them from the message cache too.
		messages := requestStrings.messages
		requestStrings.messages = nil
		build()
		requestStrings.messages = messages
		// join alternates skeleton runs (even segments) and cached large strings (odd segments).
		for i, segment := range built.segments {
			if i%2 == 0 {
				skeleton += float64(len(segment))
			}
		}
		if len(built.segments) < 3 {
			t.Fatalf("the build at %d turns spliced no cached string (%d segments)", turns, len(built.segments))
		}
		return allocated, skeleton, float64(built.size)
	}
	for _, turns := range []int{37, 75} {
		allocated, skeleton, body := measure(turns)
		ratio := allocated / skeleton
		t.Logf("%d turns: body %.2f MB, skeleton %.0f KB, %.0f KB allocated per encode (%.2f skeletons)", turns, body/1e6, skeleton/1e3, allocated/1e3, ratio)
		if turns == 75 && ratio > 1.5 {
			t.Fatalf("at %d turns one request encode allocates %.0f bytes for a %.0f-byte skeleton (%.2fx), want at most 1.5x", turns, allocated, skeleton, ratio)
		}
	}
}
