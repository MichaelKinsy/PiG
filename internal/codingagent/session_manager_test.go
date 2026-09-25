// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

func TestEncodeCwdForSessionDir_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		cwd  string
		want string
	}{
		{"simple unix", "/tmp/foo", "--tmp-foo--"},
		{"deep path", "/Users/example/work", "--Users-example-work--"},
		{"empty", "", "----"},
		{"root", "/", "----"},
		{"no leading slash", "tmp/foo", "--tmp-foo--"},
		{"colons replaced", "/a:b/c", "--a-b-c--"},
		{"backslash replaced", `\Users\foo`, "--Users-foo--"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := encodeCwdForSessionDir(tc.cwd)
			if got != tc.want {
				t.Errorf("encodeCwdForSessionDir(%q) = %q, want %q", tc.cwd, got, tc.want)
			}
		})
	}
}

func TestDefaultSessionDirUsesAgentDirOverride(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(ENV_AGENT_DIR, agentDir)
	got := defaultSessionDir("/tmp/foo")
	want := filepath.Join(agentDir, "sessions", "--tmp-foo--")
	if got != want {
		t.Fatalf("defaultSessionDir() = %q, want %q", got, want)
	}
}

func TestDefaultSessionDir(t *testing.T) {
	dir := defaultSessionDir("/tmp/foo")
	if !strings.Contains(dir, "sessions") {
		t.Errorf("expected 'sessions' in path, got %s", dir)
	}
	if !strings.HasSuffix(dir, "--tmp-foo--") {
		t.Errorf("expected dir to end with --tmp-foo--, got %s", dir)
	}
}

func TestSessionHeaderMatchesUpstreamShape(t *testing.T) {
	headerType := reflect.TypeFor[SessionHeader]()
	var fields []string
	for field := range headerType.Fields() {
		fields = append(fields, field.Name)
	}
	want := []string{"Type", "Version", "ID", "Timestamp", "CWD", "ParentSession"}
	if !slices.Equal(fields, want) {
		t.Fatalf("SessionHeader fields = %v, want %v", fields, want)
	}
}

