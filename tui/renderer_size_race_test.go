package tui

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
)

// resizableTerminal is a terminal whose size changes while a renderer reads it.
type resizableTerminal struct {
	*ProcessTerminal
	columns, rows atomic.Int32
}

func (r *resizableTerminal) Columns() int { return int(r.columns.Load()) }
func (r *resizableTerminal) Rows() int    { return int(r.rows.Load()) }

// frameSizer is the part of a renderer a frame uses to take the terminal's size.
type frameSizer interface {
	TUI
	updateSize()
	lockFrame()
	unlockFrame()
}

// The extension host reads Width and Height from its own goroutines (a /reload's geometry resynchronization runs on a worker while the owner loop keeps painting), and every frame takes the terminal's size first. Both reads stay race-free under -race, and the last size a frame took is the one they report.
func TestRendererWidthAndHeightAreReadableWhileFramesTakeTheSize(t *testing.T) {
	for _, tc := range []struct {
		name string
		new  func(Terminal) frameSizer
	}{
		{"main screen", func(terminal Terminal) frameSizer { return NewTuiMainScreen(terminal, nil, "") }},
		{"alt screen", func(terminal Terminal) frameSizer { return NewTuiAltScreen(terminal, nil, "") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			terminal := &resizableTerminal{ProcessTerminal: NewProcessTerminalWithOutput(nil, nil, &out)}
			terminal.columns.Store(80)
			terminal.rows.Store(24)
			screen := tc.new(terminal)
			if screen.Width() != 80 || screen.Height() != 24 {
				t.Fatalf("size before any frame = %dx%d, want the terminal's 80x24", screen.Width(), screen.Height())
			}

			const frames = 500
			var reader sync.WaitGroup
			reader.Go(func() {
				for range frames {
					if width, height := screen.Width(), screen.Height(); width < 80 || width > 90 || height < 24 || height > 30 {
						t.Errorf("size read mid-resize = %dx%d, outside every size the terminal had", width, height)
						return
					}
				}
			})
			for i := range frames {
				terminal.columns.Store(int32(80 + i%11))
				terminal.rows.Store(int32(24 + i%7))
				screen.lockFrame()
				screen.updateSize()
				screen.unlockFrame()
			}
			reader.Wait()
			if screen.Width() != terminal.Columns() || screen.Height() != terminal.Rows() {
				t.Fatalf("size after the last frame = %dx%d, want the terminal's %dx%d", screen.Width(), screen.Height(), terminal.Columns(), terminal.Rows())
			}
		})
	}
}
