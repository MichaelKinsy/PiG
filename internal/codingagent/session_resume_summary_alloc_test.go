//go:build !race

package codingagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// The resume picker reads each session's name, first message, message count, last activity and message texts (buildSessionInfo). For a
// user or assistant line it decodes the whole line into a generic value (json.Unmarshal into any), so a session of large write calls,
// thinking or compaction summaries costs the picker a decoded copy of all of it, though it keeps only the text blocks. A member walk that
// decodes only the members the summary reads costs those members.
func TestResumePickerSummaryDecodesOnlyWhatItReads(t *testing.T) {
	const turns = 120
	dir := t.TempDir()
	s, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range turns {
		if _, err := s.AppendMessage(mkUserMsg(fmt.Sprintf("write file %d", i))); err != nil {
			t.Fatal(err)
		}
		call := mkAssistantMsg(fmt.Sprintf("writing f%d.go", i))
		call.Assistant.Content = append(call.Assistant.Content, ai.ToolCall{ID: fmt.Sprintf("call%d", i), Name: "write", Arguments: ai.JsonObject{
			"path":    fmt.Sprintf("f%d.go", i),
			"content": strings.Repeat(fmt.Sprintf("\tline %d of \"f%d.go\"\n", i%89, i), 32<<10/24),
		}})
		call.Assistant.StopReason = ai.StopReasonToolUse
		if _, err := s.AppendMessage(call); err != nil {
			t.Fatal(err)
		}
		result := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: fmt.Sprintf("call%d", i), ToolName: "write",
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "wrote it"}}}}
		if _, err := s.AppendMessage(result); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendMessage(mkAssistantMsg(fmt.Sprintf("done %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	st, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	// The first summary of a file is a cold scan; later ones resume from the picker's summary cache.
	var info SessionInfo
	allocated := lwAllocated(func() { info, err = summarizeSessionFileContext(context.Background(), s.Path()) })
	if err != nil || info.MessageCount != 4*turns {
		t.Fatalf("summary: %d messages, %v; want %d", info.MessageCount, err, 4*turns)
	}
	ratio := float64(allocated) / float64(st.Size())
	const ceiling = 0.25
	t.Logf("picker summary of a %d-byte session allocated %d B (%.2f per file byte)", st.Size(), allocated, ratio)
	if ratio > ceiling {
		t.Fatalf("the picker summary of a %d-byte session allocated %d B (%.2f per file byte); want at most %.2f: each user and assistant line is decoded whole into a generic value (session_resume.go summaryPartial.feed, json.Unmarshal into any)",
			st.Size(), allocated, ratio, ceiling)
	}
}
