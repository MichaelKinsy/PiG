package compaction

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/coding-agent/test/compaction-summary-reasoning.test.ts.
// The four rejection cases (tool call and length stop, for history and split
// turn summaries) need the provider response, so they are ported against the
// session completer in coding/session_summarization_test.go
// (TestSummarizationRejectsIncompleteResponses).

type summaryCall struct {
	model    *ai.Model
	messages []agent.AgentMessage
	options  ai.StreamOptions
}

// summaryRecorder mirrors the test's completeSimpleMock: it records each call
// and answers with the mock summary response.
type summaryRecorder struct {
	calls []summaryCall
}

func (r *summaryRecorder) CompleteSimple(_ context.Context, model *ai.Model, _ string, messages []agent.AgentMessage, options ai.StreamOptions) (string, *ai.Usage, error) {
	r.calls = append(r.calls, summaryCall{model: model, messages: messages, options: options})
	usage := mockSummaryUsage()
	return "## Goal\nTest summary", &usage, nil
}

func mockSummaryUsage() ai.Usage {
	return ai.Usage{Input: 10, Output: 10, TotalTokens: 20}
}

func createSummaryModel(reasoning bool, maxTokens int, compat *ai.ModelCompat) *ai.Model {
	model := &ai.Model{
		ID:           "non-reasoning-model",
		DisplayName:  "Non-reasoning Model",
		Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: maxTokens},
		ProviderMeta: ai.ProviderMetadata{ProviderID: "anthropic", API: "anthropic-messages", BaseURL: "https://api.anthropic.com", Compat: compat, Reasoning: reasoning},
	}
	if reasoning {
		model.ID, model.DisplayName = "reasoning-model", "Reasoning Model"
		model.Capabilities.MaxThinking = ai.ThinkingHigh
	}
	return model
}

func summarizeThisMessages() []agent.AgentMessage {
	return []agent.AgentMessage{{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "Summarize this."}}}}}
}

func summaryPrompt(messages []agent.AgentMessage) string {
	var prompt strings.Builder
	for _, message := range messages {
		if message.User == nil {
			continue
		}
		for _, block := range message.User.Content {
			if text, ok := block.(ai.TextContent); ok {
				prompt.WriteString(text.Text)
			}
		}
	}
	return prompt.String()
}

func TestGenerateSummaryUsesThinkingLevelForReasoningModels(t *testing.T) {
	recorder := &summaryRecorder{}
	text, usage, err := generateSummary(t.Context(), summarizeThisMessages(), "", 2000, createSummaryModel(true, 8192, nil), recorder, nil, "", ai.ThinkingMedium, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if text != "## Goal\nTest summary" {
		t.Fatalf("text = %q", text)
	}
	if want := mockSummaryUsage(); usage == nil || !reflect.DeepEqual(*usage, want) {
		t.Fatalf("usage = %+v, want %+v", usage, want)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(recorder.calls))
	}
	if got := recorder.calls[0].options.Thinking; got != ai.ThinkingMedium {
		t.Fatalf("reasoning = %q, want medium", got)
	}
}

func TestGenerateSummaryPreservesStringResult(t *testing.T) {
	text, _, err := generateSummary(t.Context(), summarizeThisMessages(), "", 2000, createSummaryModel(false, 8192, nil), &summaryRecorder{}, nil, "", "", nil, "")
	if err != nil || text != "## Goal\nTest summary" {
		t.Fatalf("generateSummary = %q, %v", text, err)
	}
}

