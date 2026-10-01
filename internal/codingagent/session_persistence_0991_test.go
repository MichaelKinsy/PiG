package codingagent

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// These tests cover the session-file creation rule of upstream 0.99.1 beyond the
// ported cases: `_hasConversation` counts only user and assistant message
// entries (.upstream/v0.99.1/packages/coding-agent/src/core/session-manager.ts:1166-1170),
// `_persist` writes the file on the first such entry (1172-1185), and a file that
// was loaded is already flushed (1043-1049 for an empty file, 1050-1051 otherwise).

func TestSessionFileCreationConversationRule(t *testing.T) {
	for _, tc := range []struct {
		name    string
		append  func(t *testing.T, s *Session)
		created bool
	}{
		{"empty session", func(*testing.T, *Session) {}, false},
		{"session name only", func(t *testing.T, s *Session) {
			if _, err := s.AppendSessionInfo("named"); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"custom entry only", func(t *testing.T, s *Session) { upstreamCustom(t, s, "state", map[string]any{"a": 1}) }, false},
		{"custom message only", func(t *testing.T, s *Session) {
			if _, err := s.AppendCustomMessage("note", "text", true, nil); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"bash execution only", func(t *testing.T, s *Session) {
			if _, err := s.AppendBashExecution(BashExecutionMessage{Command: "true"}); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"user message", func(t *testing.T, s *Session) { upstreamSessionUser(t, s, "hi") }, true},
		{"assistant message", func(t *testing.T, s *Session) { upstreamSessionAssistant(t, s, "hi") }, true},
		{"errored assistant message", func(t *testing.T, s *Session) {
			m := mkAssistantMsg("")
			m.Assistant.StopReason = ai.StopReasonError
			m.Assistant.ErrorMessage = "provider failed"
			if _, err := s.AppendMessage(m); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"aborted assistant message", func(t *testing.T, s *Session) {
			m := mkAssistantMsg("")
			m.Assistant.StopReason = ai.StopReasonAborted
			if _, err := s.AppendMessage(m); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"empty user text", func(t *testing.T, s *Session) {
			if _, err := s.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: ""}}}}); err != nil {
				t.Fatal(err)
			}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := tempSessionMgr(t).Create("", "")
			if err != nil {
				t.Fatal(err)
			}
			tc.append(t, s)
			_, statErr := os.Stat(s.Path())
			if tc.created != (statErr == nil) {
				t.Fatalf("file created = %v, want %v (stat: %v)", statErr == nil, tc.created, statErr)
			}
		})
	}
}

// A crash after the first user message and before any reply leaves the prompt on disk and resumable.
func TestSessionCrashBeforeFirstReplyKeepsThePrompt(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSessionManagerWithDir(dir, dir).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	upstreamSessionUser(t, s, "first question")
	// No Close and no assistant message: the process ends here.
	resumed, err := NewSessionManagerWithDir(dir, dir).Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := readSessionFileRoles(t, resumed.Path()); !slices.Equal(got, []string{"session", "user"}) {
		t.Fatalf("roles = %v", got)
	}
	upstreamSessionAssistant(t, resumed, "answer")
	if got := readSessionFileRoles(t, resumed.Path()); !slices.Equal(got, []string{"session", "user", "assistant"}) {
		t.Fatalf("roles after resume = %v", got)
	}
}

// A file that holds only a header (an empty file initialised by Open, which Pi also writes) is loaded as flushed:
// the first message appends to it and the header is not written twice (session-manager.ts:1035-1049).
func TestHeaderOnlySessionFileResumesAsFlushed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "header-only.jsonl")
	if err := os.WriteFile(path, []byte(fileOperationHeader(dir, "header-only")), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSessionManagerWithDir(dir, dir).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.EntryCount(); got != 0 {
		t.Fatalf("EntryCount = %d, want 0", got)
	}
	if err := s.AppendThinkingLevelChange("off"); err != nil {
		t.Fatal(err)
	}
	if got := readSessionFileRoles(t, path); !slices.Equal(got, []string{"session", "thinking_level_change"}) {
		t.Fatalf("a loaded header-only file appends setup entries directly: %v", got)
	}
	upstreamSessionUser(t, s, "hello")
	if got := readSessionFileRoles(t, path); !slices.Equal(got, []string{"session", "thinking_level_change", "user"}) {
		t.Fatalf("roles = %v", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), `"type":"session"`); n != 1 {
		t.Fatalf("header written %d times", n)
	}
}

