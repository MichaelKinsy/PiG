// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func activityAssistant(text string, timestamp int64) agent.AgentMessage {
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}},
		API: "openai-completions", Provider: "openai", ModelID: "test",
		Usage: &ai.Usage{Input: 1, Output: 1, TotalTokens: 2}, StopReason: ai.StopReasonStop, Timestamp: timestamp,
	}}
}

// .upstream/v0.87.1/packages/coding-agent/test/session-info-modified-timestamp.test.ts:49
func TestSessionInfoUsesLastUserAssistantMessageTimestampInsteadOfFileMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	header := `{"type":"session","id":"test-session","version":3,"timestamp":"1970-01-01T00:00:00.000Z","cwd":"/tmp"}`
	if err := os.WriteFile(path, []byte(header+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sm := NewSessionManagerWithDir("/tmp", dir)
	session, err := sm.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	first := activityAssistant("hi", 1000)
	if _, err := session.AppendMessage(first); err != nil {
		t.Fatal(err)
	}
	before := time.UnixMilli(2000)
	if err := os.Chtimes(path, before, before); err != nil {
		t.Fatal(err)
	}
	later := activityAssistant("later", 3000)
	if _, err := session.AppendMessage(later); err != nil {
		t.Fatal(err)
	}
	infos, err := sm.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("sessions = %v", infos)
	}
	if got := infos[0].Modified.UnixMilli(); got != later.Assistant.Timestamp || got == before.UnixMilli() {
		t.Fatalf("modified = %d, want message timestamp %d, not mtime %d", got, later.Assistant.Timestamp, before.UnixMilli())
	}
	fmt.Printf("SESSION_ACTIVITY modified=%d\n", infos[0].Modified.UnixMilli())
}

// Pi session-manager.ts:790-877: max user/assistant activity, entry-time fallback,
// then header time, then mtime. Metadata and tool results do not count as activity.
func TestSessionActivityTimestampFallbacks(t *testing.T) {
	const headerTime = "2025-01-01T00:00:00.000Z"
	const entryTime = "2025-01-02T00:00:00.000Z"
	entryMs := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC).UnixMilli()
	headerMs := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	cases := []struct {
		name, header, body string
		want               int64
	}{
		{"spaced JSON fields", headerTime, `{ "role": "user", "content": "hi", "timestamp": 3000 }`, 3000},
		{"empty", headerTime, "", headerMs},
		{"invalid header time", "bad", "", 9000},
		{"entry timestamp fallback", headerTime, `{"role":"user","content":"hi"}`, entryMs},
		{"zero message timestamp falls back to header", headerTime, `{"role":"assistant","content":[],"timestamp":0}`, headerMs},
		{"tool is not activity", headerTime, `{"role":"toolResult","content":[],"timestamp":9000000}`, headerMs},
		{"no content is not activity", headerTime, `{"role":"user","timestamp":9000000}`, headerMs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			data := fmt.Sprintf("{\"type\":\"session\",\"id\":\"test\",\"version\":3,\"timestamp\":%q,\"cwd\":\"/tmp\"}\n", tc.header)
			if tc.body != "" {
				data += fmt.Sprintf("{\"type\":\"message\",\"id\":\"msg\",\"parentId\":null,\"timestamp\":%q,\"message\":%s}\n", entryTime, tc.body)
			}
			if tc.name == "spaced JSON fields" {
				data = strings.ReplaceAll(data, `"type":"message"`, `"type": "message"`)
			}
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, time.UnixMilli(9000), time.UnixMilli(9000)); err != nil {
				t.Fatal(err)
			}
			got, err := summarizeSessionFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got.Modified.UnixMilli() != tc.want {
				t.Fatalf("modified = %d, want %d", got.Modified.UnixMilli(), tc.want)
			}
		})
	}
}

func TestSessionActivityOrderingDoesNotChangeContinueMtimeSelection(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManagerWithDir("/project", dir)
	for i, id := range []string{"old", "new"} {
		s, err := sm.Create(id, "")
		if err != nil {
			t.Fatal(err)
		}
		message := activityAssistant(id, int64(2000-i*1000))
		if _, err := s.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(s.Path(), time.Unix(int64(i+10), 0), time.Unix(int64(i+10), 0)); err != nil {
			t.Fatal(err)
		}
	}
	infos, err := sm.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 || infos[0].ID != "old" {
		t.Fatalf("activity order = %+v", infos)
	}
	for _, path := range []string{sm.FindMostRecent(), sm.FindMostRecentForContinue()} {
		s, err := sm.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if s.ID() != "new" {
			t.Fatalf("continue picked %q, want new by mtime", s.ID())
		}
	}
	fmt.Println("SESSION_ACTIVITY order=old,new continue=new")
}
