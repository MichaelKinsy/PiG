package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports of packages/coding-agent/test/session-context-edit.test.ts cases that
// exercise the session manager alone. The estimate and compaction-preparation
// cases live in internal/codingagent/compaction.

func contextEditAssistant(text string) agent.AgentMessage {
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role:       agent.RoleAssistant,
		Content:    []ai.AssistantContentBlock{ai.TextContent{Text: text}},
		API:        "faux",
		Provider:   "faux",
		ModelID:    "faux",
		Usage:      &ai.Usage{Input: 10, Output: 1, TotalTokens: 11},
		StopReason: ai.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}}
}

func contextEditUser(text string) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{
		Role:      agent.RoleUser,
		Content:   []ai.UserContentBlock{ai.TextContent{Text: text}},
		Timestamp: time.Now().UnixMilli(),
	}}
}

func replacementText(t *testing.T, text string) *ContextEditReplacement {
	t.Helper()
	raw, err := marshalSessionLine(text)
	if err != nil {
		t.Fatal(err)
	}
	return &ContextEditReplacement{Content: raw}
}

func replacementBlocks(t *testing.T, text string) *ContextEditReplacement {
	t.Helper()
	raw, err := json.Marshal([]map[string]string{{"type": "text", "text": text}})
	if err != nil {
		t.Fatal(err)
	}
	return &ContextEditReplacement{Content: raw}
}

