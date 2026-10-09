package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's footer.ts:124-128 reads the current SessionManager name. A fork keeps
// only the selected prefix (session-manager.ts:createBranchedSession), not the
// latest name on the source's abandoned suffix.
func TestInteractiveForkFooterUsesSelectedSessionName(t *testing.T) {
	for _, tc := range []struct {
		name         string
		firstMessage bool
		prefixNames  []string
		wantName     string
	}{
		{name: "first message leaves empty session", firstMessage: true},
		{name: "second message before name"},
		{name: "name retained in prefix", prefixNames: []string{"Earlier name"}, wantName: "Earlier name"},
		{name: "explicitly cleared prefix name", prefixNames: []string{"Earlier name", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := resumeThinkingMode(t, false, userMsg("ORIGINAL_A"), assistantMsg("", ai.TextContent{Text: "ANSWER_A"}))
			prepareAssistantEventTest(t, m)
			m.editor = tui.NewEditor()
			m.opts.SessionDir = t.TempDir()
			m.opts.CWD = t.TempDir()
			bindReplacementTestHandle(t, m)
			source := m.currentSession()
			for _, name := range tc.prefixNames {
				if _, err := source.AppendSessionInfo(name); err != nil {
					t.Fatal(err)
				}
			}
			selected, err := source.AppendMessage(userMsg("ORIGINAL_B"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.AppendMessage(assistantMsg("", ai.TextContent{Text: "ANSWER_B"})); err != nil {
				t.Fatal(err)
			}
			text := "ORIGINAL_B"
			if tc.firstMessage {
				selected = source.GetEntries()[0].Base().ID
				text = "ORIGINAL_A"
			}
			sc := m.buildSlashContext(t.Context())
			sc.Args = "Smoke Current"
			if err := nameHandler(sc); err != nil {
				t.Fatal(err)
			}
			assertFooterName(t, m, "Smoke Current")
			if err := sc.ForkToNewSession(selected); err != nil {
				t.Fatal(err)
			}
			if m.currentSession() == source || m.currentSession().Path() == source.Path() {
				t.Fatal("fork did not replace the session")
			}
			if got := m.currentSession().GetSessionName(); got != tc.wantName {
				t.Fatalf("fork name = %q, want %q", got, tc.wantName)
			}
			assertFooterName(t, m, tc.wantName)
			if got := m.editor.Text(); got != text {
				t.Fatalf("selected prompt = %q, want %q", got, text)
			}
			if got := source.GetSessionName(); got != "Smoke Current" {
				t.Fatalf("fork changed the source name: %q", got)
			}
		})
	}
}

// Clone retains a name in its prefix; /new clears it. Neither command may
// depend on an earlier command's cached footer name.
func TestInteractiveCloneAndNewFooterSessionNames(t *testing.T) {
	m := sessionChromeMode(t)
	original := m.currentSession()
	if err := cloneHandler(m.buildSlashContext(t.Context())); err != nil {
		t.Fatal(err)
	}
	if m.currentSession() == original {
		t.Fatal("clone did not replace the session")
	}
	assertFooterName(t, m, "My Session")
	if err := newHandler(m.buildSlashContext(t.Context())); err != nil {
		t.Fatal(err)
	}
	assertFooterName(t, m, "")
}

// Session redraws refresh the name before rendering the transcript, even when
// the replacement or restored entries did not emit a name-change event.
func TestInteractiveSessionRenderRefreshesFooterName(t *testing.T) {
	m := sessionChromeMode(t)
	for _, name := range []string{"Restored 名称", ""} {
		if _, err := m.currentSession().AppendSessionInfo(name); err != nil {
			t.Fatal(err)
		}
		m.renderSessionEntries()
		assertFooterName(t, m, name)
	}
}

func assertFooterName(t *testing.T, m *InteractiveMode, name string) {
	t.Helper()
	want := "."
	if name != "" {
		want += " • " + name
	}
	// Keep the exact ANSI and spacing contract of the whole name row.
	if got := m.statusLine.Render(120)[0]; got != dim(want) {
		t.Fatalf("footer name row = %q, want %q", got, dim(want))
	}
}
