package codingagent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// ─── Helpers ──────────────────────────────────────────────────────────────────

func tempSessionMgr(t *testing.T) *SessionManager {
	t.Helper()
	dir := t.TempDir()
	return NewSessionManagerWithDir(dir, dir)
}

func mkUserMsg(text string) agent.AgentMessage {
	return agent.AgentMessage{
		User: &agent.UserMessage{
			Role:      "user",
			Content:   []ai.UserContentBlock{ai.TextContent{Text: text}},
			Timestamp: time.Now().UnixMilli(),
		},
	}
}

func mkAssistantMsg(text string) agent.AgentMessage {
	return agent.AgentMessage{
		Assistant: &agent.AssistantMessage{
			Role:      "assistant",
			Content:   []ai.AssistantContentBlock{ai.TextContent{Text: text}},
			Timestamp: time.Now().UnixMilli(),
		},
	}
}

// flushSession persists a freshly-created session by appending an
// assistant message, which is upstream's disk-flush trigger
// (SessionManager._persist hasAssistant gate). Tests asserting on-disk
// state must flush first, mirroring real usage where a session is only
// written once the model replies. Without it a fresh session has no file.
func flushSession(t *testing.T, sess *Session) {
	t.Helper()
	if _, err := sess.AppendMessage(mkAssistantMsg("ok")); err != nil {
		t.Fatalf("flush session: %v", err)
	}
}

func readJSONLLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("parse line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

// ─── Header shape ──────────────────────────────────────────────────────────────

func TestSessionV3HeaderShape(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-test-123", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	flushSession(t, sess)
	lines := readJSONLLines(t, sess.Path())
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (header + flush message); got %d", len(lines))
	}
	h := lines[0]
	// Mandatory fields per chunk-d.md acceptance line:
	// {"type":"session","version":3,"id":...,"timestamp":...,"cwd":...}
	for _, k := range []string{"type", "version", "id", "timestamp", "cwd"} {
		if _, ok := h[k]; !ok {
			t.Errorf("header missing %q: %#v", k, h)
		}
	}
	if h["type"] != "session" {
		t.Errorf("type=%v want session", h["type"])
	}
	if int(h["version"].(float64)) != 3 {
		t.Errorf("version=%v want 3", h["version"])
	}
	if h["id"] != "sess-test-123" {
		t.Errorf("id=%v", h["id"])
	}
	// parentSession MUST be omitted on plain Create (per `omitempty`).
	if _, ok := h["parentSession"]; ok {
		t.Errorf("parentSession should be absent on plain create; got %v", h["parentSession"])
	}
}

// ─── Round-trip ────────────────────────────────────────────────────────────────

func TestSessionRoundTrip(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-rt", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if _, err := sess.AppendMessage(mkAssistantMsg("msg" + string(rune('0'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	// Snapshot on-disk bytes.
	original, err := os.ReadFile(sess.Path())
	if err != nil {
		t.Fatal(err)
	}

	// Reload + verify entry count + leaf state.
	sm2 := NewSessionManagerWithDir(sm.cwd, sm.sessionDir)
	loaded, err := sm2.Load(sess.Path())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := len(loaded.Entries()); got != 5 {
		t.Errorf("entries: %d want 5", got)
	}
	if loaded.LeafID() == nil {
		t.Errorf("leafID nil after load")
	}

	// Lines should be byte-equal to what was written (no reformatting on load).
	again, _ := os.ReadFile(sess.Path())
	if !equalBytes(original, again) {
		t.Errorf("byte-equality lost on read-back")
	}
}

// ─── Message entry shape ──────────────────────────────────────────────────────

func TestSessionMessageEntryShape(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-shape", "")
	id1, _ := sess.AppendMessage(mkUserMsg("hello"))
	id2, _ := sess.AppendMessage(mkAssistantMsg("hi back"))

	lines := readJSONLLines(t, sess.Path())
	if len(lines) != 3 {
		t.Fatalf("lines: %d want 3 (header + 2 messages)", len(lines))
	}

	m1 := lines[1]
	// Required keys per the spec: type, id, parentId, timestamp, message.
	for _, k := range []string{"type", "id", "parentId", "timestamp", "message"} {
		if _, ok := m1[k]; !ok {
			t.Errorf("entry missing %q: %#v", k, m1)
		}
	}
	if m1["type"] != "message" {
		t.Errorf("type=%v want message", m1["type"])
	}
	if m1["id"] != id1 {
		t.Errorf("id=%v want %v", m1["id"], id1)
	}
	// First message's parentId MUST be null (root).
	if v, ok := m1["parentId"]; !ok || v != nil {
		t.Errorf("first-entry parentId must be null; got ok=%v val=%v", ok, v)
	}

	m2 := lines[2]
	if m2["parentId"] != id1 {
		t.Errorf("second entry parentId=%v want %v (must point at first entry)", m2["parentId"], id1)
	}
	if m2["id"] != id2 {
		t.Errorf("second id=%v want %v", m2["id"], id2)
	}
}

// ─── Fork ──────────────────────────────────────────────────────────────────────

