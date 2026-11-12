//go:build parity

package runner

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"time"
)

// beginSynchronizedOutput starts every frame Pi's renderers and PiG's write: tui.ts, tui-main-screen.ts and tui-alt-screen.ts wrap a frame in
// CSI ? 2026 h ... CSI ? 2026 l.
var beginSynchronizedOutput = []byte("\x1b[?2026h")

// cursorOnlyFrame is a frame that repaints nothing: the synchronized-output frame a render writes when no row changed, which only moves the cursor
// to its row and hides it (29 bytes in Pi and PiG).
var cursorOnlyFrame = regexp.MustCompile(`\x1b\[\?2026h\x1b\[\d+;1H\x1b\[\?25l\x1b\[\?2026l`)

// finalFrameQuietDefault is how long a final_frame observation waits for a late frame: several of the 16 ms render throttles.
const finalFrameQuietDefault = 300 * time.Millisecond

// FinalFrameResult is what a step's final_frame saw in the raw terminal transcript.
type FinalFrameResult struct {
	// Observed reports that a step carried a final_frame.
	Observed bool
	// Text is the reply text the frame must carry.
	Text string
	// Found reports that the transcript holds Text.
	Found bool
	// Trailing is the number of frames that begin after the last Text in the transcript.
	Trailing int
	// Tail is the bytes after the last Text, for the failure message.
	Tail string
	// Frames is the raw bytes of the frames that begin after the last Text (from the first CSI ? 2026 h on), for the cursor-only count.
	Frames string
	// WriteLog reports that the frames were read from the step's write log, the bytes the renderers wrote, and not from the terminal transcript.
	WriteLog bool
}

// source names, for a failure message, the file the observation read.
func (r FinalFrameResult) source() string {
	if r.WriteLog {
		return "the write log (PI_TUI_WRITE_LOG)"
	}
	return "the raw transcript"
}

// CursorOnly is the number of cursor-only frames among the frames that begin after the last Text.
func (r FinalFrameResult) CursorOnly() int {
	return len(cursorOnlyFrame.FindAllString(r.Frames, -1))
}

// finalFrameQuiet is how long a final_frame step lets the pane run on after its waits, so a late frame is in the transcript when it is read.
func finalFrameQuiet(spec TmuxFinalFrame) time.Duration {
	if spec.QuietMilliseconds > 0 {
		return time.Duration(spec.QuietMilliseconds) * time.Millisecond
	}
	return finalFrameQuietDefault
}

// finalFrameSource is the file a final_frame observation reads: the terminal transcript or, when the step names one, the write log of the
// binary's renderers, with the scenario's tokens expanded.
func finalFrameSource(transcriptPath string, spec TmuxFinalFrame, expand func(string) string) string {
	if spec.WriteLog == "" {
		return transcriptPath
	}
	return expand(spec.WriteLog)
}

// observeFinalFrame reads the raw transcript (tmux pipe-pane) for frames written after the last Text. It reads the finished transcript: finishTranscript has returned nil, so the file holds every byte the pane wrote.
func observeFinalFrame(transcriptPath string, spec TmuxFinalFrame) FinalFrameResult {
	raw, _ := os.ReadFile(transcriptPath)
	result := FinalFrameResult{Observed: true, Text: spec.Text, WriteLog: spec.WriteLog != ""}
	at := bytes.LastIndex(raw, []byte(spec.Text))
	if at < 0 {
		return result
	}
	tail := raw[at+len(spec.Text):]
	result.Found = true
	result.Trailing = bytes.Count(tail, beginSynchronizedOutput)
	if first := bytes.Index(tail, beginSynchronizedOutput); first >= 0 {
		result.Frames = string(tail[first:])
	}
	result.Tail = fmt.Sprintf("%q", tail[:min(len(tail), 200)])
	return result
}

// merge adds the observation of a later step to r.
func (r FinalFrameResult) merge(next FinalFrameResult) FinalFrameResult {
	if !next.Observed {
		return r
	}
	if !r.Observed {
		return next
	}
	r.Found = r.Found && next.Found
	r.WriteLog = r.WriteLog || next.WriteLog
	r.Trailing += next.Trailing
	r.Frames += next.Frames
	if next.Trailing > 0 || !next.Found {
		r.Text, r.Tail = next.Text, next.Tail
	}
	return r
}
