package compaction

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// labelledMessages is overWindowMessages with its own label, so two message runs stay distinguishable in a request.
func labelledMessages(label string, count, chars int) []agent.AgentMessage {
	messages := make([]agent.AgentMessage, count)
	for index := range messages {
		text := fmt.Sprintf("%s%03d ", label, index) + strings.Repeat("x", chars)
		messages[index] = agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}}
	}
	return messages
}

// assertSummarizedInOrder checks that every accepted request stays within the limit, that each accepted request after the first carries the summary of the accepted request before it, and that every label appears in order across the accepted requests.
func assertSummarizedInOrder(t *testing.T, completer *overWindowCompleter, labels []string) {
	t.Helper()
	seen, previous := 0, -1
	for index, request := range completer.requests {
		if len(request)/4 > completer.limitTokens {
			continue
		}
		if previous >= 0 && !strings.Contains(request, fmt.Sprintf("<previous-summary>\nsummary-%d\n</previous-summary>", previous+1)) {
			t.Fatalf("accepted request %d does not carry the summary of accepted request %d", index+1, previous+1)
		}
		previous = index
		for position, label := range labels {
			if strings.Contains(request, label) {
				if position != seen {
					t.Fatalf("accepted request %d holds %q, want the messages in order (next %q)", index+1, label, labels[seen])
				}
				seen++
			}
		}
	}
	if seen != len(labels) {
		t.Fatalf("%d of %d messages were summarized", seen, len(labels))
	}
}

func messageLabels(label string, count int) []string {
	labels := make([]string, count)
	for index := range labels {
		labels[index] = fmt.Sprintf("%s%03d ", label, index)
	}
	return labels
}

// The halves of a refused chunk form one chain: the second half updates the summary of the first, so nothing summarized before the refusal is lost.
func TestGenerateSummaryHalvesThreadTheSummary(t *testing.T) {
	model := &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 1000000}}
	completer := &overWindowCompleter{limitTokens: 3000}
	if _, _, err := GenerateSummaryWithUsageUsing(t.Context(), overWindowMessages(60, 1000), model, 500, "key", nil, "", "", "", completer, nil, nil, nil, ai.RetryCallbacks{}, ""); err != nil {
		t.Fatalf("summarization failed: %v", err)
	}
	assertSummarizedInOrder(t, completer, messageLabels("m", 60))
}

// A long agent run under one user prompt is a split turn whose prefix holds nearly all of the history. Compacting it must not send that prefix in one request: the part of the prefix that does not fit moves into the chunked history summary, and the turn-prefix request keeps the last window-sized part.
func TestCompactionOfASplitTurnLongerThanTheWindowIsChunked(t *testing.T) {
	model := &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 6000}}
	completer := &overWindowCompleter{limitTokens: 6000}
	prep := CompactionPreparation{
		FirstKeptEntryID:    "kept",
		MessagesToSummarize: labelledMessages("h", 2, 100),
		TurnPrefixMessages:  labelledMessages("p", 60, 1000),
		IsSplitTurn:         true,
		Settings:            CompactionSettings{ReserveTokens: 500},
	}
	result, err := Compact(t.Context(), prep, model, "key", nil, "", "", CompleterStreamFn(completer), nil, nil, ai.RetryCallbacks{}, "")
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	for index, request := range completer.requests {
		if len(request)/4 > completer.limitTokens {
			t.Fatalf("request %d is %d tokens, over the window", index+1, len(request)/4)
		}
	}
	last := completer.requests[len(completer.requests)-1]
	if !strings.Contains(last, "# Instructions\n"+turnPrefixSummarizationPrompt) || !strings.Contains(last, "p059 ") || strings.Contains(last, "p000 ") {
		t.Fatalf("the turn-prefix request does not hold the last part of the prefix only")
	}
	if !strings.Contains(result.Summary, "**Turn Context (split turn):**") {
		t.Fatalf("summary %q lost the split-turn layout", result.Summary)
	}
	assertSummarizedInOrder(t, &overWindowCompleter{limitTokens: completer.limitTokens, requests: completer.requests[:len(completer.requests)-1]}, append(messageLabels("h", 2), messageLabels("p", 60)[:60-strings.Count(last, "[User]: p")]...))
}