func TestForkCreatesSibling(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-fork", "")
	id1, _ := sess.AppendMessage(mkUserMsg("first"))
	_, _ = sess.AppendMessage(mkAssistantMsg("a1"))
	id3, _ := sess.AppendMessage(mkUserMsg("second"))

	// Fork from id1 → next append's parentId must be id1.
	if err := sess.Fork(id1); err != nil {
		t.Fatalf("fork: %v", err)
	}
	if leaf := sess.LeafID(); leaf == nil || *leaf != id1 {
		t.Errorf("leaf after fork: %v want %s", leaf, id1)
	}

	siblingID, _ := sess.AppendMessage(mkUserMsg("alt-second"))
	lines := readJSONLLines(t, sess.Path())

	// The sibling must be on disk.
	var sibling map[string]any
	for _, l := range lines {
		if l["id"] == siblingID {
			sibling = l
		}
	}
	if sibling == nil {
		t.Fatal("sibling not persisted")
	}
	if sibling["parentId"] != id1 {
		t.Errorf("sibling parentId=%v want %v", sibling["parentId"], id1)
	}

	// id3 (the abandoned tail) is still on disk \u2014 we do NOT delete on fork.
	found := false
	for _, l := range lines {
		if l["id"] == id3 {
			found = true
		}
	}
	if !found {
		t.Errorf("abandoned entry %s should still be on disk after fork", id3)
	}
}

// ─── Clone ─────────────────────────────────────────────────────────────────────

func TestCloneWritesNewFileWithLinearPath(t *testing.T) {
	sm := tempSessionMgr(t)
	src, _ := sm.Create("sess-source", "")
	id1, _ := src.AppendMessage(mkUserMsg("a"))
	id2, _ := src.AppendMessage(mkAssistantMsg("b"))
	id3, _ := src.AppendMessage(mkUserMsg("c"))
	// Branch off id1 to create an orphan tail.
	_ = src.Fork(id1)
	_, _ = src.AppendMessage(mkUserMsg("orphan"))

	// Clone the path-to-id3 \u2014 should NOT include the orphan.
	clone, err := sm.Clone(src, id3)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if clone.Path() == src.Path() {
		t.Errorf("clone wrote into the same file as source")
	}
	if clone.ParentSession() != src.Path() {
		t.Errorf("parentSession=%q want %q", clone.ParentSession(), src.Path())
	}

	lines := readJSONLLines(t, clone.Path())
	// Expect: header + (id1, id2, id3) = 4 lines. NO orphan.
	if len(lines) != 4 {
		t.Errorf("clone has %d lines want 4: %#v", len(lines), lines)
	}
	header := lines[0]
	if header["parentSession"] != src.Path() {
		t.Errorf("clone header parentSession=%v", header["parentSession"])
	}
	wantIDs := []string{id1, id2, id3}
	for i, want := range wantIDs {
		if got := lines[i+1]["id"]; got != want {
			t.Errorf("clone line %d id=%v want %v", i+1, got, want)
		}
	}
}

// forkToNewSessionFile branches the picked user message into a NEW file
// at its parent (excluding the message itself) and returns the message
// text for editor prefill. Mirrors upstream /fork (position "before").
func TestForkToNewSessionFile_BranchesAtParentIntoNewFile(t *testing.T) {
	sm := tempSessionMgr(t)
	src, _ := sm.Create("sess-fork-src", "")
	_, _ = src.AppendMessage(mkUserMsg("u1"))
	a1, _ := src.AppendMessage(mkAssistantMsg("a1"))
	u2, _ := src.AppendMessage(mkUserMsg("u2 SELECTED"))
	_, _ = src.AppendMessage(mkAssistantMsg("a2"))

	newSess, selectedText, err := sm.ForkToNewSession(src, u2)
	if err != nil {
		t.Fatalf("forkToNewSessionFile: %v", err)
	}
	if newSess.Path() == src.Path() {
		t.Errorf("/fork must create a NEW file, got same path as source")
	}
	if newSess.ParentSession() != src.Path() {
		t.Errorf("parentSession=%q want %q", newSess.ParentSession(), src.Path())
	}
	if selectedText != "u2 SELECTED" {
		t.Errorf("selectedText=%q want %q", selectedText, "u2 SELECTED")
	}
	// Branch is up to a1 (parent of u2): the selected message and its
	// reply are excluded so the user can re-submit an edited version.
	if leaf := newSess.LeafID(); leaf == nil || *leaf != a1 {
		t.Errorf("new leaf=%v want a1=%s", leaf, a1)
	}
	if _, ok := newSess.EntryByID(u2); ok {
		t.Errorf("forked session must exclude the selected message u2")
	}
}

func TestForkToNewSessionFile_RootMessageStartsEmptyChild(t *testing.T) {
	sm := tempSessionMgr(t)
	src, _ := sm.Create("sess-fork-root", "")
	u1, _ := src.AppendMessage(mkUserMsg("only message"))

	newSess, selectedText, err := sm.ForkToNewSession(src, u1)
	if err != nil {
		t.Fatalf("forkToNewSessionFile: %v", err)
	}
	if newSess.Path() == src.Path() {
		t.Errorf("/fork must create a NEW file, got same path as source")
	}
	if newSess.ParentSession() != src.Path() {
		t.Errorf("parentSession=%q want %q", newSess.ParentSession(), src.Path())
	}
	if selectedText != "only message" {
		t.Errorf("selectedText=%q want %q", selectedText, "only message")
	}
	if leaf := newSess.LeafID(); leaf != nil {
		t.Errorf("root fork should start empty, got leaf=%v", *leaf)
	}
}

func TestForkToNewSessionFile_UnknownEntryErrors(t *testing.T) {
	sm := tempSessionMgr(t)
	src, _ := sm.Create("sess-fork-err", "")
	_, _ = src.AppendMessage(mkUserMsg("u1"))
	if _, _, err := sm.ForkToNewSession(src, "nonexistent"); err == nil {
		t.Fatal("expected error for unknown entry id")
	}
}

// ─── BuildContext ─────────────────────────────────────────────────────────────

