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

// sizeReaders is what the input, overlay and viewport goroutines of a renderer call.
type sizeReaders interface {
	frameSizer
	ShowOverlay(Component, OverlayOptions) *OverlayHandle
	ActiveOverlay() Component
	HideOverlay()
	hasOverlay() bool
	isOverlayFocused() bool
	SetRenderDispatcher(func(func()))
}

// The overlay paths (ShowOverlay, OverlayHandle, HideOverlay, ActiveOverlay, the focus and visibility checks of an input event) and the alt screen's mouse handling evaluate overlay visibility and place pointer events against the renderer's size from goroutines that hold neither the frame nor the owner loop (an extension's overlay control call, a terminal input reader), while every frame takes the terminal's size. Each reads the size through size, the one reader of what the frame stores, so none races with the frame's update. Run with -race: a reader of a plain width or height field is reported against updateSize.
func TestOverlayAndInputReadersDoNotRaceWithTheFrameSize(t *testing.T) {
	for _, tc := range []struct {
		name string
		new  func(Terminal) sizeReaders
		// input drives the viewport input a screen has; nil for a screen without it.
		input func(sizeReaders)
	}{
		{"main screen", func(terminal Terminal) sizeReaders { return NewTuiMainScreen(terminal, nil, "") }, nil},
		{"alt screen", func(terminal Terminal) sizeReaders { return NewTuiAltScreen(terminal, nil, "") }, func(screen sizeReaders) {
			alt := screen.(*TuiAltScreen)
			for _, input := range []string{"\x1b[<0;3;2M", "\x1b[<32;9;3M", "\x1b[<0;9;3m", "\x1b[<35;5;5M"} {
				alt.HandleViewportInput(input)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			terminal := &resizableTerminal{ProcessTerminal: NewProcessTerminalWithOutput(nil, nil, &out)}
			terminal.columns.Store(80)
			terminal.rows.Store(24)
			screen := tc.new(terminal)
			screen.SetRenderDispatcher(func(func()) {})
			screen.Add(NewText("hello"))
			options := OverlayOptions{visible: func(width, height int) bool { return width >= 80 && height >= 24 }}

			const frames = 500
			start := make(chan struct{})
			var readers sync.WaitGroup
			readers.Go(func() {
				<-start
				for i := range frames {
					handle := screen.ShowOverlay(NewText("overlay"), options)
					screen.ActiveOverlay()
					screen.hasOverlay()
					screen.isOverlayFocused()
					handle.SetHidden(i%2 == 0)
					handle.SetHidden(i%2 != 0)
					handle.Hide()
					screen.ShowOverlay(NewText("overlay"), options)
					screen.HideOverlay()
					if tc.input != nil {
						tc.input(screen)
					}
				}
			})
			close(start)
			for i := range frames {
				terminal.columns.Store(int32(80 + i%11))
				terminal.rows.Store(int32(24 + i%7))
				screen.lockFrame()
				screen.updateSize()
				screen.unlockFrame()
			}
			readers.Wait()
		})
	}
}
