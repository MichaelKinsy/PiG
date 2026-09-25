package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// ─── Helpers ──────────────────────────────────────────────────────────────────

func mustSessionEntry(t *testing.T, v map[string]any) codingagent.SessionEntry {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	var base codingagent.SessionEntryBase
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatalf("unmarshal base: %v", err)
	}
	return codingagent.NewSessionEntry(raw, base)
}

func userEntry(id, text string) map[string]any {
	return map[string]any{
		"type": "message", "id": id, "parentId": nil, "timestamp": time.Now().UTC().Format(time.RFC3339),
		"message": map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": text}},
		},
	}
}

func assistantEntry(id, text string) map[string]any {
	return map[string]any{
		"type": "message", "id": id, "parentId": nil, "timestamp": time.Now().UTC().Format(time.RFC3339),
		"message": map[string]any{
			"role":    "assistant",
			"content": []any{map[string]any{"type": "text", "text": text}},
		},
	}
}

func bashEntry(id, cmd, out string) map[string]any {
	return map[string]any{
		"type": "bash_execution", "id": id, "parentId": nil, "timestamp": time.Now().UTC().Format(time.RFC3339),
		"role": "bashExecution", "command": cmd, "output": out,
	}
}

func compactionEntry(id, summary, firstKeptID string, tokensBefore int) map[string]any {
	return map[string]any{
		"type": "compaction", "id": id, "parentId": nil, "timestamp": time.Now().UTC().Format(time.RFC3339),
		"summary": summary, "firstKeptEntryId": firstKeptID, "tokensBefore": tokensBefore,
	}
}

// fakeCompleter returns a fixed string for all CompleteSimple calls.
type fakeCompleter struct {
	response  string
	usage     *ai.Usage
	maxTokens []int
}

func (f *fakeCompleter) CompleteSimple(_ context.Context, _ *ai.Model, _ string, _ []agent.AgentMessage, options ai.StreamOptions) (string, *ai.Usage, error) {
	f.maxTokens = append(f.maxTokens, options.MaxTokens)
	return f.response, f.usage, nil
}

func TestCapMaxTokens(t *testing.T) {
	cases := []struct {
		name   string
		budget int
		model  *ai.Model
		want   int
	}{
		{name: "uncapped without model limit", budget: 1000, model: &ai.Model{}, want: 1000},
		{name: "capped by model max output", budget: 1000, model: &ai.Model{Capabilities: ai.ModelCapabilities{MaxOutputTokens: 600}}, want: 600},
		{name: "negative budget clamps to zero", budget: -1, model: &ai.Model{Capabilities: ai.ModelCapabilities{MaxOutputTokens: 600}}, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := capMaxTokens(tc.budget, tc.model); got != tc.want {
				t.Fatalf("capMaxTokens(%d) = %d, want %d", tc.budget, got, tc.want)
			}
		})
	}
}

func TestCompactMissingFirstKeptEntryMatchesUpstreamError(t *testing.T) {
	_, err := Compact(t.Context(), CompactionPreparation{
		MessagesToSummarize: []agent.AgentMessage{{User: &agent.UserMessage{Role: "user"}}},
		Settings:            CompactionSettings{ReserveTokens: 100},
	}, &ai.Model{}, &fakeCompleter{response: "summary"}, nil, "", "", nil, "")
	if err == nil || err.Error() != "First kept entry has no UUID - session may need migration" {
		t.Fatalf("Compact() error = %v", err)
	}
}

// ─── TestShouldCompact ────────────────────────────────────────────────────────

