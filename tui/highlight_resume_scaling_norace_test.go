//go:build !race && !pig_strip_syntax_highlight

package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

func TestScalingStreamedCodeBlockHighlight(t *testing.T) {
	// A streamed code block is highlighted again on every frame. highlight.js scans the whole block (Pi's highlightCode), so a
	// stream is quadratic in the block. PiG resumes a language whose patterns cannot read across lines from the last recorded
	// state (highlightResumable), so the scan of a frame is the new text; each frame still returns a slice of one header per
	// line. Every run streams a block with a first line of its own, so neither the highlight memo nor a resume state of an
	// earlier run serves it.
	runs := 0
	testscaling.Check(t, testscaling.Case{Name: "streamed python code block of n bytes", Size: 2000, Bound: testscaling.Linear, BytesBound: linearWithFrameCopy, Prepare: func(n int) func() {
		runs++
		code := "# run " + strconv.Itoa(runs) + "\n" + strings.TrimPrefix(fenceOf(t, "python", n), "```python\n")
		code = code[:n]
		return func() {
			for end := 24; ; end += 24 {
				end = min(end, len(code))
				highlightCodeLines(code[:end], "python", true)
				if end == len(code) {
					return
				}
			}
		}
	}})
}