func TestSessionManager_Create(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/tmp/test", dir)
	sess, err := sm.Create("test-id-1", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.ID() != "test-id-1" {
		t.Errorf("ID = %q, want test-id-1", sess.ID())
	}
	if sess.Path() == "" {
		t.Fatal("session path is empty")
	}
	// Deferred flush: no file until the first assistant message
	// (upstream _persist hasAssistant gate).
	if _, err := os.Stat(sess.Path()); !os.IsNotExist(err) {
		t.Fatalf("session file should not exist before first assistant message, stat err = %v", err)
	}
	flushSession(t, sess)
	if _, err := os.Stat(sess.Path()); err != nil {
		t.Fatalf("session file not on disk after flush: %v", err)
	}
}

func TestSessionManager_Create_SetsAsCurrent(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/tmp/test", dir)
	sess, err := sm.Create("id-1", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sm.Current() != sess {
		t.Error("Current() should return the just-created session")
	}
}

func TestSessionManager_Create_WithParent(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/tmp/test", dir)
	sess, err := sm.Create("child-1", "/some/parent.jsonl")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.ParentSession() != "/some/parent.jsonl" {
		t.Errorf("ParentSession = %q, want /some/parent.jsonl", sess.ParentSession())
	}
}

// ForkFromFile (upstream `pig --fork`) copies the FULL entry tree from a
// source file into a fresh session file recording parentSession: unlike
// Clone, which keeps only the linear path to a chosen leaf.
func TestSessionManagerForkFromFile_CopiesFullTreeToNewFile(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/tmp/test-forkfrom", dir)
	src, _ := sm.Create("sess-forkfrom", "")
	u1, _ := src.AppendMessage(mkUserMsg("u1"))
	a1, _ := src.AppendMessage(mkAssistantMsg("a1"))
	// Branch off u1 so a1 becomes an orphan tail; append an assistant on
	// the new branch to flush the whole tree (incl. a1) to disk.
	_ = src.Fork(u1)
	u2, _ := src.AppendMessage(mkUserMsg("branch-b"))
	a2, _ := src.AppendMessage(mkAssistantMsg("reply-b"))

	forked, err := sm.ForkFromFile(src.Path())
	if err != nil {
		t.Fatalf("ForkFromFile: %v", err)
	}
	if forked.Path() == src.Path() {
		t.Errorf("--fork must create a NEW file, got same path as source")
	}
	if forked.ID() == src.ID() {
		t.Errorf("forked session must have a fresh id, got %q", forked.ID())
	}
	absSrc, _ := filepath.Abs(src.Path())
	if forked.ParentSession() != absSrc {
		t.Errorf("parentSession=%q want %q", forked.ParentSession(), absSrc)
	}
	// Full tree preserved, including the orphan a1 that Clone would drop.
	for _, id := range []string{u1, a1, u2, a2} {
		if _, ok := forked.EntryByID(id); !ok {
			t.Errorf("forked session missing entry %s (full tree not copied)", id)
		}
	}
	// Source file stays intact and independent.
	if _, err := os.Stat(src.Path()); err != nil {
		t.Errorf("source file gone after fork: %v", err)
	}
}

func TestSessionManagerForkFromFile_MissingSourceErrors(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/tmp/test-forkfrom", dir)
	if _, err := sm.ForkFromFile(filepath.Join(dir, "does-not-exist.jsonl")); err == nil {
		t.Fatal("expected error forking a missing source file")
	}
}

func TestSessionManager_CreateAndLoad(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/tmp/test", dir)
	sess, err := sm.Create("roundtrip-1", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	flushSession(t, sess)
	path := sess.Path()

	sm2 := NewSessionManagerWithDir("/tmp/test", dir)
	loaded, err := sm2.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ID() != "roundtrip-1" {
		t.Errorf("loaded ID = %q, want roundtrip-1", loaded.ID())
	}
	if loaded.CWD() != "/tmp/test" {
		t.Errorf("loaded CWD = %q, want /tmp/test", loaded.CWD())
	}
}

func TestSessionManagerLoadResolvesAbsoluteSessionPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	source := NewSession("absolute-resume", dir)
	source.path = "resume.jsonl"
	flushSession(t, source)

	manager := NewSessionManagerWithDir(dir, ".")
	loaded, err := manager.Load("resume.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(loaded.Path()) {
		t.Fatalf("loaded session path = %q, want absolute", loaded.Path())
	}
	want := filepath.Join(dir, "resume.jsonl")
	if loaded.Path() != want {
		t.Fatalf("loaded session path = %q, want %q", loaded.Path(), want)
	}
}

func TestSessionManager_Load_NonExistent(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/tmp/test", dir)
	_, err := sm.Load(filepath.Join(dir, "nope.jsonl"))
	if err == nil {
		t.Fatal("expected error loading non-existent file")
	}
}

func TestSessionManager_Load_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	badFile := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(badFile, []byte("not json at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSessionManagerWithDir("/tmp/test", dir)
	_, err := sm.Load(badFile)
	if err == nil {
		t.Fatal("expected error loading invalid JSON")
	}
}

func TestSessionManager_Load_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	emptyFile := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(emptyFile, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSessionManagerWithDir("/tmp/test", dir)
	_, err := sm.Load(emptyFile)
	if err == nil {
		t.Fatal("expected error loading empty file")
	}
}

func TestSessionManager_Load_WrongHeaderType(t *testing.T) {
	dir := t.TempDir()
	badHeader := filepath.Join(dir, "wrong.jsonl")
	header, _ := json.Marshal(map[string]any{"type": "not_session", "id": "x"})
	if err := os.WriteFile(badHeader, append(header, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSessionManagerWithDir("/tmp/test", dir)
	_, err := sm.Load(badHeader)
	if err == nil {
		t.Fatal("expected error for wrong header type")
	}
}

func TestSessionManager_ListSessions_Empty(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/tmp/test", dir)
	infos, err := sm.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(infos))
	}
}

func TestSessionManager_ListSessions_Multiple(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/tmp/test", dir)
	for _, id := range []string{"s1", "s2", "s3"} {
		sess, err := sm.Create(id, "")
		if err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
		flushSession(t, sess)
	}
	infos, err := sm.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(infos) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(infos))
	}
}

func TestSessionManager_ListSessions_NonExistentDir(t *testing.T) {
	sm := NewSessionManagerWithDir("/tmp/test", filepath.Join(t.TempDir(), "nonexistent"))
	infos, err := sm.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(infos))
	}
}

