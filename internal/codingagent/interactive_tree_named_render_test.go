package codingagent

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// D270: the hold stops what only asks for a frame; a paint that names a render (showStatus, showError, the managed-tool status: tuiInst.Render()) still paints, with the prompt gone, as Pi's does (interactive-mode.ts showStatus requests the render at once). The test posts such a paint to the main loop while the navigation waits.
func TestTreeNavigationNamedRenderStillPaintsDuringTheHold(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paint func(m *InteractiveMode)
		want  string
	}{
		{"showStatus", func(m *InteractiveMode) { m.showStatus("NAMED STATUS") }, "NAMED STATUS"},
		{"showManagedToolStatus", func(m *InteractiveMode) {
			m.showManagedToolStatus(tools.ToolStatus{Type: "info", Message: "NAMED TOOL STATUS"})
		}, "NAMED TOOL STATUS"},
		{"showWarning", func(m *InteractiveMode) { m.showWarning("NAMED WARNING") }, "NAMED WARNING"},
		{"appendBorderedNotice", func(m *InteractiveMode) { m.appendBorderedNotice(tui.NewText("NAMED BORDERED NOTICE")) }, "NAMED BORDERED NOTICE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, log, h := treeFramesMode(t, "OLD LEAF")
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				m.runCtx = ctx
				m.editorContainer.SetChildren(tui.NewText("SUMMARY PROMPT"))
				m.tuiInst.Render()
				log.text()
				m.editorContainer.SetChildren(m.editor)
				m.tuiInst.RequestRender()
				var during string
				h.navigate = func(context.Context) (NavigateTreeResult, error) {
					if err := m.postToMain(ctx, func() { tc.paint(m) }); err != nil {
						return NavigateTreeResult{}, err
					}
					time.Sleep(200 * time.Millisecond)
					during = log.text()
					return NavigateTreeResult{}, nil
				}
				if _, err := m.navigateTree(ctx, "target", false, ""); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(during, tc.want) {
					t.Fatalf("the named render did not paint during the hold: %q", during)
				}
				// The frame is differential: the old transcript's rows did not change and are not written again; the prompt's row is replaced by the editor.
				if strings.Contains(during, "SUMMARY PROMPT") || !strings.Contains(during, "─") {
					t.Fatalf("the named render painted %q, want the prompt gone and the editor drawn", during)
				}
			})
		})
	}
}
