package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// overWindowCompleter answers like a provider with a hard input limit: a request whose text is longer than limitTokens (four characters per token) is
// refused with the overflow error, any other request is answered with a numbered summary that names the previous summary it was given.
type overWindowCompleter struct {
	limitTokens int
	requests    []string
}

func (c *overWindowCompleter) CompleteSimple(_ context.Context, _ *ai.Model, _ string, messages []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
	var text strings.Builder
	for _, block := range messages[0].User.Content.(ai.UserContentBlocks) {
		text.WriteString(block.(ai.TextContent).Text)
	}
	c.requests = append(c.requests, text.String())
	if tokens := len(text.String()) / 4; tokens > c.limitTokens {
		return "", nil, fmt.Errorf("prompt is too long: %d tokens > %d maximum", tokens, c.limitTokens)
	}
	return fmt.Sprintf("summary-%d", len(c.requests)), &ai.Usage{Input: len(text.String()) / 4, Output: 5, TotalTokens: len(text.String())/4 + 5}, nil
}

func overWindowMessages(count, chars int) []agent.AgentMessage {
	messages := make([]agent.AgentMessage, count)
	for index := range messages {
		text := fmt.Sprintf("m%03d ", index) + strings.Repeat("x", chars)
		messages[index] = agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}}
	}
	return messages
}

// Pi 1.1.0 and Pi main send the whole history in one summary request (compaction.ts generateSummaryWithUsage), so a history longer than the model's context window fails with the provider's overflow error and the session can never be compacted (reproduced with both CLIs against a mock endpoint: "prompt is too long: 12034 tokens > 6000 maximum"). PiG summarizes such a history chunk by chunk, each chunk updating the summary of the one before.
func TestGenerateSummaryOfAHistoryLongerThanTheWindowIsChunked(t *testing.T) {
	model := &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 6000}}
	messages := overWindowMessages(60, 1000)
	completer := &overWindowCompleter{limitTokens: 6000}
	summary, usage, err := GenerateSummaryWithUsageUsing(t.Context(), messages, model, 500, "key", nil, "", "", "", completer, nil, nil, nil, ai.RetryCallbacks{}, "")
	if err != nil {
		t.Fatalf("summarization failed: %v", err)
	}
	if len(completer.requests) < 3 {
		t.Fatalf("%d requests, want the 60 messages split over several", len(completer.requests))
	}
	if want := fmt.Sprintf("summary-%d", len(completer.requests)); summary != want {
		t.Fatalf("summary %q, want the last chunk's %q", summary, want)
	}
	seen := 0
	for index, request := range completer.requests {
		if index > 0 && !strings.Contains(request, fmt.Sprintf("<previous-summary>\nsummary-%d\n</previous-summary>", index)) {
			t.Fatalf("request %d does not carry the summary of request %d", index+1, index)
		}
		for message := range messages {
			if strings.Contains(request, fmt.Sprintf("m%03d ", message)) {
				if message != seen {
					t.Fatalf("request %d holds message %d, want the messages in order (next %d)", index+1, message, seen)
				}
				seen++
			}
		}
	}
	if seen != len(messages) {
		t.Fatalf("%d of %d messages were summarized", seen, len(messages))
	}
	if usage == nil || usage.Output != 5*len(completer.requests) {
		t.Fatalf("usage %+v, want the usage of every request added", usage)
	}
}

// A history that fits the window is one request, as in Pi.
func TestGenerateSummaryOfAHistoryThatFitsTheWindowIsOneRequest(t *testing.T) {
	model := &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	completer := &overWindowCompleter{limitTokens: 100000}
	if _, _, err := GenerateSummaryWithUsageUsing(t.Context(), overWindowMessages(60, 1000), model, 500, "key", nil, "", "", "", completer, nil, nil, nil, ai.RetryCallbacks{}, ""); err != nil {
		t.Fatal(err)
	}
	if len(completer.requests) != 1 {
		t.Fatalf("%d requests, want 1", len(completer.requests))
	}
}