func TestSummarizeSessionFiles_ProcessesManySessions(t *testing.T) {
	dir := t.TempDir()
	files := make([]string, 0, 25)
	for i := range 25 {
		path := filepath.Join(dir, fmt.Sprintf("sess-%02d.jsonl", i))
		header, err := json.Marshal(map[string]any{"type": "session", "id": path, "cwd": dir, "timestamp": "2026-01-01T00:00:00Z"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(header, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	infos := summarizeSessionFiles(files)
	if len(infos) != len(files) {
		t.Fatalf("len(infos) = %d, want %d", len(infos), len(files))
	}
}

func TestSessionManager_SaveAndReload(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/tmp/test", dir)
	sess, err := sm.Create("persist-1", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Append an entry via AppendEntry. Use an assistant role so the
	// session flushes to disk (upstream _persist hasAssistant gate).
	entry := MessageEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "message",
			ID:        "entry-001",
			ParentID:  nil,
			Timestamp: "2026-01-01T00:00:00Z",
		},
		Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant}},
	}
	if err := sess.AppendEntry(entry); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}

	// Reload
	sm2 := NewSessionManagerWithDir("/tmp/test", dir)
	loaded, err := sm2.Load(sess.Path())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entries := loaded.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Base.ID != "entry-001" {
		t.Errorf("entry ID = %q, want entry-001", entries[0].Base.ID)
	}
	if entries[0].Base.Type != "message" {
		t.Errorf("entry type = %q, want message", entries[0].Base.Type)
	}
}

func TestSessionManager_SessionDir(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/tmp/test", dir)
	if sm.SessionDir() != dir {
		t.Errorf("SessionDir = %q, want %q", sm.SessionDir(), dir)
	}
}

func TestSessionManager_CWD(t *testing.T) {
	sm := NewSessionManagerWithDir("/my/cwd", t.TempDir())
	if sm.CWD() != "/my/cwd" {
		t.Errorf("CWD = %q, want /my/cwd", sm.CWD())
	}
}

func TestSessionManager_Current_NilByDefault(t *testing.T) {
	sm := NewSessionManagerWithDir("/tmp/test", t.TempDir())
	if sm.Current() != nil {
		t.Error("Current() should be nil before any Create/Load")
	}
}

func TestSessionManager_Load_SetsAsCurrent(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/tmp/test", dir)
	sess, err := sm.Create("x", "")
	if err != nil {
		t.Fatal(err)
	}
	flushSession(t, sess)
	path := sess.Path()

	sm2 := NewSessionManagerWithDir("/tmp/test", dir)
	loaded, err := sm2.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if sm2.Current() != loaded {
		t.Error("Load should set Current()")
	}
}