func TestShouldCompact(t *testing.T) {
	s := CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}
	cases := []struct {
		name          string
		contextTokens int
		contextWindow int
		want          bool
	}{
		{"over_threshold", 130_000, 128_000, true},
		{"under_threshold", 110_000, 128_000, false},
		{"at_threshold", 128_000 - 16_384, 128_000, false},
		{"just_over_threshold", 128_000 - 16_384 + 1, 128_000, true},
		{"zero_window", 130_000, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldCompact(tc.contextTokens, tc.contextWindow, s); got != tc.want {
				t.Errorf("ShouldCompact(%d, %d) = %v, want %v", tc.contextTokens, tc.contextWindow, got, tc.want)
			}
		})
	}

	// disabled setting always returns false
	disabled := CompactionSettings{Enabled: false, ReserveTokens: 16384}
	if ShouldCompact(999_999, 128_000, disabled) {
		t.Error("disabled settings should never compact")
	}
}

// ─── TestBashExecutionCutPoint ────────────────────────────────────────────────

// TestBashExecutionCutPoint proves that bash_execution entries appear in
// findValidCutPoints output. Folds row 4.y.1.
func TestBashExecutionCutPoint(t *testing.T) {
	entries := []codingagent.SessionEntry{
		mustSessionEntry(t, userEntry("u1", "hello")),
		mustSessionEntry(t, assistantEntry("a1", "ok")),
		mustSessionEntry(t, bashEntry("b1", "ls -la", "file1.go\nfile2.go")),
		mustSessionEntry(t, assistantEntry("a2", "done")),
	}

	pts := findValidCutPoints(entries, 0, len(entries))

	// Must include the bash_execution entry at index 2.
	found := false
	for _, idx := range pts {
		if entries[idx].Base.Type == "bash_execution" {
			found = true
		}
	}
	if !found {
		t.Errorf("bash_execution entry not found in cut points: %v", pts)
	}
}

// ─── TestFindCutPoint ─────────────────────────────────────────────────────────

func TestFindCutPoint(t *testing.T) {
	// Build a 10-entry session: 5 user+assistant turns, each ~400 chars.
	// keepRecentTokens=5000 should keep roughly the last 2 turns (2*2 entries ≈ 200 tokens).
	// The cut should land on a user or assistant message, never on a tool result.
	entries := make([]codingagent.SessionEntry, 0, 10)
	for range 5 {
		n := len(entries)
		uid := strings.Repeat("u", 1) + strings.Repeat("0", 0)
		_ = uid
		id := func(prefix string, i int) string {
			return prefix + string(rune('a'+i))
		}
		entries = append(entries,
			mustSessionEntry(t, userEntry(id("u", n), strings.Repeat("w", 400))),
			mustSessionEntry(t, assistantEntry(id("a", n), strings.Repeat("r", 400))),
		)
	}

	cut := FindCutPoint(entries, 0, len(entries), 5000)

	// FirstKeptEntryIndex must be valid.
	if cut.FirstKeptEntryIndex < 0 || cut.FirstKeptEntryIndex >= len(entries) {
		t.Fatalf("FirstKeptEntryIndex %d out of range [0, %d)", cut.FirstKeptEntryIndex, len(entries))
	}

	// The cut entry must never be a tool_result message.
	cutEntry := entries[cut.FirstKeptEntryIndex]
	if cutEntry.Base.Type == "message" {
		me, ok := cutEntry.AsMessage()
		if ok && me.Message.ToolResult != nil {
			t.Errorf("cut landed on a tool_result entry at index %d", cut.FirstKeptEntryIndex)
		}
	}
}

// ─── TestPrepareCompaction ────────────────────────────────────────────────────