// A provider whose real count is above the estimate refuses a chunk; the chunk is halved until it is accepted.
func TestGenerateSummaryHalvesAChunkTheProviderRefusesAsTooLong(t *testing.T) {
	model := &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 1000000}}
	completer := &overWindowCompleter{limitTokens: 3000}
	if _, _, err := GenerateSummaryWithUsageUsing(t.Context(), overWindowMessages(60, 1000), model, 500, "key", nil, "", "", "", completer, nil, nil, nil, ai.RetryCallbacks{}, ""); err != nil {
		t.Fatalf("summarization failed: %v", err)
	}
	if len(completer.requests) < 4 {
		t.Fatalf("%d requests, want the refused first request and its halves", len(completer.requests))
	}
	last := completer.requests[len(completer.requests)-1]
	if len(last)/4 > 3000 {
		t.Fatalf("the last request is %d tokens, over the provider's limit", len(last)/4)
	}
}

// One message longer than the whole window cannot be split: the provider's error stays the error.
func TestGenerateSummaryOfOneMessageLongerThanTheWindowKeepsTheProvidersError(t *testing.T) {
	model := &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 6000}}
	completer := &overWindowCompleter{limitTokens: 6000}
	_, _, err := GenerateSummaryWithUsageUsing(t.Context(), overWindowMessages(1, 40000), model, 500, "key", nil, "", "", "", completer, nil, nil, nil, ai.RetryCallbacks{}, "")
	if err == nil || !strings.Contains(err.Error(), "prompt is too long") || errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want the provider's overflow error", err)
	}
}

func overWindowBranchEntries(count, tokens int) []codingagent.SessionEntry {
	entries := make([]codingagent.SessionEntry, count)
	var parent *string
	for index := range entries {
		id := fmt.Sprintf("e%03d", index)
		entries[index] = makeMessageEntry(id, parent, tokens)
		parent = &id
	}
	return entries
}

// Pi's branch summary sends the newest entries that fit the window by the character estimate and reports the provider's overflow error when the real token count is higher (branch-summarization.ts generateBranchSummary). PiG retries with the newest half of what it sent.
func TestGenerateBranchSummaryHalvesARequestTheProviderRefusesAsTooLong(t *testing.T) {
	completer := &overWindowCompleter{limitTokens: 4500}
	result := GenerateBranchSummary(t.Context(), overWindowBranchEntries(10, 1000), GenerateBranchSummaryOptions{Model: &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}, Completer: completer})
	if result.Error != "" || result.Aborted {
		t.Fatalf("result %+v, want a summary", result)
	}
	if len(completer.requests) < 3 {
		t.Fatalf("%d requests, want the refused request and its halvings", len(completer.requests))
	}
	if last := completer.requests[len(completer.requests)-1]; len(last)/4 > 4500 {
		t.Fatalf("the accepted request is %d tokens, over the provider's limit", len(last)/4)
	}
}

// A branch that the provider accepts is one request, as in Pi.
func TestGenerateBranchSummaryOfABranchThatFitsIsOneRequest(t *testing.T) {
	completer := &overWindowCompleter{limitTokens: 100000}
	result := GenerateBranchSummary(t.Context(), overWindowBranchEntries(10, 1000), GenerateBranchSummaryOptions{Model: &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}, Completer: completer})
	if result.Error != "" || len(completer.requests) != 1 {
		t.Fatalf("result %+v after %d requests, want one request", result, len(completer.requests))
	}
}

// One message the provider refuses stays Pi's error.
func TestGenerateBranchSummaryOfOneRefusedMessageKeepsTheProvidersError(t *testing.T) {
	completer := &overWindowCompleter{limitTokens: 500}
	result := GenerateBranchSummary(t.Context(), overWindowBranchEntries(1, 1000), GenerateBranchSummaryOptions{Model: &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}, Completer: completer})
	if !strings.Contains(result.Error, "prompt is too long") || len(completer.requests) != 1 {
		t.Fatalf("result %+v after %d requests, want the provider's error from one request", result, len(completer.requests))
	}
}
