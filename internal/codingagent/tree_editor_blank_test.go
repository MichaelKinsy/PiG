package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// interactive-mode.ts fills the editor with the navigated entry's text only when `!this.editor.getText().trim()`: String.prototype.trim removes U+FEFF and U+00A0 but not U+0085.
func TestTreeNavigationFillsAnEditorBlankByJSTrim(t *testing.T) {
	for _, tc := range []struct{ name, draft, want string }{
		{"empty", "", "target text"},
		{"spaces", "  \t", "target text"},
		{"byte order mark", "\ufeff", "target text"},
		{"no-break space and line separator", "\u00a0\u2028", "target text"},
		{"next line is not JS whitespace", "\u0085", "\u0085"},
		{"text", "draft", "draft"},
	} {
		for _, extension := range []bool{false, true} {
			name := tc.name
			if extension {
				name += "/ctx.navigateTree"
			}
			t.Run(name, func(t *testing.T) {
				m := statusBorderMode(t, true)
				h := &lifecycleNavigationHandle{events: make(chan agent.AgentEvent, 8)}
				m.opts.SessionHandle = h
				m.eventCh = h.events
				h.navigate = func(context.Context) (NavigateTreeResult, error) {
					return NavigateTreeResult{EditorText: "target text"}, nil
				}
				m.editor.SetText(tc.draft)
				if extension {
					m.runCtx = t.Context()
					if _, err := m.extensionNavigateTreeContext(t.Context(), "target", nil); err != nil {
						t.Fatal(err)
					}
					for drained := false; !drained; {
						select {
						case task := <-m.uiTaskCh:
							task()
						default:
							drained = true
						}
					}
				} else {
					sc := m.buildSlashContext(t.Context())
					sc.ShowExtensionSelector = func(string, []string, string) (string, bool) { return "No summary", true }
					if err := treeNavigateWithSummarize(sc, "target"); err != nil {
						t.Fatal(err)
					}
				}
				if got := m.editor.Text(); got != tc.want {
					t.Fatalf("editor %q after navigation, want %q", got, tc.want)
				}
			})
		}
	}
}