func TestPrepareCompaction(t *testing.T) {
	// Build a 20-entry session with a prior compaction at entry 5.
	// Layout: 5 entries (u+a pairs), compaction entry, then 14 more entries.
	entries := make([]codingagent.SessionEntry, 0, 20)

	// First 4 entries (2 turns).
	for i := range 2 {
		entries = append(entries,
			mustSessionEntry(t, userEntry("u"+string(rune('0'+i)), strings.Repeat("x", 200))),
			mustSessionEntry(t, assistantEntry("a"+string(rune('0'+i)), strings.Repeat("y", 200))),
		)
	}

	// Compaction entry pointing at "u2" as firstKeptEntryId.
	entries = append(entries, mustSessionEntry(t, compactionEntry("c0", "Previous summary.", "u2", 5000)))

	// 15 more entries starting at "u2" (the firstKeptEntryId).
	for i := range 7 {
		uid := "u" + string(rune('2'+i))
		aid := "a" + string(rune('2'+i))
		entries = append(entries,
			mustSessionEntry(t, userEntry(uid, strings.Repeat("m", 200))),
			mustSessionEntry(t, assistantEntry(aid, strings.Repeat("n", 200))),
		)
	}
	// One final user entry.
	entries = append(entries, mustSessionEntry(t, userEntry("ufinal", strings.Repeat("z", 200))))

	s := CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 500}
	prep := PrepareCompaction(entries, s)

	if prep == nil {
		t.Fatal("PrepareCompaction returned nil; expected non-nil")
	}
	if prep.FirstKeptEntryID == "" {
		t.Error("FirstKeptEntryID is empty")
	}
	if len(prep.MessagesToSummarize) == 0 {
		t.Error("MessagesToSummarize is empty; expected at least some messages")
	}
	if prep.PreviousSummary != "Previous summary." {
		t.Errorf("PreviousSummary = %q, want %q", prep.PreviousSummary, "Previous summary.")
	}
}

// TestPrepareCompaction_NothingToCompact proves the 0.79.10 empty-compaction
// guard: when keepRecentTokens retains every message (session too small), no
// message falls into the summarize window, so PrepareCompaction returns nil.
// Mirrors upstream compaction.ts (0.79.10): messagesToSummarize.length===0 &&
// turnPrefixMessages.length===0 → return undefined. Without the guard the
// function returns a non-nil preparation with an empty MessagesToSummarize,
// driving a degenerate empty compaction that upstream refuses.
func TestPrepareCompaction_NothingToCompact(t *testing.T) {
	entries := []codingagent.SessionEntry{
		mustSessionEntry(t, userEntry("u0", "hello")),
		mustSessionEntry(t, assistantEntry("a0", "hi there")),
		mustSessionEntry(t, userEntry("u1", "another question")),
		mustSessionEntry(t, assistantEntry("a1", "a short reply")),
	}
	// keepRecentTokens far exceeds the session size, so findCutPoint keeps
	// everything and nothing is left to summarize.
	s := CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}
	if prep := PrepareCompaction(entries, s); prep != nil {
		t.Fatalf("PrepareCompaction = %+v; want nil (nothing to summarize)", prep)
	}
}

// TestPrepareCompaction_PriorCompactionKeptFromRoot guards the boundaryStart
// fix: when the prior compaction's firstKeptEntryId resolves to index 0, every
// entry up to the prior compaction must still feed the next summary's input,
// not be skipped. The old `boundaryStart == 0` sentinel conflated "found at
// index 0" with "not found" and dropped them: losing conversation up to the
// last compaction. Upstream keys the fallback off `firstKeptEntryIndex >= 0`.
func TestPrepareCompaction_PriorCompactionKeptFromRoot(t *testing.T) {
	const marker = "FIRSTENTRYMARKER"
	entries := []codingagent.SessionEntry{
		mustSessionEntry(t, userEntry("u0", marker)),                        // index 0 == firstKeptEntryId
		mustSessionEntry(t, assistantEntry("a0", strings.Repeat("y", 200))), // index 1
		mustSessionEntry(t, compactionEntry("c0", "Prev.", "u0", 5000)),     // index 2
	}
	for i := range 8 {
		entries = append(entries,
			mustSessionEntry(t, userEntry("un"+string(rune('0'+i)), strings.Repeat("m", 200))),
			mustSessionEntry(t, assistantEntry("an"+string(rune('0'+i)), strings.Repeat("n", 200))),
		)
	}

	s := CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 500}
	prep := PrepareCompaction(entries, s)
	if prep == nil {
		t.Fatal("PrepareCompaction returned nil")
	}
	found := false
	for _, m := range prep.MessagesToSummarize {
		if m.User == nil {
			continue
		}
		for _, c := range m.User.Content {
			if tc, ok := c.(ai.TextContent); ok && strings.Contains(tc.Text, marker) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("MessagesToSummarize dropped the index-0 entry (firstKeptEntryId); boundaryStart bug regressed")
	}
}

