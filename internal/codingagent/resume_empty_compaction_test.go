package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// session-manager.ts entryToMessages maps every compaction entry to createCompactionSummaryMessage(entry.summary, ...) and addMessageToChat
// renders it (interactive-mode.ts case "compactionSummary"), whatever the summary is; only a branch summary with an empty summary is dropped
// (`entry.type === "branch_summary" && entry.summary`). A compaction that produced an empty summary still shows its header after a resume.
func TestResumedCompactionWithEmptySummaryIsShown(t *testing.T) {
	m := resumedModeWithOutputPad(t, 1, func(sess *Session) {
		first, err := sess.AppendMessage(userMsg("first question"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sess.AppendCompaction("", first, 100, nil, false, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := sess.AppendMessage(assistantMsg("", ai.TextContent{Text: "answer"})); err != nil {
			t.Fatal(err)
		}
	})
	m.rebuildChatFromSession()
	if got := renderedChat(m); !strings.Contains(got, "Compacted from 100 tokens") {
		t.Fatalf("a resumed compaction with an empty summary shows no summary component:\n%s", got)
	}
}
