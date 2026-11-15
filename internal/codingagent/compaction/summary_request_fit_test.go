package compaction

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/compactiontypes"
)

// windowCompleter answers like a provider with a context window: a request whose input (its text at charsPerToken characters per token) and the
// output cap it asks for together exceed the window is refused with the overflow error; any other request is answered with a numbered summary,
// summaryChars long, that names nothing else.
type windowCompleter struct {
	window, outputCap, charsPerToken, summaryChars int
	requests                                       []string
	refused                                        int
}

func (c *windowCompleter) CompleteSimple(_ context.Context, _ *ai.Model, _ string, messages []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
	var text strings.Builder
	for _, block := range messages[0].User.Content.(ai.UserContentBlocks) {
		text.WriteString(block.(ai.TextContent).Text)
	}
	c.requests = append(c.requests, text.String())
	if tokens := len(text.String())/c.charsPerToken + c.outputCap; tokens > c.window {
		c.refused++
		return "", nil, fmt.Errorf("prompt is too long: %d tokens > %d maximum", tokens, c.window)
	}
	return fmt.Sprintf("summary-%d ", len(c.requests)) + strings.Repeat("s", c.summaryChars), &ai.Usage{Output: 5, TotalTokens: 5}, nil
}

// oldSplitRequests is the number of requests the split of the first form of D167 made for a history of messages: chunks of 0.75 x (window - 2 x
// output cap - instructions) by the plain estimate.
func oldSplitRequests(messages []agent.AgentMessage, window, outputCap int) int {
	room := int(float64(window-ceilQuarter(len(SummarizationSystemPrompt)+len(UPDATE_SUMMARIZATION_PROMPT))-2*outputCap) * 0.75)
	requests, used := 1, 0
	for index := range messages {
		size := ceilQuarter(jsLength(SerializeConversation(convertToLlm(messages[index:index+1])))) + 1
		if used > 0 && used+size > room {
			requests++
			used = 0
		}
		used += size
	}
	return requests
}

// compactHistory compacts messages with the session's measured token ratio (0: a session without usage).
func compactHistory(t *testing.T, completer *windowCompleter, messages []agent.AgentMessage, window, outputCap int, tokenRatio float64) (CompactionResult, error) {
	t.Helper()
	model := &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: window, MaxOutputTokens: outputCap}}
	prep := CompactionPreparation{FirstKeptEntryID: "kept", MessagesToSummarize: messages, Settings: CompactionSettings{ReserveTokens: 16384}}
	compactiontypes.SetTokenRatio(&prep, tokenRatio)
	return Compact(t.Context(), prep, model, "key", nil, "", "", CompleterStreamFn(completer), nil, nil, ai.RetryCallbacks{}, "")
}

// A history that fits the model's window together with the output cap the summary request asks for, by the estimate scaled by the ratio the
// session's usage measured and the margin, is one request, as in Pi (D167: the split serves only histories one request cannot hold).
func TestCompactionOfAHistoryThatFitsWithItsOutputCapIsOneRequest(t *testing.T) {
	for _, test := range []struct {
		window, outputCap, charsPerToken, thousandTokens, wantRequests int
	}{
		{1_000_000, 128_000, 4, 474, 1},
		{1_000_000, 128_000, 4, 750, 1},
		{1_000_000, 128_000, 4, 800, 1},
		{1_000_000, 128_000, 3, 474, 1},
		{1_000_000, 128_000, 3, 900, 2},
		{400_000, 128_000, 3, 190, 1},
		{200_000, 8_192, 3, 130, 1},
		{128_000, 16_384, 3, 75, 1},
	} {
		t.Run(fmt.Sprintf("window %d cap %d history %dk at %d characters per token", test.window, test.outputCap, test.thousandTokens, test.charsPerToken), func(t *testing.T) {
			completer := &windowCompleter{window: test.window, outputCap: test.outputCap, charsPerToken: test.charsPerToken, summaryChars: 4000}
			if _, err := compactHistory(t, completer, overWindowMessages(test.thousandTokens, 4000-15), test.window, test.outputCap, 4/float64(test.charsPerToken)); err != nil {
				t.Fatalf("compaction failed: %v", err)
			}
			if len(completer.requests) != test.wantRequests || completer.refused != 0 {
				t.Fatalf("%d summary requests, %d refused, want %d and none", len(completer.requests), completer.refused, test.wantRequests)
			}
		})
	}
}

// With a measured ratio the split never sends more requests than the first form of D167 did, and never one the provider refuses, over the shapes
// and history sizes of the measurement: a window of 128k to 1M with an output cap of 8k to 128k, a history of 30 % to 120 % of the window, a
// summary of 20k tokens (or 85 % of a smaller cap) carried from request to request, for providers counting four and three characters per
// token, each measured by the session's usage.
func TestSplitOfAHistoryIsNeverMoreRequestsThanTheFirstFormAndNeverRefused(t *testing.T) {
	for _, charsPerToken := range []int{4, 3} {
		for _, shape := range [][2]int{{1_000_000, 128_000}, {1_000_000, 64_000}, {400_000, 128_000}, {200_000, 64_000}, {200_000, 8_192}, {128_000, 16_384}} {
			window, outputCap := shape[0], shape[1]
			summaryChars := 4 * min(20_000, outputCap*85/100)
			for _, percent := range []int{30, 50, 60, 70, 80, 90, 100, 120} {
				t.Run(fmt.Sprintf("%d chars per token window %d cap %d history %d%%", charsPerToken, window, outputCap, percent), func(t *testing.T) {
					messages := overWindowMessages(window/1000*percent/100, 4000-15)
					completer := &windowCompleter{window: window, outputCap: outputCap, charsPerToken: charsPerToken, summaryChars: summaryChars}
					result, err := compactHistory(t, completer, messages, window, outputCap, 4/float64(charsPerToken))
					if err != nil {
						t.Fatalf("compaction failed: %v", err)
					}
					if completer.refused != 0 {
						t.Fatalf("%d requests refused", completer.refused)
					}
					if old := oldSplitRequests(messages, window, outputCap); len(completer.requests) > old {
						t.Fatalf("%d requests, the first form sent %d", len(completer.requests), old)
					}
					if !strings.HasPrefix(result.Summary, fmt.Sprintf("summary-%d ", len(completer.requests))) {
						t.Fatalf("summary %.20q is not the last request's", result.Summary)
					}
				})
			}
		}
	}
}

// A session without usage has no ratio to calibrate by: it is split exactly as the first form of D167 split it, request for request.
func TestCompactionOfASessionWithoutUsageIsTheFirstFormOfTheSplit(t *testing.T) {
	for _, shape := range [][2]int{{1_000_000, 128_000}, {400_000, 128_000}, {200_000, 8_192}, {128_000, 16_384}} {
		window, outputCap := shape[0], shape[1]
		for _, percent := range []int{30, 60, 90, 120} {
			t.Run(fmt.Sprintf("window %d cap %d history %d%%", window, outputCap, percent), func(t *testing.T) {
				messages := overWindowMessages(window/1000*percent/100, 4000-15)
				completer := &windowCompleter{window: window, outputCap: outputCap, charsPerToken: 4, summaryChars: 4000}
				if _, err := compactHistory(t, completer, messages, window, outputCap, 0); err != nil {
					t.Fatalf("compaction failed: %v", err)
				}
				if want := oldSplitRequests(messages, window, outputCap); len(completer.requests) != want {
					t.Fatalf("%d requests, the first form sent %d", len(completer.requests), want)
				}
			})
		}
	}
}