// TestPrepareCompaction_NilWhenLastEntryIsCompaction verifies we return nil
// when the last entry is already a compaction.
func TestPrepareCompaction_NilWhenLastEntryIsCompaction(t *testing.T) {
	entries := []codingagent.SessionEntry{
		mustSessionEntry(t, userEntry("u1", "hi")),
		mustSessionEntry(t, compactionEntry("c1", "summary", "u1", 100)),
	}
	s := DefaultCompactionSettings
	if got := PrepareCompaction(entries, s); got != nil {
		t.Error("expected nil when last entry is compaction")
	}
}

// ─── TestCompact_FakeCompleter ────────────────────────────────────────────────

type orderedCompleter struct {
	historyStarted chan struct{}
	prefixStarted  chan struct{}
	releaseHistory chan struct{}
}

func (c *orderedCompleter) CompleteSimple(_ context.Context, _ *ai.Model, _ string, messages []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
	var prompt strings.Builder
	if len(messages) > 0 && messages[0].User != nil {
		for _, block := range messages[0].User.Content {
			if text, ok := block.(ai.TextContent); ok {
				prompt.WriteString(text.Text)
			}
		}
	}
	if strings.Contains(prompt.String(), "# Instructions\n"+turnPrefixSummarizationPrompt) {
		close(c.prefixStarted)
		return "prefix", nil, nil
	}
	close(c.historyStarted)
	<-c.releaseHistory
	return "history", nil, nil
}

func TestCompactCustomInstructionsReachSummaryPrompt(t *testing.T) {
	var prompt strings.Builder
	completer := simpleCompleterFunc(func(_ context.Context, _ *ai.Model, _ string, messages []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
		if len(messages) > 0 && messages[0].User != nil {
			for _, block := range messages[0].User.Content {
				if text, ok := block.(ai.TextContent); ok {
					prompt.WriteString(text.Text)
				}
			}
		}
		return "summary", nil, nil
	})
	prep := CompactionPreparation{
		FirstKeptEntryID: "keep-1",
		MessagesToSummarize: []agent.AgentMessage{{User: &agent.UserMessage{
			Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "history"}},
		}}},
		Settings: CompactionSettings{ReserveTokens: 1000},
	}
	if _, err := Compact(context.Background(), prep, &ai.Model{}, completer, nil, "Keep exact file names", "", nil, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.String(), "Additional focus: Keep exact file names") {
		t.Fatalf("summary prompt omitted custom instructions:\n%s", prompt.String())
	}
}

type simpleCompleterFunc func(context.Context, *ai.Model, string, []agent.AgentMessage, ai.StreamOptions) (string, *ai.Usage, error)

func (f simpleCompleterFunc) CompleteSimple(ctx context.Context, model *ai.Model, systemPrompt string, messages []agent.AgentMessage, options ai.StreamOptions) (string, *ai.Usage, error) {
	return f(ctx, model, systemPrompt, messages, options)
}

func TestCompactSplitTurnAwaitsHistoryBeforePrefix(t *testing.T) {
	completer := &orderedCompleter{
		historyStarted: make(chan struct{}),
		prefixStarted:  make(chan struct{}),
		releaseHistory: make(chan struct{}),
	}
	prep := CompactionPreparation{
		FirstKeptEntryID: "keep-1",
		MessagesToSummarize: []agent.AgentMessage{{User: &agent.UserMessage{
			Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "history"}},
		}}},
		TurnPrefixMessages: []agent.AgentMessage{{User: &agent.UserMessage{
			Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "prefix"}},
		}}},
		IsSplitTurn: true,
		Settings:    CompactionSettings{ReserveTokens: 1000},
	}
	done := make(chan error, 1)
	go func() {
		_, err := Compact(context.Background(), prep, &ai.Model{}, completer, nil, "", "", nil, "")
		done <- err
	}()

	select {
	case <-completer.historyStarted:
	case <-time.After(time.Second):
		t.Fatal("history summary did not start")
	}
	prefixStartedEarly := false
	select {
	case <-completer.prefixStarted:
		prefixStartedEarly = true
	case <-time.After(100 * time.Millisecond):
	}
	close(completer.releaseHistory)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if prefixStartedEarly {
		t.Fatal("turn-prefix summary started before history summary completed")
	}
	writeClosureTrace(t,
		closureTraceEvent{kind: "start", subject: "history-summary", value: "history-started"},
		closureTraceEvent{kind: "complete", subject: "history-summary", value: "history-completed"},
		closureTraceEvent{kind: "start", subject: "turn-prefix-summary", value: "prefix-started-after-history"},
	)
}