func TestSessionManager_Create_CreatesDir(t *testing.T) {
	base := t.TempDir()
	nestedDir := filepath.Join(base, "a", "b", "c")
	sm := NewSessionManagerWithDir("/tmp/test", nestedDir)
	if _, err := sm.Create("deep-1", ""); err != nil {
		t.Fatalf("Create with nested dir: %v", err)
	}
	info, err := os.Stat(nestedDir)
	if err != nil {
		t.Fatalf("nested dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestFileTimestamp(t *testing.T) {
	got := fileTimestamp("2026-01-15T10:30:45.123456789Z")
	want := "2026-01-15T10-30-45-123456789Z"
	if got != want {
		t.Errorf("fileTimestamp = %q, want %q", got, want)
	}
}

// TestSummarizeSessionFile_MessageDerivedFields pins the streaming
// summarizer's parsing of message/session_info entries: count, first user
// message, name, and the concatenated search text. Catches regressions in
// the line-by-line scan that replaced the full loadSessionFile parse.
func TestSummarizeSessionFile_MessageDerivedFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	lines := []string{
		`{"type":"session","id":"sid","cwd":"/work","timestamp":"2026-01-01T00:00:00Z"}`,
		`{"type":"message","id":"m1","parentId":null,"timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"deploy login"}]}}`,
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
		`{"type":"session_info","id":"i1","timestamp":"2026-01-01T00:00:03Z","name":"my session"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := summarizeSessionFile(path)
	if err != nil {
		t.Fatalf("summarizeSessionFile: %v", err)
	}
	if info.ID != "sid" || info.CWD != "/work" {
		t.Errorf("header fields: id=%q cwd=%q", info.ID, info.CWD)
	}
	if info.MessageCount != 2 {
		t.Errorf("MessageCount = %d, want 2", info.MessageCount)
	}
	if info.FirstMessage != "deploy login" {
		t.Errorf("FirstMessage = %q, want %q", info.FirstMessage, "deploy login")
	}
	if info.Name != "my session" {
		t.Errorf("Name = %q, want %q", info.Name, "my session")
	}
	if info.AllMessagesText != "deploy login done" {
		t.Errorf("AllMessagesText = %q", info.AllMessagesText)
	}
}

// TestSummarizeSessionFile_RejectsHeaderless mirrors loadSessionFile's
// guard: a file whose first line is not a session header is an error (so
// the picker skips it) rather than a zero-value summary.
func TestSummarizeSessionFilePreservesStringContentAndLatestName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sess.jsonl")
	lines := []string{
		`{"type":"session","id":"sid","cwd":"/work","timestamp":"2026-01-01T00:00:00Z"}`,
		`{"type":"message","id":"m1","parentId":null,"timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":"legacy prompt"}}`,
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"visible answer"},{"type":"toolCall","id":"call","name":"read","arguments":{"payload":"ignored"}}]}}`,
		`{"type":"message","id":"m3","parentId":"m2","timestamp":"2026-01-01T00:00:03Z","message":{"role":"toolResult","content":[{"type":"text","text":"tool output is not picker text"}]}}`,
		`{"type":"session_info","id":"i1","timestamp":"2026-01-01T00:00:04Z","name":"temporary"}`,
		`{"type":"session_info","id":"i2","timestamp":"2026-01-01T00:00:05Z","name":"  "}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := summarizeSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.MessageCount != 3 || info.FirstMessage != "legacy prompt" || info.AllMessagesText != "legacy prompt visible answer" || info.Name != "" {
		t.Fatalf("summary = %#v", info)
	}
}

func TestSummarizeSessionEntryFallsBackForReorderedFields(t *testing.T) {
	entry, ok := summarizeSessionEntry([]byte(`{"id":"m1","type":"message","message":{"content":"hello","role":"user"}}`))
	if !ok || entry.Type != "message" || entry.Message.Role != "user" || entry.Message.Text != "hello" {
		t.Fatalf("reordered entry = %#v, ok=%v", entry, ok)
	}
	if _, ok := summarizeSessionEntry([]byte(`{"type":"message","message":{"role":"toolResult","content":`)); ok {
		t.Fatal("malformed canonical message was accepted")
	}
}

func BenchmarkListSessionsRecorded(b *testing.B) {
	dir := os.Getenv("PIG_BENCH_SESSION_DIR")
	if dir == "" {
		b.Skip("set PIG_BENCH_SESSION_DIR to a private Session directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		b.Fatal(err)
	}
	var totalBytes int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			b.Fatal(err)
		}
		totalBytes += info.Size()
	}
	b.SetBytes(totalBytes)
	b.ReportAllocs()
	cwd := os.Getenv("PIG_BENCH_SESSION_CWD")
	if cwd == "" {
		cwd = "."
	}
	manager := NewSessionManagerWithDir(cwd, dir)
	var infos []SessionInfo
	b.ResetTimer()
	for range b.N {
		infos, err = manager.ListSessions()
		if err != nil {
			b.Fatal(err)
		}
		if len(infos) == 0 {
			b.Fatal("no sessions found")
		}
	}
	b.StopTimer()
	searchBytes := 0
	for _, info := range infos {
		searchBytes += len(info.AllMessagesText)
	}
	b.ReportMetric(float64(len(infos)), "sessions")
	b.ReportMetric(float64(searchBytes), "search-bytes")
}

func TestSummarizeSessionFile_RejectsHeaderless(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"message","id":"x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := summarizeSessionFile(path); err == nil {
		t.Fatal("expected error for headerless file")
	}
}
