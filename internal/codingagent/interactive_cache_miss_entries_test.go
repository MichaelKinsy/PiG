package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts renderSessionItems derives cache-miss notices with collectCacheMisses(sessionManager.getEntries()): every entry of the
// file in file order, not only the current branch. A response on a second branch therefore compares with the request before it in the file,
// here the first branch's cached request, as the live notice (detectCacheMiss over getEntries) and /session's re-billed total do.
func TestRebuiltCacheMissNoticesScanEveryEntryInFileOrder(t *testing.T) {
	dir := t.TempDir()
	sm := NewSettingsManager(dir, dir)
	if err := sm.SetShowCacheMissNotices(true); err != nil {
		t.Fatal(err)
	}
	session := NewSession("cache-miss-branches", dir)
	cached := &agent.AssistantMessage{Role: "assistant", Provider: "test", ModelID: "model", Timestamp: 1, Usage: &ai.Usage{Input: 1_000, CacheWrite: 49_000}}
	uncached := &agent.AssistantMessage{Role: "assistant", Provider: "test", ModelID: "model", Timestamp: 2, Usage: &ai.Usage{Input: 50_000}}
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: cached}); err != nil {
		t.Fatal(err)
	}
	session.ResetLeaf()
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: uncached}); err != nil {
		t.Fatal(err)
	}
	if branch := session.GetBranch(); len(branch) != 1 {
		t.Fatalf("branch has %d entries, want only the second response", len(branch))
	}
	mode := &InteractiveMode{
		opts:          InteractiveModeOptions{SettingsManager: sm, SessionHandle: &recordingCompactHandle{inner: session}},
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 100, 30),
	}
	mode.rebuildChatFromSession()
	if got := strings.Join(mode.chatContainer.Render(100), "\n"); !strings.Contains(got, "Cache miss: 50k tokens re-billed") {
		t.Fatalf("rebuilt second branch has no cache miss notice:\n%s", got)
	}
}
