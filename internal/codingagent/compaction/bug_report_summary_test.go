package compaction

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func bugSummaryMessages(texts ...string) []agent.AgentMessage {
	messages := make([]agent.AgentMessage, len(texts))
	for i, text := range texts {
		messages[i] = agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: text}}}}
	}
	return messages
}

// Mirrors upstream generateBugReportSummary: system prompt, conversation,
// user report, instructions, and a 4096-token ceiling capped by the model.
func TestGenerateBugReportSummaryBuildsUpstreamPrompt(t *testing.T) {
	var gotSystem, gotPrompt string
	var gotMax int
	completer := simpleCompleterFunc(func(_ context.Context, _ *ai.Model, system string, messages []agent.AgentMessage, options ai.StreamOptions) (string, *ai.Usage, error) {
		gotSystem, gotMax = system, options.MaxTokens
		gotPrompt = messages[0].User.Content[0].(ai.TextContent).Text
		return "  ## What went wrong\nHung.  ", nil, nil
	})
	model := &ai.Model{ID: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 100_000, MaxOutputTokens: 2048}}
	summary, err := GenerateBugReportSummary(context.Background(), GenerateBugReportSummaryOptions{
		Messages: bugSummaryMessages("run the tests"), Hint: " tool hung ", Model: model, Completer: completer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary != "## What went wrong\nHung." || gotMax != 2048 || gotSystem != bugSummarySystemPrompt {
		t.Fatalf("summary=%q max=%d system=%q", summary, gotMax, gotSystem)
	}
	for _, want := range []string{"<conversation>\n", "run the tests", "<user-report>\ntool hung\n</user-report>", bugSummaryInstructions} {
		if !strings.Contains(gotPrompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Contains(gotPrompt, "Note: only the last") {
		t.Error("prompt notes truncation although every message fit")
	}
}

func TestGenerateBugReportSummaryKeepsNewestMessagesWithinBudget(t *testing.T) {
	long := strings.Repeat("x", 4000)
	selected := selectBugReportMessages(bugSummaryMessages(long, long, "newest"), 1100)
	if len(selected) != 2 || selected[1].User.Content[0].(ai.TextContent).Text != "newest" {
		t.Fatalf("selected %d messages", len(selected))
	}
	if got := selectBugReportMessages(bugSummaryMessages(long), 1); len(got) != 1 {
		t.Fatal("the newest message must always be kept")
	}
}

func TestGenerateBugReportSummaryFailures(t *testing.T) {
	model := &ai.Model{ID: "m"}
	empty := simpleCompleterFunc(func(context.Context, *ai.Model, string, []agent.AgentMessage, ai.StreamOptions) (string, *ai.Usage, error) {
		return "  ", nil, nil
	})
	if _, err := GenerateBugReportSummary(context.Background(), GenerateBugReportSummaryOptions{Model: model, Completer: empty}); err == nil || err.Error() != "Bug report summary was empty" {
		t.Fatalf("empty summary error = %v", err)
	}
	if _, err := GenerateBugReportSummary(context.Background(), GenerateBugReportSummaryOptions{Completer: empty}); err == nil || err.Error() != "No model selected" {
		t.Fatalf("missing model error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := simpleCompleterFunc(func(context.Context, *ai.Model, string, []agent.AgentMessage, ai.StreamOptions) (string, *ai.Usage, error) {
		cancel()
		return "", nil, context.Canceled
	})
	if _, err := GenerateBugReportSummary(ctx, GenerateBugReportSummaryOptions{Model: model, Completer: cancelled}); err == nil || err.Error() != "Bug report summary was cancelled" {
		t.Fatalf("cancelled error = %v", err)
	}
}
