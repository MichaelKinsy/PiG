package codingagent

import (
	"strings"
	"testing"
)

// Pi restores a compacted session from sessionManager.buildContextEntries() (interactive-mode.ts:4220-4223 renderInitialMessages, :4272-4275
// rebuildChatFromMessages), and buildContextEntries (session-manager.ts:476-508) returns the latest compaction FIRST, then the entries from
// firstKeptEntryId up to it, then the entries after it. When firstKeptEntryId names no entry before the compaction, or is absent, nothing before the
// compaction is kept (:499-507 compares each entry id with it). The expected rows below are what real Pi 1.2.0+b06c1f2c paints for the same session
// file (tmux frames, `pi --session-dir sessions -c`), with showCacheMissNotices on and off.
func TestResumedCompactedTranscriptFollowsPiContextEntryOrder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		firstKept func(kept string) string
		want      []string
	}{
		{"kept range before the compaction", func(kept string) string { return kept }, []string{"Compacted from 999 tokens", "SECOND q", "SECOND a", "THIRD q", "THIRD a"}},
		{"first kept id names no entry", func(string) string { return "nonexistent" }, []string{"Compacted from 999 tokens", "THIRD q", "THIRD a"}},
		{"first kept id absent", func(string) string { return "" }, []string{"Compacted from 999 tokens", "THIRD q", "THIRD a"}},
	} {
		for _, notices := range []bool{false, true} {
			for _, path := range []string{"startup", "rebuild"} {
				t.Run(tc.name+map[bool]string{false: "/notices off", true: "/notices on"}[notices]+"/"+path, func(t *testing.T) {
					m := resumedModeWithOutputPad(t, 1, func(sess *Session) {
						must := func(id string, err error) string {
							t.Helper()
							if err != nil {
								t.Fatal(err)
							}
							return id
						}
						must(sess.AppendMessage(mkUserMsg("FIRST q")))
						must(sess.AppendMessage(mkAssistantMsg("FIRST a")))
						kept := must(sess.AppendMessage(mkUserMsg("SECOND q")))
						must(sess.AppendMessage(mkAssistantMsg("SECOND a")))
						must(sess.AppendCompaction("SUMMARY", tc.firstKept(kept), 999, nil, false, nil))
						must(sess.AppendMessage(mkUserMsg("THIRD q")))
						must(sess.AppendMessage(mkAssistantMsg("THIRD a")))
					})
					m.opts.SettingsManager = NewInMemorySettingsManager(Settings{ShowCacheMissNotices: notices})
					if path == "startup" {
						m.opts.ResumePath = m.currentSession().Path()
						m.renderInitialMessages()
					} else {
						m.rebuildChatFromSession()
					}
					got := renderedChat(m)
					at := -1
					for _, want := range tc.want {
						i := strings.Index(got, want)
						if i <= at {
							t.Fatalf("want %q after offset %d, in order %q; transcript:\n%s", want, at, tc.want, got)
						}
						at = i
					}
					if strings.Contains(got, "FIRST") {
						t.Errorf("the transcript shows the summarized first turn:\n%s", got)
					}
				})
			}
		}
	}
}
