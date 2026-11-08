// Package vterm is the Go counterpart of pi-tui's test VirtualTerminal
// (packages/tui/test/virtual-terminal.ts): a tui.Terminal whose output is
// replayed into the internal/tui/termsim emulator so tests can read the
// viewport a user would see.
//
// Unlike the xterm.js original, writes are applied synchronously, so there is
// no flush step; Flush exists only to keep the upstream call shape.
package vterm

import (
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/tui/termsim"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// Terminal implements pitui.Terminal over a termsim.Grid. All methods are safe
// for concurrent use; renders happen on the TUI's own goroutine while tests
// read the viewport from theirs.
type Terminal struct {
	mu       sync.Mutex
	grid     *termsim.Grid
	onInput  func(string)
	onResize func()
	title    string
	cursorOn bool
	paste    bool
}

var _ pitui.Terminal = (*Terminal)(nil)

// New returns a columns x rows virtual terminal.
func New(columns, rows int) *Terminal {
	return &Terminal{grid: termsim.New(rows, columns), cursorOn: true}
}

// Start records the callbacks and enables bracketed paste, as upstream does.
func (t *Terminal) Start(onInput func(string), onResize func()) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onInput, t.onResize = onInput, onResize
	t.paste = true
	return nil
}

// Stop drops the callbacks and disables bracketed paste.
func (t *Terminal) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onInput, t.onResize = nil, nil
	t.paste = false
}

// DrainInput is a no-op: there is no stdin to drain.
func (*Terminal) DrainInput(time.Duration, time.Duration) error { return nil }

// Write feeds data to the emulator.
func (t *Terminal) Write(data string) {
	t.mu.Lock()
	t.grid.WriteString(data)
	t.mu.Unlock()
}

// Columns is the terminal width.
func (t *Terminal) Columns() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.grid.Cols
}

// Rows is the terminal height.
func (t *Terminal) Rows() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.grid.Rows
}

// KittyProtocolActive is always true, as upstream's virtual terminal reports.
func (*Terminal) KittyProtocolActive() bool { return true }

// MoveBy moves the cursor down (positive) or up (negative) by lines.
func (t *Terminal) MoveBy(lines int) {
	switch {
	case lines > 0:
		t.Write("\x1b[" + itoa(lines) + "B")
	case lines < 0:
		t.Write("\x1b[" + itoa(-lines) + "A")
	}
}

// HideCursor hides the cursor.
func (t *Terminal) HideCursor() {
	t.mu.Lock()
	t.cursorOn = false
	t.grid.WriteString("\x1b[?25l")
	t.mu.Unlock()
}

// ShowCursor shows the cursor.
func (t *Terminal) ShowCursor() {
	t.mu.Lock()
	t.cursorOn = true
	t.grid.WriteString("\x1b[?25h")
	t.mu.Unlock()
}

// CursorVisible reports whether the cursor is shown.
func (t *Terminal) CursorVisible() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cursorOn
}

// ClearLine erases from the cursor to the end of the line.
func (t *Terminal) ClearLine() { t.Write("\x1b[K") }

// ClearFromCursor erases from the cursor to the end of the screen.
func (t *Terminal) ClearFromCursor() { t.Write("\x1b[J") }

// ClearScreen clears the screen and homes the cursor.
func (t *Terminal) ClearScreen() { t.Write("\x1b[2J\x1b[H") }

// SetTitle writes the OSC 0 title sequence and remembers the title.
func (t *Terminal) SetTitle(title string) {
	t.mu.Lock()
	t.title = title
	t.grid.WriteString("\x1b]0;" + title + "\x07")
	t.mu.Unlock()
}

// Title is the last title set.
func (t *Terminal) Title() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.title
}

// SetProgress is a no-op.
func (*Terminal) SetProgress(bool) {}

// SetProgramStatus is a no-op.
func (*Terminal) SetProgramStatus(pitui.ProgramStatus) {}

// RestoreOnExit writes nothing: the virtual terminal owns no process modes.
func (*Terminal) RestoreOnExit(string, string) {}

// BracketedPaste reports whether Start enabled bracketed paste.
func (t *Terminal) BracketedPaste() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.paste
}

// SendInput delivers data to the input callback given to Start, if any. The
// callback runs on the caller's goroutine and outside the terminal's lock.
func (t *Terminal) SendInput(data string) {
	t.mu.Lock()
	fn := t.onInput
	t.mu.Unlock()
	if fn != nil {
		fn(data)
	}
}

// Resize changes the size and invokes the resize callback.
func (t *Terminal) Resize(columns, rows int) {
	t.mu.Lock()
	t.grid.Resize(rows, columns)
	fn := t.onResize
	t.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Flush exists for upstream call-shape parity; writes are already applied.
func (*Terminal) Flush() {}

// Viewport returns the visible rows with trailing spaces trimmed, like
// xterm's translateToString(true).
func (t *Terminal) Viewport() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Split(t.grid.String(), "\n")
}

// ScrollBuffer returns scrollback followed by the viewport.
func (t *Terminal) ScrollBuffer() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append(t.grid.Scrollback(), strings.Split(t.grid.String(), "\n")...)
}

// Cursor returns the cursor column (x) and row (y) within the viewport.
func (t *Terminal) Cursor() (x, y int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	row, col := t.grid.Cursor()
	return col, row
}

// Reset clears the screen, scrollback and modes.
func (t *Terminal) Reset() {
	t.mu.Lock()
	t.grid.Reset()
	t.mu.Unlock()
}

// Grid exposes the emulator for style-level assertions. Callers must not use
// it concurrently with a running TUI.
func (t *Terminal) Grid() *termsim.Grid { return t.grid }

func itoa(n int) string {
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
