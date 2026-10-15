package truncate

import "strings"

// TailWindow is TruncateTail of a stream of chunks without keeping the stream: Append folds a chunk in and Result equals TruncateTail of everything
// appended so far. TruncateTail's walk reads only the last maxBytes+1 bytes of its input, so the window keeps maxBytes+2 bytes (a trailing newline is
// not a counted line, so a window of maxBytes+1 could let a cut first line fit by exactly the byte the newline hides) and the totals are counted as
// the chunks arrive. A block that re-truncates its whole output on every frame costs the output length per frame; this costs the window.
//
// The kept bytes are the last keep bytes of buf. buf grows to twice keep before it drops what is older, so a chunk costs its own length: the bytes the
// window keeps move once per keep bytes appended, not on every chunk.
type TailWindow struct {
	maxBytes, maxLines int
	buf                []byte
	totalBytes         int
	newlines           int
	endsWithNewline    bool
}

// NewTailWindow returns an empty window for TruncateTail's limits.
func NewTailWindow(maxBytes, maxLines int) *TailWindow {
	return &TailWindow{maxBytes: maxBytes, maxLines: maxLines}
}

func (w *TailWindow) keep() int { return w.maxBytes + 2 }

// window is the suffix of the content the walk can read.
func (w *TailWindow) window() []byte {
	if over := len(w.buf) - w.keep(); over > 0 {
		return w.buf[over:]
	}
	return w.buf
}

// Reset replaces the content with content.
func (w *TailWindow) Reset(content string) {
	w.buf, w.totalBytes, w.newlines, w.endsWithNewline = w.buf[:0], 0, 0, false
	w.Append(content)
}

// Append adds a chunk to the end of the content.
func (w *TailWindow) Append(chunk string) {
	if chunk == "" {
		return
	}
	w.totalBytes += len(chunk)
	w.newlines += strings.Count(chunk, "\n")
	w.endsWithNewline = chunk[len(chunk)-1] == '\n'
	keep := w.keep()
	if len(chunk) >= keep {
		w.buf = append(w.buf[:0], chunk[len(chunk)-keep:]...)
		return
	}
	if len(w.buf)+len(chunk) > 2*keep {
		w.buf = append(w.buf[:0], w.window()[len(chunk):]...)
	}
	w.buf = append(w.buf, chunk...)
}

// Result is TruncateTail of the whole content. It copies only the kept text.
func (w *TailWindow) Result() Result {
	totalLines := w.newlines
	if !w.endsWithNewline && w.totalBytes > 0 {
		totalLines++
	}
	window := w.window()
	if totalLines <= w.maxLines && w.totalBytes <= w.maxBytes {
		// Only content within maxBytes fits, and the window then holds all of it.
		return Result{Content: string(window), TotalLines: totalLines, TotalBytes: w.totalBytes, OutputLines: totalLines, OutputBytes: w.totalBytes}
	}
	cut := CutTail(window, w.maxBytes, w.maxLines)
	return tailResult(string(window[cut.Start:cut.End]), cut, w.maxBytes, w.maxLines, w.totalBytes, totalLines)
}
