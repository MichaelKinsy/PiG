package tui

// Ports packages/tui/src/tui.ts TuiBase.terminal: the Terminal a renderer drives.

// rendererTerminal is the Terminal of a renderer with a fixed size and output (NewWithOutput): control sequences go to the renderer's writer and the dimensions are the renderer's, not the process terminal's.
type rendererTerminal struct {
	*ProcessTerminal
	base *tuiBase
}

func (r *rendererTerminal) Columns() int { columns, _ := r.base.fixedDimensions(); return columns }
func (r *rendererTerminal) Rows() int    { _, rows := r.base.fixedDimensions(); return rows }

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

// setFixedDimensionsLocked sets the size of a fixed-size renderer and publishes it to lock-free readers. The caller holds t.mu or owns t exclusively during construction.
func (t *tuiBase) setFixedDimensionsLocked(columns, rows int) {
	t.width, t.height = columns, rows
	t.fixedSizeSnapshot.Store(uint64(uint32(columns))<<32 | uint64(uint32(rows)))
}

// fixedDimensions is the last size setFixedDimensionsLocked published.
func (t *tuiBase) fixedDimensions() (columns, rows int) {
	packed := t.fixedSizeSnapshot.Load()
	return int(int32(packed >> 32)), int(int32(packed))
}