func TestCompactTurnPrefixCancellationSurfacesUpstreamError(t *testing.T) {
	prep := CompactionPreparation{
		FirstKeptEntryID: "keep-1",
		TurnPrefixMessages: []agent.AgentMessage{{User: &agent.UserMessage{
			Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "prefix"}},
		}}},
		IsSplitTurn: true,
		Settings:    CompactionSettings{ReserveTokens: 1000},
	}
	completer := &cancelledCompleter{}
	_, err := Compact(context.Background(), prep, &ai.Model{}, completer, nil, "", "", nil, "")
	if err == nil {
		t.Fatal("Compact error = nil")
	}
	if got, want := err.Error(), "Turn prefix summarization failed: This operation was aborted"; got != want {
		t.Fatalf("Compact error = %q, want %q", got, want)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("summarization failure must not be classified as direct operation cancellation")
	}
	writeClosureTrace(t,
		closureTraceEvent{kind: "cancel", subject: "turn-prefix-summary", value: context.Canceled.Error()},
		closureTraceEvent{kind: "error", subject: "compact", value: err.Error()},
	)
}

type cancelledCompleter struct{}

func (*cancelledCompleter) CompleteSimple(context.Context, *ai.Model, string, []agent.AgentMessage, ai.StreamOptions) (string, *ai.Usage, error) {
	return "", nil, context.Canceled
}

func TestCompact_UsesMaxTokensBudget(t *testing.T) {
	prep := CompactionPreparation{
		FirstKeptEntryID:    "kept-entry-1",
		MessagesToSummarize: []agent.AgentMessage{{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "hello"}}}}},
		TokensBefore:        1000,
		FileOps:             NewFileOps(),
		Settings:            CompactionSettings{ReserveTokens: 1000, KeepRecentTokens: 20000},
	}

	fc := &fakeCompleter{response: "## Summary\nThis is the summary."}
	model := &ai.Model{ID: "fake-model", Capabilities: ai.ModelCapabilities{MaxOutputTokens: 600}}

	result, err := Compact(context.Background(), prep, model, fc, nil, "", "", nil, "")
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	if result.Summary == "" {
		t.Fatal("Summary is empty")
	}
	if len(fc.maxTokens) != 1 {
		t.Fatalf("CompleteSimple call count = %d, want 1", len(fc.maxTokens))
	}
	if fc.maxTokens[0] != 600 {
		t.Fatalf("maxTokens = %d, want 600", fc.maxTokens[0])
	}
}

func TestCompact_UsesStreamFnWhenProvided(t *testing.T) {
	prep := &CompactionPreparation{
		FirstKeptEntryID:    "keep-1",
		MessagesToSummarize: []agent.AgentMessage{{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "hello"}}}}},
		Settings:            CompactionSettings{ReserveTokens: 1000},
		FileOps:             FileOperations{},
	}
	completer := &fakeCompleter{response: "fallback"}
	streamCalls := 0
	streamFn := func(_ context.Context, _ *ai.Model, _ string, _ []agent.AgentMessage, options ai.StreamOptions) (string, *ai.Usage, error) {
		streamCalls++
		if options.MaxTokens <= 0 {
			t.Fatalf("maxTokens = %d, want > 0", options.MaxTokens)
		}
		return "stream summary", nil, nil
	}

	result, err := Compact(context.Background(), *prep, &ai.Model{}, completer, streamFn, "", "", nil, "")
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.Summary != "stream summary" {
		t.Fatalf("summary = %q", result.Summary)
	}
	if streamCalls != 1 {
		t.Fatalf("streamCalls = %d, want 1", streamCalls)
	}
	if len(completer.maxTokens) != 0 {
		t.Fatalf("CompleteSimple should not run when streamFn is provided; maxTokens calls = %v", completer.maxTokens)
	}
}

