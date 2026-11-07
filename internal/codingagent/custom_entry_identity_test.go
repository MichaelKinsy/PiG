package codingagent

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// entryCards is an entry renderer written as Pi's extensions write one: the card's expansion lives in a map keyed by the entry (Pi's WeakMap keys it by
// the session-manager entry object) and a left click toggles it.
type entryCards struct {
	states     map[*extension.RenderIdentity]*bool
	identities []*extension.RenderIdentity
}

func (c *entryCards) render(entry extension.CustomEntry, options extension.EntryRenderOptions, _ extension.Theme) extension.Component {
	c.identities = append(c.identities, options.Identity)
	if c.states == nil {
		c.states = map[*extension.RenderIdentity]*bool{}
	}
	state := c.states[options.Identity]
	if state == nil {
		state = new(bool)
		c.states[options.Identity] = state
	}
	content := &renderFunc{func() string { return fmt.Sprintf("%v:expanded=%t", entry.Data, *state) }}
	return tui.NewMouseRegion(content, func(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
		if event.Type != tui.MouseClick || event.Button != tui.MouseButtonLeft {
			return nil
		}
		*state = !*state
		return &tui.TuiMouseEventResult{Handled: true}
	})
}

// cards reads the card rows of the transcript: "first:expanded=false|second:expanded=true".
func cardRows(m *InteractiveMode) string {
	var out []string
	for _, line := range m.chatContainer.Render(80) {
		if strings.Contains(line, ":expanded=") {
			out = append(out, strings.TrimSpace(stripANSITest(line)))
		}
	}
	return strings.Join(out, "|")
}

// clickCard clicks the row of the card whose text starts with name.
func clickCard(t *testing.T, m *InteractiveMode, name string) *tui.TuiMouseDispatchResult {
	t.Helper()
	lines := m.chatContainer.Render(80)
	for row, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(stripANSITest(line)), name+":expanded=") {
			return tui.DispatchMouseEvent(m.chatContainer, tui.TuiMouseEvent{Type: tui.MouseClick, Button: tui.MouseButtonLeft, X: 2, Y: row, ScreenX: 2, ScreenY: row, Width: 80, Height: len(lines)})
		}
	}
	t.Fatalf("no card %q in %q", name, renderedChat(m))
	return nil
}

func entryCardMode(t *testing.T, cards *entryCards) *InteractiveMode {
	t.Helper()
	m := resumedModeWithOutputPad(t, 1, func(sess *Session) {
		// A session file is written once it holds a message.
		if _, err := sess.AppendMessage(userMsg("hello")); err != nil {
			t.Fatal(err)
		}
		for _, data := range []string{"first", "second"} {
			if _, err := sess.AppendCustomEntry("card", data); err != nil {
				t.Fatal(err)
			}
		}
	})
	ext := extension.Extension{EntryRenderers: map[string]extension.EntryRenderer{"card": cards.render}}
	m.newRunner = inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	m.repaintInitialMessages()
	return m
}

// Pi renders the same session-manager entry object on every rebuild of the transcript (renderSessionEntries returns the entries from getBranch), so
// the state an entry renderer keeps for it survives compaction, /tree and the resume paint. PiG decodes the entry again each time and hands the
// renderer the session's identity for the entry id, so the card a click expanded is still expanded after each rebuild, and each card keeps its own.
func TestEntryRendererStateSurvivesEveryTranscriptRebuild(t *testing.T) {
	cards := &entryCards{}
	m := entryCardMode(t, cards)
	if got := cardRows(m); got != "first:expanded=false|second:expanded=false" {
		t.Fatalf("rows = %q", got)
	}
	if result := clickCard(t, m, "second"); result == nil || !result.Handled {
		t.Fatalf("click on the second card = %+v, want handled", result)
	}
	const want = "first:expanded=false|second:expanded=true"
	if got := cardRows(m); got != want {
		t.Fatalf("after the click rows = %q, want %q", got, want)
	}
	first, second := cards.identities[0], cards.identities[1]
	if first == nil || second == nil || first == second {
		t.Fatalf("identities %p and %p, want two distinct non-nil ones", first, second)
	}

	rebuilds := []struct {
		name string
		run  func()
	}{
		// A compaction or a model switch rebuilds the transcript from the Session (rebuildChatFromMessages).
		{"rebuild from the session", m.rebuildChatFromSession},
		// A /tree navigation, a resume and a new session repaint it from the Session (chatContainer.clear(); renderInitialMessages()).
		{"repaint after /tree or /resume", m.repaintInitialMessages},
		// The startup paint of --resume and --continue.
		{"initial render", func() {
			m.chatContainer.Clear()
			m.opts.ResumePath = m.currentSession().Path()
			m.renderInitialMessages()
		}},
		// The live compaction_end rebuild.
		{"compaction result", func() {
			m.renderCompactionResult(agent.CompactionEndEvent{HasResult: true, Summary: "summary"}, append([]SessionEntry{nil}, m.currentSession().GetBranch()...))
		}},
		// A /tree navigation to the first entry and back: the second entry leaves the branch and returns.
		{"leaving and returning to the branch", func() {
			session := m.currentSession()
			leaf := session.GetLeafID()
			parent := session.GetBranch()[1].Base().ID
			if err := session.SetLeafID(&parent); err != nil {
				t.Fatal(err)
			}
			m.repaintInitialMessages()
			if got := cardRows(m); got != "first:expanded=false" {
				t.Fatalf("on the first entry rows = %q", got)
			}
			if err := session.SetLeafID(leaf); err != nil {
				t.Fatal(err)
			}
			m.repaintInitialMessages()
		}},
	}
	for _, rebuild := range rebuilds {
		rebuild.run()
		if got := cardRows(m); got != want {
			t.Fatalf("%s: rows = %q, want %q (the card's state was lost)", rebuild.name, got, want)
		}
	}
	for i, identity := range cards.identities {
		if identity != first && identity != second {
			t.Fatalf("renderer call %d got identity %p, a third one besides %p and %p", i, identity, first, second)
		}
	}
	// A click after the rebuilds toggles from the kept state.
	if result := clickCard(t, m, "second"); result == nil || !result.Handled {
		t.Fatalf("click after the rebuilds = %+v, want handled", result)
	}
	if got := cardRows(m); got != "first:expanded=false|second:expanded=false" {
		t.Fatalf("after the click rows = %q", got)
	}
}

