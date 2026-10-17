// SPDX-License-Identifier: MIT

//go:build !race

package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// recordRetentionReads and recordRetentionFileBytes size the reads: each file is larger than the read tool's byte limit, so every result is a
// truncated read whose details.truncation.content is the kept text, as a long agent run's reads of large source files are.
const (
	recordRetentionReads     = 64
	recordRetentionFileBytes = 64 << 10
)

// ceilingSessionOverheadPerTextByte bounds the heap a session retains for its message entries beyond the messages themselves, per byte of
// tool-result text. The session holds the appended message (Pi's session-manager.ts appendMessage keeps the object the agent appended), so
// what it adds is its index, about 0.01 per text byte here. On integrate-042 1166362571 every entry also keeps its encoded JSONL record, which
// writes a truncated read's text twice (content and details.truncation.content) with its escapes: 2.41 per text byte, with or without a
// session file.
const ceilingSessionOverheadPerTextByte = 0.25

// recordRetentionMessages runs the read tool over recordRetentionReads distinct source files in dir and returns the tool-result messages the
// agent loop makes of the results, and the bytes of text they hold.
func recordRetentionMessages(t *testing.T, dir string) ([]agent.AgentMessage, int) {
	t.Helper()
	read := tools.ReadTool{CWD: dir}
	messages := make([]agent.AgentMessage, 0, recordRetentionReads)
	textBytes := 0
	for i := range recordRetentionReads {
		var b strings.Builder
		for line := 0; b.Len() < recordRetentionFileBytes; line++ {
			fmt.Fprintf(&b, "\tlog.Printf(\"file %d line %d: %%s\", %q) // %08x\n", i, line, fmt.Sprintf("v%d.%d", i*line, line), uint32(i*2654435761+line*40503))
		}
		name := fmt.Sprintf("file%02d.go", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		params, _ := json.Marshal(map[string]string{"path": name})
		result, err := read.Execute(context.Background(), fmt.Sprintf("call_%d", i), params, nil)
		if err != nil {
			t.Fatal(err)
		}
		details, ok := result.Details.(*tools.ReadDetails)
		if !ok || details.Truncation == nil {
			t.Fatalf("read of %s returned details %#v; want a truncated read", name, result.Details)
		}
		for _, block := range result.Content {
			if text, ok := block.(ai.TextContent); ok {
				textBytes += len(text.Text)
			}
		}
		messages = append(messages, agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: fmt.Sprintf("call_%d", i),
			ToolName: "read", Content: result.Content, Details: result.Details, Timestamp: int64(1_700_000_000_000 + i)}})
	}
	return messages, textBytes
}

// heapLive is the heap in use after two collections.
func heapLive() uint64 {
	var stats runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// A session retains for each message entry what Pi's does, the appended message, and not a second, encoded copy of it: the agent and its
// transcript hold the tool results anyway, so a session (with or without a file) that keeps every entry's JSONL record holds each read's
// text again, twice for a truncated read. Byte parity is unaffected: a record that encoding the entry reproduces is the entry's encoding.
func TestSessionRetainsNoRecordOfAppendedMessages(t *testing.T) {
	cases := []struct {
		name string
		open func(t *testing.T) *Session
	}{
		{"no-session", func(t *testing.T) *Session { return NewSession("record-retention", t.TempDir()) }},
		{"session-file", func(t *testing.T) *Session {
			session := NewSession("record-retention", t.TempDir())
			session.SetPath(filepath.Join(t.TempDir(), "session.jsonl"))
			// The user message opens the gate that writes the file, so every read result below is appended to it as it is appended.
			if _, err := session.AppendMessage(mkUserMsg("read the sources")); err != nil {
				t.Fatal(err)
			}
			return session
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			messages, textBytes := recordRetentionMessages(t, t.TempDir())
			session := c.open(t)
			before := heapLive()
			for _, message := range messages {
				if _, err := session.AppendMessage(message); err != nil {
					t.Fatal(err)
				}
			}
			after := heapLive()
			runtime.KeepAlive(messages)
			if path := session.Path(); path != "" {
				if data, err := os.ReadFile(path); err != nil || len(data) < textBytes {
					t.Fatalf("session file %s holds %d bytes (%v); want every read result written", path, len(data), err)
				}
			}
			runtime.KeepAlive(session)
			overhead := float64(after) - float64(before)
			ratio := overhead / float64(textBytes)
			t.Logf("%d reads, %d text bytes: session retains %.0f B beyond the messages (%.2f per text byte)", len(messages), textBytes, overhead, ratio)
			if ratio > ceilingSessionOverheadPerTextByte {
				t.Fatalf("session retains %.0f B (%.2f per text byte) for %d appended read results holding %d text bytes; want at most %.2f per text byte: each entry keeps its encoded record",
					overhead, ratio, len(messages), textBytes, ceilingSessionOverheadPerTextByte)
			}
		})
	}
}
