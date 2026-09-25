package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// ─── Test helpers ─────────────────────────────────────────────────────────────

// memSession is a minimal ReadonlySession for tests.
type memSession struct {
	entries map[string]codingagent.SessionEntry
	// roots: list of root→leaf chains as ID slices
	chains map[string][]string // leafID → ordered IDs (root first)
}

func (m *memSession) Branch(leafID string) []codingagent.SessionEntry {
	ids, ok := m.chains[leafID]
	if !ok {
		return nil
	}
	out := make([]codingagent.SessionEntry, 0, len(ids))
	for _, id := range ids {
		if e, ok := m.entries[id]; ok {
			out = append(out, e)
		}
	}
	return out
}

func (m *memSession) EntryByID(id string) (codingagent.SessionEntry, bool) {
	e, ok := m.entries[id]
	return e, ok
}

// makeEntry builds a SessionEntry of the given type with the given IDs.
func makeEntry(typ, id string, parentID *string) codingagent.SessionEntry {
	base := codingagent.SessionEntryBase{
		Type:     typ,
		ID:       id,
		ParentID: parentID,
	}
	raw, _ := json.Marshal(map[string]any{
		"type":     typ,
		"id":       id,
		"parentId": parentIDOrNull(parentID),
	})
	return codingagent.NewSessionEntry(raw, base)
}

func parentIDOrNull(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// makeMessageEntry builds a user "message" SessionEntry.
func makeMessageEntry(id string, parentID *string, tokens int) codingagent.SessionEntry {
	text := make([]byte, tokens*4) // 4 chars/token heuristic
	for i := range text {
		text[i] = 'x'
	}
	base := codingagent.SessionEntryBase{
		Type:     "message",
		ID:       id,
		ParentID: parentID,
	}
	raw, _ := json.Marshal(map[string]any{
		"type":     "message",
		"id":       id,
		"parentId": parentIDOrNull(parentID),
		"message": map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": string(text)}},
		},
	})
	return codingagent.NewSessionEntry(raw, base)
}

// makeBranchSummaryEntry builds a branch_summary SessionEntry.
func makeBranchSummaryEntry(id string, parentID *string, summary string, fromHook bool, details *BranchSummaryDetails) codingagent.SessionEntry {
	base := codingagent.SessionEntryBase{
		Type:     "branch_summary",
		ID:       id,
		ParentID: parentID,
	}
	body := map[string]any{
		"type":     "branch_summary",
		"id":       id,
		"parentId": parentIDOrNull(parentID),
		"fromId":   "",
		"summary":  summary,
		"fromHook": fromHook,
	}
	if details != nil {
		body["details"] = details
	}
	raw, _ := json.Marshal(body)
	return codingagent.NewSessionEntry(raw, base)
}

// branchFakeCompleter implements SimpleCompleter for branch summarization tests.
type branchFakeCompleter struct {
	result  string
	err     error
	aborted bool // if true, returns context.Canceled mimicking abort
}

func (f *branchFakeCompleter) CompleteSimple(ctx context.Context, _ *ai.Model, _ string, _ []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
	if f.aborted {
		return "", nil, context.Canceled
	}
	if f.err != nil {
		return "", nil, f.err
	}
	return f.result, nil, nil
}

// ─── TestCollectEntriesForBranchSummary ───────────────────────────────────────

// TestCollectEntriesForBranchSummary: 4-node chain A→B→C→D.
// oldLeafID="D", targetID="B"
// Expected: entries=[C,D] in chronological order, commonAncestorID="B".
func TestCollectEntriesForBranchSummary(t *testing.T) {
	// Build chain A→B→C→D
	entA := makeEntry("message", "A", nil)
	entB := makeEntry("message", "B", new("A"))
	entC := makeEntry("message", "C", new("B"))
	entD := makeEntry("message", "D", new("C"))

	sess := &memSession{
		entries: map[string]codingagent.SessionEntry{
			"A": entA, "B": entB, "C": entC, "D": entD,
		},
		chains: map[string][]string{
			"D": {"A", "B", "C", "D"},
			"B": {"A", "B"},
		},
	}

	result := CollectEntriesForBranchSummary(sess, "D", "B")

	if result.CommonAncestorID != "B" {
		t.Errorf("commonAncestorID: got %q, want %q", result.CommonAncestorID, "B")
	}
	if len(result.Entries) != 2 {
		t.Fatalf("entries length: got %d, want 2", len(result.Entries))
	}
	if result.Entries[0].Base.ID != "C" {
		t.Errorf("entries[0].ID: got %q, want %q", result.Entries[0].Base.ID, "C")
	}
	if result.Entries[1].Base.ID != "D" {
		t.Errorf("entries[1].ID: got %q, want %q", result.Entries[1].Base.ID, "D")
	}
}

// TestCollectEntries_NoOldLeaf: oldLeafID="" → empty entries, empty commonAncestorID.
func TestCollectEntries_NoOldLeaf(t *testing.T) {
	sess := &memSession{
		entries: map[string]codingagent.SessionEntry{},
		chains:  map[string][]string{},
	}

	result := CollectEntriesForBranchSummary(sess, "", "B")

	if len(result.Entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(result.Entries))
	}
	if result.CommonAncestorID != "" {
		t.Errorf("expected empty commonAncestorID, got %q", result.CommonAncestorID)
	}
}

// ─── TestPrepareBranchEntries ─────────────────────────────────────────────────

