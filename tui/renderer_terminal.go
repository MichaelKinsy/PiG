package tui

// Ports packages/tui/src/tui.ts TuiBase.terminal: the Terminal a renderer drives.

// rendererTerminal is the Terminal of a renderer with a fixed size and output (NewWithOutput): control sequences go to the renderer's writer and the dimensions are the renderer's, not the process terminal's.
type rendererTerminal struct {
	*ProcessTerminal
	base *tuiBase
}

func (r *rendererTerminal) Columns() int { columns, _ := r.base.size(); return columns }
func (r *rendererTerminal) Rows() int    { _, rows := r.base.size(); return rows }

var _ Terminal = (*rendererTerminal)(nil)

// Terminal returns the terminal this renderer draws on (tui.ts TuiBase.terminal, a public property). A renderer built for the process terminal returns that terminal; one built with a fixed output and size returns a terminal that writes to the output and reports the size. The same value comes back every time.
//
// Terminal and the returned terminal's size never take t.mu: components such as the Editor (editor.ts render reads tui.terminal.rows) call them from Render while the renderer holds t.mu for the frame.
func (t *tuiBase) Terminal() Terminal {
	if t.terminal != nil {
		return t.terminal
	}
	if derived := t.derivedTerminal.Load(); derived != nil {
		return *derived
	}
	var terminal Terminal = processTerminal
	if t.fixedSize {
		terminal = &rendererTerminal{ProcessTerminal: NewProcessTerminalWithOutput(nil, nil, t.out), base: t}
	}
	if t.derivedTerminal.CompareAndSwap(nil, &terminal) {
		return terminal
	}
	return *t.derivedTerminal.Load()
}

// setSize records the renderer's size, the one place it is stored. The frame that holds t.mu, SetFixedSize with t.mu, or the constructor calls it.
func (t *tuiBase) setSize(columns, rows int) {
	t.sizeSnapshot.Store(uint64(uint32(columns))<<32 | uint64(uint32(rows)))
}

// size is the renderer's current size, readable from any goroutine and during a frame. Every reader uses it, so a frame that updates the size on its goroutine never races with an input, overlay or extension-host goroutine.
func (t *tuiBase) size() (columns, rows int) {
	packed := t.sizeSnapshot.Load()
	return int(int32(packed >> 32)), int(int32(packed))
}
