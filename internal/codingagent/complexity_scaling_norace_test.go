//go:build !race

package codingagent

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// Complexity gates for the per-session-operation paths of the Session log.

// scalingExchange appends one user prompt, an assistant tool call, its result and the final answer.
func scalingExchange(s *Session, i int) {
	call := "call_" + strconv.Itoa(i)
	usage := &ai.Usage{Input: 1000 + i, Output: 50, TotalTokens: 1050 + i}
	for _, message := range []agent.AgentMessage{
		{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("question %d about the repository layout", i)}}, Timestamp: int64(i)}},
		{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ToolCall{ID: call, Name: "read", Arguments: ai.JsonObject{"path": fmt.Sprintf("file%d.go", i)}}}, StopReason: ai.StopReasonToolUse, Usage: usage, Provider: "scaling", ModelID: "scaling-model", Timestamp: int64(i)}},
		{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: call, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package main\n\nfunc main() {}\n"}}, Timestamp: int64(i)}},
		{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d: one file per upstream module", i)}}, StopReason: ai.StopReasonStop, Usage: usage, Provider: "scaling", ModelID: "scaling-model", Timestamp: int64(i)}},
	} {
		if _, err := s.AppendMessage(message); err != nil {
			panic(err)
		}
	}
}

func scalingSessionLog(n int) *Session {
	s := NewSession("scaling", "/tmp/scaling")
	for i := range n {
		scalingExchange(s, i)
	}
	return s
}

func scalingSessionFile(t *testing.T, n int) (*SessionManager, string) {
	dir := t.TempDir()
	manager := NewSessionManagerWithDir("/tmp/scaling", dir)
	s, err := manager.Create("scaling-"+strconv.Itoa(n), "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		scalingExchange(s, i)
	}
	return manager, s.Path()
}

func TestScalingSessionLogOperations(t *testing.T) {
	// Appending one entry writes one line: its cost does not depend on the entries before it (session-manager.ts _appendEntry, _persist).
	testscaling.Check(t, testscaling.Case{Name: "session append one exchange (persisted) at history n", Size: 50, Bound: testscaling.Constant, Prepare: func(n int) func() {
		manager, path := scalingSessionFile(t, n)
		s, err := manager.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return func() { scalingExchange(s, n) }
	}})
	// Opening a session reads and decodes each line once.
	testscaling.Check(t, testscaling.Case{Name: "session load at history n", Size: 50, Bound: testscaling.Linear, Prepare: func(n int) func() {
		manager, path := scalingSessionFile(t, n)
		return func() {
			if _, err := manager.Load(path); err != nil {
				panic(err)
			}
		}
	}})
	for _, tc := range []struct {
		name string
		op   func(*Session)
	}{
		// buildSessionContext walks the leaf's path once.
		{"session buildSessionContext at history n", func(s *Session) { s.BuildSessionContext() }},
		{"session getBranch at history n", func(s *Session) { s.GetBranch() }},
		// getTree indexes every entry and sorts each child list.
		{"session getTree at history n", func(s *Session) { s.GetTree() }},
		// /tree navigation moves the leaf and rebuilds the context of the new branch.
		{"session tree navigation to the first answer at history n", func(s *Session) {
			first := s.GetEntries()[3].Base().ID
			if err := s.Branch(first); err != nil {
				panic(err)
			}
			s.BuildSessionContext()
		}},
	} {
		testscaling.Check(t, testscaling.Case{Name: tc.name, Size: 100, Bound: testscaling.Linear, Prepare: func(n int) func() {
			s := scalingSessionLog(n)
			return func() { tc.op(s) }
		}})
	}
}

func TestScalingSessionSearchKeystroke(t *testing.T) {
	// A keystroke in the session selector's search scans every session's text, as Pi's fuzzyMatch does; the text is prepared
	// once, so a keystroke allocates nothing that grows with it.
	testscaling.Check(t, testscaling.Case{Name: "session search keystroke at message text n", Size: 2000, Bound: testscaling.Constant, Prepare: func(n int) func() {
		sessions := make([]SessionInfo, 40)
		for i := range sessions {
			sessions[i] = SessionInfo{Path: fmt.Sprintf("/s%d.jsonl", i), ID: fmt.Sprintf("id%d", i), CWD: "/work", AllMessagesText: strings.Repeat("some words of a conversation ", n/30)}
		}
		var cache sessionSearches
		cache.filterAndSort(sessions, "conv", sessionSortRelevance)
		queries := []string{"conve", "conv"}
		next := 0
		return func() {
			cache.filterAndSort(sessions, queries[next%2], sessionSortRelevance)
			next++
		}
	}})
}

func TestScalingStreamedWriteContent(t *testing.T) {
	// A write call's content streams in as growing arguments; each update highlights the new lines and keeps the rest from the
	// cache (WriteHighlightCache, write.ts), so the update of a file of n lines costs the new text, not the file. The size
	// starts above 17 updates of three lines, where the 50-line leading block stops changing; each update then highlights it again
	// (about 400 allocations, as Pi does), and below that the highlight memo serves it, which would read as growth.
	testscaling.Check(t, testscaling.Case{Name: "streamed write content of n updates", Size: 1000, Bound: testscaling.Linear, Prepare: func(n int) func() {
		return func() {
			var content strings.Builder
			var cache *writeHighlightCache
			for i := range n {
				fmt.Fprintf(&content, "func f%d(x int) string {\n\treturn \"value %d\" // note\n}\n", i, i)
				cache = updateWriteHighlightCacheIncremental(cache, "sample.go", content.String())
			}
		}
	}})
}