// TestPrepareBranchEntries_TokenBudget: entries summing to ~8000 tokens with
// budget=5000 → total ≤ 5000; file ops collected from ALL entries.
func TestPrepareBranchEntries_TokenBudget(t *testing.T) {
	// Each entry is 2000 tokens (2000*4 = 8000 chars). Four entries = 8000 tokens.
	e1 := makeMessageEntry("1", nil, 2000)
	e2 := makeMessageEntry("2", new("1"), 2000)
	e3 := makeMessageEntry("3", new("2"), 2000)
	e4 := makeMessageEntry("4", new("3"), 2000)

	entries := []codingagent.SessionEntry{e1, e2, e3, e4}
	prep := PrepareBranchEntries(entries, 5000)

	if prep.TotalTokens > 5000 {
		t.Errorf("TotalTokens %d exceeds budget 5000", prep.TotalTokens)
	}
}

// TestPrepareBranchEntries_SummaryEntryFitsPriority: a branch_summary entry
// that would exceed budget at 89% usage → still included.
func TestPrepareBranchEntries_SummaryEntryFitsPriority(t *testing.T) {
	// Budget = 1000. First build a message using 880 tokens (88%).
	msgEntry := makeMessageEntry("1", nil, 880)
	// Build a branch_summary entry with ~200 tokens (would push to 1080 > 1000).
	// 200 * 4 = 800 chars summary text.
	summaryText := make([]byte, 800)
	for i := range summaryText {
		summaryText[i] = 's'
	}
	bsEntry := makeBranchSummaryEntry("2", new("1"), string(summaryText), false, nil)

	entries := []codingagent.SessionEntry{msgEntry, bsEntry}
	prep := PrepareBranchEntries(entries, 1000)

	// The branch_summary should be included (priority fit: 88% < 90%).
	found := false
	for _, msg := range prep.Messages {
		if msg.Custom != nil {
			if role, _ := msg.Custom["role"].(string); role == agent.RoleBranchSummary {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected branch_summary message to be included via priority fit, but it was not")
	}
}

// ─── TestGenerateBranchSummary ────────────────────────────────────────────────

func makeTestEntries() []codingagent.SessionEntry {
	return []codingagent.SessionEntry{makeMessageEntry("1", nil, 10)}
}

// TestGenerateBranchSummary_Abort: fake completer returns aborted context →
// BranchSummaryResult{Aborted: true}.
func TestGenerateBranchSummary_Abort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so ctx.Err() != nil

	completer := &branchFakeCompleter{aborted: true}
	result := GenerateBranchSummary(ctx, makeTestEntries(), GenerateBranchSummaryOptions{
		Model:     &ai.Model{},
		Completer: completer,
	})

	if !result.Aborted {
		t.Errorf("expected Aborted=true, got %+v", result)
	}
}

// TestGenerateBranchSummary_Error: fake completer returns error →
// BranchSummaryResult{Error: "..."}.
func TestGenerateBranchSummary_Error(t *testing.T) {
	completer := &branchFakeCompleter{err: errors.New("LLM down")}
	result := GenerateBranchSummary(context.Background(), makeTestEntries(), GenerateBranchSummaryOptions{
		Model:     &ai.Model{},
		Completer: completer,
	})

	if result.Error == "" {
		t.Error("expected non-empty Error field")
	}
	if result.Aborted {
		t.Error("expected Aborted=false")
	}
}

// TestGenerateBranchSummary_Success: fake completer returns "my summary" →
// result has BRANCH_SUMMARY_PREAMBLE prefix.
func TestGenerateBranchSummary_Success(t *testing.T) {
	completer := &branchFakeCompleter{result: "my summary"}
	result := GenerateBranchSummary(context.Background(), makeTestEntries(), GenerateBranchSummaryOptions{
		Model:     &ai.Model{},
		Completer: completer,
	})

	if result.Error != "" || result.Aborted {
		t.Fatalf("unexpected error/abort: %+v", result)
	}
	if !strings.HasPrefix(result.Summary, BRANCH_SUMMARY_PREAMBLE) {
		t.Errorf("summary does not start with preamble:\ngot:  %q\nwant prefix: %q", result.Summary, BRANCH_SUMMARY_PREAMBLE)
	}
	if !strings.Contains(result.Summary, "my summary") {
		t.Errorf("summary missing LLM content, got: %q", result.Summary)
	}
}

// TestBranchSummaryMaxTokens pins upstream generateBranchSummary's response
// budget: min(4096, model.maxTokens), where a zero model limit means 4096. The
// input token budget (context window minus reserve) must not reach the request.
func TestBranchSummaryMaxTokens(t *testing.T) {
	cases := []struct {
		name            string
		maxOutputTokens int
		want            int
	}{
		{name: "large output limit caps at 4096", maxOutputTokens: 64000, want: 4096},
		{name: "small output limit wins", maxOutputTokens: 1024, want: 1024},
		{name: "unknown output limit uses 4096", maxOutputTokens: 0, want: 4096},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			completer := &fakeCompleter{response: "summary"}
			model := &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: tc.maxOutputTokens}}
			result := GenerateBranchSummary(context.Background(), makeTestEntries(), GenerateBranchSummaryOptions{
				Model:         model,
				Completer:     completer,
				ReserveTokens: 16384,
			})
			if result.Error != "" || result.Aborted {
				t.Fatalf("unexpected result: %+v", result)
			}
			if !slices.Equal(completer.maxTokens, []int{tc.want}) {
				t.Fatalf("maxTokens = %v, want [%d]", completer.maxTokens, tc.want)
			}
		})
	}
}