// A split turn whose prefix fits keeps Pi's two requests unchanged.
func TestCompactionOfASplitTurnThatFitsKeepsPisRequests(t *testing.T) {
	model := &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	completer := &overWindowCompleter{limitTokens: 100000}
	prep := CompactionPreparation{
		FirstKeptEntryID:    "kept",
		MessagesToSummarize: labelledMessages("h", 2, 100),
		TurnPrefixMessages:  labelledMessages("p", 10, 100),
		IsSplitTurn:         true,
		Settings:            CompactionSettings{ReserveTokens: 500},
	}
	if _, err := Compact(t.Context(), prep, model, "key", nil, "", "", CompleterStreamFn(completer), nil, nil, ai.RetryCallbacks{}, ""); err != nil {
		t.Fatal(err)
	}
	if len(completer.requests) != 2 || strings.Contains(completer.requests[0], "p000 ") || !strings.Contains(completer.requests[1], "p000 ") || !strings.Contains(completer.requests[1], "p009 ") {
		t.Fatalf("requests %d, want Pi's history request and the whole prefix in the turn-prefix request", len(completer.requests))
	}
}

// Poisoned history (an errored assistant turn with no content, an aborted turn, empty text, an orphaned tool call and a tool result) is chunked like any other: every user message is summarized once, in order, within the window.
func TestGenerateSummaryChunksPoisonedHistory(t *testing.T) {
	model := &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 6000}}
	var messages []agent.AgentMessage
	for index, user := range labelledMessages("u", 30, 1000) {
		messages = append(messages, user)
		switch index % 5 {
		case 0:
			messages = append(messages, agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", StopReason: ai.StopReasonError, ErrorMessage: "boom"}})
		case 1:
			messages = append(messages, agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", StopReason: ai.StopReasonAborted, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "partial"}}}})
		case 2:
			messages = append(messages, agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", StopReason: ai.StopReasonStop, Content: []ai.AssistantContentBlock{ai.TextContent{Text: ""}}}})
		case 3:
			messages = append(messages, agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "orphan", Name: "read", Arguments: map[string]any{"path": "a"}}}}})
		case 4:
			messages = append(messages, agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "t", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "result"}}}})
		}
	}
	completer := &overWindowCompleter{limitTokens: 6000}
	if _, _, err := GenerateSummaryWithUsageUsing(t.Context(), messages, model, 500, "key", nil, "", "", "", completer, nil, nil, nil, ai.RetryCallbacks{}, ""); err != nil {
		t.Fatalf("summarization failed: %v", err)
	}
	if len(completer.requests) < 3 {
		t.Fatalf("%d requests, want the history split over several", len(completer.requests))
	}
	assertSummarizedInOrder(t, completer, messageLabels("u", 30))
}

// A refused branch whose newest message is larger than half of what was sent: the halved budget alone would select no message and report "No content to summarize" as a summary. The next request holds the newest message.
func TestGenerateBranchSummaryHalvingKeepsTheNewestMessage(t *testing.T) {
	entries := overWindowBranchEntries(10, 300)
	parent := "e009"
	entries = append(entries, makeMessageEntry("big", &parent, 3100))
	completer := &overWindowCompleter{limitTokens: 4000}
	result := GenerateBranchSummary(t.Context(), entries, GenerateBranchSummaryOptions{Model: &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}, Completer: completer})
	if result.Error != "" || !strings.Contains(result.Summary, fmt.Sprintf("summary-%d", len(completer.requests))) || len(completer.requests) != 2 {
		t.Fatalf("result %+v after %d requests, want the summary of the newest message from the second request", result, len(completer.requests))
	}
}

// A smaller budget that selects the same messages again (the older ones estimate at zero tokens) ends with the provider's error instead of repeating the refused request.
func TestGenerateBranchSummaryHalvingWithoutProgressKeepsTheProvidersError(t *testing.T) {
	first := "empty"
	entries := []codingagent.SessionEntry{makeMessageEntry(first, nil, 0), makeMessageEntry("big", &first, 1000)}
	completer := &overWindowCompleter{limitTokens: 500}
	result := GenerateBranchSummary(t.Context(), entries, GenerateBranchSummaryOptions{Model: &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}, Completer: completer})
	if !strings.Contains(result.Error, "prompt is too long") || len(completer.requests) != 1 {
		t.Fatalf("result %+v after %d requests, want the provider's error from one request", result, len(completer.requests))
	}
}
