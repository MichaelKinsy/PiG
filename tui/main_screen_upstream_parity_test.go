package tui

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// piMainScreenModel is a minimal direct port of Pi 0.84.0
// TuiMainScreen.doRender. It covers the text-only branches exercised below.
// In particular, a change above previousViewportTop calls fullRender(true),
// whose synchronized frame is ESC[2J, ESC[H], ESC[3J], then one replay of the
// current logical transcript.
type piMainScreenModel struct {
	previousLines       []string
	previousWidth       int
	previousHeight      int
	hardwareCursorRow   int
	maxLinesRendered    int
	previousViewportTop int
}

func (p *piMainScreenModel) render(lines []string, width, height int, force bool) string {
	widthChanged := p.previousWidth != 0 && p.previousWidth != width
	heightChanged := p.previousHeight != 0 && p.previousHeight != height
	previousBufferLength := height
	if p.previousHeight > 0 {
		previousBufferLength = p.previousViewportTop + p.previousHeight
	}
	prevViewportTop := p.previousViewportTop
	if heightChanged {
		prevViewportTop = max(0, previousBufferLength-height)
	}
	fullRender := func(clear bool) string {
		var out strings.Builder
		out.WriteString("\x1b[?2026h")
		if clear {
			out.WriteString("\x1b[2J\x1b[H\x1b[3J")
		}
		for i, line := range lines {
			if i > 0 {
				out.WriteString("\r\n")
			}
			out.WriteString(line)
		}
		out.WriteString("\x1b[?2026l")
		p.hardwareCursorRow = max(0, len(lines)-1)
		if clear {
			p.maxLinesRendered = len(lines)
		} else {
			p.maxLinesRendered = max(p.maxLinesRendered, len(lines))
		}
		p.previousViewportTop = max(0, max(height, len(lines))-height)
		p.previousLines = slices.Clone(lines)
		p.previousWidth = width
		p.previousHeight = height
		return out.String()
	}

	if force {
		p.previousLines = nil
		p.previousWidth = -1
		p.previousHeight = -1
		p.hardwareCursorRow = 0
		p.maxLinesRendered = 0
		p.previousViewportTop = 0
		widthChanged, heightChanged = true, true
	}
	if len(p.previousLines) == 0 && !widthChanged && !heightChanged {
		return fullRender(false)
	}
	if widthChanged || heightChanged {
		return fullRender(true)
	}

	firstChanged, lastChanged := -1, -1
	for i := range max(len(lines), len(p.previousLines)) {
		oldLine, newLine := "", ""
		if i < len(p.previousLines) {
			oldLine = p.previousLines[i]
		}
		if i < len(lines) {
			newLine = lines[i]
		}
		if oldLine != newLine {
			if firstChanged == -1 {
				firstChanged = i
			}
			lastChanged = i
		}
	}
	appended := len(lines) > len(p.previousLines)
	if appended {
		if firstChanged == -1 {
			firstChanged = len(p.previousLines)
		}
		lastChanged = len(lines) - 1
	}
	if firstChanged == -1 {
		p.previousViewportTop = prevViewportTop
		p.previousHeight = height
		return ""
	}
	if firstChanged >= len(lines) || firstChanged < prevViewportTop {
		return fullRender(true)
	}

	appendStart := appended && firstChanged == len(p.previousLines) && firstChanged > 0
	moveTargetRow := firstChanged
	if appendStart {
		moveTargetRow--
	}
	hardwareCursorRow := p.hardwareCursorRow
	prevViewportBottom := prevViewportTop + height - 1
	var out strings.Builder
	out.WriteString("\x1b[?2026h")
	if moveTargetRow > prevViewportBottom {
		currentScreenRow := min(max(hardwareCursorRow-prevViewportTop, 0), height-1)
		if moveToBottom := height - 1 - currentScreenRow; moveToBottom > 0 {
			fmt.Fprintf(&out, "\x1b[%dB", moveToBottom)
		}
		scroll := moveTargetRow - prevViewportBottom
		out.WriteString(strings.Repeat("\r\n", scroll))
		prevViewportTop += scroll
		hardwareCursorRow = moveTargetRow
	}
	currentScreenRow := hardwareCursorRow - prevViewportTop
	targetScreenRow := moveTargetRow - prevViewportTop
	if diff := targetScreenRow - currentScreenRow; diff > 0 {
		fmt.Fprintf(&out, "\x1b[%dB", diff)
	} else if diff < 0 {
		fmt.Fprintf(&out, "\x1b[%dA", -diff)
	}
	if appendStart {
		out.WriteString("\r\n")
	} else {
		out.WriteString("\r")
	}
	renderEnd := min(lastChanged, len(lines)-1)
	for i := firstChanged; i <= renderEnd; i++ {
		if i > firstChanged {
			out.WriteString("\r\n")
		}
		out.WriteString("\x1b[2K")
		out.WriteString(lines[i])
	}
	finalCursorRow := renderEnd
	if len(p.previousLines) > len(lines) {
		if renderEnd < len(lines)-1 {
			moveDown := len(lines) - 1 - renderEnd
			fmt.Fprintf(&out, "\x1b[%dB", moveDown)
			finalCursorRow = len(lines) - 1
		}
		extraLines := len(p.previousLines) - len(lines)
		out.WriteString(strings.Repeat("\r\n\x1b[2K", extraLines))
		fmt.Fprintf(&out, "\x1b[%dA", extraLines)
	}
	out.WriteString("\x1b[?2026l")
	p.hardwareCursorRow = finalCursorRow
	p.maxLinesRendered = max(p.maxLinesRendered, len(lines))
	p.previousViewportTop = max(prevViewportTop, finalCursorRow-height+1)
	p.previousLines = slices.Clone(lines)
	p.previousWidth = width
	p.previousHeight = height
	return out.String()
}