func TestCompact_FakeCompleter(t *testing.T) {
	prep := CompactionPreparation{
		FirstKeptEntryID: "kept-entry-1",
		MessagesToSummarize: []agent.AgentMessage{
			{
				User: &agent.UserMessage{
					Role:    "user",
					Content: []ai.UserContentBlock{ai.TextContent{Text: "do something"}},
				},
			},
		},
		TokensBefore: 1000,
		FileOps:      NewFileOps(),
		Settings:     DefaultCompactionSettings,
	}
	prep.FileOps.Read["internal/foo.go"] = struct{}{}

	fc := &fakeCompleter{response: "## Summary\nThis is the summary."}
	model := &ai.Model{ID: "fake-model"}

	result, err := Compact(context.Background(), prep, model, fc, nil, "", "", nil, "")
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	if result.Summary == "" {
		t.Error("Summary is empty")
	}
	if !strings.Contains(result.Summary, "## Summary") {
		t.Errorf("Summary does not contain fake response; got: %q", result.Summary[:min(100, len(result.Summary))])
	}
	if result.FirstKeptEntryID != "kept-entry-1" {
		t.Errorf("FirstKeptEntryID = %q, want %q", result.FirstKeptEntryID, "kept-entry-1")
	}
	if result.TokensBefore != 1000 {
		t.Errorf("TokensBefore = %d, want 1000", result.TokensBefore)
	}
}

func TestCombineUsage(t *testing.T) {
	aReasoning, aCacheWrite1h := 1, 1
	bReasoning, bCacheWrite1h := 2, 2
	a := &ai.Usage{Input: 10, Output: 5, Reasoning: &aReasoning, CacheRead: 2, CacheWrite: 3, CacheWrite1h: &aCacheWrite1h}
	b := &ai.Usage{Input: 20, Output: 7, Reasoning: &bReasoning, CacheRead: 4, CacheWrite: 6, CacheWrite1h: &bCacheWrite1h}
	got := combineUsage(a, b)
	if got.Input != 30 || got.Output != 12 || got.CacheRead != 6 || got.CacheWrite != 9 || got.Reasoning == nil || *got.Reasoning != 3 || got.CacheWrite1h == nil || *got.CacheWrite1h != 3 {
		t.Fatalf("combineUsage = %+v", *got)
	}
	if combineUsage(nil, b) != b || combineUsage(a, nil) != a {
		t.Fatalf("combineUsage should return the non-nil operand when one side is nil")
	}
}

// TestCompactPropagatesUsage verifies the summarization call's usage surfaces on
// CompactionResult so it can be persisted and counted toward session cost.
func TestCompactPropagatesUsage(t *testing.T) {
	prep := CompactionPreparation{
		FirstKeptEntryID: "keep-1",
		MessagesToSummarize: []agent.AgentMessage{{User: &agent.UserMessage{
			Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "hi"}},
		}}},
		Settings: CompactionSettings{ReserveTokens: 1000},
	}
	fc := &fakeCompleter{response: "summary", usage: &ai.Usage{Input: 42, Output: 7}}
	result, err := Compact(context.Background(), prep, &ai.Model{}, fc, nil, "", "", nil, "")
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.Usage == nil || result.Usage.Input != 42 || result.Usage.Output != 7 {
		t.Fatalf("result.Usage = %+v, want input 42 output 7", result.Usage)
	}
}
