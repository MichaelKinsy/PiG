package codingagent

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi renderSessionEntries (interactive-mode.ts:4053) turns a message entry into its message whatever the role
// (sessionEntryToContextMessages), and addMessageToChat (:3880-3936) draws a displayed "custom" message as a
// CustomMessageComponent, a "compactionSummary" or "branchSummary" message as a spacer and its summary component,
// and skips a "system" message and a custom message with display false. A Session file written by hand or by
// another tool can hold such messages in message entries; each Pig path that rebuilds the transcript must draw them.
func TestResumedSessionRendersCustomAndSummaryMessageRoles(t *testing.T) {
	messages := []map[string]any{
		{"role": agent.RoleCustom, "customType": "note", "content": "shown note", "display": true, "details": map[string]any{"k": 1}, "timestamp": 1760000000000},
		{"role": agent.RoleCustom, "customType": "note", "content": "hidden note", "display": false, "timestamp": 1760000000001},
		{"role": agent.RoleCompactionSummary, "summary": "compacted earlier work", "tokensBefore": 12345, "timestamp": 1760000000002},
		{"role": agent.RoleBranchSummary, "summary": "the abandoned branch", "fromId": "x1", "timestamp": 1760000000003},
		{"role": "system", "content": "system text", "timestamp": 1760000000004},
		{"role": agent.RoleCustom, "customType": "blocks", "content": []any{map[string]any{"type": "text", "text": "block one"}, map[string]any{"type": "text", "text": "block two"}}, "display": true, "timestamp": 1760000000005},
		{"role": agent.RoleUser, "content": "resume-end", "timestamp": 1760000000006},
	}
	for _, path := range []struct {
		name   string
		render func(*InteractiveMode)
	}{
		{"initial resume (--session, --continue)", (*InteractiveMode).renderSessionEntries},
		{"rebuild (/tree, cache-miss and display settings, extension navigation)", (*InteractiveMode).rebuildChatFromSession},
		{"session replacement (/resume, /new, /fork, /clone, reload)", (*InteractiveMode).renderCurrentSessionState},
		{"compaction rebuild", func(m *InteractiveMode) { m.renderSessionEntryList(m.currentSession().GetBranch(), false) }},
	} {
		t.Run(path.name, func(t *testing.T) {
			dir, file := writeRawBashSession(t, messages...)
			m := resumeBashModeFromFile(t, dir, file)
			path.render(m)

			theme := m.markdownThemeWithSettings()
			want := [][]string{
				tui.NewCustomMessageComponent(&tui.CustomMessage{CustomType: "note", Content: "shown note"}, nil, theme, 1).Render(80),
				tui.NewSpacer(1).Render(80),
				tui.NewCompactionSummaryMessageComponent(tui.CompactionSummaryMessage{Summary: "compacted earlier work", TokensBefore: 12345}, theme, 1).Render(80),
				tui.NewSpacer(1).Render(80),
				tui.NewBranchSummaryMessageComponent(tui.BranchSummaryMessage{Summary: "the abandoned branch"}, theme, 1).Render(80),
				tui.NewCustomMessageComponent(&tui.CustomMessage{CustomType: "blocks", Content: []any{map[string]any{"type": "text", "text": "block one"}, map[string]any{"type": "text", "text": "block two"}}}, nil, theme, 1).Render(80),
			}
			children := m.chatContainer.Children()
			if len(children) < len(want) {
				t.Fatalf("chat has %d children, want at least %d before the user message:\n%s", len(children), len(want), renderedChat(m))
			}
			for i, rows := range want {
				if got := children[i].Render(80); !slices.Equal(got, rows) {
					t.Errorf("child %d rows\n got %q\nwant %q", i, got, rows)
				}
			}
			// The summaries start collapsed: the compaction shows its token count, not its text.
			if plain := renderedChat(m); !containsInOrder(plain, "shown note", "Compacted from 12,345 tokens", "block one", "block two", "resume-end") {
				t.Errorf("transcript order differs from the Session:\n%s", plain)
			}
			for _, absent := range []string{"hidden note", "system text"} {
				if plain := renderedChat(m); strings.Contains(plain, absent) {
					t.Errorf("transcript shows %q:\n%s", absent, plain)
				}
			}
		})
	}
}

func containsInOrder(s string, parts ...string) bool {
	for _, part := range parts {
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return true
}