func TestGenerateSummaryUsesFreshRoutingSessionsWithoutPromptCaching(t *testing.T) {
	recorder := &summaryRecorder{}
	for range 2 {
		if _, _, err := generateSummary(t.Context(), summarizeThisMessages(), "", 2000, createSummaryModel(false, 8192, nil), recorder, nil, "", "", nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(recorder.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(recorder.calls))
	}
	for _, call := range recorder.calls {
		if call.options.Env["PI_CACHE_RETENTION"] != "none" || call.options.CacheRetention != ai.CacheRetentionNone {
			t.Fatalf("cache retention = env %q option %q, want none", call.options.Env["PI_CACHE_RETENTION"], call.options.CacheRetention)
		}
	}
	first, second := recorder.calls[0].options.SessionID, recorder.calls[1].options.SessionID
	if first == "" || first == second {
		t.Fatalf("routing session IDs = %q, %q; want two distinct IDs", first, second)
	}
}

func TestCompleteSummarizationHonorsCallerRoutingAndToolChoiceWithoutPromptCaching(t *testing.T) {
	recorder := &summaryRecorder{}
	callerEnv := ai.ProviderEnv{"PI_CACHE_RETENTION": "long"}
	options := ai.StreamOptions{
		SessionID:      "current-routing-session",
		Env:            callerEnv,
		CacheRetention: ai.CacheRetentionLong,
		ToolChoice:     "auto",
	}
	if _, _, err := completeSummarization(t.Context(), createSummaryModel(false, 8192, nil), recorder, nil, nil, "Summarize", nil, options); err != nil {
		t.Fatal(err)
	}
	got := recorder.calls[0].options
	if got.SessionID != "current-routing-session" || got.Env["PI_CACHE_RETENTION"] != "none" || got.CacheRetention != ai.CacheRetentionNone || got.ToolChoice != "auto" {
		t.Fatalf("request options = session %q cache env %q retention %q tool choice %#v", got.SessionID, got.Env["PI_CACHE_RETENTION"], got.CacheRetention, got.ToolChoice)
	}
	if callerEnv["PI_CACHE_RETENTION"] != "long" {
		t.Fatal("completeSummarization mutated the caller's env")
	}
}

func TestCompactSplitTurnPreservesPreviousSummaryWithoutHistoryRequest(t *testing.T) {
	recorder := &summaryRecorder{}
	prep := CompactionPreparation{
		FirstKeptEntryID:   "entry-keep",
		TurnPrefixMessages: summarizeThisMessages(),
		IsSplitTurn:        true,
		TokensBefore:       100,
		PreviousSummary:    "previous checkpoint",
		FileOps:            NewFileOps(),
		Settings:           CompactionSettings{Enabled: true, ReserveTokens: 2000, KeepRecentTokens: 20},
	}
	result, err := Compact(t.Context(), prep, createSummaryModel(false, 8192, nil), recorder, nil, "", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(recorder.calls))
	}
	if !strings.Contains(result.Summary, "previous checkpoint") {
		t.Fatalf("summary = %q", result.Summary)
	}
	// Regression test for #9652: clear boundaries and continuation wording avoid the reasoning-extraction false positive.
	prompt := summaryPrompt(recorder.calls[0].messages)
	for _, want := range []string{"# Conversation\n[User]: Summarize this.", "# Instructions\nThe messages above are earlier context from an ongoing conversation."} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestGenerateSummaryOmitsReasoning(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reasoning bool
		level     ai.ThinkingLevel
	}{
		{"thinking off", true, ai.ThinkingOff},
		{"non-reasoning model", false, ai.ThinkingMedium},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &summaryRecorder{}
			if _, _, err := generateSummary(t.Context(), summarizeThisMessages(), "", 2000, createSummaryModel(tc.reasoning, 8192, nil), recorder, nil, "", tc.level, nil, ""); err != nil {
				t.Fatal(err)
			}
			if len(recorder.calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(recorder.calls))
			}
			if got := recorder.calls[0].options.Thinking; got != "" {
				t.Fatalf("reasoning = %q, want unset", got)
			}
		})
	}
}

// Refusal fallback stays model metadata: ai.StreamOptions has no fallback
// field, and the summary request carries the caller's model unchanged so the
// Anthropic provider reads Compat.AllowedFallbackModels itself.
func TestGenerateSummaryLeavesRefusalFallbackToModelMetadata(t *testing.T) {
	fallbacks := []ai.AnthropicAllowedFallbackModel{{Provider: "anthropic", Model: "claude-opus-4-8", Cost: ai.ModelCost{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}}}
	for _, tc := range []struct {
		name   string
		compat *ai.ModelCompat
	}{
		{"allowed fallback targets", &ai.ModelCompat{AllowedFallbackModels: fallbacks}},
		{"no fallback targets", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &summaryRecorder{}
			model := createSummaryModel(true, 8192, tc.compat)
			if _, _, err := generateSummary(t.Context(), summarizeThisMessages(), "", 2000, model, recorder, nil, "", "", nil, ""); err != nil {
				t.Fatal(err)
			}
			if len(recorder.calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(recorder.calls))
			}
			if recorder.calls[0].model != model || !reflect.DeepEqual(model.ProviderMeta.Compat, tc.compat) {
				t.Fatalf("summary model = %+v, want caller model with compat %+v", recorder.calls[0].model, tc.compat)
			}
		})
	}
}

func TestCompactClampsSummaryMaxTokensToModelOutputCap(t *testing.T) {
	recorder := &summaryRecorder{}
	prep := CompactionPreparation{
		FirstKeptEntryID:    "entry-keep",
		MessagesToSummarize: summarizeThisMessages(),
		TurnPrefixMessages:  summarizeThisMessages(),
		IsSplitTurn:         true,
		TokensBefore:        600000,
		FileOps:             NewFileOps(),
		Settings:            CompactionSettings{Enabled: true, ReserveTokens: 500000, KeepRecentTokens: 20000},
	}
	result, err := Compact(t.Context(), prep, createSummaryModel(false, 128000, nil), recorder, nil, "", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := (ai.Usage{Input: 20, Output: 20, TotalTokens: 40}); result.Usage == nil || !reflect.DeepEqual(*result.Usage, want) {
		t.Fatalf("usage = %+v, want %+v", result.Usage, want)
	}
	var maxTokens []int
	for _, call := range recorder.calls {
		maxTokens = append(maxTokens, call.options.MaxTokens)
	}
	if !reflect.DeepEqual(maxTokens, []int{128000, 128000}) {
		t.Fatalf("maxTokens = %v, want [128000 128000]", maxTokens)
	}
}