func TestBuildContextSimple(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-ctx", "")
	_, _ = sess.AppendMessage(mkUserMsg("u1"))
	_, _ = sess.AppendMessage(mkAssistantMsg("a1"))
	_, _ = sess.AppendMessage(mkUserMsg("u2"))

	ctx := sess.BuildContext(nil)
	if len(ctx) != 3 {
		t.Fatalf("context len=%d want 3", len(ctx))
	}
	if ctx[0].User == nil || ctx[1].Assistant == nil || ctx[2].User == nil {
		t.Errorf("role order wrong: %+v", ctx)
	}
}

func TestBuildContextLeafOnFork(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-ctx-fork", "")
	id1, _ := sess.AppendMessage(mkUserMsg("u1"))
	_, _ = sess.AppendMessage(mkUserMsg("abandoned"))
	_ = sess.Fork(id1)
	_, _ = sess.AppendMessage(mkUserMsg("alt"))

	ctx := sess.BuildContext(nil)
	if len(ctx) != 2 {
		t.Errorf("context len=%d want 2 (u1 + alt; abandoned excluded)", len(ctx))
	}
	if extractUserText(ctx[1]) != "alt" {
		t.Errorf("expected 'alt' as leaf; got %q", extractUserText(ctx[1]))
	}
}

func TestBuildContextLeafIDExplicit(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-ctx-explicit", "")
	id1, _ := sess.AppendMessage(mkUserMsg("u1"))
	_, _ = sess.AppendMessage(mkUserMsg("u2"))
	_, _ = sess.AppendMessage(mkUserMsg("u3"))

	// Walk only to id1.
	ctx := sess.BuildContext(&id1)
	if len(ctx) != 1 {
		t.Errorf("explicit leafID context len=%d want 1", len(ctx))
	}
}

func TestBuildContextNoEntriesReturnsEmpty(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-empty", "")
	if got := sess.BuildContext(nil); len(got) != 0 {
		t.Errorf("empty session should yield empty context; got %d", len(got))
	}
}

// ─── ListSessions / FindMostRecent / FindByID ─────────────────────────────────

func TestFindMostRecentSession(t *testing.T) {
	sm := tempSessionMgr(t)
	a, _ := sm.Create("sess-a", "")
	_, _ = a.AppendMessage(mkAssistantMsg("x"))
	time.Sleep(20 * time.Millisecond)
	b, _ := sm.Create("sess-b", "")
	_, _ = b.AppendMessage(mkAssistantMsg("y"))
	time.Sleep(20 * time.Millisecond)
	c, _ := sm.Create("sess-c", "")
	_, _ = c.AppendMessage(mkAssistantMsg("z"))

	// Drop a corrupted file in the dir \u2014 must not crash listing.
	if err := os.WriteFile(filepath.Join(sm.SessionDir(), "garbage.jsonl"), []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := sm.FindMostRecent()
	if got != c.Path() {
		t.Errorf("FindMostRecent=%q want %q (newest)", got, c.Path())
	}
}

func TestFindByID(t *testing.T) {
	sm := tempSessionMgr(t)
	_, _ = sm.Create("sess-aaa", "")
	want, _ := sm.Create("sess-target", "")
	_, _ = sm.Create("sess-zzz", "")
	flushSession(t, want)
	if got := sm.FindByID("sess-target"); got != want.Path() {
		t.Errorf("FindByID=%q want %q", got, want.Path())
	}
	if got := sm.FindByID("missing"); got != "" {
		t.Errorf("FindByID(missing)=%q want empty", got)
	}
}

func TestListSessionsSummariesPopulated(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-pop", "")
	_, _ = sess.AppendMessage(mkUserMsg("first user message"))
	_, _ = sess.AppendMessage(mkAssistantMsg("first assistant reply"))
	_, _ = sess.AppendMessage(mkUserMsg("second user message"))

	infos, err := sm.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("infos: %d", len(infos))
	}
	info := infos[0]
	if info.MessageCount != 3 {
		t.Errorf("MessageCount=%d want 3", info.MessageCount)
	}
	if info.FirstMessage != "first user message" {
		t.Errorf("FirstMessage=%q", info.FirstMessage)
	}
	if !strings.Contains(info.AllMessagesText, "second user message") {
		t.Errorf("AllMessagesText missing later message: %q", info.AllMessagesText)
	}
}

// ─── Tree ──────────────────────────────────────────────────────────────────────

func TestTreeFromEntries(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-tree", "")
	root, _ := sess.AppendMessage(mkUserMsg("root"))
	// Three branches off root.
	_ = sess.Fork(root)
	_, _ = sess.AppendMessage(mkUserMsg("branch-1"))
	_ = sess.Fork(root)
	_, _ = sess.AppendMessage(mkUserMsg("branch-2"))
	_ = sess.Fork(root)
	_, _ = sess.AppendMessage(mkUserMsg("branch-3"))

	tree := sess.Tree()
	if len(tree.Children) != 1 {
		t.Fatalf("expected 1 root child; got %d", len(tree.Children))
	}
	rootNode := tree.Children[0]
	if rootNode.Entry.Base.ID != root {
		t.Errorf("root node id=%v want %v", rootNode.Entry.Base.ID, root)
	}
	if len(rootNode.Children) != 3 {
		t.Errorf("root should have 3 children (3 branches); got %d", len(rootNode.Children))
	}
}

// ─── Helpers used in tests ────────────────────────────────────────────────────

func extractUserText(m agent.AgentMessage) string {
	if m.User == nil {
		return ""
	}
	for _, c := range m.User.Content {
		raw, _ := json.Marshal(c)
		var probe struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &probe)
		return probe.Text
	}
	return ""
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ─── AgentMessage wire round-trip ────────────────────────────────────────────

