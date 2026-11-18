//go:build parity

package runner

import "regexp"

// The kinds of write a window compared with write_kinds is reduced to. A kind carries the structure of the program's writes (what kind of write comes after
// which) and none of the content of a frame, which holds the run's random directory, the terminal's size and a clock.
// endSynchronizedOutput ends every frame (CSI ? 2026 l), the counterpart of beginSynchronizedOutput.
const endSynchronizedOutput = "\x1b[?2026l"

const (
	// kindFrame is one synchronized frame, CSI ? 2026 h to CSI ? 2026 l, whatever it holds.
	kindFrame = "frame"
	// kindCursorPlacement is one standalone hardware-cursor placement: the cursor moves (CSI A, B, C, D, G, H or d) and then is hidden or shown (CSI ? 25 l or h),
	// written outside a frame.
	kindCursorPlacement = "cursor-placement"
	// kindTitle is one window title, OSC 0 or OSC 2.
	kindTitle = "title"
	// kindRenderRun is, when a window collapses frame runs, one or more frames that each end in their cursor placement, with nothing else written between them.
	kindRenderRun = "render-run"
)

var (
	cursorMove       = regexp.MustCompile(`^\x1b\[[0-9;]*[ABCDGHd]$`)
	cursorVisibility = regexp.MustCompile(`^\x1b\[\?25[lh]$`)
	windowTitle      = regexp.MustCompile(`^\x1b\][02];`)
)

// writeKinds reduces the tokens of a terminal stream to the ordered kinds of its writes: each synchronized frame is kindFrame, each run of cursor moves that
// ends in a cursor visibility change is kindCursorPlacement, each window title is kindTitle, and every other token (a mode set, a query, a text run) stands
// for itself. The counts are kept: two frames are two kinds, and a placement the program writes after a frame is one more. With collapseRuns, each maximal run of
// frame-then-placement pairs is one kindRenderRun: the number of frames in a run is not compared (it is for a window whose frame count depends on the timing of a
// terminal reply), while every placement that does not follow a frame, and every other write between runs, still is.
func writeKinds(tokens []string, collapseRuns bool) []string {
	var kinds []string
	var moves []string
	flushMoves := func() {
		kinds = append(kinds, moves...)
		moves = nil
	}
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == string(beginSynchronizedOutput):
			flushMoves()
			for i+1 < len(tokens) && tokens[i] != endSynchronizedOutput {
				i++
			}
			kinds = append(kinds, kindFrame)
		case cursorMove.MatchString(token):
			moves = append(moves, token)
		case cursorVisibility.MatchString(token):
			moves = nil
			kinds = append(kinds, kindCursorPlacement)
		case windowTitle.MatchString(token):
			flushMoves()
			kinds = append(kinds, kindTitle)
		default:
			flushMoves()
			kinds = append(kinds, token)
		}
	}
	flushMoves()
	if !collapseRuns {
		return kinds
	}
	collapsed := kinds[:0:0]
	for i := 0; i < len(kinds); i++ {
		if kinds[i] == kindFrame && i+1 < len(kinds) && kinds[i+1] == kindCursorPlacement {
			for i+2 < len(kinds) && kinds[i+2] == kindFrame && i+3 < len(kinds) && kinds[i+3] == kindCursorPlacement {
				i += 2
			}
			collapsed = append(collapsed, kindRenderRun)
			i++
			continue
		}
		collapsed = append(collapsed, kinds[i])
	}
	return collapsed
}
