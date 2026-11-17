package codingagent

import (
	"regexp"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// terminalCalls is a renderer's terminal with the title and progress calls recorded instead of written, as the fake `ui.terminal` of
// Pi's interactive mode tests records setTitle and setProgress. Every other call goes to the renderer's own terminal.
type terminalCalls struct {
	tui.Terminal

	mu       sync.Mutex
	titles   []string
	progress []bool
	// onTitle, if set, hears each title on the goroutine that wrote it.
	onTitle func(title string)
}

func (c *terminalCalls) SetTitle(title string) {
	c.mu.Lock()
	c.titles = append(c.titles, title)
	notify := c.onTitle
	c.mu.Unlock()
	if notify != nil {
		notify(title)
	}
}

func (c *terminalCalls) SetProgress(active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.progress = append(c.progress, active)
}

// takeTitles returns the titles written since the last take.
func (c *terminalCalls) takeTitles() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	titles := c.titles
	c.titles = nil
	return titles
}

// takeProgress returns the progress calls made since the last take.
func (c *terminalCalls) takeProgress() []bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	progress := c.progress
	c.progress = nil
	return progress
}

// mainScreenOnTerminal is a main-screen renderer that reports terminal as the terminal it draws on. It keeps every other method of the
// renderer, so the mode's optional renderer interfaces still hold.
type mainScreenOnTerminal struct {
	*tui.TuiMainScreen
	terminal tui.Terminal
}

func (u mainScreenOnTerminal) Terminal() tui.Terminal { return u.terminal }

// altScreenOnTerminal is mainScreenOnTerminal for the fullscreen renderer.
type altScreenOnTerminal struct {
	*tui.TuiAltScreen
	terminal tui.Terminal
}

func (u altScreenOnTerminal) Terminal() tui.Terminal { return u.terminal }

// recordTerminalCalls makes m's renderer report a terminal that records the title and progress calls the mode makes on it: what the
// mode writes on `m.tuiInst.Terminal()`, and nothing it writes past the renderer.
func recordTerminalCalls(t testing.TB, m *InteractiveMode) *terminalCalls {
	t.Helper()
	m.rendererMu.Lock()
	defer m.rendererMu.Unlock()
	if m.tuiInst == nil {
		t.Fatal("recordTerminalCalls: the mode has no renderer")
	}
	calls := &terminalCalls{Terminal: m.tuiInst.Terminal()}
	switch ui := m.tuiInst.(type) {
	case *tui.TuiMainScreen:
		m.tuiInst = mainScreenOnTerminal{TuiMainScreen: ui, terminal: calls}
	case *tui.TuiAltScreen:
		m.tuiInst = altScreenOnTerminal{TuiAltScreen: ui, terminal: calls}
	default:
		t.Fatalf("recordTerminalCalls: renderer %T is neither the main-screen nor the fullscreen renderer", m.tuiInst)
	}
	// A write off the owner loop reads the terminal where createInteractiveTui kept it (rememberDrawTerminal), so the recorder takes that place too.
	recorded := tui.Terminal(calls)
	m.drawTerminal.Store(&recorded)
	return calls
}

var (
	terminalTitleSequence    = regexp.MustCompile("\x1b\\]0;([^\x07]*)\x07")
	terminalProgressSequence = regexp.MustCompile("\x1b\\]9;4;[^\x07]*\x07")
)

// terminalTitlesIn lists the titles of the OSC 0 sequences in the bytes a terminal received, in order.
func terminalTitlesIn(written string) []string {
	var titles []string
	for _, match := range terminalTitleSequence.FindAllStringSubmatch(written, -1) {
		titles = append(titles, match[1])
	}
	return titles
}

// terminalProgressIn lists the OSC 9;4 sequences in the bytes a terminal received, in order.
func terminalProgressIn(written string) []string {
	return terminalProgressSequence.FindAllString(written, -1)
}
