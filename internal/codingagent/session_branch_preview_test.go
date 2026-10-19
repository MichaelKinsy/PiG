package codingagent

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// previewByAppend is the boundary preview as it was built: a session that took each branch entry through AppendEntry, which encodes and decodes its record again.
func previewByAppend(t *testing.T, s *Session) *Session {
	t.Helper()
	header := s.GetHeader()
	preview := NewSession(header.ID, header.CWD)
	if leaf := s.GetLeafID(); leaf != nil {
		for _, entry := range s.GetBranch(*leaf) {
			if err := preview.AppendEntry(entry); err != nil {
				t.Fatal(err)
			}
		}
	}
	return preview
}

func previewByBranch(s *Session) *Session {
	var branch []SessionEntry
	if leaf := s.GetLeafID(); leaf != nil {
		branch = s.GetBranch(*leaf)
	}
	return NewBranchSession(s.GetHeader(), branch)
}

// projectionRecord is everything a boundary preview reads of a projection: each projected entry's id, record and messages, the messages, and the settings.
func projectionRecord(t *testing.T, s *Session) string {
	t.Helper()
	projection := s.BuildSessionProjection()
	var out strings.Builder
	for _, entry := range projection.Entries {
		messages, err := json.Marshal(entry.Messages)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, "%s %s %s\n", entry.SourceEntry.Base().ID, entry.SourceEntry.Raw(), messages)
	}
	messages, err := json.Marshal(projection.Messages)
	if err != nil {
		t.Fatal(err)
	}
	model, err := json.Marshal(projection.Model)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&out, "%s\n%s %s %v\n", messages, projection.ThinkingLevel, model, projection.HasContextEdits)
	branch := s.GetContextBranch()
	for _, entry := range branch {
		fmt.Fprintf(&out, "ctx %s %s\n", entry.Base().ID, entry.Raw())
	}
	return out.String()
}

func branchPreviewSource(t *testing.T) (*Session, []string) {
	t.Helper()
	s := NewSession("preview-source", t.TempDir())
	var ids []string
	add := func(id string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		return id
	}
	now := time.Now().UnixMilli()
	user := func(text string) agent.AgentMessage {
		return agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: now}}
	}
	for i := range 6 {
		call := fmt.Sprintf("call_%d", i)
		add(s.AppendMessage(user(fmt.Sprintf("question %d \u2028 with \"escapes\" \u00e9", i))))
		if i == 1 {
			add(s.AppendModelChange("anthropic", "claude"))
			add(s.AppendThinkingLevelChange("high"))
		}
		add(s.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ToolCall{ID: call, Name: "read", Arguments: ai.JsonObject{"path": "a.go", "n": 3}}}, StopReason: ai.StopReasonToolUse, API: "anthropic-messages", Provider: "anthropic", ModelID: "claude", Usage: &ai.Usage{Input: 10, Output: 5, TotalTokens: 15}, Timestamp: now}}))
		add(s.AppendMessage(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: call, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package main\n"}}, Details: map[string]any{"lines": 1}, Timestamp: now}}))
		add(s.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d", i)}}, StopReason: ai.StopReasonStop, API: "anthropic-messages", Provider: "anthropic", ModelID: "claude", Usage: &ai.Usage{Input: 10, Output: 5, TotalTokens: 15}, Timestamp: now}}))
		if i == 2 {
			add(s.AppendCustomEntry("note", map[string]any{"k": []int{1, 2}}))
			add(s.AppendCustomMessageEntry("ctx", "custom body", true, map[string]any{"d": 1}))
			label := "mark"
			add(s.AppendLabelChange(ids[0], &label))
		}
		if i == 3 {
			add(s.AppendContextEdit(ids[len(ids)-2], nil))
		}
	}
	return s, ids
}

// A preview that takes the branch's entries as they are holds the same projection as one that takes each through AppendEntry, before and after the drafts a handler returns, with every entry's record and every message equal.
func TestBranchSessionProjectionEqualsAppendedPreview(t *testing.T) {
	s, ids := branchPreviewSource(t)
	drafts := []struct {
		name  string
		apply func(*Session) error
	}{
		{"none", func(*Session) error { return nil }},
		{"custom", func(p *Session) error { _, err := p.AppendCustomEntry("draft", map[string]any{"a": 1}); return err }},
		{"custom_message", func(p *Session) error {
			_, err := p.AppendCustomMessageEntry("draft", "hello", false, nil)
			return err
		}},
		{"context_edit", func(p *Session) error { _, err := p.AppendContextEdit(ids[3], nil); return err }},
		{"compaction", func(p *Session) error {
			_, err := p.AppendCompaction("summary", ids[len(ids)-4], 1234, nil, true, nil)
			return err
		}},
		{"compaction then message", func(p *Session) error {
			if _, err := p.AppendCompaction("summary", ids[len(ids)-6], 1234, map[string]any{"x": 1}, true, nil); err != nil {
				return err
			}
			_, err := p.AppendCustomMessageEntry("after", "tail", true, nil)
			return err
		}},
	}
	for _, draft := range drafts {
		t.Run(draft.name, func(t *testing.T) {
			old, shared, applied := previewByAppend(t, s), previewByBranch(s), previewByBranch(s)
			held := len(old.GetBranch(*old.GetLeafID()))
			if err := draft.apply(old); err != nil {
				t.Fatal(err)
			}
			// The drafts' ids and timestamps are fresh at each append, so the branch session takes the entries the appended preview made, as they are.
			for _, entry := range old.GetBranch(*old.GetLeafID())[held:] {
				if err := shared.AppendEntry(entry); err != nil {
					t.Fatal(err)
				}
			}
			want, got := projectionRecord(t, old), projectionRecord(t, shared)
			if got != want {
				t.Fatalf("branch session projection differs from the appended preview\nwant:\n%s\ngot:\n%s", want, got)
			}
			// A draft appended to the branch session through the API a preview uses lands as it does on the appended preview.
			if err := draft.apply(applied); err != nil {
				t.Fatal(err)
			}
			if a, b := len(old.BuildSessionProjection().Entries), len(applied.BuildSessionProjection().Entries); a != b {
				t.Fatalf("projected entries %d, want %d", b, a)
			}
			if a, b := old.GetEntryCount(), applied.GetEntryCount(); a != b {
				t.Fatalf("entry count %d, want %d", b, a)
			}
		})
	}
}

// The preview never writes into the session whose entries it shares.
func TestBranchSessionLeavesTheSourceUntouched(t *testing.T) {
	s, _ := branchPreviewSource(t)
	before := projectionRecord(t, s)
	count, revision, leaf := s.GetEntryCount(), s.Revision(), *s.GetLeafID()
	preview := previewByBranch(s)
	if _, err := preview.AppendCompaction("summary", leaf, 1, nil, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := preview.AppendCustomMessageEntry("draft", "x", true, nil); err != nil {
		t.Fatal(err)
	}
	if after := projectionRecord(t, s); after != before || s.GetEntryCount() != count || s.Revision() != revision || *s.GetLeafID() != leaf {
		t.Fatal("a preview draft changed the session it was taken from")
	}
}