func TestOpenEmptyFileWritesHeaderAndKeepsAppending(t *testing.T) {
	dir := t.TempDir()
	path := fileOperationWrite(t, dir, "empty.jsonl", "")
	s, err := NewSessionManagerWithDir(dir, dir).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	upstreamSessionUser(t, s, "hello")
	if got := readSessionFileRoles(t, path); !slices.Equal(got, []string{"session", "user"}) {
		t.Fatalf("roles = %v", got)
	}
}

// getEntryCount reads the index size instead of copying entries (session-manager.ts:1511).
func TestSessionEntryCountExcludesTheHeader(t *testing.T) {
	s := NewSession("count", "/project")
	if got := s.EntryCount(); got != 0 {
		t.Fatalf("empty = %d", got)
	}
	upstreamSessionUser(t, s, "a")
	upstreamSessionAssistant(t, s, "b")
	upstreamCustom(t, s, "state", nil)
	if got := s.EntryCount(); got != 3 {
		t.Fatalf("EntryCount = %d, want 3", got)
	}
	if got := s.EntryCount(); got != len(s.Entries()) {
		t.Fatalf("EntryCount %d differs from len(Entries) %d", got, len(s.Entries()))
	}
}

// getEntryCount is byId.size (session-manager.ts:1511-1513): a loaded file that repeats an entry ID counts the ID once,
// while getEntries still returns every record (1520-1522).
func TestSessionEntryCountCountsDistinctIDsOfALoadedFile(t *testing.T) {
	dir := t.TempDir()
	user := `{"type":"message","id":"dup","parentId":null,"timestamp":"2025-01-01T00:00:01Z","message":{"role":"user","content":"hi","timestamp":1}}` + "\n"
	path := fileOperationWrite(t, dir, "dup.jsonl", fileOperationHeader(dir, "dup-ids")+user+user)
	s, err := NewSessionManagerWithDir(dir, dir).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(s.Entries()); got != 2 {
		t.Fatalf("Entries = %d, want both records", got)
	}
	if got := s.EntryCount(); got != 1 {
		t.Fatalf("EntryCount = %d, want 1 distinct ID", got)
	}
}

// Concurrent appenders race for the first flush: whichever message arrives first creates the file, and the file must
// end up with one header and every entry in the order the Session recorded them (session-manager.ts:1172-1185 is
// single-threaded upstream, so this is the Go ownership contract for the same rule).
func TestConcurrentFirstMessagesCreateOneCompleteFile(t *testing.T) {
	const writers = 16
	for round := range 12 {
		s, err := tempSessionMgr(t).Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range writers {
			wg.Go(func() {
				<-start
				if i%2 == 0 {
					upstreamSessionUser(t, s, "user")
				} else {
					upstreamSessionAssistant(t, s, "assistant")
				}
			})
		}
		close(start)
		wg.Wait()
		records := readJSONLLines(t, s.Path())
		if len(records) != writers+1 || records[0]["type"] != "session" {
			t.Fatalf("round %d: %d records, first %v", round, len(records), records[0]["type"])
		}
		entries := s.Entries()
		for i, entry := range entries {
			if records[i+1]["id"] != entry.Base.ID {
				t.Fatalf("round %d: file entry %d is %v, Session recorded %s", round, i, records[i+1]["id"], entry.Base.ID)
			}
		}
	}
}

// The first flush opens the file exclusively (`openSync(file, "wx")`, session-manager.ts:1175): a file that appeared at the
// session's path after it was selected is reported as EEXIST and left untouched, never truncated.
func TestFirstFlushDoesNotOverwriteAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "later.jsonl")
	s, err := NewSessionManagerWithDir(dir, dir).Load(path)
	if err != nil {
		t.Fatal(err)
	}
	const other = "written by someone else\n"
	if err := os.WriteFile(path, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	m := mkUserMsg("hello")
	_, err = s.AppendMessage(m)
	if want := "EEXIST: file already exists, open '" + path + "'"; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("error %v does not wrap os.ErrExist", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != other {
		t.Fatalf("existing file was changed: %q, %v", data, err)
	}
}
