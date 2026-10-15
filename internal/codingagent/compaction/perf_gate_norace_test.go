//go:build !race && linux

package compaction

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// perfBranch returns the root-to-leaf branch of a persisted session of entries messages, alternating user and assistant, about 400 bytes of text each.
func perfBranch(t *testing.T, entries int) []codingagent.SessionEntry {
	t.Helper()
	dir := t.TempDir()
	session, err := codingagent.NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("lorem ipsum dolor sit amet ", 15)
	for i := range entries {
		message := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("question %d: %s", i, text)}}, Timestamp: int64(i)}}
		if i%2 == 1 {
			message = agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d: %s", i, text)}}, Timestamp: int64(i)}}
		}
		if _, err := session.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	return session.GetBranch()
}

// Preparing a compaction (projection, token estimate and cut point over the branch) costs a constant allocation per entry examined.
func TestCompactionPreparePerEntryAllocationGate(t *testing.T) {
	const n = 1000
	branches := map[int][]codingagent.SessionEntry{}
	for _, entries := range []int{n, 2 * n, 4 * n} {
		branches[entries] = perfBranch(t, entries)
	}
	settings := DefaultCompactionSettings
	run := func(entries int) {
		if PrepareCompaction(branches[entries], settings) == nil {
			t.Fatalf("no compaction prepared for %d entries", entries)
		}
	}
	marginal := perfgate.CheckLinear(t, "compaction.prepare_entry", n, 1.5, run)
	perfgate.Check(t, "compaction.prepare_entry.allocs", marginal.Allocs)
	perfgate.Check(t, "compaction.prepare_entry.bytes", marginal.Bytes)
}