func (v *scrollVT) resize(width, height int) {
	all := append(slices.Clone(v.scrollback), v.rows...)
	v.w, v.h = width, height
	if len(all) < height {
		rows := make([][]rune, height-len(all))
		for i := range rows {
			rows[i] = blankVTRow(width)
		}
		all = append(rows, all...)
	}
	cut := max(0, len(all)-height)
	v.scrollback = all[:cut]
	v.rows = all[cut:]
	for i := range v.rows {
		row := slices.Clone(v.rows[i])
		if len(row) > width {
			row = row[:width]
		}
		if len(row) < width {
			row = append(row, blankVTRow(width-len(row))...)
		}
		v.rows[i] = row
	}
	v.row = min(v.row, height-1)
	v.col = min(v.col, width-1)
}

func (v *scrollVT) logicalRows() []string {
	rows := make([]string, 0, len(v.scrollback)+len(v.rows))
	for _, row := range v.scrollback {
		rows = append(rows, strings.TrimRight(string(row), " "))
	}
	for _, row := range v.rows {
		rows = append(rows, strings.TrimRight(string(row), " "))
	}
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

func TestMainScreenFramesMatchPi084LogicalHistory(t *testing.T) {
	const initialWidth, initialHeight = 40, 6
	width, height := initialWidth, initialHeight
	lines := []string{"START", "H1", "H2", "H3", "H4", "H5", "ASSIST-0", "EDITOR", "FOOT"}
	component := renderFuncComponent(func(int) []string { return lines })
	var pigOut bytes.Buffer
	pig := NewWithOutput(&pigOut, width, height)
	pig.Add(component)
	pi := &piMainScreenModel{}
	pigTerminal := newScrollVT(width, height)
	piTerminal := newScrollVT(width, height)
	fullReplays := 0

	render := func(name string, force bool) {
		t.Helper()
		pigOut.Reset()
		if force {
			pig.ForceFullRender()
		}
		pig.Render()
		pigFrame := pigOut.String()
		piFrame := pi.render(lines, width, height, force)
		for label, frame := range map[string]string{"Pig": pigFrame, "Pi": piFrame} {
			if starts, ends := strings.Count(frame, "\x1b[?2026h"), strings.Count(frame, "\x1b[?2026l"); starts != 1 || ends != 1 {
				t.Fatalf("%s %s synchronized-output frame = %d starts, %d ends", name, label, starts, ends)
			}
		}
		pigTerminal.apply(pigFrame)
		piTerminal.apply(piFrame)
		if strings.Contains(pigFrame, "\x1b[3J") {
			fullReplays++
		}
		pigVisible := make([]string, height)
		piVisible := make([]string, height)
		for i := range height {
			pigVisible[i] = pigTerminal.visibleLine(i)
			piVisible[i] = piTerminal.visibleLine(i)
		}
		if !slices.Equal(pigVisible, piVisible) {
			t.Fatalf("%s visible Pig=%q Pi=%q", name, pigVisible, piVisible)
		}
		wantViewport := lines[max(0, len(lines)-height):]
		for i := range height {
			want := ""
			if i < len(wantViewport) {
				want = wantViewport[i]
			}
			if got := pigTerminal.visibleLine(i); got != want {
				t.Fatalf("%s visible row %d=%q want %q", name, i, got, want)
			}
		}
		if got := pigTerminal.logicalRows(); !slices.Equal(got, lines) {
			t.Fatalf("%s scrollable transcript=%q want %q", name, got, lines)
		}
		if got := piTerminal.logicalRows(); !slices.Equal(got, lines) {
			t.Fatalf("%s Pi scrollable transcript=%q want %q", name, got, lines)
		}
		for _, marker := range lines {
			if marker != "" && strings.Count(strings.Join(pigTerminal.logicalRows(), "\n"), marker) != 1 {
				t.Fatalf("%s marker %q does not survive exactly once: %q", name, marker, pigTerminal.logicalRows())
			}
		}
	}

	render("initial", false)
	beforeStreamingReplays := fullReplays
	for i := 1; i <= 3; i++ {
		lines[len(lines)-3] = fmt.Sprintf("ASSIST-%d", i)
		render(fmt.Sprintf("stream-%d", i), false)
	}
	if fullReplays != beforeStreamingReplays {
		t.Fatalf("ordinary streaming full-replayed %d times", fullReplays-beforeStreamingReplays)
	}

	lines = append(lines[:len(lines)-2], "GROW-1", "GROW-2", "EDITOR", "FOOT")
	render("viewport-growth", false)
	lines[1] = "H1-CHANGED"
	render("change-above-viewport", false)

	lines = append(lines[:6], "TOOL-0", "TOOL-1", "TOOL-2", "TOOL-3", "TOOL-4", "EDITOR", "FOOT")
	render("tall-tool", false)
	lines = append(lines[:6], "TOOL-DONE", "EDITOR", "FOOT")
	render("tool-collapse", false)

	lines = []string{"START", "H1-CHANGED", "H2", "H3", "EDITOR-A", "EDITOR-B", "EDITOR-C", "EDITOR-D", "EDITOR-E", "EDITOR-F", "FOOT"}
	render("editor-grow", false)
	lines = []string{"START", "H1-CHANGED", "H2", "H3", "EDITOR", "FOOT"}
	render("editor-shrink", false)

	width = 48
	pig.width = width
	pigTerminal.resize(width, height)
	piTerminal.resize(width, height)
	render("width-change", false)
	height = 8
	pig.height = height
	pigTerminal.resize(width, height)
	piTerminal.resize(width, height)
	render("height-change", false)

	lines = []string{"COMPACTION-SUMMARY", "KEPT-TAIL", "EDITOR", "FOOT"}
	render("semantic-compaction", true)
	for _, obsolete := range []string{"START", "H1-CHANGED", "TOOL-DONE"} {
		if strings.Contains(strings.Join(pigTerminal.logicalRows(), "\n"), obsolete) {
			t.Fatalf("semantic compaction retained obsolete marker %q", obsolete)
		}
	}
}