func mustAppendContextMessage(t *testing.T, sess *Session, message agent.AgentMessage) string {
	t.Helper()
	id, err := sess.AppendMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustEdit(t *testing.T, sess *Session, targetID string, replacement *ContextEditReplacement) string {
	t.Helper()
	id, err := sess.AppendContextEdit(targetID, replacement)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustCompact(t *testing.T, sess *Session, summary, firstKept string, tokensBefore int) string {
	t.Helper()
	id, err := sess.AppendCompaction(summary, firstKept, tokensBefore, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// projectedText mirrors the upstream test's text()/summary readers.
func projectedText(message agent.AgentMessage) string {
	var out strings.Builder
	switch {
	case message.User != nil:
		for _, block := range message.User.Content {
			if text, ok := block.(ai.TextContent); ok {
				out.WriteString(text.Text)
			}
		}
	case message.Assistant != nil:
		for _, block := range message.Assistant.Content {
			if text, ok := block.(ai.TextContent); ok {
				out.WriteString(text.Text)
			}
		}
	case message.ToolResult != nil:
		for _, block := range message.ToolResult.Content {
			if text, ok := block.(ai.TextContent); ok {
				out.WriteString(text.Text)
			}
		}
	case message.Custom != nil:
		if summary, ok := message.Custom["summary"].(string); ok {
			return summary
		}
		if content, ok := message.Custom["content"].(string); ok {
			return content
		}
	}
	return out.String()
}

func projectedRoles(messages []agent.AgentMessage) []string {
	roles := make([]string, len(messages))
	for i, message := range messages {
		roles[i] = message.Role()
	}
	return roles
}

func projectedTexts(messages []agent.AgentMessage) []string {
	texts := make([]string, len(messages))
	for i, message := range messages {
		texts[i] = projectedText(message)
	}
	return texts
}

func entryReplacementJSON(t *testing.T, sess *Session, id string) string {
	t.Helper()
	entry, ok := sess.EntryByID(id)
	if !ok {
		t.Fatalf("entry %s missing", id)
	}
	var edit struct {
		Replacement json.RawMessage `json:"replacement"`
	}
	if err := json.Unmarshal(entry.Raw(), &edit); err != nil {
		t.Fatal(err)
	}
	return string(edit.Replacement)
}

func TestSessionContextEditOmitsATargetOnlyFromModelProjection(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	mustAppendContextMessage(t, sess, contextEditUser("request"))
	assistantID := mustAppendContextMessage(t, sess, contextEditAssistant("partial"))
	result := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role:       agent.RoleToolResult,
		ToolCallID: "call-1",
		ToolName:   "read",
		Content:    []ai.ToolResultMessageContent{ai.TextContent{Text: "raw output"}},
		Details:    map[string]any{"path": "large.txt"},
		IsError:    true,
		Timestamp:  time.Now().UnixMilli(),
	}}
	resultID := mustAppendContextMessage(t, sess, result)
	resultRaw, _ := sess.EntryByID(resultID)
	rawBefore := string(resultRaw.Raw())
	mustEdit(t, sess, assistantID, nil)
	mustEdit(t, sess, resultID, nil)

	messages := 0
	for _, entry := range sess.Branch(*sess.LeafID()) {
		if entry.Base.Type == "message" {
			messages++
		}
	}
	if messages != 3 {
		t.Fatalf("branch message entries = %d, want 3", messages)
	}
	if got := projectedRoles(sess.BuildSessionProjection().Messages); !slices.Equal(got, []string{"user"}) {
		t.Fatalf("projected roles = %v, want [user]", got)
	}
	after, _ := sess.EntryByID(resultID)
	if string(after.Raw()) != rawBefore {
		t.Fatalf("target entry changed:\n%s\n%s", rawBefore, after.Raw())
	}
}

func TestSessionContextEditReplacesOnlyContentAndLetsTheLatestEditWin(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	targetID := mustAppendContextMessage(t, sess, contextEditAssistant("original"))
	mustEdit(t, sess, targetID, replacementBlocks(t, "first"))
	mustEdit(t, sess, targetID, nil)
	mustEdit(t, sess, targetID, replacementBlocks(t, "restored"))

	projected := sess.BuildSessionProjection().Messages
	if len(projected) != 1 || projected[0].Assistant == nil {
		t.Fatalf("projection = %#v, want one assistant", projected)
	}
	if got := projectedText(projected[0]); got != "restored" {
		t.Fatalf("projected text = %q, want restored", got)
	}
	if projected[0].Assistant.Usage == nil || projected[0].Assistant.Usage.TotalTokens != 11 {
		t.Fatalf("replacement changed usage: %#v", projected[0].Assistant.Usage)
	}
	raw, _ := sess.EntryByID(targetID)
	original, _ := raw.AsMessage()
	if got := projectedText(original.Message); got != "original" {
		t.Fatalf("raw entry text = %q, want original", got)
	}
}

func TestSessionContextEditNormalizesStringReplacementsForArrayOnlyAssistantAndToolResultRoles(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	assistantID := mustAppendContextMessage(t, sess, contextEditAssistant("original"))
	resultID := mustAppendContextMessage(t, sess, agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role:       agent.RoleToolResult,
		ToolCallID: "call-1",
		ToolName:   "read",
		Content:    []ai.ToolResultMessageContent{ai.TextContent{Text: "original result"}},
		Timestamp:  time.Now().UnixMilli(),
	}})
	assistantEditID := mustEdit(t, sess, assistantID, replacementText(t, "assistant replacement"))
	resultEditID := mustEdit(t, sess, resultID, replacementText(t, "result replacement"))

	if got := entryReplacementJSON(t, sess, assistantEditID); got != `{"content":[{"type":"text","text":"assistant replacement"}]}` {
		t.Fatalf("assistant replacement = %s", got)
	}
	if got := entryReplacementJSON(t, sess, resultEditID); got != `{"content":[{"type":"text","text":"result replacement"}]}` {
		t.Fatalf("tool-result replacement = %s", got)
	}
	projected := sess.BuildSessionProjection().Messages
	if len(projected) != 2 || projected[0].Assistant == nil || projected[1].ToolResult == nil {
		t.Fatalf("projection roles = %v", projectedRoles(projected))
	}
	if got := projectedTexts(projected); !slices.Equal(got, []string{"assistant replacement", "result replacement"}) {
		t.Fatalf("projected texts = %v", got)
	}
}