func TestAgentMessageRoundTripAssistantKeepsModelProviderAndUsage(t *testing.T) {
	in := agent.AgentMessage{
		Assistant: &agent.AssistantMessage{
			Role:      "assistant",
			Content:   []ai.AssistantContentBlock{ai.TextContent{Text: "hi"}},
			Timestamp: 1234,
			Provider:  "github-copilot",
			ModelID:   "claude-sonnet-4.5",
			Usage:     &ai.Usage{Input: 100, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 190},
		},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out agent.AgentMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Assistant == nil {
		t.Fatal("expected assistant after round-trip")
	}
	if out.Assistant.Provider != "github-copilot" || out.Assistant.ModelID != "claude-sonnet-4.5" {
		t.Fatalf("round-trip model/provider = %q/%q", out.Assistant.Provider, out.Assistant.ModelID)
	}
	if out.Assistant.Usage == nil || out.Assistant.Usage.TotalTokens != 190 {
		t.Fatalf("round-trip usage = %+v", out.Assistant.Usage)
	}
}

func TestAgentMessageRoundTripToolResult(t *testing.T) {
	in := agent.AgentMessage{
		ToolResult: &agent.ToolResultMessage{
			Role:       agent.RoleToolResult,
			ToolCallID: "call-1",
			ToolName:   "search",
			Content: []ai.ToolResultMessageContent{
				ai.TextContent{Text: "ok output"},
				ai.ImageContent{MimeType: "image/png", Data: "QUJD"},
			},
			Timestamp: 1234,
		},
	}
	raw, _ := json.Marshal(in)
	var out agent.AgentMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ToolResult == nil {
		t.Fatalf("expected ToolResult after round-trip; got %+v", out)
	}
	if out.ToolResult.ToolCallID != "call-1" {
		t.Errorf("tool_call_id lost")
	}
	// ToolName and Images must survive persistence and resume: Gemini's
	// functionResponse.name needs the name, and tool-result images must replay.
	if out.ToolResult.ToolName != "search" {
		t.Errorf("tool_name lost on round-trip: %q", out.ToolResult.ToolName)
	}
	images := out.ToolResult.Images()
	if len(images) != 1 || images[0].Data != "QUJD" {
		t.Errorf("images lost on round-trip: %+v", images)
	}
}

func TestSessionResumePersistedMessagesReadbackable(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, _ := sm.Create("sess-resume", "")
	_, _ = sess.AppendMessage(mkUserMsg("hello"))
	_, _ = sess.AppendMessage(mkAssistantMsg("hi back"))
	_, _ = sess.AppendMessage(mkUserMsg("how are you"))

	// Resume from disk into a fresh manager.
	sm2 := NewSessionManagerWithDir(sm.cwd, sm.sessionDir)
	loaded, err := sm2.Load(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	ctx := loaded.BuildContext(nil)
	if len(ctx) != 3 {
		t.Fatalf("resumed context len=%d want 3", len(ctx))
	}
	if extractUserText(ctx[0]) != "hello" {
		t.Errorf("ctx[0]=%q", extractUserText(ctx[0]))
	}
	if ctx[1].Assistant == nil {
		t.Errorf("ctx[1] not assistant; got %+v", ctx[1])
	}
	if extractUserText(ctx[2]) != "how are you" {
		t.Errorf("ctx[2]=%q", extractUserText(ctx[2]))
	}
}

func TestGetSessionNameLatestWins(t *testing.T) {
	// GetSessionName mirrors upstream session-manager.ts:923-933.
	// Multiple session_info entries: the latest non-empty trimmed name wins,
	// and an empty trimmed name explicitly clears the previous name.
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-name-walk", "")
	if err != nil {
		t.Fatal(err)
	}

	if got := sess.GetSessionName(); got != "" {
		t.Errorf("fresh session name=%q want empty", got)
	}

	mkInfo := func(name string) SessionInfoEntry {
		id, _ := generateEntryID()
		parent := sess.LeafID()
		return SessionInfoEntry{
			SessionEntryBase: SessionEntryBase{
				Type:      "session_info",
				ID:        id,
				ParentID:  parent,
				Timestamp: "2026-05-02T00:00:00Z",
			},
			Name: name,
		}
	}

	if err := sess.AppendEntry(mkInfo("first")); err != nil {
		t.Fatal(err)
	}
	if got := sess.GetSessionName(); got != "first" {
		t.Errorf("after first set: got=%q want=first", got)
	}

	if err := sess.AppendEntry(mkInfo("  second  ")); err != nil {
		t.Fatal(err)
	}
	if got := sess.GetSessionName(); got != "second" {
		t.Errorf("after second set (with whitespace): got=%q want=second", got)
	}

	// Empty trimmed name clears.
	if err := sess.AppendEntry(mkInfo("   ")); err != nil {
		t.Fatal(err)
	}
	if got := sess.GetSessionName(); got != "" {
		t.Errorf("after explicit clear: got=%q want empty", got)
	}
}

// ─── bash_execution entry persistence ─────────────────────────────

func TestAppendBashExecution_RoundTrip(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-bash", "")
	if err != nil {
		t.Fatal(err)
	}
	flushSession(t, sess)
	zero := 0
	id, err := sess.AppendBashExecution("ls -la", "total 0\n", &zero, false, false, "", false)
	if err != nil {
		t.Fatalf("AppendBashExecution: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty entry id")
	}
	// Reload and assert the entry survived.
	sm2 := NewSessionManagerWithDir(sm.cwd, sm.sessionDir)
	loaded, err := sm2.Load(sess.Path())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	entries := loaded.Entries()
	var bashEntry *SessionEntry
	for i := range entries {
		if entries[i].Base.Type == "bash_execution" {
			bashEntry = &entries[i]
		}
	}
	if bashEntry == nil {
		t.Fatalf("bash_execution entry not found among %d entries", len(entries))
	}
	// Decode and verify shape.
	var bx BashExecutionEntry
	if err := json.Unmarshal(bashEntry.Raw(), &bx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if bx.Role != "bashExecution" {
		t.Errorf("role=%q want `bashExecution`", bx.Role)
	}
	if bx.Command != "ls -la" {
		t.Errorf("command=%q want `ls -la`", bx.Command)
	}
	if bx.ExitCode == nil || *bx.ExitCode != 0 {
		t.Errorf("exitCode=%v want 0", bx.ExitCode)
	}
	if bx.ExcludeFromContext {
		t.Errorf("excludeFromContext=true, want false")
	}
}

// proves the on-disk JSON matches upstream's wire shape:
// outer `type:"message"` wrapping inner `message.role:"bashExecution"`
// with flat command/output/exitCode/... fields: NOT pig's pre-shape
// of top-level `type:"bash_execution"` with flat fields.
func TestAppendBashExecution_UpstreamWireShape(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-bash-wire", "")
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	if _, err := sess.AppendBashExecution("ls", "out\n", &zero, false, false, "", false); err != nil {
		t.Fatalf("AppendBashExecution: %v", err)
	}
	entries := sess.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1", len(entries))
	}
	raw := entries[0].Raw()

	// Outer discriminator must be "message" on disk.
	var probe struct {
		Type    string `json:"type"`
		Message struct {
			Role     string `json:"role"`
			Command  string `json:"command"`
			Output   string `json:"output"`
			ExitCode *int   `json:"exitCode"`
		} `json:"message"`
		// Flat fields must NOT be present at top level (legacy shape leak).
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if probe.Type != "message" {
		t.Errorf("on-disk type=%q want `message` (upstream parity)", probe.Type)
	}
	if probe.Message.Role != "bashExecution" {
		t.Errorf("inner role=%q want `bashExecution`", probe.Message.Role)
	}
	if probe.Message.Command != "ls" {
		t.Errorf("inner command=%q want `ls`", probe.Message.Command)
	}
	if probe.Command != "" {
		t.Errorf("legacy top-level command must NOT appear on disk; got %q", probe.Command)
	}
}

// proves backwards-compat: legacy pig sessions with
// top-level `type:"bash_execution"` still load and decode.
func TestBashExecutionEntry_LegacyShapeStillLoads(t *testing.T) {
	legacy := `{"type":"bash_execution","id":"abc","parentId":null,"timestamp":"2026-05-11T00:00:00Z","role":"bashExecution","command":"echo hi","output":"hi\n","exitCode":0,"cancelled":false,"truncated":false}`
	var bx BashExecutionEntry
	if err := json.Unmarshal([]byte(legacy), &bx); err != nil {
		t.Fatalf("legacy unmarshal: %v", err)
	}
	if bx.Type != "bash_execution" {
		t.Errorf("Base.Type=%q want bash_execution", bx.Type)
	}
	if bx.Command != "echo hi" {
		t.Errorf("Command=%q", bx.Command)
	}
	if bx.Output != "hi\n" {
		t.Errorf("Output=%q", bx.Output)
	}
}

// TestAppendBashExecution_ProjectsBashExecutionMessages mirrors Pi's
// sessionEntryToContextMessages: a bash entry projects its bashExecution
// message, including `!!cmd` entries, and provider conversion alone drops the
// excluded ones (agent convertToLLM, covered in agent/messages_bash_test.go).
func TestAppendBashExecution_ProjectsBashExecutionMessages(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-bash-ex", "")
	if err != nil {
		t.Fatal(err)
	}
	// Mix: one user message, one normal !cmd, one !!cmd (excluded).
	if _, err := sess.AppendMessage(mkUserMsg("hi")); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if _, err := sess.AppendBashExecution("echo public", "public\n", &zero, false, false, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendBashExecution("echo secret", "secret\n", &zero, false, false, "", true); err != nil {
		t.Fatal(err)
	}
	ctx := sess.BuildContext(nil)
	if len(ctx) != 3 {
		t.Fatalf("BuildContext len=%d want 3:\n%v", len(ctx), ctx)
	}
	for i, want := range []struct {
		command  string
		excluded bool
	}{{"echo public", false}, {"echo secret", true}} {
		message := ctx[i+1].Custom
		if message["role"] != agent.RoleBashExecution || message["command"] != want.command {
			t.Fatalf("context[%d] = %#v, want bashExecution %q", i+1, message, want.command)
		}
		if excluded, _ := message["excludeFromContext"].(bool); excluded != want.excluded {
			t.Fatalf("context[%d] excludeFromContext = %v, want %v", i+1, excluded, want.excluded)
		}
	}
}

// TestAppendThinkingLevelChange verifies that AppendThinkingLevelChange writes
// a thinking_level_change entry that round-trips correctly.
func TestAppendThinkingLevelChange(t *testing.T) {
	sm := tempSessionMgr(t)

	sess, err := sm.Create("sess-thinking-level", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := sess.AppendThinkingLevelChange("medium"); err != nil {
		t.Fatalf("AppendThinkingLevelChange: %v", err)
	}
	flushSession(t, sess)

	// Reload from disk and verify entry.
	loaded, err := loadSessionFile(sess.Path())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	var found bool
	for _, e := range loaded.entries {
		if e.Base.Type == "thinking_level_change" {
			found = true
			var ent ThinkingLevelEntry
			if err := json.Unmarshal(e.raw, &ent); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if ent.ThinkingLevel != "medium" {
				t.Errorf("ThinkingLevel: got %q want %q", ent.ThinkingLevel, "medium")
			}
		}
	}
	if !found {
		t.Error("thinking_level_change entry not found in reloaded session")
	}
}

// ─── 3.2e: AppendCompaction, AppendBranchSummary, BuildContext ordering ───────

func TestAppendCompaction(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-compaction-test", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Add two messages first so firstKeptEntryID can reference the second.
	idA, err := sess.AppendMessage(mkUserMsg("hello"))
	if err != nil {
		t.Fatalf("AppendMessage A: %v", err)
	}
	idB, err := sess.AppendMessage(mkAssistantMsg("world"))
	if err != nil {
		t.Fatalf("AppendMessage B: %v", err)
	}

	compID, err := sess.AppendCompaction("compact summary", idB, 1500, nil, false, &ai.Usage{Input: 111, Output: 22, CacheRead: 3})
	if err != nil {
		t.Fatalf("AppendCompaction: %v", err)
	}

	// Leaf must have advanced to the compaction entry.
	if leaf := sess.LeafID(); leaf == nil || *leaf != compID {
		t.Errorf("leaf after compaction: got %v want %q", leaf, compID)
	}

	// Verify the stored entry round-trips correctly.
	e, ok := sess.EntryByID(compID)
	if !ok {
		t.Fatalf("entry %q not found", compID)
	}
	var comp CompactionEntry
	if err := json.Unmarshal(e.Raw(), &comp); err != nil {
		t.Fatalf("unmarshal CompactionEntry: %v", err)
	}
	if comp.Summary != "compact summary" {
		t.Errorf("Summary: got %q want %q", comp.Summary, "compact summary")
	}
	if comp.FirstKeptEntryID != idB {
		t.Errorf("FirstKeptEntryID: got %q want %q", comp.FirstKeptEntryID, idB)
	}
	if comp.TokensBefore != 1500 {
		t.Errorf("TokensBefore: got %d want 1500", comp.TokensBefore)
	}
	// Summarization usage must persist so compaction cost counts toward the
	// session total on reload (mirrors upstream getSessionStats).
	if comp.Usage == nil || comp.Usage.Input != 111 || comp.Usage.Output != 22 || comp.Usage.CacheRead != 3 {
		t.Errorf("Usage: got %+v want input 111 output 22 cacheRead 3", comp.Usage)
	}
	if comp.Type != "compaction" {
		t.Errorf("Type: got %q want %q", comp.Type, "compaction")
	}

	// Parent must be idA's successor (idB).
	_ = idA // used above for ordering; compaction parent should be idB
	if comp.ParentID == nil || *comp.ParentID != idB {
		t.Errorf("ParentID: got %v want %q", comp.ParentID, idB)
	}

	// Reload and verify persistence.
	loaded, err := loadSessionFile(sess.path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	var found bool
	for _, ent := range loaded.entries {
		if ent.Base.ID == compID {
			found = true
			var c2 CompactionEntry
			if err := json.Unmarshal(ent.Raw(), &c2); err != nil {
				t.Fatalf("reload unmarshal: %v", err)
			}
			if c2.FirstKeptEntryID != idB {
				t.Errorf("reloaded FirstKeptEntryID: got %q want %q", c2.FirstKeptEntryID, idB)
			}
		}
	}
	if !found {
		t.Error("compaction entry not found in reloaded session")
	}
}

func TestAppendBranchSummary(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-bs-test", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	idA, err := sess.AppendMessage(mkUserMsg("first"))
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	// Pass idA explicitly as the parent (upstream branchWithSummary passes
	// newLeafId explicitly; pig no longer auto-wires nil → current leaf).
	bsID, err := sess.AppendBranchSummary(&idA, "branch summary text", nil, false, nil)
	if err != nil {
		t.Fatalf("AppendBranchSummary: %v", err)
	}

	// Leaf must be the new branch_summary entry.
	if leaf := sess.LeafID(); leaf == nil || *leaf != bsID {
		t.Errorf("leaf after branch_summary: got %v want %q", leaf, bsID)
	}

	e, ok := sess.EntryByID(bsID)
	if !ok {
		t.Fatalf("entry %q not found", bsID)
	}
	var bs BranchSummaryEntry
	if err := json.Unmarshal(e.Raw(), &bs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if bs.Summary != "branch summary text" {
		t.Errorf("Summary: got %q want %q", bs.Summary, "branch summary text")
	}
	if bs.Type != "branch_summary" {
		t.Errorf("Type: got %q want %q", bs.Type, "branch_summary")
	}

	// Explicit parentID = idA must be preserved.
	if bs.ParentID == nil || *bs.ParentID != idA {
		t.Errorf("ParentID: got %v want %q", bs.ParentID, idA)
	}
	if bs.FromID != idA {
		t.Errorf("FromID: got %q want %q", bs.FromID, idA)
	}
}

// TestAppendBranchSummaryRecordsSourceAndDestination ports upstream
// session-manager/tree-traversal.test.ts "branchWithSummary: inserts branch
// summary with the source and destination and advances leaf".
func TestAppendBranchSummaryRecordsSourceAndDestination(t *testing.T) {
	sess := NewSession("sess-bs-source", t.TempDir())
	id1, err := sess.AppendMessage(mkUserMsg("1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(mkAssistantMsg("2")); err != nil {
		t.Fatal(err)
	}
	id3, err := sess.AppendMessage(mkUserMsg("3"))
	if err != nil {
		t.Fatal(err)
	}
	usage := &ai.Usage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100, Cost: ai.UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1}}

	summaryID, err := sess.AppendBranchSummary(&id1, "Summary of abandoned work", nil, false, usage)
	if err != nil {
		t.Fatalf("AppendBranchSummary: %v", err)
	}
	if leaf := sess.LeafID(); leaf == nil || *leaf != summaryID {
		t.Fatalf("leaf = %v, want summary %q", leaf, summaryID)
	}
	entry, ok := sess.EntryByID(summaryID)
	if !ok {
		t.Fatal("summary entry missing")
	}
	var summary BranchSummaryEntry
	if err := json.Unmarshal(entry.Raw(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.ParentID == nil || *summary.ParentID != id1 {
		t.Errorf("parentId = %v, want %q", summary.ParentID, id1)
	}
	if summary.FromID != id3 {
		t.Errorf("fromId = %q, want abandoned leaf %q", summary.FromID, id3)
	}
	if summary.Summary != "Summary of abandoned work" {
		t.Errorf("summary = %q", summary.Summary)
	}
	if summary.Usage == nil || *summary.Usage != *usage {
		t.Errorf("usage = %+v, want %+v", summary.Usage, usage)
	}
}

// TestAppendBranchSummaryRejectsUnknownEntry ports upstream
// tree-traversal.test.ts "branchWithSummary: throws for non-existent entry".
func TestAppendBranchSummaryRejectsUnknownEntry(t *testing.T) {
	sess := NewSession("sess-bs-missing", t.TempDir())
	if _, err := sess.AppendMessage(mkUserMsg("hello")); err != nil {
		t.Fatal(err)
	}
	before := len(sess.Entries())
	missing := "nonexistent"
	_, err := sess.AppendBranchSummary(&missing, "summary", nil, false, nil)
	if err == nil || err.Error() != "Entry nonexistent not found" {
		t.Fatalf("error = %v, want Entry nonexistent not found", err)
	}
	if got := len(sess.Entries()); got != before {
		t.Fatalf("entries = %d, want %d", got, before)
	}
}

func TestAppendBranchSummary_RootLevel(t *testing.T) {
	// Verify nil parentID creates a ROOT-LEVEL entry (no parent).
	// This is the summarize-to-root case in navigateTree (3.2l fix):
	// when the user navigates to the first user message, the summary
	// must be a child of nil, not of the current leaf.
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-bs-root", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Append a message so the session has a non-nil leaf.
	firstID, err := sess.AppendMessage(mkUserMsg("first"))
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if leaf := sess.LeafID(); leaf == nil || *leaf != firstID {
		t.Fatal("precondition: leaf must be firstID")
	}

	// Append branch_summary with nil parent: must NOT inherit firstID as parent.
	bsID, err := sess.AppendBranchSummary(nil, "root summary", nil, false, nil)
	if err != nil {
		t.Fatalf("AppendBranchSummary(nil): %v", err)
	}

	e, ok := sess.EntryByID(bsID)
	if !ok {
		t.Fatalf("entry %q not found", bsID)
	}
	var bs BranchSummaryEntry
	_ = json.Unmarshal(e.Raw(), &bs)
	if bs.ParentID != nil {
		t.Errorf("RootLevel: ParentID should be nil, got %q", *bs.ParentID)
	}
	// Upstream branchWithSummary records the abandoned leaf, not the parent.
	if bs.FromID != firstID {
		t.Errorf("RootLevel: FromID = %q, want abandoned leaf %q", bs.FromID, firstID)
	}

	// Branch from the new leaf must contain ONLY the branch_summary entry.
	branch := sess.Branch(*sess.LeafID())
	if len(branch) != 1 || branch[0].Base.Type != "branch_summary" {
		types := make([]string, len(branch))
		for i, e := range branch {
			types[i] = e.Base.Type
		}
		t.Errorf("RootLevel: branch len=%d types=%v; want [branch_summary]", len(branch), types)
	}
}

// TestBuildContextCompactionOrder verifies the upstream ordering rule:
// summary first, then kept entries before compaction (from firstKeptEntryID),
// then entries after compaction. The compaction node itself is NOT emitted.
//
// Chain: A(user) → B(user) → C(compaction, firstKeptEntryID=B) → D(user) → E(user)
// Expected messages: [compaction_summary, B, D, E]
func TestBuildContextCompactionOrder(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-ctx-order", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	idA, err := sess.AppendMessage(mkUserMsg("A"))
	if err != nil {
		t.Fatalf("A: %v", err)
	}
	idB, err := sess.AppendMessage(mkUserMsg("B"))
	if err != nil {
		t.Fatalf("B: %v", err)
	}
	_, err = sess.AppendCompaction("the summary", idB, 999, nil, false, nil)
	if err != nil {
		t.Fatalf("compaction: %v", err)
	}
	idD, err := sess.AppendMessage(mkUserMsg("D"))
	if err != nil {
		t.Fatalf("D: %v", err)
	}
	idE, err := sess.AppendMessage(mkUserMsg("E"))
	if err != nil {
		t.Fatalf("E: %v", err)
	}
	_ = idA // A is before firstKeptEntryID=B and should NOT appear
	_ = idD
	_ = idE

	msgs := sess.BuildContext(nil)

	if len(msgs) != 4 {
		t.Fatalf("BuildContext: got %d messages want 4: %+v", len(msgs), msgs)
	}

	// Message 0: compaction summary.
	if msgs[0].Custom == nil || msgs[0].Custom["role"] != agent.RoleCompactionSummary {
		t.Fatalf("msg[0]: expected compactionSummary, got %+v", msgs[0])
	}
	if msgs[0].Custom["summary"] != "the summary" {
		t.Errorf("msg[0]: summary = %v", msgs[0].Custom["summary"])
	}

	// Message 1: B (firstKeptEntryID).
	if msgs[1].User == nil {
		t.Fatal("msg[1]: expected user message for B")
	}
	txt1, ok := msgs[1].User.Content[0].(ai.TextContent)
	if !ok {
		t.Fatalf("msg[1]: expected TextContent, got %T", msgs[1].User.Content[0])
	}
	if txt1.Text != "B" {
		t.Errorf("msg[1]: got %q want %q", txt1.Text, "B")
	}

	// Message 2: D (after compaction).
	txt2, ok := msgs[2].User.Content[0].(ai.TextContent)
	if !ok {
		t.Fatalf("msg[2]: expected TextContent, got %T", msgs[2].User.Content[0])
	}
	if txt2.Text != "D" {
		t.Errorf("msg[2]: got %q want %q", txt2.Text, "D")
	}

	// Message 3: E.
	txt3, ok := msgs[3].User.Content[0].(ai.TextContent)
	if !ok {
		t.Fatalf("msg[3]: expected TextContent, got %T", msgs[3].User.Content[0])
	}
	if txt3.Text != "E" {
		t.Errorf("msg[3]: got %q want %q", txt3.Text, "E")
	}
}

// TestBuildContextCompactionPrefix verifies the exact upstream LLM-context
// wrapping for compaction summaries. Upstream uses XML-wrapped prose:
//
//	"The conversation history before this point was compacted..."
//	"\n\n<summary>\n" + text + "\n</summary>"
//
// Reference: messages.ts COMPACTION_SUMMARY_PREFIX/SUFFIX.
// Previously pig used "[Compacted summary]\n": divergence fixed in F/B pass
// after 3.2l.
func TestBuildContextCompactionPrefix(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-compaction-prefix", "")
	if err != nil {
		t.Fatal(err)
	}

	idA, _ := sess.AppendMessage(mkUserMsg("Q"))
	_, _ = sess.AppendCompaction("my compact summary", idA, 100, nil, false, nil)

	msgs := sess.BuildContext(nil)
	if len(msgs) == 0 {
		t.Fatal("BuildContext returned empty")
	}
	summary, ok := msgs[0].Custom["summary"].(string)
	if !ok {
		t.Fatalf("compaction summary = %T, want string", msgs[0].Custom["summary"])
	}
	txt := agent.CompactionSummaryContextText(summary)
	const wantPrefix = "The conversation history before this point was compacted into the following summary:"
	if !strings.Contains(txt, wantPrefix) {
		t.Errorf("compaction prefix missing\ngot: %q\nwant contains: %q", txt, wantPrefix)
	}
	if !strings.Contains(txt, "<summary>") {
		t.Errorf("compaction <summary> tag missing: %q", txt)
	}
	if !strings.Contains(txt, "</summary>") {
		t.Errorf("compaction </summary> tag missing: %q", txt)
	}
	if !strings.Contains(txt, "my compact summary") {
		t.Errorf("compaction summary body missing: %q", txt)
	}
}

// TestBuildContextBranchSummaryPrefix verifies the exact upstream LLM-context
// wrapping for branch summary entries. Upstream uses XML-wrapped prose:
//
//	"The following is a summary of a branch that this conversation came back from:"
//	"\n\n<summary>\n" + text + "</summary>"
//
// Reference: messages.ts BRANCH_SUMMARY_PREFIX/SUFFIX.
// Previously pig used "[Branch summary]\n": divergence fixed in F/B pass
// after 3.2l.
func TestBuildContextBranchSummaryPrefix(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-branch-summary-prefix", "")
	if err != nil {
		t.Fatal(err)
	}

	// Build: user → asst → branch_summary → new_user
	idU, _ := sess.AppendMessage(mkUserMsg("hello"))
	bsID, err := sess.AppendBranchSummary(&idU, "my branch summary", nil, false, nil)
	if err != nil {
		t.Fatalf("AppendBranchSummary: %v", err)
	}
	_ = bsID

	msgs := sess.BuildContext(nil)
	// branch_summary entry is the leaf parent, new leaf = bsID.
	// Branch: [user "hello" → branch_summary]
	// BuildContext emits both as messages.
	var branchSummary agent.AgentMessage
	for _, message := range msgs {
		if message.Custom != nil && message.Custom["role"] == agent.RoleBranchSummary {
			branchSummary = message
			break
		}
	}
	if branchSummary.Custom == nil {
		t.Fatalf("branch summary message not found in BuildContext output; msgs=%+v", msgs)
	}
	branchSummaryText, ok := branchSummary.Custom["summary"].(string)
	if !ok {
		t.Fatalf("branch summary = %T, want string", branchSummary.Custom["summary"])
	}
	branchText := agent.BranchSummaryContextText(branchSummaryText)

	const wantPrefix = "The following is a summary of a branch that this conversation came back from:"
	if !strings.Contains(branchText, wantPrefix) {
		t.Errorf("branch summary prefix missing\ngot: %q\nwant contains: %q", branchText, wantPrefix)
	}
	if !strings.Contains(branchText, "<summary>") {
		t.Errorf("branch <summary> tag missing: %q", branchText)
	}
	if !strings.Contains(branchText, "</summary>") {
		t.Errorf("branch </summary> tag missing: %q", branchText)
	}
	if !strings.Contains(branchText, "my branch summary") {
		t.Errorf("branch summary body missing: %q", branchText)
	}
}
