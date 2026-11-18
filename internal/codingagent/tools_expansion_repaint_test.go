package codingagent

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi's app.tools.expand action calls toggleToolOutputExpansion (interactive-mode.ts:3234, :4741), which calls setToolsExpanded (:4745-4762): every mounted expandable
// child is set, then showStatus adds or updates the status line and ends in ui.requestRender() (:3964-3982). The screen changes in the frame that request schedules, not in
// the toggle itself, so a toggle whose status line asks for no render leaves the mounted transcript showing the old rows until some other render comes.
func TestToolsExpansionToggleRepaintsTheMountedTranscriptInTheScheduledFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, term := newTickRenderProbe(t, "regular")
		for range 3 {
			m.chatContainer.Add(tui.NewSkillInvocationMessageComponent(tui.ParsedSkillBlock{Name: "inspect", Content: "SKILL_BODY_MARKER"}, nil, 1, nil, "", ""))
		}
		m.tuiInst.Render()
		term.take()
		frameAfterToggle := func() string {
			time.Sleep(100 * time.Millisecond)
			synctest.Wait()
			m.drainMainLoopOnce()
			return widthx.StripAnsi(term.take())
		}

		m.toggleAllTools()
		expanded := frameAfterToggle()
		if got := strings.Count(expanded, "SKILL_BODY_MARKER"); got != 3 {
			t.Fatalf("the frame after expanding shows the body of %d of 3 mounted rows: %q", got, expanded)
		}
		if !strings.Contains(expanded, "Tool output: expanded") {
			t.Fatalf("the frame after expanding lacks the status line: %q", expanded)
		}

		m.toggleAllTools()
		collapsed := frameAfterToggle()
		if strings.Contains(collapsed, "SKILL_BODY_MARKER") || !strings.Contains(collapsed, "Tool output: collapsed") {
			t.Fatalf("the frame after collapsing: %q", collapsed)
		}
		if rows := strings.Join(m.chatContainer.Render(120), "\n"); strings.Contains(rows, "SKILL_BODY_MARKER") {
			t.Fatalf("a mounted row still shows its body after collapsing: %q", rows)
		}
	})
}