// An entry appended while the transcript is open renders through the live path with the entry's identity; a rebuild then renders the same one.
func TestLiveAppendedEntryKeepsItsIdentityThroughARebuild(t *testing.T) {
	cards := &entryCards{}
	m := entryCardMode(t, cards)
	entry, err := AppendExtensionEntry(m.currentSession(), "card", "live", nil)
	if err != nil {
		t.Fatal(err)
	}
	m.addCustomEntryToChat(entry)
	if got := cardRows(m); got != "first:expanded=false|second:expanded=false|live:expanded=false" {
		t.Fatalf("rows = %q", got)
	}
	live := cards.identities[len(cards.identities)-1]
	if result := clickCard(t, m, "live"); result == nil || !result.Handled {
		t.Fatalf("click on the live card = %+v, want handled", result)
	}
	m.rebuildChatFromSession()
	if got := cardRows(m); got != "first:expanded=false|second:expanded=false|live:expanded=true" {
		t.Fatalf("after the rebuild rows = %q", got)
	}
	if cards.identities[len(cards.identities)-1] != live {
		t.Fatal("the rebuild handed the live entry a new identity")
	}
}

// An entry no renderer draws gets no identity, so a transcript of such entries holds nothing per rebuild, and the identities of the entries that are
// drawn are one per entry however many times the transcript is rebuilt.
func TestEntryRenderIdentitiesAreBoundedByTheEntries(t *testing.T) {
	cards := &entryCards{}
	m := entryCardMode(t, cards)
	session := m.currentSession()
	if _, err := session.AppendCustomEntry("no-renderer", "plain"); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		m.rebuildChatFromSession()
	}
	if got := len(session.renderIdentities.ids); got != 2 {
		t.Fatalf("session holds %d identities after 50 rebuilds of 2 drawn entries and 1 undrawn, want 2", got)
	}
}

// Pi builds new entry objects when the session manager loads another file, starts a new session or branches into a new one, so the state kept for the
// old entries is gone: the session drops its identities with its entries, and the extension is told to release the state kept for them once the
// transcript has dropped its components.
func TestEntryRenderIdentitiesGoWithTheSessionLog(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replace func(t *testing.T, s *Session)
	}{
		{"new session", func(t *testing.T, s *Session) {
			if _, err := s.NewSession(nil); err != nil {
				t.Fatal(err)
			}
		}},
		{"branch into a new session", func(t *testing.T, s *Session) {
			if _, err := s.CreateBranchedSession(*s.GetLeafID()); err != nil {
				t.Fatal(err)
			}
		}},
		{"another session file", func(t *testing.T, s *Session) {
			if err := s.SetSessionFile(s.Path()); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cards := &entryCards{}
			m := entryCardMode(t, cards)
			session := m.currentSession()
			old := weak.Make(session.EntryRenderIdentity(session.GetBranch()[1].Base().ID))
			if len(session.renderIdentities.ids) != 2 {
				t.Fatalf("session holds %d identities, want 2", len(session.renderIdentities.ids))
			}
			tc.replace(t, session)
			if got := len(session.renderIdentities.ids); got != 0 {
				t.Fatalf("the replaced log left %d identities", got)
			}
			// The transcript still draws the old components until it is rebuilt; dropping them leaves nothing that holds the identity.
			m.chatContainer.Clear()
			m.chatContainer = tui.NewContainer()
			cards.states, cards.identities = nil, nil
			deadline := time.Now().Add(10 * time.Second)
			for old.Value() != nil && time.Now().Before(deadline) {
				runtime.GC()
				time.Sleep(time.Millisecond)
			}
			if old.Value() != nil {
				t.Fatal("the identity of an entry the session no longer holds is still reachable")
			}
		})
	}
}
