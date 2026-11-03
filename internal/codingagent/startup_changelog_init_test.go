//go:build !pig_strip_changelog

package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// changelogSessionHandle is a Session handle whose agent is the only part init() reads for the changelog.
type changelogSessionHandle struct {
	InteractiveSessionHandle
	agent *agent.Agent
}

func (h changelogSessionHandle) Agent() *agent.Agent { return h.agent }

// interactive-mode.ts getChangelogForDisplay (1357-1382) skips the changelog only when `session.state.messages.length > 0`. A resumed Session that holds no message
// is not skipped, and a Session with messages is, however it was started: neither the first-start record nor the update entries are produced for it.
func TestChangelogForDisplayFollowsTheSessionMessages(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	userMessage := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}}}
	for _, tc := range []struct {
		name         string
		messages     []agent.AgentMessage
		lastSeen     string
		wantRecorded string
		wantEntries  bool
	}{
		{"a Session without messages records the first start", nil, "", "9.9.9", false},
		{"a Session with messages records nothing on a first start", []agent.AgentMessage{userMessage}, "", "", false},
		{"a Session without messages shows the entries of an update", nil, "0.0.1", "9.9.9", true},
		{"a Session with messages shows no entries of an update", []agent.AgentMessage{userMessage}, "0.0.1", "0.0.1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm := NewInMemorySettingsManager(Settings{LastChangelogVersion: tc.lastSeen})
			started := mustNewAgent(agent.AgentOptions{})
			started.SetMessages(tc.messages)
			m := &InteractiveMode{opts: InteractiveModeOptions{SettingsManager: sm, AppVersion: "9.9.9", SessionHandle: changelogSessionHandle{agent: started}}}
			if got := m.getChangelogForDisplay(); (len(got) > 0) != tc.wantEntries {
				t.Fatalf("entries = %d, want entries %v", len(got), tc.wantEntries)
			}
			if got := sm.GetLastChangelogVersion(); got != tc.wantRecorded {
				t.Fatalf("recorded version = %q, want %q", got, tc.wantRecorded)
			}
		})
	}
}
