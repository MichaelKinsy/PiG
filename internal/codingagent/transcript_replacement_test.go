package codingagent

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/termsim"
)

func TestCompactionReplacesPhysicalTranscript(t *testing.T) {
	const (
		width             = 72
		height            = 8
		startupMarker     = "UNIQUE-STARTUP-MARKER"
		historicalHeading = "### UNIQUE-HISTORICAL-HEADING"
		retainedHeading   = "### UNIQUE-RETAINED-HEADING"
		tailHeading       = "### UNIQUE-RETAINED-TAIL"
		summaryMarker     = "UNIQUE-COMPACTION-SUMMARY"
	)

	sm := tempSessionMgr(t)
	sess, err := sm.Create("semantic-transcript-replacement", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(mkUserMsg(historicalHeading)); err != nil {
		t.Fatal(err)
	}
	for i := range 24 {
		if _, err := sess.AppendMessage(mkUserMsg("obsolete transcript row " + string(rune('a'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	keepID, err := sess.AppendMessage(mkUserMsg(retainedHeading))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendCompaction(summaryMarker, keepID, 12345, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(mkUserMsg(tailHeading)); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	renderer := tui.NewWithOutput(&output, width, height)
	chat := tui.NewContainer()
	renderer.Add(tui.NewText(startupMarker))
	renderer.Add(chat)
	chat.Add(tui.NewText(historicalHeading))
	for i := range 24 {
		chat.Add(tui.NewText("obsolete physical row " + string(rune('a'+i))))
	}
	chat.Add(tui.NewText(retainedHeading))
	chat.Add(tui.NewText(tailHeading))

	terminal := termsim.New(height, width)
	renderer.Render()
	terminal.WriteString(output.String())
	output.Reset()

	ag := agent.NewAgent(agent.AgentOptions{})
	handle := &recordingCompactHandle{agent: ag, inner: sess}
	mode := NewInteractiveMode(InteractiveOptions{SessionHandle: handle})
	mode.tuiInst = renderer
	mode.chatContainer = chat
	mode.statusContainer = tui.NewContainer()
	mode.statusLine = NewStatusLine(nil, "", nil)
	mode.agent = ag
	mode.runCtx = context.Background()
	mode.toolsExpanded = true

	mode.handleAgentEvent(agent.CompactionEndEvent{
		Reason:       "manual",
		Summary:      summaryMarker,
		TokensBefore: 12345,
	})
	terminal.WriteString(output.String())

	if !strings.Contains(output.String(), "\x1b[2J\x1b[H\x1b[3J") {
		t.Fatal("semantic transcript replacement did not clear obsolete terminal scrollback")
	}

	physicalTranscript := strings.Join(append(terminal.Scrollback(), terminal.String()), "\n")
	for marker, want := range map[string]int{
		startupMarker:     1,
		historicalHeading: 0,
		retainedHeading:   1,
		tailHeading:       1,
		summaryMarker:     1,
	} {
		if got := strings.Count(physicalTranscript, marker); got != want {
			t.Errorf("physical transcript contains %q %d time(s), want %d:\n%s", marker, got, want, physicalTranscript)
		}
	}

	retainedAt := strings.Index(physicalTranscript, retainedHeading)
	summaryAt := strings.Index(physicalTranscript, summaryMarker)
	tailAt := strings.Index(physicalTranscript, tailHeading)
	if retainedAt < 0 || summaryAt <= retainedAt || tailAt <= summaryAt {
		t.Errorf("retained transcript order = retained:%d summary:%d tail:%d, want retained < summary < tail", retainedAt, summaryAt, tailAt)
	}
}
