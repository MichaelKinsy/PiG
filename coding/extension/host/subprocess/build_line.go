package subprocess

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/internal/buildprogress"
)

// buildLine is the one live status line a load pass shows while it compiles
// extensions: how many cells have finished of those planned, and which
// extensions are compiling now. It rewrites itself in place, so sixteen cells
// compiling in parallel are one line, not sixteen.
//
// pig additive (D20): Pi loads extension source in process and never compiles.
type buildLine struct {
	mu        sync.Mutex
	out       io.Writer
	width     int
	planned   map[string]bool
	finished  map[string]bool
	compiling []compileEntry
	shown     bool
}

type compileEntry struct{ cell, names string }

func newBuildLine(out io.Writer, width int, cellKeys []string) *buildLine {
	planned := make(map[string]bool, len(cellKeys))
	for _, key := range cellKeys {
		planned[key] = true
	}
	if width < 20 {
		width = 80
	}
	return &buildLine{out: out, width: width, planned: planned, finished: map[string]bool{}}
}

// compileStarted records that cell began compiling the named extensions. The
// line appears with the first compile, so a warm start prints nothing.
func (l *buildLine) compileStarted(cell, names string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.compiling = append(l.compiling, compileEntry{cell, names})
	l.draw()
}

// cellFinished records that cell no longer needs work, compiled or not.
func (l *buildLine) cellFinished(cell string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.planned[cell] {
		l.finished[cell] = true
	}
	l.compiling = slices.DeleteFunc(l.compiling, func(entry compileEntry) bool { return entry.cell == cell })
	if l.shown {
		l.draw()
	}
}

// clear removes the line.
func (l *buildLine) clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.shown {
		_, _ = io.WriteString(l.out, "\r\x1b[2K")
		l.shown = false
	}
}

func (l *buildLine) draw() {
	text := fmt.Sprintf("Building extensions [%d/%d] (first run, will be cached)", len(l.finished), len(l.planned))
	if len(l.compiling) > 0 {
		text += ": " + l.compiling[0].names
		if more := len(l.compiling) - 1; more > 0 {
			text += fmt.Sprintf(" +%d more", more)
		}
	}
	if runes := []rune(text); len(runes) > l.width-1 {
		text = string(runes[:l.width-2]) + "…"
	}
	_, _ = io.WriteString(l.out, "\r\x1b[2K"+text)
	l.shown = true
}

type buildLineKey struct{}

// withBuildLine returns ctx carrying the load pass's status line and the
// function that removes it. It shows a line only for interactive mode on a
// terminal, and never when an explicit build observer owns progress.
// pig additive (D20): native cold-build notices belong to interactive mode, not to a terminal file descriptor alone.
func (h *Host) withBuildLine(ctx context.Context, cells []CellSpec) (context.Context, func()) {
	if ctx.Value(buildLineKey{}) != nil || buildprogress.Enabled(ctx) || h.mode != "tui" || !term.IsTerminal(int(os.Stderr.Fd())) {
		return ctx, func() {}
	}
	keys := make([]string, len(cells))
	for i, cell := range cells {
		keys[i] = cell.Key
	}
	width, _, _ := term.GetSize(int(os.Stderr.Fd()))
	line := newBuildLine(os.Stderr, width, keys)
	return context.WithValue(ctx, buildLineKey{}, line), line.clear
}

// observeBuildLine reports cell's compiles to the pass's line. The returned
// function marks the cell finished.
func observeBuildLine(ctx context.Context, cell CellSpec) (context.Context, func()) {
	line, ok := ctx.Value(buildLineKey{}).(*buildLine)
	if !ok {
		return ctx, func() {}
	}
	ctx = buildprogress.Observe(ctx, func(event buildprogress.Event) {
		if strings.HasPrefix(event.Phase, "Compiling ") {
			names, _, _ := strings.Cut(event.Step, " (")
			line.compileStarted(cell.Key, names)
		}
	}, false)
	return ctx, func() { line.cellFinished(cell.Key) }
}