func TestSessionContextEditNormalizesImportedStringReplacementsWhileProjectingArrayOnlyRoles(t *testing.T) {
	// The upstream case mutates an in-memory edit; Pig models the same input
	// as a Pi-written session file whose edit carries a string replacement.
	dir := t.TempDir()
	path := filepath.Join(dir, "imported.jsonl")
	lines := strings.Join([]string{
		`{"type":"session","version":3,"id":"imported","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/work"}`,
		`{"type":"message","id":"a1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"original"}],"api":"faux","provider":"faux","model":"faux","usage":{"input":10,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":11,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1767225601000}}`,
		`{"type":"context_edit","id":"e1","parentId":"a1","timestamp":"2026-01-01T00:00:02.000Z","targetId":"a1","replacement":{"content":"imported replacement"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := NewSessionManagerWithDir(dir, dir).Load(path)
	if err != nil {
		t.Fatal(err)
	}
	projected := sess.BuildSessionProjection().Messages
	if len(projected) != 1 || projected[0].Assistant == nil {
		t.Fatalf("projection = %v", projectedRoles(projected))
	}
	content := projected[0].Assistant.Content
	if len(content) != 1 {
		t.Fatalf("assistant content = %#v, want one text block", content)
	}
	if text, ok := content[0].(ai.TextContent); !ok || text.Text != "imported replacement" {
		t.Fatalf("assistant content = %#v", content)
	}
}

func TestSessionContextEditKeepsEditsBranchRelative(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	targetID := mustAppendContextMessage(t, sess, contextEditUser("original"))
	mustEdit(t, sess, targetID, replacementText(t, "edited"))
	if got := projectedText(sess.BuildSessionProjection().Messages[0]); got != "edited" {
		t.Fatalf("edited projection = %q", got)
	}
	if err := sess.Fork(targetID); err != nil {
		t.Fatal(err)
	}
	if got := projectedText(sess.BuildSessionProjection().Messages[0]); got != "original" {
		t.Fatalf("branch projection = %q, want original", got)
	}
}

func TestSessionContextEditUsesASelfReferencingCompactionToRetainNoPrecedingEntries(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	mustAppendContextMessage(t, sess, contextEditUser("discarded"))
	compactionID := mustCompact(t, sess, "exact handoff", "", 100)
	mustAppendContextMessage(t, sess, contextEditUser("after"))

	entry, _ := sess.EntryByID(compactionID)
	var compaction CompactionEntry
	if err := json.Unmarshal(entry.Raw(), &compaction); err != nil {
		t.Fatal(err)
	}
	if compaction.Type != "compaction" || compaction.FirstKeptEntryID != compactionID {
		t.Fatalf("compaction = %+v, want self-referencing firstKeptEntryId", compaction)
	}
	projected := sess.BuildSessionProjection().Messages
	if got := projectedRoles(projected); !slices.Equal(got, []string{agent.RoleCompactionSummary, agent.RoleUser}) {
		t.Fatalf("roles = %v", got)
	}
	if got := projectedTexts(projected); !slices.Equal(got, []string{"exact handoff", "after"}) {
		t.Fatalf("texts = %v", got)
	}
}

func TestSessionContextEditAppliesPostCompactionEditsToRetainedPreCompactionEntries(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	mustAppendContextMessage(t, sess, contextEditUser("summarized"))
	retainedID := mustAppendContextMessage(t, sess, contextEditUser("original retained"))
	mustCompact(t, sess, "summary", retainedID, 100)
	mustEdit(t, sess, retainedID, replacementText(t, "edited retained"))

	if got := projectedTexts(sess.BuildSessionProjection().Messages); !slices.Equal(got, []string{"summary", "edited retained"}) {
		t.Fatalf("texts = %v", got)
	}
}

func TestSessionContextEditSupportsRepeatedRetainNoneCompactions(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	mustAppendContextMessage(t, sess, contextEditUser("discarded"))
	mustCompact(t, sess, "first handoff", "", 100)
	mustAppendContextMessage(t, sess, contextEditUser("also discarded"))
	secondID := mustCompact(t, sess, "second handoff", "", 50)

	entry, _ := sess.EntryByID(secondID)
	var compaction CompactionEntry
	if err := json.Unmarshal(entry.Raw(), &compaction); err != nil {
		t.Fatal(err)
	}
	if compaction.FirstKeptEntryID != secondID {
		t.Fatalf("firstKeptEntryId = %q, want %q", compaction.FirstKeptEntryID, secondID)
	}
	if got := projectedTexts(sess.BuildSessionProjection().Messages); !slices.Equal(got, []string{"second handoff"}) {
		t.Fatalf("texts = %v", got)
	}
}

func TestSessionContextEditRejectsInvalidTargetsAndReplacements(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	userID := mustAppendContextMessage(t, sess, contextEditUser("first"))
	if err := sess.AppendModelSwitch("faux", "faux", ""); err != nil {
		t.Fatal(err)
	}
	modelChangeID := *sess.LeafID()
	otherID := mustAppendContextMessage(t, sess, contextEditUser("other branch"))
	if err := sess.Fork(userID); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		target      string
		replacement *ContextEditReplacement
		want        string
	}{
		{"object content", userID, &ContextEditReplacement{Content: json.RawMessage(`{"text":"x"}`)}, "Context edit replacement must be null or contain string/array content"},
		{"missing content", userID, &ContextEditReplacement{}, "Context edit replacement must be null or contain string/array content"},
		{"unknown target", "missing", nil, "Entry missing not found"},
		{"off branch", otherID, nil, "Entry " + otherID + " is not on the active branch"},
	}
	for _, tc := range cases {
		if _, err := sess.AppendContextEdit(tc.target, tc.replacement); err == nil || err.Error() != tc.want {
			t.Fatalf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	if err := sess.Fork(modelChangeID); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendContextEdit(modelChangeID, nil); err == nil || err.Error() != "Entry "+modelChangeID+" does not contribute editable model content" {
		t.Fatalf("model_change target: err = %v", err)
	}
}

func TestSessionContextEditEntryMatchesPiWireBytes(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	targetID := mustAppendContextMessage(t, sess, contextEditUser("a"))
	omitID := mustEdit(t, sess, targetID, nil)
	replaceID := mustEdit(t, sess, targetID, replacementText(t, "<keep & literal>"))

	for _, tc := range []struct {
		id, parent, tail string
	}{
		{omitID, targetID, `,"targetId":"` + targetID + `","replacement":null}`},
		{replaceID, omitID, `,"targetId":"` + targetID + `","replacement":{"content":"<keep & literal>"}}`},
	} {
		entry, _ := sess.EntryByID(tc.id)
		want := `{"type":"context_edit","id":"` + tc.id + `","parentId":"` + tc.parent + `","timestamp":"` + entry.Base.Timestamp + `"` + tc.tail
		if got := string(entry.Raw()); got != want {
			t.Fatalf("entry bytes:\n got %s\nwant %s", got, want)
		}
	}
}

func TestSessionContextEditRoundTripsPiSessionFilesByteForByte(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi.jsonl")
	lines := []string{
		`{"type":"session","version":3,"id":"pi-file","timestamp":"2024-12-03T14:00:00.000Z","cwd":"/work"}`,
		`{"type":"message","id":"c3d4e5f6","parentId":null,"timestamp":"2024-12-03T14:10:00.000Z","message":{"role":"user","content":"a <b> & c","timestamp":1733235000000}}`,
		`{"type":"context_edit","id":"f6g7h8i9","parentId":"c3d4e5f6","timestamp":"2024-12-03T14:10:30.000Z","targetId":"c3d4e5f6","replacement":{"content":[{"type":"text","text":"x < y"}]}}`,
		`{"type":"context_edit","id":"g6h7i8j9","parentId":"f6g7h8i9","timestamp":"2024-12-03T14:11:00.000Z","targetId":"c3d4e5f6","replacement":null}`,
		`{"type":"compaction","id":"h1","parentId":"g6h7i8j9","timestamp":"2024-12-03T14:12:00.000Z","summary":"s","firstKeptEntryId":"h1","tokensBefore":5,"fromHook":false,"systemMessage":{"role":"system","content":"sys","timestamp":1733235120000}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sm := NewSessionManagerWithDir(dir, dir)
	sess, err := sm.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, entry := range sess.Entries() {
		if string(entry.Raw()) != lines[i+1] {
			t.Fatalf("entry %d changed:\n got %s\nwant %s", i, entry.Raw(), lines[i+1])
		}
	}
	clone, err := sm.Clone(sess, "h1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(clone.Path())
	if err != nil {
		t.Fatal(err)
	}
	cloned := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if !slices.Equal(cloned[1:], lines[1:]) {
		t.Fatalf("clone rewrote entries:\n%s", strings.Join(cloned[1:], "\n"))
	}
	projected := clone.BuildSessionProjection().Messages
	if got := projectedRoles(projected); !slices.Equal(got, []string{"system", agent.RoleCompactionSummary}) {
		t.Fatalf("roles after retain-none compaction with system state = %v", got)
	}
}

func TestAppendCompactionRecordsCurrentProjectedSystemState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "system.jsonl")
	lines := strings.Join([]string{
		`{"type":"session","version":3,"id":"sys","timestamp":"2024-12-03T14:00:00.000Z","cwd":"/work"}`,
		`{"type":"message","id":"s1","parentId":null,"timestamp":"2024-12-03T14:00:01.000Z","message":{"role":"system","content":"base <rules>","sections":{"env":"linux"},"toolsAdded":[{"name":"read","description":"Read","parameters":{"type":"object"}}],"timestamp":1}}`,
		`{"type":"message","id":"s2","parentId":"s1","timestamp":"2024-12-03T14:00:02.000Z","message":{"role":"system","content":"more","sections":{"env":null},"timestamp":2}}`,
		`{"type":"message","id":"u1","parentId":"s2","timestamp":"2024-12-03T14:00:03.000Z","message":{"role":"user","content":"hi","timestamp":3}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := NewSessionManagerWithDir(dir, dir).Load(path)
	if err != nil {
		t.Fatal(err)
	}
	id := mustCompact(t, sess, "summary", "u1", 10)
	entry, _ := sess.EntryByID(id)
	var fields struct {
		Timestamp     string          `json:"timestamp"`
		SystemMessage json.RawMessage `json:"systemMessage"`
	}
	if err := json.Unmarshal(entry.Raw(), &fields); err != nil {
		t.Fatal(err)
	}
	stamp, err := time.Parse(time.RFC3339Nano, fields.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"role":"system","content":"base <rules>\n\nmore","toolsAdded":[{"name":"read","description":"Read","parameters":{"type":"object"}}],"timestamp":` + jsonInt(stamp.UnixMilli()) + `}`
	if string(fields.SystemMessage) != want {
		t.Fatalf("systemMessage:\n got %s\nwant %s", fields.SystemMessage, want)
	}
	if !strings.Contains(string(entry.Raw()), `"fromHook":false,"systemMessage":`) {
		t.Fatalf("compaction key order differs from Pi: %s", entry.Raw())
	}
	// Retained system messages are replaced by the compaction's replay.
	if got := projectedRoles(sess.BuildSessionProjection().Messages); !slices.Equal(got, []string{"system", agent.RoleCompactionSummary, agent.RoleUser}) {
		t.Fatalf("roles = %v", got)
	}
}

func jsonInt(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
