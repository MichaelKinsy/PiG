package compaction

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports of the estimate and compaction-preparation cases in
// packages/coding-agent/test/session-context-edit.test.ts. The projection-only
// cases live in internal/codingagent/session_manager_context_edit_test.go.

func editAssistant(text string, usage ai.Usage) agent.AgentMessage {
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role:       agent.RoleAssistant,
		Content:    []ai.AssistantContentBlock{ai.TextContent{Text: text}},
		API:        "faux",
		Provider:   "faux",
		ModelID:    "faux",
		Usage:      &usage,
		StopReason: ai.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}}
}

var defaultEditUsage = ai.Usage{Input: 10, Output: 1, TotalTokens: 11}

func editUser(text string) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{
		Role:      agent.RoleUser,
		Content:   []ai.UserContentBlock{ai.TextContent{Text: text}},
		Timestamp: time.Now().UnixMilli(),
	}}
}

type editSession struct {
	t    *testing.T
	sess *codingagent.Session
}

func newEditSession(t *testing.T) *editSession {
	t.Helper()
	return &editSession{t: t, sess: codingagent.NewSession("s", t.TempDir())}
}

func (e *editSession) message(message agent.AgentMessage) string {
	e.t.Helper()
	id, err := e.sess.AppendMessage(message)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// rawMessage appends a message entry whose message is decoded from JSON, for
// roles AppendMessage routes elsewhere (system messages).
func (e *editSession) rawMessage(message string) string {
	e.t.Helper()
	var decoded agent.AgentMessage
	if err := json.Unmarshal([]byte(message), &decoded); err != nil {
		e.t.Fatal(err)
	}
	id, err := codingagent.GenerateEntryID()
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.sess.AppendEntry(codingagent.MessageEntry{
		SessionEntryBase: codingagent.SessionEntryBase{Type: "message", ID: id, ParentID: e.sess.LeafID(), Timestamp: codingagent.RFC3339NowNano()},
		Message:          decoded,
	}); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *editSession) custom(customType string, data any) string {
	e.t.Helper()
	id, err := codingagent.GenerateEntryID()
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.sess.AppendEntry(codingagent.CustomEntry{
		SessionEntryBase: codingagent.SessionEntryBase{Type: "custom", ID: id, ParentID: e.sess.LeafID(), Timestamp: codingagent.RFC3339NowNano()},
		CustomType:       customType,
		Data:             data,
	}); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *editSession) customMessage(customType, content string) string {
	e.t.Helper()
	id, err := e.sess.AppendCustomMessage(customType, content, false, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *editSession) edit(targetID string, replacement any) {
	e.t.Helper()
	var edit *codingagent.ContextEditReplacement
	if replacement != nil {
		raw, err := json.Marshal(replacement)
		if err != nil {
			e.t.Fatal(err)
		}
		edit = &codingagent.ContextEditReplacement{Content: raw}
	}
	if _, err := e.sess.AppendContextEdit(targetID, edit); err != nil {
		e.t.Fatal(err)
	}
}

func (e *editSession) compact(summary, firstKept string, tokensBefore int) {
	e.t.Helper()
	if _, err := e.sess.AppendCompaction(summary, firstKept, tokensBefore, nil, false, nil); err != nil {
		e.t.Fatal(err)
	}
}

func (e *editSession) branch() []codingagent.SessionEntry { return e.sess.Branch(*e.sess.LeafID()) }

func (e *editSession) estimate() agent.ContextUsageEstimate {
	return EstimateProjectedContextTokens(e.sess.BuildSessionProjection(), e.branch())
}

func (e *editSession) prepare() *CompactionPreparation {
	settings := DefaultCompactionSettings
	settings.KeepRecentTokens = 1
	return PrepareCompaction(e.branch(), settings)
}

func messagesJSON(t *testing.T, messages []agent.AgentMessage) string {
	t.Helper()
	raw, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestSessionContextEditUsesOnlyTheNewestSummaryWhenARepeatedCompactionRetainsEntriesBeforeTheOlderCompaction(t *testing.T) {
	e := newEditSession(t)
	e.message(editUser("summarized first"))
	retainedID := e.message(editUser("retained"))
	e.compact("first summary", retainedID, 100)
	e.message(editAssistant("after first compaction", defaultEditUsage))
	e.compact("second summary", retainedID, 80)
	e.message(editUser(strings.Repeat("new tail ", 100)))

	var summaries []string
	for _, message := range e.sess.BuildSessionProjection().Messages {
		if summary, ok := message.Custom["summary"].(string); ok {
			summaries = append(summaries, summary)
		}
	}
	if !slices.Equal(summaries, []string{"second summary"}) {
		t.Fatalf("summaries = %v", summaries)
	}
	if prep := e.prepare(); prep == nil || prep.PreviousSummary != "second summary" {
		t.Fatalf("preparation = %+v, want previous summary %q", prep, "second summary")
	}
}

func TestSessionContextEditDoesNotTrustPreEditAssistantUsageForProjectedContextEstimates(t *testing.T) {
	e := newEditSession(t)
	largeUserID := e.message(editUser(strings.Repeat("discarded input ", 2_000)))
	assistantID := e.message(editAssistant("small answer", ai.Usage{Input: 10_000, Output: 1, TotalTokens: 10_001}))
	e.edit(largeUserID, nil)

	edited := e.estimate()
	if edited.UsageTokens != 0 || edited.Tokens >= 100 {
		t.Fatalf("edited estimate = %+v, want no usage and < 100 tokens", edited)
	}
	e.edit(assistantID, nil)
	if got := e.estimate().Tokens; got != 0 {
		t.Fatalf("tokens after omitting everything = %d, want 0", got)
	}
}

func TestSessionContextEditUsesAssistantUsageCapturedAfterTheLatestContextEdit(t *testing.T) {
	e := newEditSession(t)
	userID := e.message(editUser("original"))
	e.edit(userID, "edited")
	e.message(editAssistant("answer", ai.Usage{Input: 4_000, Output: 100, TotalTokens: 4_100}))
	e.message(editUser("next"))

	got := e.estimate()
	if got.UsageTokens != 4_100 || got.TrailingTokens != 1 || got.Tokens != 4_101 {
		t.Fatalf("estimate = %+v, want usage 4100, trailing 1, tokens 4101", got)
	}
}

func TestSessionContextEditDoesNotReusePostEditAssistantUsageAfterALaterCompaction(t *testing.T) {
	e := newEditSession(t)
	userID := e.message(editUser("small input"))
	e.edit(userID, "edited input")
	e.message(editAssistant("answer", ai.Usage{Input: 50_000, Output: 1, TotalTokens: 50_001}))
	e.compact("small summary", userID, 50_001)

	got := e.estimate()
	if got.UsageTokens != 0 || got.Tokens >= 100 {
		t.Fatalf("estimate = %+v, want no usage and < 100 tokens", got)
	}
}

func TestSessionContextEditIncludesEffectiveSystemAndToolContextInEditedEstimates(t *testing.T) {
	e := newEditSession(t)
	system, err := json.Marshal(map[string]any{
		"role":    "system",
		"content": strings.Repeat("system prompt ", 3_000),
		"toolsAdded": []map[string]any{{
			"name":        "example",
			"description": strings.Repeat("tool declaration ", 100),
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
		}},
		"timestamp": time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.rawMessage(string(system))
	userID := e.message(editUser("ask"))
	e.message(editAssistant("done", defaultEditUsage))
	e.edit(userID, "ask")

	if got := e.estimate().Tokens; got <= 10_000 {
		t.Fatalf("tokens = %d, want > 10000", got)
	}
}

func TestSessionContextEditDoesNotAdvancePastABoundaryReplacementOfTheCandidateInput(t *testing.T) {
	e := newEditSession(t)
	e.message(editUser("old request"))
	e.message(editAssistant("old answer", defaultEditUsage))
	replacedUserID := e.message(editUser("original input"))
	assistantID := e.message(editAssistant("answered original input", defaultEditUsage))
	e.edit(replacedUserID, strings.Repeat("NEW-INSTRUCTION ", 100))
	e.edit(assistantID, nil)
	e.custom("bookkeeping", map[string]any{"source": "test"})

	prep := e.prepare()
	if prep == nil || prep.FirstKeptEntryID != replacedUserID {
		t.Fatalf("preparation = %+v, want first kept %s", prep, replacedUserID)
	}
	if strings.Contains(messagesJSON(t, prep.MessagesToSummarize), "NEW-INSTRUCTION") || strings.Contains(messagesJSON(t, prep.TurnPrefixMessages), "NEW-INSTRUCTION") {
		t.Fatal("replacement input was summarized")
	}
}

func TestSessionContextEditDoesNotLetMetadataMoveTheCutPastUnsentBoundaryInput(t *testing.T) {
	e := newEditSession(t)
	e.message(editUser("old request"))
	e.message(editAssistant("old answer", defaultEditUsage))
	instructionID := e.customMessage("next-work", strings.Repeat("UNSENT-INSTRUCTION ", 100))
	e.custom("bookkeeping", map[string]any{"source": "test"})

	prep := e.prepare()
	if prep == nil || prep.FirstKeptEntryID != instructionID {
		t.Fatalf("preparation = %+v, want first kept %s", prep, instructionID)
	}
	if strings.Contains(messagesJSON(t, prep.MessagesToSummarize), "UNSENT-INSTRUCTION") || strings.Contains(messagesJSON(t, prep.TurnPrefixMessages), "UNSENT-INSTRUCTION") {
		t.Fatal("unsent input was summarized")
	}
}

func TestSessionContextEditDoesNotTreatAnOmittedCustomMessageAsARecoveryAttempt(t *testing.T) {
	e := newEditSession(t)
	e.message(editUser(strings.Repeat("unanswered input ", 100)))
	customID := e.customMessage("temporary", "temporary context")
	e.edit(customID, nil)

	if prep := e.prepare(); prep != nil {
		t.Fatalf("preparation = %+v, want nil", prep)
	}
}

func TestSessionContextEditAdvancesPastInputForAnOmittedAssistantRecoverySuffixWithMetadata(t *testing.T) {
	e := newEditSession(t)
	userID := e.message(editUser(strings.Repeat("recovery input ", 100)))
	attemptID := e.message(editAssistant("failed attempt", defaultEditUsage))
	e.edit(attemptID, nil)
	e.custom("bookkeeping", map[string]any{"source": "test"})

	prep := e.prepare()
	if prep == nil || prep.FirstKeptEntryID != attemptID {
		t.Fatalf("preparation = %+v, want first kept %s", prep, attemptID)
	}
	if len(prep.TurnPrefixMessages) != 1 || prep.TurnPrefixMessages[0].User == nil || !strings.Contains(messagesJSON(t, prep.TurnPrefixMessages), "recovery input") {
		t.Fatalf("turn prefix = %s, want the recovery input", messagesJSON(t, prep.TurnPrefixMessages))
	}
	if strings.Contains(messagesJSON(t, prep.MessagesToSummarize), "recovery input") {
		t.Fatal("recovery input was summarized as history")
	}
	if userID == attemptID {
		t.Fatal("user and attempt share an ID")
	}
}

func TestSessionContextEditPreparesCompactionFromEditedModelContent(t *testing.T) {
	e := newEditSession(t)
	omittedID := e.message(editUser(strings.Repeat("OMIT-ME ", 100)))
	e.message(editAssistant(strings.Repeat("old answer ", 100), defaultEditUsage))
	e.edit(omittedID, nil)
	e.message(editUser("keep"))
	e.message(editAssistant("suffix", defaultEditUsage))

	prep := e.prepare()
	if prep == nil {
		t.Fatal("preparation = nil")
	}
	if strings.Contains(messagesJSON(t, prep.MessagesToSummarize), "OMIT-ME") || strings.Contains(messagesJSON(t, prep.TurnPrefixMessages), "OMIT-ME") {
		t.Fatal("omitted content was summarized")
	}
}

// upstreamCompactionSource returns the pinned Pi compaction source file.
func upstreamCompactionSource(t *testing.T, name string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "..", ".upstream", "current", "packages", "coding-agent", "src", "core", "compaction", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(source)
}

// upstreamTemplateConstant returns a `const NAME = `...`;` template literal
// from source, expanding ${OTHER} references to other template constants.
func upstreamTemplateConstant(t *testing.T, source, name string) string {
	t.Helper()
	match := regexp.MustCompile("(?s)(?:export )?const " + name + " = `(.*?)`;").FindStringSubmatch(source)
	if match == nil {
		t.Fatalf("constant %s not found in upstream source", name)
	}
	return regexp.MustCompile(`\$\{([A-Z_]+)\}`).ReplaceAllStringFunc(match[1], func(reference string) string {
		return upstreamTemplateConstant(t, source, reference[2:len(reference)-1])
	})
}

func TestSummarizationPromptsMatchUpstreamSourceByteForByte(t *testing.T) {
	compactionSource := upstreamCompactionSource(t, "compaction.ts")
	for _, tc := range []struct {
		name, got string
	}{
		{"TURN_PREFIX_SUMMARIZATION_PROMPT", turnPrefixSummarizationPrompt},
		{"SUMMARIZATION_PROMPT", SUMMARIZATION_PROMPT},
		{"UPDATE_SUMMARIZATION_PROMPT", UPDATE_SUMMARIZATION_PROMPT},
	} {
		if want := upstreamTemplateConstant(t, compactionSource, tc.name); tc.got != want {
			t.Errorf("%s differs from upstream:\n got %q\nwant %q", tc.name, tc.got, want)
		}
	}
	if want := upstreamTemplateConstant(t, upstreamCompactionSource(t, "utils.ts"), "SUMMARIZATION_SYSTEM_PROMPT"); SummarizationSystemPrompt != want {
		t.Errorf("SUMMARIZATION_SYSTEM_PROMPT differs from upstream:\n got %q\nwant %q", SummarizationSystemPrompt, want)
	}
}

func TestTurnPrefixSummaryUsesMarkdownHeadingFraming(t *testing.T) {
	var prompts []string
	completer := simpleCompleterFunc(func(_ context.Context, _ *ai.Model, systemPrompt string, messages []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
		if systemPrompt != SummarizationSystemPrompt {
			t.Errorf("system prompt = %q", systemPrompt)
		}
		prompts = append(prompts, projectedMessageText(messages[0]))
		return "prefix summary", nil, nil
	})
	prefix := []agent.AgentMessage{editUser("build the parser"), editAssistant("started on lexer.go", defaultEditUsage)}
	result, err := Compact(context.Background(), CompactionPreparation{
		FirstKeptEntryID:   "keep",
		TurnPrefixMessages: prefix,
		IsSplitTurn:        true,
		PreviousSummary:    "earlier checkpoint",
		Settings:           CompactionSettings{ReserveTokens: 1000},
	}, &ai.Model{}, completer, nil, "", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Conversation\n" + SerializeConversation(convertToLlm(prefix)) + "\n\n# Instructions\n" + turnPrefixSummarizationPrompt
	if len(prompts) != 1 || prompts[0] != want {
		t.Fatalf("turn prefix prompt:\n got %q\nwant %q", prompts, want)
	}
	// With no history to summarize, the previous summary stands in for it.
	if !strings.HasPrefix(result.Summary, "earlier checkpoint\n\n---\n\n**Turn Context (split turn):**\n\nprefix summary") {
		t.Fatalf("summary = %q", result.Summary)
	}
}

func projectedMessageText(message agent.AgentMessage) string {
	var text strings.Builder
	if message.User != nil {
		for _, block := range message.User.Content {
			if block, ok := block.(ai.TextContent); ok {
				text.WriteString(block.Text)
			}
		}
	}
	return text.String()
}

func TestEstimateTokensCountsJavaScriptLengthsImagesAndCustomBlocks(t *testing.T) {
	cases := []struct {
		name    string
		message agent.AgentMessage
		want    int
	}{
		{"astral characters count two UTF-16 units", editUser("😀😀"), 1},
		{"multi-byte BMP characters count one unit", editUser("éééé"), 1},
		{"images count 4800 characters", agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.ImageContent{Data: "x", MimeType: "image/png"}}}}, 1200},
		{"custom content blocks", agent.AgentMessage{Custom: map[string]any{"role": agent.RoleCustom, "content": []any{map[string]any{"type": "text", "text": "abcdefgh"}}}}, 2},
		{"unknown roles count zero", agent.AgentMessage{Custom: map[string]any{"role": "future", "content": "abcdefgh"}}, 0},
		{"system content, sections, and tools", agent.AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText("abcd"), Sections: ai.OrderedSections{{Name: "env", Value: new("efgh")}}}}, 2},
	}
	for _, tc := range cases {
		if got := EstimateTokens(tc.message); got != tc.want {
			t.Errorf("%s: EstimateTokens = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestConvertToLlmPreservesTypedSystemState(t *testing.T) {
	system := ai.SystemMessage{Content: ai.SystemText("instructions"), Sections: ai.OrderedSections{{Name: "env", Value: new("linux")}}, ToolsAdded: []ai.ToolSchema{{Name: "read"}}, Timestamp: 123}
	converted := convertToLlm([]agent.AgentMessage{{System: &system}})
	if len(converted) != 1 {
		t.Fatalf("converted %d messages, want system baseline", len(converted))
	}
	got, ok := converted[0].(ai.SystemMessage)
	if !ok || got.Timestamp != system.Timestamp || ai.GetCurrentSystemPrompt(converted) != ai.GetCurrentSystemPrompt([]ai.Message{system}) || len(ai.GetCurrentTools(converted)) != 1 {
		t.Fatalf("lost system state: %#v", converted)
	}
}
